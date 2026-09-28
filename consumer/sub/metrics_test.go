package sub

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// lagRecorder is a session that captures the lag gauge after each record,
// before ConsumeClaim returns and removes the series.
type lagRecorder struct {
	fakeSession
	m    *subMetrics
	lags []float64
}

func (s *lagRecorder) MarkMessage(m *sarama.ConsumerMessage, md string) {
	s.fakeSession.MarkMessage(m, md)
	s.lags = append(s.lags, testutil.ToFloat64(s.m.lag.WithLabelValues("t", "0")))
}

func TestConsumerMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newSubMetrics(reg)

	claim := &fakeClaim{msgs: make(chan *sarama.ConsumerMessage, 3), hwm: 10}
	for _, off := range []int64{5, 6, 7} {
		claim.msgs <- &sarama.ConsumerMessage{Topic: "t", Offset: off}
	}
	close(claim.msgs)

	calls := 0
	g := &groupHandler{metrics: m, log: discard,
		opts: Options{MaxRetries: 1, RetryBackoff: time.Millisecond, DeadLetter: &fakeDLQ{failures: 1}},
		handle: func(_ context.Context, r *Record) error {
			calls++
			switch r.Offset {
			case 6:
				if calls == 2 {
					return errors.New("transient") // retried once, then succeeds
				}
			case 7:
				return Permanent(errors.New("malformed")) // dead-lettered on the second DLQ attempt
			}
			return nil
		}}
	sess := &lagRecorder{fakeSession: fakeSession{ctx: context.Background()}, m: m}
	if err := g.ConsumeClaim(sess, claim); err != nil {
		t.Fatal(err)
	}

	want := `
# HELP gokafka_consumer_records_total Records consumed, by topic and result (ok, dead_letter, skipped).
# TYPE gokafka_consumer_records_total counter
gokafka_consumer_records_total{result="dead_letter",topic="t"} 1
gokafka_consumer_records_total{result="ok",topic="t"} 2
gokafka_consumer_records_total{result="skipped",topic="t"} 0
# HELP gokafka_consumer_retries_total Handler retries after a transient error.
# TYPE gokafka_consumer_retries_total counter
gokafka_consumer_retries_total{topic="t"} 1
# HELP gokafka_consumer_dead_letter_errors_total Failed attempts to write a record to its dead-letter topic.
# TYPE gokafka_consumer_dead_letter_errors_total counter
gokafka_consumer_dead_letter_errors_total{topic="t"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want),
		"gokafka_consumer_records_total", "gokafka_consumer_retries_total", "gokafka_consumer_dead_letter_errors_total"); err != nil {
		t.Error(err)
	}
	// High watermark 10 is the next offset to be written: after offset 7, 2 records remain.
	if got := sess.lags; len(got) != 3 || got[0] != 4 || got[2] != 2 {
		t.Errorf("lag after each record = %v, want [4 3 2]", got)
	}
	if n := testutil.CollectAndCount(reg, "gokafka_consumer_lag"); n != 0 {
		t.Errorf("lag series should be removed when the claim ends, found %d", n)
	}
}
