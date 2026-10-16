// package main cannot be imported, so its tests are internal.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// spans records every span of the package's tests. Only telemetry tests
// call instrument, so request spans are theirs.
var spans = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	os.Exit(m.Run())
}

// requestCount is how many requests the duration metric holds for the labels.
func requestCount(listener, route, code string) uint64 {
	var m dto.Metric
	requestDuration.WithLabelValues(listener, route, code).(prometheus.Histogram).Write(&m)
	return m.GetHistogram().GetSampleCount()
}

func serve(h http.Handler, method, url string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestInstrumentMetrics(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	h := instrument("metrics_test", s.device)
	serve(h, http.MethodGet, "/api/setup", map[string]string{"ID": "AA:BB:CC:DD:EE:FF"})
	serve(h, http.MethodGet, "/nope", nil)

	if got := requestCount("metrics_test", "GET /api/setup", "200"); got != 1 {
		t.Errorf("got %d setup requests, want 1", got)
	}
	if got := requestCount("metrics_test", "unmatched", "404"); got != 1 {
		t.Errorf("got %d unmatched requests, want 1", got)
	}
	body := get(t, s.Admin(), "/metrics").Body.String()
	if want := `listener="metrics_test"`; !strings.Contains(body, want) {
		t.Errorf("got /metrics without %s", want)
	}
}

func TestInstrumentTraces(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	device := instrument("device", s.device)
	admin := instrument("admin", s.Admin())

	setup := serve(device, http.MethodGet, "/api/setup", map[string]string{"ID": "AA:BB:CC:DD:EE:FF"})
	var registered struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &registered); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if rec := serve(device, http.MethodGet, "/api/display", map[string]string{"ID": "AA:BB:CC:DD:EE:FF", "Access-Token": registered.APIKey}); rec.Code != http.StatusOK {
		t.Fatalf("display: got %d %s", rec.Code, rec.Body)
	}
	serve(admin, http.MethodGet, "/healthz", nil)
	serve(admin, http.MethodGet, "/metrics", nil)

	var display trace.SpanContext
	for _, span := range spans.Ended() {
		switch span.Name() {
		case "GET /api/display":
			display = span.SpanContext()
		case "GET /healthz", "GET /metrics":
			t.Errorf("got span %q, want probes and scrapes untraced", span.Name())
		}
	}
	if !display.IsValid() {
		t.Fatal("got no span for the display request")
	}
	for _, span := range spans.Ended() {
		if span.Name() == "render e1001/hello" && span.Parent().SpanID() == display.SpanID() {
			return
		}
	}
	t.Error("got no render span under the display request")
}
