package sub

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/tomdong2010/good-gokafka/internal/metrics"
)

// Values of the "result" label on gokafka_consumer_records_total.
const (
	resultOK         = "ok"
	resultDeadLetter = "dead_letter"
	resultSkipped    = "skipped"
)

type subMetrics struct {
	records          *prometheus.CounterVec
	retries          *prometheus.CounterVec
	deadLetterErrors *prometheus.CounterVec
	duration         *prometheus.HistogramVec
	lag              *prometheus.GaugeVec
}

// newSubMetrics registers the consumer metrics with reg; with a nil reg the
// metrics still work but are not exported.
func newSubMetrics(reg prometheus.Registerer) *subMetrics {
	f := promauto.With(reg)
	const sub = "consumer"
	return &subMetrics{
		records: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "records_total",
			Help: "Records consumed, by topic and result (ok, dead_letter, skipped).",
		}, []string{"topic", "result"}),
		retries: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "retries_total",
			Help: "Handler retries after a transient error.",
		}, []string{"topic"}),
		deadLetterErrors: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "dead_letter_errors_total",
			Help: "Failed attempts to write a record to its dead-letter topic.",
		}, []string{"topic"}),
		duration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "processing_duration_seconds",
			Help:    "Time to process a record, including retries and dead-lettering.",
			Buckets: prometheus.ExponentialBuckets(0.0005, 2, 16), // 0.5ms .. ~16s
		}, []string{"topic"}),
		lag: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metrics.Namespace, Subsystem: sub, Name: "lag",
			Help: "Records between the last processed offset and the partition's high watermark.",
		}, []string{"topic", "partition"}),
	}
}

// initTopic creates the series for topic up front, so rate() and
// histogram_quantile() see a zero value before the first record.
func (m *subMetrics) initTopic(topic string) {
	for _, result := range []string{resultOK, resultDeadLetter, resultSkipped} {
		m.records.WithLabelValues(topic, result)
	}
	m.retries.WithLabelValues(topic)
	m.deadLetterErrors.WithLabelValues(topic)
	m.duration.WithLabelValues(topic)
}

func partitionLabel(p int32) string { return strconv.FormatInt(int64(p), 10) }
