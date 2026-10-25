package device

import "time"

// minSleep is the shortest main-page sleep. A device that wakes just before a
// wake time already shows current data, so it sleeps until the one after.
const minSleep = time.Minute

// Schedule is when devices wake on their timer: every Battery or USB
// interval, by the device's power, from First up to and including Last, in
// local time. A Last at or before First is on the next day.
type Schedule struct {
	First   time.Duration // since local midnight
	Last    time.Duration // since local midnight
	Battery time.Duration
	USB     time.Duration
	// Detail is how long a page shown after a button press stays up.
	Detail   time.Duration
	Location *time.Location
}

// next returns the first wake time after t, every interval from First.
func (s Schedule) next(t time.Time, interval time.Duration) time.Time {
	t = t.In(s.Location)
	last := s.Last
	if last <= s.First {
		last += 24 * time.Hour
	}
	// Yesterday's window can reach past midnight into today.
	y, m, d := t.AddDate(0, 0, -1).Date()
	for day := range 3 {
		for off := s.First; off <= last; off += interval {
			// Built from wall-clock fields, so DST shifts the night sleep, not the wakes.
			w := time.Date(y, m, d+day, 0, int(off/time.Minute), 0, 0, s.Location)
			if w.After(t) {
				return w
			}
		}
	}
	panic("device: schedule has no wake within two days")
}

// interval is the time between timer wakes on USB or battery power.
func (s Schedule) interval(usb bool) time.Duration {
	if usb {
		return s.USB
	}
	return s.Battery
}

// refresh is the device's sleep in whole seconds, at least 1, after showing a
// page at now with timer wakes every interval.
func (s Schedule) refresh(now time.Time, detail bool, interval time.Duration) int {
	var sleep time.Duration
	if detail {
		sleep = min(s.Detail, s.next(now, interval).Sub(now))
	} else {
		sleep = s.next(now.Add(minSleep), interval).Sub(now)
	}
	return max(1, int((sleep+time.Second-1)/time.Second))
}
