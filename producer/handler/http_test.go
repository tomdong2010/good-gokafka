package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/tomdong2010/good-gokafka/internal/message"
	"github.com/tomdong2010/good-gokafka/producer/pub"
)

type fakePublisher struct {
	records []pub.Record
	err     error
}

func (f *fakePublisher) Publish(_ context.Context, r pub.Record) (pub.Result, error) {
	if f.err != nil {
		return pub.Result{}, f.err
	}
	f.records = append(f.records, r)
	return pub.Result{Partition: 1, Offset: 42}, nil
}

func (f *fakePublisher) Close() error { return nil }

const validBody = `{"from":"Wuriyanto","content":{"header":"This is Message 2","body":"Hello Kafka"}}`

func do(t *testing.T, p pub.Publisher, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHTTPHandler("topic", p, message.JSONCodec{}, Options{Logger: log}).Routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var resp map[string]any
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid JSON response %q: %v", rec.Body, err)
		}
	}
	return rec, resp
}

func TestPublishOK(t *testing.T) {
	p := &fakePublisher{}
	rec, resp := do(t, p, http.MethodPost, "/api/send", validBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if resp["partition"] != 1.0 || resp["offset"] != 42.0 {
		t.Errorf("response = %v", resp)
	}
	if len(p.records) != 1 {
		t.Fatalf("published %d records", len(p.records))
	}
	r := p.records[0]
	if r.Topic != "topic" || string(r.Key) != "Wuriyanto" || r.ContentType != "application/json" {
		t.Errorf("record = %+v", r)
	}
	got, err := message.JSONCodec{}.Decode(r.Value)
	if err != nil || got.Content.Body != "Hello Kafka" {
		t.Errorf("value decodes to %+v, %v", got, err)
	}
}

func TestPublishRejects(t *testing.T) {
	tests := []struct {
		name, method, body string
		status             int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"malformed json", http.MethodPost, `{"from":`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, `{"from":"a","content":{"body":"b"},"extra":1}`, http.StatusBadRequest},
		{"trailing data", http.MethodPost, validBody + validBody, http.StatusBadRequest},
		{"missing body", http.MethodPost, `{"from":"a","content":{}}`, http.StatusUnprocessableEntity},
		{"too large", http.MethodPost, `{"from":"` + strings.Repeat("x", MaxRequestBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakePublisher{}
			rec, _ := do(t, p, tt.method, "/api/send", tt.body)
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body)
			}
			if len(p.records) != 0 {
				t.Errorf("published %d records", len(p.records))
			}
		})
	}
}

func TestPublishBrokerError(t *testing.T) {
	rec, resp := do(t, &fakePublisher{err: errors.New("dial tcp 10.0.0.1:9092: refused")}, http.MethodPost, "/api/send", validBody)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.1") {
		t.Errorf("response leaks broker error: %v", resp)
	}
}

func TestHealthz(t *testing.T) {
	if rec, _ := do(t, &fakePublisher{}, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHTTPHandler("topic", &fakePublisher{}, message.JSONCodec{}, Options{Logger: log, Registerer: reg}).Routes()
	for _, body := range []string{validBody, validBody, `{"from":"a","content":{}}`} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(body)))
	}

	want := `
# HELP gokafka_producer_messages_total Messages received by the API, by topic and result (sent, invalid, failed).
# TYPE gokafka_producer_messages_total counter
gokafka_producer_messages_total{result="failed",topic="topic"} 0
gokafka_producer_messages_total{result="invalid",topic="topic"} 1
gokafka_producer_messages_total{result="sent",topic="topic"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "gokafka_producer_messages_total"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(reg, "gokafka_producer_http_requests_total"); n != 2 { // codes 200 and 422
		t.Errorf("http_requests_total has %d series, want 2", n)
	}
	if n := testutil.CollectAndCount(reg, "gokafka_producer_publish_duration_seconds"); n != 1 {
		t.Errorf("publish_duration_seconds has %d series, want 1", n)
	}
}
