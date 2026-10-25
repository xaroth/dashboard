package render

import "time"

// SetTimeouts shortens the response timeouts, so tests need not wait 30 s.
func SetTimeouts(p *Process, render, describe time.Duration) {
	p.renderTimeout, p.describeTimeout = render, describe
}

// SetClock replaces the clock of response timeouts and restart backoff. Call
// before the first request.
func SetClock(p *Process, now func() time.Time, after func(time.Duration) <-chan time.Time) {
	p.now, p.after = now, after
}
