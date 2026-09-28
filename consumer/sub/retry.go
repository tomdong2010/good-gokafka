package sub

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/IBM/sarama"
)

// Headers added to records written to a dead-letter topic.
const (
	HeaderOriginalTopic     = "x-original-topic"
	HeaderOriginalPartition = "x-original-partition"
	HeaderOriginalOffset    = "x-original-offset"
	HeaderError             = "x-error"
)

// maxBackoff caps the delay between retries.
const maxBackoff = 30 * time.Second

type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent marks err as not worth retrying (a malformed record, for example),
// so the record goes straight to the dead-letter topic.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether err was wrapped with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// DeadLetterSink receives records that still fail after all retries.
type DeadLetterSink interface {
	Send(ctx context.Context, r *Record, cause error) error
}

// Options controls how failed records are handled.
type Options struct {
	// MaxRetries is the number of extra attempts for a record whose handler
	// returned a non-permanent error.
	MaxRetries int
	// RetryBackoff is the delay before the first retry; it doubles on each attempt.
	RetryBackoff time.Duration
	// DeadLetter receives records that fail every attempt. When nil they are
	// logged and skipped.
	DeadLetter DeadLetterSink
}

// KafkaDeadLetter writes failed records to "<original topic><Suffix>", keeping
// their key, value and headers and recording where they came from and why.
type KafkaDeadLetter struct {
	producer sarama.SyncProducer
	suffix   string
}

// NewKafkaDeadLetter returns a sink that publishes with producer.
func NewKafkaDeadLetter(producer sarama.SyncProducer, suffix string) *KafkaDeadLetter {
	return &KafkaDeadLetter{producer: producer, suffix: suffix}
}

// Topic returns the dead-letter topic for topic.
func (d *KafkaDeadLetter) Topic(topic string) string { return topic + d.suffix }

// Send implements DeadLetterSink.
func (d *KafkaDeadLetter) Send(_ context.Context, r *Record, cause error) error {
	headers := make([]sarama.RecordHeader, 0, len(r.Headers)+4)
	for k, v := range r.Headers {
		headers = append(headers, sarama.RecordHeader{Key: []byte(k), Value: []byte(v)})
	}
	headers = append(headers,
		sarama.RecordHeader{Key: []byte(HeaderOriginalTopic), Value: []byte(r.Topic)},
		sarama.RecordHeader{Key: []byte(HeaderOriginalPartition), Value: []byte(strconv.FormatInt(int64(r.Partition), 10))},
		sarama.RecordHeader{Key: []byte(HeaderOriginalOffset), Value: []byte(strconv.FormatInt(r.Offset, 10))},
		sarama.RecordHeader{Key: []byte(HeaderError), Value: []byte(cause.Error())},
	)
	msg := &sarama.ProducerMessage{
		Topic:   d.Topic(r.Topic),
		Value:   sarama.ByteEncoder(r.Value),
		Headers: headers,
	}
	if len(r.Key) > 0 {
		msg.Key = sarama.ByteEncoder(r.Key)
	}
	if _, _, err := d.producer.SendMessage(msg); err != nil {
		return fmt.Errorf("send to dead-letter topic %s: %w", msg.Topic, err)
	}
	return nil
}

// Close releases the underlying producer.
func (d *KafkaDeadLetter) Close() error { return d.producer.Close() }

// sleep waits for d or until ctx is done, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func backoff(base time.Duration, attempt int) time.Duration {
	d := base
	for i := 0; i < attempt && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}
