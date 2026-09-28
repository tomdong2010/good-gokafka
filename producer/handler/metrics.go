package handler

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tomdong2010/good-gokafka/internal/metrics"
)

// Values of the "result" label on gokafka_producer_messages_total.
const (
	resultSent    = "sent"
	resultInvalid = "invalid"
	resultFailed  = "failed"
)

type handlerMetrics struct {
	messages        *prometheus.CounterVec
	publishDuration *prometheus.HistogramVec
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// newHandlerMetrics registers the producer metrics with reg; with a nil reg
// the metrics still work but are not exported.
func newHandlerMetrics(reg prometheus.Registerer) *handlerMetrics {
	f := promauto.With(reg)
	const sub = "producer"
	return &handlerMetrics{
		messages: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "messages_total",
			Help: "Messages received by the API, by topic and result (sent, invalid, failed).",
		}, []string{"topic", "result"}),
		publishDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "publish_duration_seconds",
			Help:    "Time to publish a record and receive the broker acknowledgement.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms .. ~2s
		}, []string{"topic"}),
		requests: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "http_requests_total",
			Help: "HTTP requests by handler, method and status code.",
		}, []string{"handler", "method", "code"}),
		requestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "http_request_duration_seconds",
			Help:    "HTTP request latency by handler.",
			Buckets: prometheus.DefBuckets,
		}, []string{"handler"}),
	}
}

// init creates the series for topic up front, so rate() and histogram_quantile()
// see a zero value before the first message instead of a missing series.
func (m *handlerMetrics) init(topic string) *handlerMetrics {
	for _, result := range []string{resultSent, resultInvalid, resultFailed} {
		m.messages.WithLabelValues(topic, result)
	}
	m.publishDuration.WithLabelValues(topic)
	return m
}

func (m *handlerMetrics) instrument(name string, next http.Handler) http.Handler {
	labels := prometheus.Labels{"handler": name}
	return promhttp.InstrumentHandlerDuration(m.requestDuration.MustCurryWith(labels),
		promhttp.InstrumentHandlerCounter(m.requests.MustCurryWith(labels), next))
}
