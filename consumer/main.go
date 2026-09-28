// Command consumer reads messages from Kafka as a member of a consumer group.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/tomdong2010/good-gokafka/consumer/handler"
	"github.com/tomdong2010/good-gokafka/consumer/sub"
	"github.com/tomdong2010/good-gokafka/internal/config"
	"github.com/tomdong2010/good-gokafka/internal/kafka"
	"github.com/tomdong2010/good-gokafka/internal/message"
	"github.com/tomdong2010/good-gokafka/internal/metrics"
)

func main() {
	if err := config.LoadDotEnv(); err != nil {
		slog.Error("consumer stopped", "err", err)
		os.Exit(1)
	}
	log, err := config.Logger()
	if err != nil {
		slog.Error("consumer stopped", "err", err)
		os.Exit(1)
	}
	if err := run(log); err != nil {
		log.Error("consumer stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// ZOOKEEPER_HOST is accepted for old .env files; it always pointed at a Kafka broker.
	brokers, err := config.Brokers("ZOOKEEPER_HOST")
	if err != nil {
		return err
	}
	topics := config.List("KAFKA_TOPIC")
	if len(topics) == 0 {
		_, err := config.Required("KAFKA_TOPIC")
		return err
	}
	codec, err := message.NewCodec(config.String("MESSAGE_FORMAT", message.FormatProto))
	if err != nil {
		return err
	}
	groupID := config.String("KAFKA_GROUP_ID", "good-gokafka-consumer")
	clientID := config.String("KAFKA_CLIENT_ID", "good-gokafka-consumer")

	failureRate, err := config.Float("SIMULATE_FAILURE_RATE", 0)
	if err != nil {
		return err
	}
	if failureRate < 0 || failureRate > 1 {
		return fmt.Errorf("SIMULATE_FAILURE_RATE must be between 0 and 1, got %v", failureRate)
	}

	security, err := kafka.SecurityFromEnv()
	if err != nil {
		return err
	}
	consumerCfg := kafka.ConsumerConfig(clientID)
	dlqCfg := kafka.ProducerConfig(clientID + "-dlq")
	for _, cfg := range []*sarama.Config{consumerCfg, dlqCfg} {
		if err := security.Apply(cfg); err != nil {
			return err
		}
	}

	opts, closeDLQ, err := retryOptions(brokers, dlqCfg, log)
	if err != nil {
		return err
	}
	defer closeDLQ()

	reg := metrics.NewRegistry()
	opts.Registerer = reg

	subscriber, err := sub.NewKafkaSubscriber(brokers, groupID, consumerCfg, opts, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := subscriber.Close(); err != nil {
			log.Error("close subscriber", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metricsAddr := config.String("METRICS_ADDR", ":9100")
	stopMetrics := serveMetrics(metricsAddr, reg, log)
	defer stopMetrics()

	log.Info("consumer started", "brokers", brokers, "topics", topics, "group", groupID,
		"max_retries", opts.MaxRetries, "dead_letter", opts.DeadLetter != nil, "metrics", metricsAddr)
	var handle sub.Handler = handler.NewWorkerHandler(codec, log).Handle
	if failureRate > 0 {
		log.Warn("simulating transient handler failures", "rate", failureRate)
		handle = handler.SimulateFailures(handle, failureRate)
	}
	if err := subscriber.Subscribe(ctx, topics, handle); err != nil {
		return err
	}
	log.Info("shutting down")
	return nil
}

// serveMetrics exposes /metrics and /healthz on addr and returns a function
// that shuts the server down.
func serveMetrics(addr string, reg *prometheus.Registry, log *slog.Logger) func() {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler(reg))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// retryOptions reads the retry and dead-letter settings and, when the
// dead-letter topic is enabled, connects the producer that writes to it.
func retryOptions(brokers []string, dlqCfg *sarama.Config, log *slog.Logger) (sub.Options, func(), error) {
	noop := func() {}
	maxRetries, err := config.Int("MAX_RETRIES", 3)
	if err != nil {
		return sub.Options{}, noop, err
	}
	if maxRetries < 0 {
		return sub.Options{}, noop, fmt.Errorf("MAX_RETRIES must not be negative, got %d", maxRetries)
	}
	retryBackoff, err := config.Duration("RETRY_BACKOFF", 200*time.Millisecond)
	if err != nil {
		return sub.Options{}, noop, err
	}
	opts := sub.Options{MaxRetries: maxRetries, RetryBackoff: retryBackoff}

	enabled, err := config.Bool("DLQ_ENABLED", true)
	if err != nil || !enabled {
		return opts, noop, err
	}
	producer, err := sarama.NewSyncProducer(brokers, dlqCfg)
	if err != nil {
		return sub.Options{}, noop, fmt.Errorf("create dead-letter producer: %w", err)
	}
	dlq := sub.NewKafkaDeadLetter(producer, config.String("DLQ_SUFFIX", ".dlq"))
	opts.DeadLetter = dlq
	return opts, func() {
		if err := dlq.Close(); err != nil {
			log.Error("close dead-letter producer", "err", err)
		}
	}, nil
}
