// Package pub publishes records to Kafka.
package pub

import (
	"context"
	"fmt"

	"github.com/IBM/sarama"

	"github.com/tomdong2010/good-gokafka/internal/message"
)

// Record is a single message to publish.
type Record struct {
	Topic       string
	Key         []byte // records with the same key go to the same partition, preserving their order
	Value       []byte
	ContentType string // stored in the "content-type" record header when non-empty
}

// Result reports where a record was written.
type Result struct {
	Partition int32 `json:"partition"`
	Offset    int64 `json:"offset"`
}

// Publisher publishes records to a message broker.
type Publisher interface {
	Publish(ctx context.Context, r Record) (Result, error)
	Close() error
}

// KafkaPublisher is a Publisher backed by a sarama.SyncProducer.
type KafkaPublisher struct {
	producer sarama.SyncProducer
}

// NewConfig returns the producer configuration: idempotent writes acknowledged by
// all in-sync replicas, so retries can neither lose nor duplicate records.
func NewConfig(clientID string) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.ClientID = clientID
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Idempotent = true
	cfg.Net.MaxOpenRequests = 1 // required by the idempotent producer
	cfg.Producer.Retry.Max = 5
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Producer.Compression = sarama.CompressionSnappy
	return cfg
}

// NewKafkaPublisher connects a synchronous producer to brokers.
func NewKafkaPublisher(brokers []string, cfg *sarama.Config) (*KafkaPublisher, error) {
	prd, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("create kafka producer: %w", err)
	}
	return NewKafkaPublisherFrom(prd), nil
}

// NewKafkaPublisherFrom wraps an existing producer; mainly useful for tests.
func NewKafkaPublisherFrom(prd sarama.SyncProducer) *KafkaPublisher {
	return &KafkaPublisher{producer: prd}
}

// Publish sends r and waits for the broker acknowledgement. sarama's
// SyncProducer cannot be interrupted, so ctx is only checked before sending.
func (p *KafkaPublisher) Publish(ctx context.Context, r Record) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	msg := &sarama.ProducerMessage{
		Topic: r.Topic,
		Value: sarama.ByteEncoder(r.Value),
	}
	if len(r.Key) > 0 {
		msg.Key = sarama.ByteEncoder(r.Key)
	}
	if r.ContentType != "" {
		msg.Headers = []sarama.RecordHeader{{Key: []byte(message.ContentTypeHeader), Value: []byte(r.ContentType)}}
	}

	partition, offset, err := p.producer.SendMessage(msg)
	if err != nil {
		return Result{}, fmt.Errorf("publish to %s: %w", r.Topic, err)
	}
	return Result{Partition: partition, Offset: offset}, nil
}

// Close flushes buffered records and releases the connection.
func (p *KafkaPublisher) Close() error {
	return p.producer.Close()
}
