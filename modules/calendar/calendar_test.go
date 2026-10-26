package calendar_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/calendar"
)

var ams = mustLoad("Europe/Amsterdam")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// fixtureNow is a Thursday, three days before the end of summer time.
var fixtureNow = time.Date(2026, 10, 22, 8, 0, 0, 0, ams)

const twoCalendars = `calendar: {calendars: {family: {url: "` + calendar.FakeFamilyURL + `"}, work: {url: "` + calendar.FakeWorkURL + `"}}}`

type loaded struct {
	set *module.Set
	now *time.Time
	log *bytes.Buffer
}

func load(tb testing.TB, client *http.Client, config string) loaded {
	tb.Helper()
	l := loaded{now: new(fixtureNow), log: &bytes.Buffer{}}
	l.set = moduletest.Load(tb, nil, config, module.Options{
		Registrations: []module.Registration{calendar.Module},
		HTTPClient:    client,
		Now:           func() time.Time { return *l.now },
		Location:      ams,
		Log:           slog.New(slog.NewTextHandler(l.log, nil)),
	})
	return l
}

func upcoming(tb testing.TB, set *module.Set, params map[string]any) (*calendar.Upcoming, module.Result) {
	tb.Helper()
	res := set.Resolve(tb.Context(), "calendar", "upcoming", params)
	u, _ := res.Value.(*calendar.Upcoming)
	return u, res
}

// event is an Event in short: calendar, title, local start and end, all-day.
type event struct {
	Calendar, Title, Start, End string
	AllDay                      bool
}

func short(events []calendar.Event) []event {
	out := []event{}
	for _, e := range events {
		out = append(out, event{e.Calendar, e.Title, e.Start.Format("01-02 15:04 -07"), e.End.Format("01-02 15:04 -07"), e.AllDay})
	}
	return out
}

