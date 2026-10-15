package render

import "time"

// SetTimeouts shortens the response timeouts, so tests need not wait 30 s.
func SetTimeouts(p *Process, render, describe time.Duration) {
	p.renderTimeout, p.describeTimeout = render, describe
}
