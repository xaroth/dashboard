package module

import (
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type metrics struct {
	updatesTotal   *prometheus.CounterVec
	updateDuration *prometheus.HistogramVec
	lastSuccess    *prometheus.GaugeVec
}

// newMetrics registers the module metrics with reg; nil registers nowhere.
func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		updatesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dashboard_module_updates_total",
			Help: "Module updates by result: success, failure or auth.",
		}, []string{"module", "result"}),
		updateDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dashboard_module_update_duration_seconds",
			Help:    "Duration of module updates.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"module"}),
		lastSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dashboard_module_last_success_timestamp_seconds",
			Help: "Unix time of the module's last successful update.",
		}, []string{"module"}),
	}
	if reg == nil {
		return m, nil
	}
	for _, c := range []prometheus.Collector{m.updatesTotal, m.updateDuration, m.lastSuccess} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// newTracer returns the package's tracer from tp; nil is a no-op.
func newTracer(tp trace.TracerProvider) trace.Tracer {
	if tp == nil {
		tp = noop.NewTracerProvider()
	}
	return tp.Tracer("github.com/xaroth/dashboard/module")
}
