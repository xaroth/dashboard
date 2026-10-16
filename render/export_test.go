package render

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// SetTimeouts shortens the response timeouts, so tests need not wait 30 s.
func SetTimeouts(p *Process, render, describe time.Duration) {
	p.renderTimeout, p.describeTimeout = render, describe
}

// SetClock replaces the clock of response timeouts and restart backoff. Call
// before the first request.
func SetClock(p *Process, now func() time.Time, after func(time.Duration) <-chan time.Time) {
	p.now, p.after = now, after
}

// RenderCount is how many renders of page ended with result.
func RenderCount(page, result string) uint64 {
	var m dto.Metric
	renderDuration.WithLabelValues(page, result).(prometheus.Histogram).Write(&m)
	return m.GetHistogram().GetSampleCount()
}
