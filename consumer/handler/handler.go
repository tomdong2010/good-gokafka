// Package handler processes messages consumed from Kafka.
package handler

import (
	"context"
	"log/slog"

	"github.com/tomdong2010/good-gokafka/consumer/sub"
	"github.com/tomdong2010/good-gokafka/internal/message"
)

// WorkerHandler decodes records into messages and logs them.
type WorkerHandler struct {
	codec message.Codec
	log   *slog.Logger
}

// NewWorkerHandler returns a handler that decodes records with codec unless the
// record's content-type header names another supported format.
func NewWorkerHandler(codec message.Codec, log *slog.Logger) *WorkerHandler {
	if log == nil {
		log = slog.Default()
	}
	return &WorkerHandler{codec: codec, log: log}
}

// Handle implements sub.Handler.
func (h *WorkerHandler) Handle(_ context.Context, r *sub.Record) error {
	codec := message.CodecForContentType(r.Headers[message.ContentTypeHeader], h.codec)
	msg, err := codec.Decode(r.Value)
	if err != nil {
		// Retrying cannot fix a malformed record.
		return sub.Permanent(err)
	}

	h.log.Info("message received",
		"topic", r.Topic,
		"partition", r.Partition,
		"offset", r.Offset,
		"key", string(r.Key),
		"from", msg.From,
		"header", msg.Content.Header,
		"body", msg.Content.Body,
	)
	return nil
}
