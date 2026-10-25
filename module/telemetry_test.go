package module_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/storage"
)

var telemetryNow = time.Unix(1792087200, 0)

type telemetry struct {
	set   *module.Set
	reg   *prometheus.Registry
	spans *tracetest.SpanRecorder
	tp    *sdktrace.TracerProvider
}

// loadTelemetry loads the "test" module with its own registry and tracer provider.
func loadTelemetry(tb testing.TB, yaml string) telemetry {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	tel := telemetry{reg: prometheus.NewRegistry(), spans: tracetest.NewSpanRecorder()}
	tel.tp = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tel.spans))
	tel.set, err = module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{registration()},
		Configs:       parse(tb, yaml),
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    http.DefaultClient,
		Now:           func() time.Time { return telemetryNow },
		Metrics:       tel.reg,
		Tracing:       tel.tp,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return tel
}

// endedSpan returns the only ended span.
func (tel telemetry) endedSpan(tb testing.TB) sdktrace.ReadOnlySpan {
	tb.Helper()
	ended := tel.spans.Ended()
	if len(ended) != 1 {
		tb.Fatalf("got %d spans, want 1", len(ended))
	}
	return ended[0]
}

func TestUpdateTelemetry(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		fail        string
		result      string
		lastSuccess string
		wantStatus  codes.Code
	}{
		{"success", "", "success", fmt.Sprintf("dashboard_module_last_success_timestamp_seconds{module=\"test\"} %d\n", telemetryNow.Unix()), codes.Unset},
		{"failure", "update", "failure", "", codes.Error},
		{"auth", "auth", "auth", "", codes.Error},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tel := loadTelemetry(t, "test: {key: k, fail: "+testCase.fail+"}")
			tel.set.Update(t.Context(), "test")

			want := "# HELP dashboard_module_updates_total Module updates by result: success, failure or auth.\n" +
				"# TYPE dashboard_module_updates_total counter\n" +
				"dashboard_module_updates_total{module=\"test\",result=\"" + testCase.result + "\"} 1\n"
			if testCase.lastSuccess != "" {
				want += "# HELP dashboard_module_last_success_timestamp_seconds Unix time of the module's last successful update.\n" +
					"# TYPE dashboard_module_last_success_timestamp_seconds gauge\n" + testCase.lastSuccess
			}
			if err := testutil.GatherAndCompare(tel.reg, strings.NewReader(want),
				"dashboard_module_updates_total", "dashboard_module_last_success_timestamp_seconds"); err != nil {
				t.Error(err)
			}
			if got := testutil.CollectAndCount(tel.reg, "dashboard_module_update_duration_seconds"); got != 1 {
				t.Errorf("got %d update duration series, want 1", got)
			}
			span := tel.endedSpan(t)
			if got, want := span.Name(), "test.update"; got != want {
				t.Errorf("got span %q, want %q", got, want)
			}
			// An update outside a request, like a scheduled one, starts a trace.
			if span.Parent().IsValid() {
				t.Errorf("got parent %v, want a root span", span.Parent().SpanID())
			}
			if got := span.Status().Code; got != testCase.wantStatus {
				t.Errorf("got status %v, want %v", got, testCase.wantStatus)
			}
		})
	}
}

func TestResolveSpan(t *testing.T) {
	t.Parallel()
	tel := loadTelemetry(t, "test: {key: k}")
	ctx, parent := tel.tp.Tracer("test").Start(t.Context(), "page")
	tel.set.Resolve(ctx, "test", "missing", nil)
	parent.End()

	var span sdktrace.ReadOnlySpan
	for _, s := range tel.spans.Ended() {
		if s.Name() == "test.missing" {
			span = s
		}
	}
	if span == nil {
		t.Fatal("got no span test.missing")
	}
	if span.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("got parent %v, want the caller's span %v", span.Parent().SpanID(), parent.SpanContext().SpanID())
	}
	if got := span.Status().Code; got != codes.Error {
		t.Errorf("got status %v, want %v for an unknown query", got, codes.Error)
	}
}

func TestLoadMetricsRegistered(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	for i, wantErr := range []bool{false, true} {
		db, err := storage.Open(t.Context(), t.TempDir())
		if err != nil {
			t.Fatalf("storage: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		_, err = module.Load(t.Context(), module.Options{
			Registrations: []module.Registration{registration()},
			Migrator:      db,
			Writer:        db.Writer,
			Reader:        db.Reader,
			Metrics:       reg,
		})
		if got := err != nil; got != wantErr {
			t.Errorf("load %d: got err %v, want error %v", i, err, wantErr)
		}
	}
}
