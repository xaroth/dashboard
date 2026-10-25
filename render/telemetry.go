package render

import (
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// newRenderDuration registers the render histogram with reg; nil registers nowhere.
func newRenderDuration(reg prometheus.Registerer) (*prometheus.HistogramVec, error) {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dashboard_render_duration_seconds",
		Help:    "Duration of page renders, from resolving data to the finished image, by result: success or failure.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"page", "result"})
	if reg == nil {
		return h, nil
	}
	if err := reg.Register(h); err != nil {
		return nil, err
	}
	return h, nil
}

// newTracer returns the package's tracer from tp; nil is a no-op.
func newTracer(tp trace.TracerProvider) trace.Tracer {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return tp.Tracer("github.com/xaroth/dashboard/render")
}
