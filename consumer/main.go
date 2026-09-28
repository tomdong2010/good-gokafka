// Command consumer reads messages from Kafka as a member of a consumer group.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tomdong2010/good-gokafka/consumer/handler"
	"github.com/tomdong2010/good-gokafka/consumer/sub"
	"github.com/tomdong2010/good-gokafka/internal/config"
	"github.com/tomdong2010/good-gokafka/internal/message"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("consumer stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	if err := config.LoadDotEnv(); err != nil {
		return err
	}
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

	subscriber, err := sub.NewKafkaSubscriber(brokers, groupID,
		sub.NewConfig(config.String("KAFKA_CLIENT_ID", "good-gokafka-consumer")), log)
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

	log.Info("consumer started", "brokers", brokers, "topics", topics, "group", groupID)
	worker := handler.NewWorkerHandler(codec, log)
	if err := subscriber.Subscribe(ctx, topics, worker.Handle); err != nil {
		return err
	}
	log.Info("shutting down")
	return nil
}
