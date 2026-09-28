// Package handler exposes the producer over HTTP.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

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
	metrics   *handlerMetrics
}

// Options holds the optional dependencies of HTTPHandler.
type Options struct {
	Logger *slog.Logger // defaults to slog.Default()
	// Registerer receives the handler's Prometheus metrics. When nil the
	// metrics are collected but not exported.
	Registerer prometheus.Registerer
}

// NewHTTPHandler returns a handler that publishes to topic using codec.
func NewHTTPHandler(topic string, publisher pub.Publisher, codec message.Codec, opts Options) *HTTPHandler {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &HTTPHandler{
		topic:     topic,
		publisher: publisher,
		codec:     codec,
		log:       log,
		metrics:   newHandlerMetrics(opts.Registerer).init(topic),
	}
}

// Routes registers the handler's endpoints.
func (h *HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /api/send", h.metrics.instrument("send", http.HandlerFunc(h.publish)))
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
		h.metrics.messages.WithLabelValues(h.topic, resultInvalid).Inc()
		writeJSON(w, status, response{Error: "invalid request body: " + err.Error()})
		return
	}
	if err := msg.Validate(); err != nil {
		h.metrics.messages.WithLabelValues(h.topic, resultInvalid).Inc()
		writeJSON(w, http.StatusUnprocessableEntity, response{Error: err.Error()})
		return
	}

	value, err := h.codec.Encode(&msg)
	if err != nil {
		h.log.Error("encode message", "err", err)
		h.metrics.messages.WithLabelValues(h.topic, resultFailed).Inc()
		writeJSON(w, http.StatusInternalServerError, response{Error: "cannot encode message"})
		return
	}

	start := time.Now()
	res, err := h.publisher.Publish(r.Context(), pub.Record{
		Topic:       h.topic,
		Key:         []byte(msg.From),
		Value:       value,
		ContentType: h.codec.ContentType(),
	})
	h.metrics.publishDuration.WithLabelValues(h.topic).Observe(time.Since(start).Seconds())
	if err != nil {
		h.metrics.messages.WithLabelValues(h.topic, resultFailed).Inc()
		// Broker errors can reveal internal addresses, so keep them in the log only.
		h.log.Error("publish message", "topic", h.topic, "err", err)
		writeJSON(w, http.StatusBadGateway, response{Error: "cannot publish message"})
		return
	}

	h.metrics.messages.WithLabelValues(h.topic, resultSent).Inc()
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
