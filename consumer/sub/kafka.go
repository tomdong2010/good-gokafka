// Package sub consumes records from Kafka as part of a consumer group.
package sub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IBM/sarama"
)

// Record is a consumed Kafka record.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
	Timestamp time.Time
}

// Handler processes a single record. A returned error is logged and the record
// is skipped, so one malformed record cannot stall its partition.
type Handler func(ctx context.Context, r *Record) error

// Subscriber consumes topics and dispatches records to a Handler.
type Subscriber interface {
	// Subscribe blocks until ctx is cancelled or an unrecoverable error occurs.
	Subscribe(ctx context.Context, topics []string, h Handler) error
	Close() error
}

// KafkaSubscriber is a Subscriber backed by a sarama.ConsumerGroup. Partitions
// are balanced across every instance started with the same group ID, and
// offsets are committed so a restart resumes where it stopped.
type KafkaSubscriber struct {
	group sarama.ConsumerGroup
	log   *slog.Logger
}

// NewConfig returns the consumer group configuration. Groups with no committed
// offset start from the oldest retained record.
func NewConfig(clientID string) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.ClientID = clientID
	cfg.Consumer.Return.Errors = true
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategySticky()}
	return cfg
}

// NewKafkaSubscriber joins groupID on brokers.
func NewKafkaSubscriber(brokers []string, groupID string, cfg *sarama.Config, log *slog.Logger) (*KafkaSubscriber, error) {
	group, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer group: %w", err)
	}
	return NewKafkaSubscriberFrom(group, log), nil
}

// NewKafkaSubscriberFrom wraps an existing consumer group; mainly useful for tests.
func NewKafkaSubscriberFrom(group sarama.ConsumerGroup, log *slog.Logger) *KafkaSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &KafkaSubscriber{group: group, log: log}
}

// Subscribe implements Subscriber.
func (s *KafkaSubscriber) Subscribe(ctx context.Context, topics []string, h Handler) error {
	go func() {
		for err := range s.group.Errors() {
			s.log.Error("consumer group error", "err", err)
		}
	}()

	gh := &groupHandler{handle: h, log: s.log}
	for {
		// Consume returns whenever the group rebalances, so it has to be called in a loop.
		if err := s.group.Consume(ctx, topics, gh); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				return nil
			}
			return fmt.Errorf("consume %v: %w", topics, err)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// Close leaves the group and commits the offsets marked so far.
func (s *KafkaSubscriber) Close() error {
	return s.group.Close()
}

type groupHandler struct {
	handle Handler
	log    *slog.Logger
}

func (g *groupHandler) Setup(sess sarama.ConsumerGroupSession) error {
	g.log.Info("partitions assigned", "member", sess.MemberID(), "claims", sess.Claims())
	return nil
}

func (g *groupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (g *groupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ctx := sess.Context()
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			rec := toRecord(msg)
			if err := g.handle(ctx, rec); err != nil {
				g.log.Error("skip record", "topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset, "err", err)
			}
			// Marked only after handling, so a crash replays the record (at-least-once).
			sess.MarkMessage(msg, "")
		case <-ctx.Done():
			return nil
		}
	}
}

func toRecord(msg *sarama.ConsumerMessage) *Record {
	var headers map[string]string
	if len(msg.Headers) > 0 {
		headers = make(map[string]string, len(msg.Headers))
		for _, h := range msg.Headers {
			if h != nil {
				headers[string(h.Key)] = string(h.Value)
			}
		}
	}
	return &Record{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Key:       msg.Key,
		Value:     msg.Value,
		Headers:   headers,
		Timestamp: msg.Timestamp,
	}
}
