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

// Handler processes a single record. It is called concurrently for different
// partitions. A failing record is retried (unless the error is Permanent), then
// sent to the dead-letter sink or skipped, so it does not block the records behind it.
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
	group   sarama.ConsumerGroup
	opts    Options
	log     *slog.Logger
	metrics *subMetrics
}

// NewKafkaSubscriber joins groupID on brokers.
func NewKafkaSubscriber(brokers []string, groupID string, cfg *sarama.Config, opts Options, log *slog.Logger) (*KafkaSubscriber, error) {
	group, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer group: %w", err)
	}
	return NewKafkaSubscriberFrom(group, opts, log), nil
}

// NewKafkaSubscriberFrom wraps an existing consumer group; mainly useful for tests.
func NewKafkaSubscriberFrom(group sarama.ConsumerGroup, opts Options, log *slog.Logger) *KafkaSubscriber {
	if log == nil {
		log = slog.Default()
	}
	return &KafkaSubscriber{group: group, opts: opts, log: log, metrics: newSubMetrics(opts.Registerer)}
}

// Subscribe implements Subscriber.
func (s *KafkaSubscriber) Subscribe(ctx context.Context, topics []string, h Handler) error {
	go func() {
		for err := range s.group.Errors() {
			s.log.Error("consumer group error", "err", err)
		}
	}()

	gh := &groupHandler{handle: h, opts: s.opts, log: s.log, metrics: s.metrics}
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
	handle  Handler
	opts    Options
	log     *slog.Logger
	metrics *subMetrics
}

func (g *groupHandler) Setup(sess sarama.ConsumerGroupSession) error {
	g.log.Info("partitions assigned", "member", sess.MemberID(), "claims", sess.Claims())
	return nil
}

func (g *groupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (g *groupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ctx := sess.Context()
	g.metrics.initTopic(claim.Topic())
	lag := g.metrics.lag.WithLabelValues(claim.Topic(), partitionLabel(claim.Partition()))
	// The partition may move to another member, which then reports its lag.
	defer g.metrics.lag.DeleteLabelValues(claim.Topic(), partitionLabel(claim.Partition()))
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			start := time.Now()
			if err := g.process(ctx, toRecord(msg)); err != nil {
				// Only happens on shutdown or rebalance: leave the record unmarked
				// so the next owner of the partition receives it again.
				return nil
			}
			g.metrics.duration.WithLabelValues(msg.Topic).Observe(time.Since(start).Seconds())
			lag.Set(float64(max(claim.HighWaterMarkOffset()-msg.Offset-1, 0)))
			// Marked only once handled, so a crash replays the record (at-least-once).
			sess.MarkMessage(msg, "")
		case <-ctx.Done():
			return nil
		}
	}
}

// process runs the handler with retries and hands a record that keeps failing
// to the dead-letter sink. It returns an error only when ctx is done before the
// record was dealt with.
func (g *groupHandler) process(ctx context.Context, rec *Record) error {
	err := g.handleWithRetry(ctx, rec)
	if err == nil {
		g.metrics.records.WithLabelValues(rec.Topic, resultOK).Inc()
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	log := g.log.With("topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset, "err", err)
	if g.opts.DeadLetter == nil {
		log.Error("skip record")
		g.metrics.records.WithLabelValues(rec.Topic, resultSkipped).Inc()
		return nil
	}
	// A record must not be dropped because the dead-letter topic is briefly
	// unavailable, so keep trying and hold the partition until it succeeds.
	for attempt := 0; ; attempt++ {
		dlqErr := g.opts.DeadLetter.Send(ctx, rec, err)
		if dlqErr == nil {
			log.Warn("record sent to dead-letter topic")
			g.metrics.records.WithLabelValues(rec.Topic, resultDeadLetter).Inc()
			return nil
		}
		g.metrics.deadLetterErrors.WithLabelValues(rec.Topic).Inc()
		log.Error("dead-letter send failed", "dlq_err", dlqErr, "attempt", attempt+1)
		if sleepErr := sleep(ctx, backoff(g.retryBackoff(), attempt)); sleepErr != nil {
			return sleepErr
		}
	}
}

func (g *groupHandler) handleWithRetry(ctx context.Context, rec *Record) error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = g.handle(ctx, rec); err == nil || IsPermanent(err) || attempt >= g.opts.MaxRetries {
			return err
		}
		g.metrics.retries.WithLabelValues(rec.Topic).Inc()
		g.log.Warn("retry record", "topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset,
			"attempt", attempt+1, "err", err)
		if sleepErr := sleep(ctx, backoff(g.retryBackoff(), attempt)); sleepErr != nil {
			return err
		}
	}
}

func (g *groupHandler) retryBackoff() time.Duration {
	if g.opts.RetryBackoff > 0 {
		return g.opts.RetryBackoff
	}
	return 100 * time.Millisecond
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
