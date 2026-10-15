package device

import "time"

// minSleep is the shortest main-page sleep. A device that wakes just before a
// wake time already shows current data, so it sleeps until the one after.
const minSleep = time.Minute

// Schedule is when devices wake on their timer: every Interval from First up
// to and including Last, in local time. A Last at or before First is on the
// next day.
type Schedule struct {
	First    time.Duration // since local midnight
	Last     time.Duration // since local midnight
	Interval time.Duration
	// Detail is how long a page shown after a button press stays up.
	Detail   time.Duration
	Location *time.Location
}

// next returns the first wake time after t.
func (s Schedule) next(t time.Time) time.Time {
	t = t.In(s.Location)
	last := s.Last
	if last <= s.First {
		last += 24 * time.Hour
	}
	// Yesterday's window can reach past midnight into today.
	y, m, d := t.AddDate(0, 0, -1).Date()
	for day := range 3 {
		for off := s.First; off <= last; off += s.Interval {
			// Built from wall-clock fields, so DST shifts the night sleep, not the wakes.
			w := time.Date(y, m, d+day, 0, int(off/time.Minute), 0, 0, s.Location)
			if w.After(t) {
				return w
			}
		}
	}
	panic("device: schedule has no wake within two days")
}

// refresh is the device's sleep in seconds after showing a page at now.
func (s Schedule) refresh(now time.Time, detail bool) int {
	sleep := s.next(now.Add(minSleep)).Sub(now)
	if detail {
		sleep = min(s.Detail, s.next(now).Sub(now))
	}
	return int(sleep.Round(time.Second) / time.Second)
}