func TestUpcoming(t *testing.T) {
	t.Parallel()
	fake := calendar.NewFake(t)
	l := load(t, moduletest.HTTPClient(fake), twoCalendars)
	if err := l.set.Update(t.Context(), "calendar"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	u, res := upcoming(t, l.set, map[string]any{"limit": 20})
	if res.Error != "" || res.Stale {
		t.Fatalf("got %+v, want fresh", res)
	}
	want := []event{
		{"work", "Nachtdienst", "10-22 00:00 +02", "10-22 09:00 +02", false},
		{"work", "Standup", "10-22 09:30 +02", "10-22 09:45 +02", false},
		{"work", "Call met Boston", "10-22 17:00 +02", "10-22 18:00 +02", false},
		{"family", "Tandarts", "10-23 08:30 +02", "10-23 09:15 +02", false},
		{"family", "Herfstvakantie", "10-24 00:00 +02", "11-02 00:00 +01", true},
		{"family", "Zwemles (diploma)", "10-24 11:30 +02", "10-24 12:15 +02", false},
		{"work", "Standup", "10-26 09:30 +01", "10-26 09:45 +01", false},
		{"family", "Verjaardag oma", "10-27 00:00 +01", "10-28 00:00 +01", true},
		{"work", "Standup", "10-27 09:30 +01", "10-27 09:45 +01", false},
		{"work", "Standup", "10-28 09:30 +01", "10-28 09:45 +01", false},
	}
	if diff := cmp.Diff(want, short(u.Events)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if got, want := u.Events[3].Location, "Tandartspraktijk, Hoofdstraat 1, Utrecht"; got != want {
		t.Errorf("got location %q, want %q", got, want)
	}
	wantStatus := []calendar.Status{{Name: "family", FetchedAt: &fixtureNow}, {Name: "work", FetchedAt: &fixtureNow}}
	if diff := cmp.Diff(wantStatus, u.Calendars); diff != "" {
		t.Errorf("calendars (-want +got):\n%s", diff)
	}
	if n := fake.Unmarked(); n != 0 {
		t.Errorf("got %d requests not marked secret, want 0", n)
	}

	// What pages get: local times with offsets.
	b, err := json.Marshal(u.Events[4])
	if err != nil || !strings.Contains(string(b), `"start":"2026-10-24T00:00:00+02:00","end":"2026-11-02T00:00:00+01:00","allDay":true`) {
		t.Errorf("got %s (%v), want local midnights", b, err)
	}
}

func TestUpcomingParams(t *testing.T) {
	t.Parallel()
	l := load(t, moduletest.HTTPClient(calendar.NewFake(t)), twoCalendars)
	if u, res := upcoming(t, l.set, nil); u != nil || !res.Stale {
		t.Errorf("before any update: got %v, %+v, want no value, stale", u, res)
	}
	if err := l.set.Update(t.Context(), "calendar"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	titles := func(u *calendar.Upcoming) []string {
		var out []string
		for _, e := range u.Events {
			out = append(out, e.Start.Format("01-02 ")+e.Title)
		}
		return out
	}

	// Defaults: every calendar, 7 days, 10 events.
	u, _ := upcoming(t, l.set, nil)
	if len(u.Events) != 10 || len(u.Calendars) != 2 {
		t.Errorf("got %d events of %d calendars, want 10 of 2", len(u.Events), len(u.Calendars))
	}

	u, _ = upcoming(t, l.set, map[string]any{"calendars": []string{"family"}, "days": 2})
	if diff := cmp.Diff([]string{"10-23 Tandarts"}, titles(u)); diff != "" {
		t.Errorf("family, 2 days (-want +got):\n%s", diff)
	}
	if len(u.Calendars) != 1 || u.Calendars[0].Name != "family" {
		t.Errorf("got calendars %+v, want family only", u.Calendars)
	}

	u, _ = upcoming(t, l.set, map[string]any{"calendars": []string{"family"}, "days": 31, "limit": 100})
	want := []string{"10-23 Tandarts", "10-24 Herfstvakantie", "10-24 Zwemles (diploma)", "10-27 Verjaardag oma", "10-31 Zwemles"}
	for d := 7; d <= 21; d += 7 {
		want = append(want, time.Date(2026, 10, 31+d, 0, 0, 0, 0, ams).Format("01-02 ")+"Zwemles")
	}
	if diff := cmp.Diff(want, titles(u)); diff != "" {
		t.Errorf("family, 31 days (-want +got):\n%s", diff)
	}

	u, _ = upcoming(t, l.set, map[string]any{"limit": 1})
	if diff := cmp.Diff([]string{"10-22 Nachtdienst"}, titles(u)); diff != "" {
		t.Errorf("limit 1 (-want +got):\n%s", diff)
	}

	// Later the same day: the night shift and stand-up are over.
	*l.now = time.Date(2026, 10, 22, 9, 45, 0, 0, ams)
	u, _ = upcoming(t, l.set, map[string]any{"days": 1})
	if diff := cmp.Diff([]string{"10-22 Call met Boston"}, titles(u)); diff != "" {
		t.Errorf("at 09:45, 1 day (-want +got):\n%s", diff)
	}

	for _, p := range []map[string]any{
		{"days": 32}, {"days": -1}, {"limit": 101}, {"calendars": []string{"nope"}}, {"calendars": "family"},
	} {
		if _, res := upcoming(t, l.set, p); res.Error == "" {
			t.Errorf("params %v: got no error", p)
		}
	}
}

// One failing calendar keeps its previous events and reports its error;
// the update fails only when every calendar does, never with the URL.
func TestCalendarErrors(t *testing.T) {
	t.Parallel()
	fake := calendar.NewFake(t)
	l := load(t, moduletest.HTTPClient(fake), twoCalendars)
	if err := l.set.Update(t.Context(), "calendar"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	*l.now = fixtureNow.Add(time.Hour)

	fake.SetStatus(calendar.FakeWorkURL, http.StatusInternalServerError)
	if err := l.set.Update(t.Context(), "calendar"); err != nil {
		t.Fatalf("Update with work failing: %v", err)
	}
	u, res := upcoming(t, l.set, map[string]any{"limit": 20})
	if res.Error != "" {
		t.Errorf("got error %q, want none: family worked", res.Error)
	}
	later := fixtureNow.Add(time.Hour)
	wantStatus := []calendar.Status{{Name: "family", FetchedAt: &later}, {Name: "work", FetchedAt: &fixtureNow, Error: "feed: 500 Internal Server Error"}}
	if diff := cmp.Diff(wantStatus, u.Calendars); diff != "" {
		t.Errorf("calendars (-want +got):\n%s", diff)
	}
	if n := len(u.Events); n != 9 {
		t.Errorf("got %d events, want 9: work's kept, the night shift over", n)
	}

	fake.SetStatus(calendar.FakeFamilyURL, http.StatusForbidden)
	err := l.set.Update(t.Context(), "calendar")
	if err == nil || errors.Is(err, module.ErrAuth) {
		t.Errorf("got %v, want a failure without ErrAuth: work is not rejected", err)
	}
	fake.SetStatus(calendar.FakeWorkURL, http.StatusUnauthorized)
	if err := l.set.Update(t.Context(), "calendar"); !errors.Is(err, module.ErrAuth) {
		t.Errorf("got %v, want ErrAuth: every address rejected", err)
	}

	for _, secret := range []string{"f4m1ly5ecret", "w0rk5ecret", "calendar.example.com/ical"} {
		if strings.Contains(l.log.String(), secret) {
			t.Errorf("log contains %q:\n%s", secret, l.log)
		}
		sts, err := l.set.Statuses(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range sts {
			if strings.Contains(st.Error, secret) {
				t.Errorf("module state error contains %q: %s", secret, st.Error)
			}
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestTransportErrorHidesURL(t *testing.T) {
	t.Parallel()
	l := load(t, &http.Client{Transport: failingTransport{}}, `calendar: {calendars: {family: {url: "`+calendar.FakeFamilyURL+`"}}}`)
	err := l.set.Update(t.Context(), "calendar")
	if err == nil || strings.Contains(err.Error(), "f4m1ly5ecret") || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("got %v, want the transport error without the URL", err)
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	fake := calendar.NewFake(t, calendar.ExpectFeed("https://example.com/family.ics", []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")))
	cases := []struct {
		name, config, err string
	}{
		{"webcal", `{family: {url: "webcal://example.com/family.ics"}}`, ""},
		{"no url", `{family: {}}`, "calendar family: url is required"},
		{"scheme", `{family: {url: "ftp://example.com/private-x/basic.ics"}}`, "calendar family: url must be http, https or webcal"},
		{"invalid", `{family: {url: "https://exa mple.com/private-x"}}`, "calendar family: url is not a valid URL"},
		{"name", `{Family: {url: "https://example.com/private-x"}}`, `calendar "Family": name must be lowercase letters, digits, - and _`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := load(t, moduletest.HTTPClient(fake), "calendar: {calendars: "+c.config+"}")
			err := l.set.Update(t.Context(), "calendar")
			if c.err == "" {
				if err != nil {
					t.Errorf("Update: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("got %v, want %q", err, c.err)
			}
			if strings.Contains(l.log.String(), "private-x") {
				t.Errorf("log contains the URL:\n%s", l.log)
			}
		})
	}
}
