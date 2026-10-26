// Package calendar reads named iCalendar (.ics) feeds, such as Google
// Calendar's secret address in iCal format, and expands their recurring
// events.
package calendar

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xaroth/dashboard/module"
)

type config struct {
	Calendars map[string]calendarConfig `yaml:"calendars"`
}

type calendarConfig struct {
	// URL is a credential for private feeds: it is never logged or shown.
	URL string `yaml:"url"`
}

type calendar struct {
	cfg *config
	// names are the configured calendars, sorted.
	names []string
	// urls are the feed URLs by name, webcal:// as https://.
	urls map[string]string
}

// Module is the calendar registration.
var Module = module.Define[calendar, config](
	"calendar",
	func() *config { return &config{} },
	module.WithUpdateInterval(15*time.Minute),
	module.WithStaleAfter(2*time.Hour),
	module.WithQuery("upcoming", (*calendar).upcoming),
)

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Initialize checks the calendars. Errors name the calendar, never its URL.
func (m *calendar) Initialize(_ context.Context, cfg *config, _ module.Env) error {
	m.cfg = cfg
	m.names = slices.Sorted(maps.Keys(cfg.Calendars))
	m.urls = make(map[string]string, len(cfg.Calendars))
	for _, name := range m.names {
		if !validName.MatchString(name) {
			return fmt.Errorf("calendar %q: name must be lowercase letters, digits, - and _", name)
		}
		u, err := feedURL(cfg.Calendars[name].URL)
		if err != nil {
			return fmt.Errorf("calendar %s: url %w", name, err)
		}
		m.urls[name] = u
	}
	return nil
}

// feedURL checks raw and returns it with webcal:// as https://. Its errors
// leave raw out.
func feedURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("is not a valid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "webcal":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", errors.New("must be http, https or webcal")
	}
	if u.Host == "" {
		return "", errors.New("has no host")
	}
	return u.String(), nil
}

func (m *calendar) Enabled(context.Context, module.Query) (bool, error) {
	return len(m.names) > 0, nil
}

// horizon is how many days from today an update expands, the most a query
// asks for plus a day for the update's age.
const (
	maxDays = 31
	horizon = maxDays + 1
)

// snapshot is what one update stores: each calendar's events from the start
// of the update's day to horizon days after it.
type snapshot struct {
	Calendars []stored `json:"calendars"`
}

type stored struct {
	Name string `json:"name"`
	// FetchedAt is the last successful fetch; Error is why the latest one
	// failed, which keeps the previous events.
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     string     `json:"error"`
	Events    []Event    `json:"events"`
}

// Update fetches every calendar at once. A calendar that fails keeps its
// previous events with the error; the update fails only when all do.
func (m *calendar) Update(ctx context.Context, u *module.Update) error {
	var prev snapshot
	if _, err := u.LoadSnapshot(ctx, &prev); err != nil {
		return err
	}
	now := u.Now()
	y, mo, d := now.In(u.Location).Date()
	from := time.Date(y, mo, d, 0, 0, 0, 0, u.Location)
	to := time.Date(y, mo, d+horizon, 0, 0, 0, 0, u.Location)

	type result struct {
		events  []Event
		skipped []error
		err     error
	}
	results := make([]result, len(m.names))
	var wg sync.WaitGroup
	for i, name := range m.names {
		wg.Go(func() {
			r := &results[i]
			cal, err := fetch(ctx, u.HTTP, m.urls[name])
			if err != nil {
				r.err = err
				return
			}
			r.events, r.skipped = expand(cal, from, to, u.Location)
		})
	}
	wg.Wait()

	out := snapshot{Calendars: make([]stored, 0, len(m.names))}
	var errs []error
	for i, name := range m.names {
		r := results[i]
		s := stored{Name: name}
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, r.err))
			s.Error = r.err.Error()
			if j := slices.IndexFunc(prev.Calendars, func(p stored) bool { return p.Name == name }); j >= 0 {
				s.FetchedAt, s.Events = prev.Calendars[j].FetchedAt, prev.Calendars[j].Events
			}
		} else {
			s.FetchedAt, s.Events = &now, r.events
			for i := range s.Events {
				s.Events[i].Calendar = name
			}
		}
		if s.Events == nil {
			s.Events = []Event{}
		}
		if len(r.skipped) > 0 {
			u.Log.Warn("skipped events", "calendar", name, "count", len(r.skipped), "err", r.skipped[0])
		}
		out.Calendars = append(out.Calendars, s)
	}
	if len(errs) == len(m.names) {
		if !slices.ContainsFunc(errs, func(e error) bool { return !errors.Is(e, module.ErrAuth) }) {
			return errors.Join(errs...)
		}
		return withoutAuth(errs)
	}
	// Logged only here: the scheduler logs a failed update's error.
	for _, err := range errs {
		u.Log.Warn("fetching calendar", "err", err)
	}
	return u.SaveSnapshot(out)
}

