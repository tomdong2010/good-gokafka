package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tomdong2010/good-gokafka/consumer/sub"
	"github.com/tomdong2010/good-gokafka/internal/message"
)

func TestHandle(t *testing.T) {
	msg := &message.Message{From: "a", Content: message.Content{Header: "h", Body: "b"}}
	protoBytes, _ := message.ProtoCodec{}.Encode(msg)
	jsonBytes, _ := message.JSONCodec{}.Encode(msg)

	h := NewWorkerHandler(message.ProtoCodec{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tests := []struct {
		name    string
		rec     *sub.Record
		wantErr bool
	}{
		{"default codec", &sub.Record{Value: protoBytes}, false},
		{"codec from header", &sub.Record{Value: jsonBytes, Headers: map[string]string{message.ContentTypeHeader: "application/json"}}, false},
		{"json without header", &sub.Record{Value: jsonBytes}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.Handle(context.Background(), tt.rec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Handle() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !sub.IsPermanent(err) {
				t.Fatalf("decode error %v should be permanent", err)
			}
		})
	}
}

func TestSimulateFailures(t *testing.T) {
	ok := func(context.Context, *sub.Record) error { return nil }
	if err := SimulateFailures(ok, 1)(context.Background(), &sub.Record{}); !errors.Is(err, ErrSimulated) {
		t.Errorf("rate 1: err = %v", err)
	}
	if err := SimulateFailures(ok, 0)(context.Background(), &sub.Record{}); err != nil {
		t.Errorf("rate 0: err = %v", err)
	}
	if err := SimulateFailures(ok, 1)(context.Background(), &sub.Record{}); sub.IsPermanent(err) {
		t.Error("simulated failures must be retryable")
	}
}
