package pub

import (
	"context"
	"errors"
	"testing"

	"github.com/IBM/sarama"
	"github.com/IBM/sarama/mocks"
)

func TestPublish(t *testing.T) {
	prd := mocks.NewSyncProducer(t, nil)
	prd.ExpectSendMessageWithMessageCheckerFunctionAndSucceed(func(m *sarama.ProducerMessage) error {
		key, _ := m.Key.Encode()
		if m.Topic != "t" || string(key) != "k" {
			return errors.New("unexpected topic or key")
		}
		if len(m.Headers) != 1 || string(m.Headers[0].Key) != "content-type" || string(m.Headers[0].Value) != "application/json" {
			return errors.New("missing content-type header")
		}
		return nil
	})

	p := NewKafkaPublisherFrom(prd)
	if _, err := p.Publish(context.Background(), Record{Topic: "t", Key: []byte("k"), Value: []byte("v"), ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishError(t *testing.T) {
	prd := mocks.NewSyncProducer(t, nil)
	prd.ExpectSendMessageAndFail(sarama.ErrNotLeaderForPartition)

	p := NewKafkaPublisherFrom(prd)
	t.Cleanup(func() { _ = p.Close() })
	if _, err := p.Publish(context.Background(), Record{Topic: "t", Value: []byte("v")}); !errors.Is(err, sarama.ErrNotLeaderForPartition) {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishCancelledContext(t *testing.T) {
	p := NewKafkaPublisherFrom(mocks.NewSyncProducer(t, nil))
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Publish(ctx, Record{Topic: "t"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