// withoutAuth joins errs with module.ErrAuth hidden: ErrAuth pauses the
// module until a manual update, which only fits when every calendar's
// address was rejected.
func withoutAuth(errs []error) error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		if errors.Is(err, module.ErrAuth) {
			err = errors.New(err.Error())
		}
		out = append(out, err)
	}
	return errors.Join(out...)
}

const (
	defaultDays  = 7
	defaultLimit = 10
	maxLimit     = 100
)

type upcomingParams struct {
	// Calendars are the names to merge; empty is every calendar.
	Calendars []string `json:"calendars"`
	// Days from today, today included; default 7, at most 31.
	Days int `json:"days"`
	// Limit is the most events returned; default 10, at most 100.
	Limit int `json:"limit"`
}

// Event is one occurrence of an event. An all-day event starts at midnight
// on its first day and ends at midnight after its last, in the configured
// time zone.
type Event struct {
	Calendar string    `json:"calendar"`
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	AllDay   bool      `json:"allDay"`
	Location string    `json:"location"`
}

// Status is a calendar's last fetch. FetchedAt is null before the first
// success; Error is why the latest fetch failed.
type Status struct {
	Name      string     `json:"name"`
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     string     `json:"error"`
}

// Upcoming is the events of the calendars asked for, merged.
type Upcoming struct {
	Events    []Event  `json:"events"`
	Calendars []Status `json:"calendars"`
}

// upcoming returns the events that have not ended by now and start before
// the end of the last day asked for, by start, all-day events first.
func (m *calendar) upcoming(ctx context.Context, q module.Query, p upcomingParams) (*Upcoming, error) {
	if p.Days < 0 || p.Days > maxDays {
		return nil, fmt.Errorf("days must be 0 to %d, got %d", maxDays, p.Days)
	}
	if p.Limit < 0 || p.Limit > maxLimit {
		return nil, fmt.Errorf("limit must be 0 to %d, got %d", maxLimit, p.Limit)
	}
	names := m.names
	if len(p.Calendars) > 0 {
		for _, name := range p.Calendars {
			if !slices.Contains(m.names, name) {
				return nil, fmt.Errorf("no calendar %q (have %s)", name, strings.Join(m.names, ", "))
			}
		}
		names = slices.Compact(slices.Sorted(slices.Values(p.Calendars)))
	}
	var snap snapshot
	ok, err := q.LoadSnapshot(ctx, &snap)
	if err != nil || !ok {
		return nil, err
	}
	now := q.Now()
	y, mo, d := now.In(q.Location).Date()
	end := time.Date(y, mo, d+cmp.Or(p.Days, defaultDays), 0, 0, 0, 0, q.Location)
	out := &Upcoming{Events: []Event{}, Calendars: make([]Status, 0, len(names))}
	for _, name := range names {
		st := Status{Name: name}
		if i := slices.IndexFunc(snap.Calendars, func(s stored) bool { return s.Name == name }); i >= 0 {
			s := snap.Calendars[i]
			st.FetchedAt, st.Error = s.FetchedAt, s.Error
			for _, e := range s.Events {
				if e.End.After(now) && e.Start.Before(end) || e.Start.Equal(e.End) && !e.Start.Before(now) && e.Start.Before(end) {
					e.Start, e.End = e.Start.In(q.Location), e.End.In(q.Location)
					out.Events = append(out.Events, e)
				}
			}
		}
		out.Calendars = append(out.Calendars, st)
	}
	slices.SortStableFunc(out.Events, compareEvents)
	out.Events = out.Events[:min(len(out.Events), cmp.Or(p.Limit, defaultLimit))]
	return out, nil
}

func compareEvents(a, b Event) int {
	if c := a.Start.Compare(b.Start); c != 0 {
		return c
	}
	if a.AllDay != b.AllDay {
		if a.AllDay {
			return -1
		}
		return 1
	}
	return cmp.Or(a.End.Compare(b.End), cmp.Compare(a.Calendar, b.Calendar), cmp.Compare(a.Title, b.Title))
}
