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
	"github.com/tomdong2010/good-gokafka/internal/message"
	"github.com/tomdong2010/good-gokafka/producer/handler"
	"github.com/tomdong2010/good-gokafka/producer/pub"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("producer stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	if err := config.LoadDotEnv(); err != nil {
		return err
	}
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

	publisher, err := pub.NewKafkaPublisher(brokers, pub.NewConfig(config.String("KAFKA_CLIENT_ID", "good-gokafka-producer")))
	if err != nil {
		return err
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Error("close publisher", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler.NewHTTPHandler(topic, publisher, codec, log).Routes(),
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
