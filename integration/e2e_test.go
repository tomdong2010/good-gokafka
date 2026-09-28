//go:build integration

// Package integration runs the producer and consumer against a real broker:
//
//	docker compose up -d kafka
//	go test -tags integration ./integration/...
package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"

	"github.com/tomdong2010/good-gokafka/consumer/handler"
	"github.com/tomdong2010/good-gokafka/consumer/sub"
	"github.com/tomdong2010/good-gokafka/internal/kafka"
	"github.com/tomdong2010/good-gokafka/internal/message"
	prodhandler "github.com/tomdong2010/good-gokafka/producer/handler"
	"github.com/tomdong2010/good-gokafka/producer/pub"
)

var log = slog.New(slog.NewTextHandler(io.Discard, nil))

func brokers(t *testing.T) []string {
	t.Helper()
	b := os.Getenv("KAFKA_BROKERS")
	if b == "" {
		b = "localhost:9092"
	}
	return strings.Split(b, ",")
}

func uniqueName(t *testing.T) string {
	return fmt.Sprintf("it-%s-%d", strings.ToLower(t.Name()), time.Now().UnixNano())
}

// collector is a sub.Handler that records every message it successfully decodes.
type collector struct {
	mu   sync.Mutex
	got  map[string]*message.Message // by From
	next sub.Handler
}

func (c *collector) handle(ctx context.Context, r *sub.Record) error {
	if err := c.next(ctx, r); err != nil {
		return err
	}
	msg, _ := message.CodecForContentType(r.Headers[message.ContentTypeHeader], message.ProtoCodec{}).Decode(r.Value)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got[msg.From] = msg
	return nil
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// subscribe runs a consumer group on topics until the test ends.
func subscribe(t *testing.T, topics []string, opts sub.Options, h sub.Handler) {
	t.Helper()
	s, err := sub.NewKafkaSubscriber(brokers(t), uniqueName(t), kafka.ConsumerConfig("it"), opts, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Subscribe(ctx, topics, h); err != nil {
			t.Errorf("subscribe: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = s.Close()
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Messages sent through the HTTP API in both formats reach the consumer.
func TestHTTPToConsumer(t *testing.T) {
	topic := uniqueName(t)
	publisher, err := pub.NewKafkaPublisher(brokers(t), kafka.ProducerConfig("it"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publisher.Close() })

	c := &collector{got: map[string]*message.Message{}, next: handler.NewWorkerHandler(message.ProtoCodec{}, log).Handle}
	subscribe(t, []string{topic}, sub.Options{}, c.handle)

	const n = 20
	for i, codec := range []message.Codec{message.ProtoCodec{}, message.JSONCodec{}} {
		srv := httptest.NewServer(prodhandler.NewHTTPHandler(topic, publisher, codec, prodhandler.Options{Logger: log}).Routes())
		for j := 0; j < n/2; j++ {
			body := fmt.Sprintf(`{"from":"user-%d-%d","content":{"header":"h","body":"b"}}`, i, j)
			resp, err := http.Post(srv.URL+"/api/send", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %d: %s", resp.StatusCode, b)
			}
			_ = resp.Body.Close()
		}
		srv.Close()
	}

	eventually(t, fmt.Sprintf("%d messages", n), func() bool { return c.count() == n })
}

// A record that cannot be decoded ends up on the dead-letter topic with its
// origin recorded in headers, and does not block the records behind it.
func TestPoisonRecordGoesToDeadLetter(t *testing.T) {
	topic := uniqueName(t)
	producer, err := sarama.NewSyncProducer(brokers(t), kafka.ProducerConfig("it"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Close() })

	good, _ := message.ProtoCodec{}.Encode(&message.Message{From: "after-poison", Content: message.Content{Body: "ok"}})
	for _, v := range [][]byte{[]byte("not-a-protobuf-{{{"), good} {
		// Same key, so both land on one partition in this order.
		if _, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder("k"), Value: sarama.ByteEncoder(v)}); err != nil {
			t.Fatal(err)
		}
	}

	var (
		mu         sync.Mutex
		deadLetter []*sub.Record
	)
	subscribe(t, []string{topic + ".dlq"}, sub.Options{}, func(_ context.Context, r *sub.Record) error {
		mu.Lock()
		defer mu.Unlock()
		deadLetter = append(deadLetter, r)
		return nil
	})

	dlqProducer, err := sarama.NewSyncProducer(brokers(t), kafka.ProducerConfig("it-dlq"))
	if err != nil {
		t.Fatal(err)
	}
	opts := sub.Options{MaxRetries: 2, RetryBackoff: 10 * time.Millisecond, DeadLetter: sub.NewKafkaDeadLetter(dlqProducer, ".dlq")}
	t.Cleanup(func() { _ = dlqProducer.Close() })
	c := &collector{got: map[string]*message.Message{}, next: handler.NewWorkerHandler(message.ProtoCodec{}, log).Handle}
	subscribe(t, []string{topic}, opts, c.handle)

	eventually(t, "record after the poison one", func() bool { return c.count() == 1 })
	eventually(t, "dead-lettered record", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(deadLetter) == 1
	})
	mu.Lock()
	r := deadLetter[0]
	mu.Unlock()
	if !bytes.Equal(r.Value, []byte("not-a-protobuf-{{{")) || r.Headers[sub.HeaderOriginalTopic] != topic || r.Headers[sub.HeaderError] == "" {
		t.Fatalf("dead-letter record = %+v", r)
	}
}
