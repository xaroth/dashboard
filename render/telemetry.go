package render

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/xaroth/dashboard/render")

var renderDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "dashboard_render_duration_seconds",
	Help:    "Duration of page renders, from resolving data to the finished image, by result: success or failure.",
	Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
}, []string{"page", "result"})
