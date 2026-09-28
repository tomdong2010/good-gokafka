package sub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeDLQ struct {
	mu       sync.Mutex
	failures int // number of Send calls to fail before succeeding
	sent     []*Record
	calls    int
}

func (d *fakeDLQ) Send(_ context.Context, r *Record, _ error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.calls <= d.failures {
		return errors.New("broker unavailable")
	}
	d.sent = append(d.sent, r)
	return nil
}

func oneRecordClaim() *fakeClaim {
	c := &fakeClaim{msgs: make(chan *sarama.ConsumerMessage, 1)}
	c.msgs <- &sarama.ConsumerMessage{Topic: "t", Offset: 7}
	close(c.msgs)
	return c
}

func TestRetryThenSucceed(t *testing.T) {
	calls := 0
	dlq := &fakeDLQ{}
	g := &groupHandler{metrics: newSubMetrics(nil), log: discard, opts: Options{MaxRetries: 3, RetryBackoff: time.Millisecond, DeadLetter: dlq},
		handle: func(context.Context, *Record) error {
			calls++
			if calls < 3 {
				return errors.New("transient")
			}
			return nil
		}}
	sess := &fakeSession{ctx: context.Background()}
	if err := g.ConsumeClaim(sess, oneRecordClaim()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(dlq.sent) != 0 || len(sess.marked) != 1 {
		t.Fatalf("calls=%d dlq=%d marked=%v", calls, len(dlq.sent), sess.marked)
	}
}

func TestRetriesExhaustedGoToDeadLetter(t *testing.T) {
	calls := 0
	dlq := &fakeDLQ{}
	g := &groupHandler{metrics: newSubMetrics(nil), log: discard, opts: Options{MaxRetries: 2, RetryBackoff: time.Millisecond, DeadLetter: dlq},
		handle: func(context.Context, *Record) error { calls++; return errors.New("transient") }}
	sess := &fakeSession{ctx: context.Background()}
	if err := g.ConsumeClaim(sess, oneRecordClaim()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(dlq.sent) != 1 || len(sess.marked) != 1 {
		t.Fatalf("calls=%d dlq=%d marked=%v", calls, len(dlq.sent), sess.marked)
	}
}

func TestPermanentErrorSkipsRetries(t *testing.T) {
	calls := 0
	dlq := &fakeDLQ{}
	g := &groupHandler{metrics: newSubMetrics(nil), log: discard, opts: Options{MaxRetries: 5, RetryBackoff: time.Millisecond, DeadLetter: dlq},
		handle: func(context.Context, *Record) error { calls++; return Permanent(errors.New("malformed")) }}
	sess := &fakeSession{ctx: context.Background()}
	if err := g.ConsumeClaim(sess, oneRecordClaim()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(dlq.sent) != 1 || len(sess.marked) != 1 {
		t.Fatalf("calls=%d dlq=%d marked=%v", calls, len(dlq.sent), sess.marked)
	}
}

func TestDeadLetterFailureIsRetried(t *testing.T) {
	dlq := &fakeDLQ{failures: 2}
	g := &groupHandler{metrics: newSubMetrics(nil), log: discard, opts: Options{RetryBackoff: time.Millisecond, DeadLetter: dlq},
		handle: func(context.Context, *Record) error { return Permanent(errors.New("malformed")) }}
	sess := &fakeSession{ctx: context.Background()}
	if err := g.ConsumeClaim(sess, oneRecordClaim()); err != nil {
		t.Fatal(err)
	}
	if dlq.calls != 3 || len(dlq.sent) != 1 || len(sess.marked) != 1 {
		t.Fatalf("dlq calls=%d sent=%d marked=%v", dlq.calls, len(dlq.sent), sess.marked)
	}
}

// On shutdown a record that could not be dead-lettered must stay unmarked so
// that it is delivered again.
func TestShutdownLeavesFailedRecordUnmarked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	dlq := &fakeDLQ{failures: 1 << 30}
	g := &groupHandler{metrics: newSubMetrics(nil), log: discard, opts: Options{RetryBackoff: time.Millisecond, DeadLetter: dlq},
		handle: func(context.Context, *Record) error { return Permanent(errors.New("malformed")) }}
	sess := &fakeSession{ctx: ctx}
	if err := g.ConsumeClaim(sess, oneRecordClaim()); err != nil {
		t.Fatal(err)
	}
	if len(sess.marked) != 0 {
		t.Fatalf("marked %v", sess.marked)
	}
}

func TestKafkaDeadLetterSend(t *testing.T) {
	prd := mocks.NewSyncProducer(t, nil)
	prd.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(func(m *sarama.ProducerMessage) error {
		if m.Topic != "orders.dlq" {
			return errors.New("wrong topic " + m.Topic)
		}
		want := map[string]string{
			"content-type":          "application/json",
			HeaderOriginalTopic:     "orders",
			HeaderOriginalPartition: "2",
			HeaderOriginalOffset:    "42",
			HeaderError:             "boom",
		}
		got := map[string]string{}
		for _, h := range m.Headers {
			got[string(h.Key)] = string(h.Value)
		}
		for k, v := range want {
			if got[k] != v {
				return errors.New("header " + k + " = " + got[k])
			}
		}
		return nil
	})
	d := NewKafkaDeadLetter(prd, ".dlq")
	rec := &Record{Topic: "orders", Partition: 2, Offset: 42, Key: []byte("k"), Value: []byte("v"),
		Headers: map[string]string{"content-type": "application/json"}}
	if err := d.Send(context.Background(), rec, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackoff(t *testing.T) {
	if got := backoff(100*time.Millisecond, 3); got != 800*time.Millisecond {
		t.Errorf("backoff(100ms, 3) = %v", got)
	}
	if got := backoff(time.Second, 100); got != maxBackoff {
		t.Errorf("backoff is not capped: %v", got)
	}
}

func TestPermanent(t *testing.T) {
	base := errors.New("x")
	if !IsPermanent(Permanent(base)) || IsPermanent(base) || Permanent(nil) != nil {
		t.Fatal("Permanent/IsPermanent mismatch")
	}
	if !errors.Is(Permanent(base), base) {
		t.Fatal("Permanent must unwrap")
	}
}
