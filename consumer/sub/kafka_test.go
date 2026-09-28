package sub

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/IBM/sarama"
)

type fakeSession struct {
	sarama.ConsumerGroupSession // unimplemented methods panic
	ctx                         context.Context
	marked                      []int64
}

func (s *fakeSession) Context() context.Context { return s.ctx }
func (s *fakeSession) MarkMessage(m *sarama.ConsumerMessage, _ string) {
	s.marked = append(s.marked, m.Offset)
}

type fakeClaim struct {
	sarama.ConsumerGroupClaim
	msgs chan *sarama.ConsumerMessage
	hwm  int64
}

func (c *fakeClaim) Messages() <-chan *sarama.ConsumerMessage { return c.msgs }
func (c *fakeClaim) Topic() string                            { return "t" }
func (c *fakeClaim) Partition() int32                         { return 0 }
func (c *fakeClaim) HighWaterMarkOffset() int64               { return c.hwm }

func TestConsumeClaimMarksEveryRecord(t *testing.T) {
	claim := &fakeClaim{msgs: make(chan *sarama.ConsumerMessage, 3)}
	for i := int64(0); i < 3; i++ {
		claim.msgs <- &sarama.ConsumerMessage{
			Topic:   "t",
			Offset:  i,
			Value:   []byte{byte(i)},
			Headers: []*sarama.RecordHeader{{Key: []byte("content-type"), Value: []byte("application/json")}},
		}
	}
	close(claim.msgs)

	var seen []int64
	g := &groupHandler{metrics: newSubMetrics(nil),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		handle: func(_ context.Context, r *Record) error {
			seen = append(seen, r.Offset)
			if r.Headers["content-type"] != "application/json" {
				t.Errorf("headers = %v", r.Headers)
			}
			if r.Offset == 1 {
				return errors.New("poison record")
			}
			return nil
		},
	}
	sess := &fakeSession{ctx: context.Background()}
	if err := g.ConsumeClaim(sess, claim); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 3 {
		t.Errorf("handled offsets %v", seen)
	}
	// The failing record is skipped rather than blocking the partition.
	if len(sess.marked) != 3 || sess.marked[2] != 2 {
		t.Errorf("marked offsets %v", sess.marked)
	}
}

func TestConsumeClaimStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := &groupHandler{metrics: newSubMetrics(nil), log: slog.Default(), handle: func(context.Context, *Record) error { return nil }}
	if err := g.ConsumeClaim(&fakeSession{ctx: ctx}, &fakeClaim{msgs: make(chan *sarama.ConsumerMessage)}); err != nil {
		t.Fatal(err)
	}
}
