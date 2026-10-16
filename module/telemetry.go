package module

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/xaroth/dashboard/module")

var (
	updatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dashboard_module_updates_total",
		Help: "Module updates by result: success, failure or auth.",
	}, []string{"module", "result"})
	updateDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dashboard_module_update_duration_seconds",
		Help:    "Duration of module updates.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
	}, []string{"module"})
	lastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dashboard_module_last_success_timestamp_seconds",
		Help: "Unix time of the module's last successful update.",
	}, []string{"module"})
)
