package series

import (
	"cmp"
	"fmt"
	"time"
)

// maxHistoryHours is the 5-minute retention: older steps come from hours.
const maxHistoryHours = 90 * 24

// HistoryParams are the params of a module query for a series up to now.
type HistoryParams struct {
	// Hours back from now; default 24, at most 2160 (90 days).
	Hours int `json:"hours"`
	// StepMinutes is the bucket size, dividing a day; default 15.
	StepMinutes int `json:"stepMinutes"`
}

// Range returns the from, to and step to pass to Query for p at now. to is
// the second after now's: samples are stored in whole seconds, so the
// bucket holding now is the last and has the latest sample.
func (p HistoryParams) Range(now time.Time) (from, to time.Time, step time.Duration, err error) {
	hours, minutes := cmp.Or(p.Hours, 24), cmp.Or(p.StepMinutes, 15)
	if hours < 1 || hours > maxHistoryHours {
		return from, to, step, fmt.Errorf("hours must be 1 to %d, got %d", maxHistoryHours, hours)
	}
	if minutes < 1 || minutes > 1440 || 1440%minutes != 0 {
		return from, to, step, fmt.Errorf("stepMinutes must divide 1440, got %d", minutes)
	}
	return now.Add(-time.Duration(hours) * time.Hour), now.Truncate(time.Second).Add(time.Second), time.Duration(minutes) * time.Minute, nil
}
