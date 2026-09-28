// Package handler exposes the producer over HTTP.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/tomdong2010/good-gokafka/internal/message"
	"github.com/tomdong2010/good-gokafka/producer/pub"
)

// MaxRequestBytes caps the size of a request body.
const MaxRequestBytes = 1 << 20

// HTTPHandler publishes messages received over HTTP.
type HTTPHandler struct {
	topic     string
	publisher pub.Publisher
	codec     message.Codec
	log       *slog.Logger
}

// NewHTTPHandler returns a handler that publishes to topic using codec.
func NewHTTPHandler(topic string, publisher pub.Publisher, codec message.Codec, log *slog.Logger) *HTTPHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HTTPHandler{topic: topic, publisher: publisher, codec: codec, log: log}
}

// Routes registers the handler's endpoints.
func (h *HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/send", h.publish)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, response{Message: "ok"})
	})
	return mux
}

type response struct {
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	*pub.Result
}

func (h *HTTPHandler) publish(w http.ResponseWriter, r *http.Request) {
	var msg message.Message
	if err := decodeJSON(w, r, &msg); err != nil {
		h.log.Info("reject request", "err", err)
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, response{Error: "invalid request body: " + err.Error()})
		return
	}
	if err := msg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, response{Error: err.Error()})
		return
	}

	value, err := h.codec.Encode(&msg)
	if err != nil {
		h.log.Error("encode message", "err", err)
		writeJSON(w, http.StatusInternalServerError, response{Error: "cannot encode message"})
		return
	}

	res, err := h.publisher.Publish(r.Context(), pub.Record{
		Topic:       h.topic,
		Key:         []byte(msg.From),
		Value:       value,
		ContentType: h.codec.ContentType(),
	})
	if err != nil {
		// Broker errors can reveal internal addresses, so keep them in the log only.
		h.log.Error("publish message", "topic", h.topic, "err", err)
		writeJSON(w, http.StatusBadGateway, response{Error: "cannot publish message"})
		return
	}

	h.log.Debug("message published", "topic", h.topic, "partition", res.Partition, "offset", res.Offset)
	writeJSON(w, http.StatusOK, response{Message: "message sent", Result: &res})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
