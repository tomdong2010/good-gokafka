// Command producer exposes an HTTP API that publishes messages to Kafka.
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

	"github.com/tomdong2010/good-gokafka/internal/config"
	"github.com/tomdong2010/good-gokafka/internal/kafka"
	"github.com/tomdong2010/good-gokafka/internal/message"
	"github.com/tomdong2010/good-gokafka/internal/metrics"
	"github.com/tomdong2010/good-gokafka/producer/handler"
	"github.com/tomdong2010/good-gokafka/producer/pub"
)

func main() {
	if err := config.LoadDotEnv(); err != nil {
		slog.Error("producer stopped", "err", err)
		os.Exit(1)
	}
	log, err := config.Logger()
	if err != nil {
		slog.Error("producer stopped", "err", err)
		os.Exit(1)
	}
	if err := run(log); err != nil {
		log.Error("producer stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	brokers, err := config.Brokers("KAFKA_ADDRESS")
	if err != nil {
		return err
	}
	topic, err := config.Required("KAFKA_TOPIC")
	if err != nil {
		return err
	}
	codec, err := message.NewCodec(config.String("MESSAGE_FORMAT", message.FormatProto))
	if err != nil {
		return err
	}
	addr := config.String("HTTP_ADDR", ":3000")
	shutdownTimeout, err := config.Duration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return err
	}

	security, err := kafka.SecurityFromEnv()
	if err != nil {
		return err
	}
	producerCfg := kafka.ProducerConfig(config.String("KAFKA_CLIENT_ID", "good-gokafka-producer"))
	if err := security.Apply(producerCfg); err != nil {
		return err
	}

	publisher, err := pub.NewKafkaPublisher(brokers, producerCfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Error("close publisher", "err", err)
		}
	}()

	reg := metrics.NewRegistry()
	api := handler.NewHTTPHandler(topic, publisher, codec, handler.Options{Logger: log, Registerer: reg})
	mux := http.NewServeMux()
	mux.Handle("/", api.Routes())
	mux.Handle("GET /metrics", metrics.Handler(reg))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("producer listening", "addr", addr, "brokers", brokers, "topic", topic, "format", codec.ContentType())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}
