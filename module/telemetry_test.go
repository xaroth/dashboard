package module_test

import (
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xaroth/dashboard/module"
)

// spans records every span of the package's tests; tests tell theirs apart by module name.
var spans = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	os.Exit(m.Run())
}

// endedSpan returns the ended span named name.
func endedSpan(tb testing.TB, name string) sdktrace.ReadOnlySpan {
	tb.Helper()
	for _, s := range spans.Ended() {
		if s.Name() == name {
			return s
		}
	}
	tb.Fatalf("got no span %q", name)
	return nil
}

func TestUpdateTelemetry(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		module     string
		fail       string
		result     string
		wantStatus codes.Code
	}{
		{"success", "metrics_success", "", "success", codes.Unset},
		{"failure", "metrics_failure", "update", "failure", codes.Error},
		{"auth", "metrics_auth", "auth", "auth", codes.Error},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			yaml := testCase.module + ": {key: k, fail: " + testCase.fail + "}"
			set, _ := loadRegs(t.Context(), t, yaml, []module.Registration{namedRegistration(testCase.module)})
			set.Update(t.Context(), testCase.module)

			if got := module.UpdateCount(testCase.module, testCase.result); got != 1 {
				t.Errorf("got %v %s updates, want 1", got, testCase.result)
			}
			if got := module.LastSuccess(testCase.module) != 0; got != (testCase.result == "success") {
				t.Errorf("got last success set %v, want %v", got, testCase.result == "success")
			}
			span := endedSpan(t, testCase.module+".update")
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
	set, _ := loadRegs(t.Context(), t, "metrics_query: {key: k}", []module.Registration{namedRegistration("metrics_query")})
	ctx, parent := otel.Tracer("test").Start(t.Context(), "page")
	set.Resolve(ctx, "metrics_query", "missing", nil)
	parent.End()

	span := endedSpan(t, "metrics_query.missing")
	if span.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("got parent %v, want the caller's span %v", span.Parent().SpanID(), parent.SpanContext().SpanID())
	}
	if got := span.Status().Code; got != codes.Error {
		t.Errorf("got status %v, want %v for an unknown query", got, codes.Error)
	}
}
