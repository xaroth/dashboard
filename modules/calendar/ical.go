package calendar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"github.com/xaroth/dashboard/module"
)

// maxFeedSize bounds a feed; Google's carry every past event, a few MB for
// a calendar used for years.
const maxFeedSize = 32 << 20

// fetch downloads and parses the feed at rawURL. Its errors leave the URL
// out: it is a credential.
func fetch(ctx context.Context, c *http.Client, rawURL string) (*ical.Calendar, error) {
	req, err := http.NewRequestWithContext(module.WithSecretURL(ctx), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	req.Header.Set("Accept", "text/calendar")
	resp, err := c.Do(req)
	if err != nil {
		// *url.Error quotes the URL.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("requesting feed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("feed: %s: %w", resp.Status, module.ErrAuth)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("feed: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedSize+1))
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("reading feed: %w", err)
	}
	if len(body) > maxFeedSize {
		return nil, fmt.Errorf("feed larger than %d MB", maxFeedSize>>20)
	}
	cal, err := ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}
	return cal, nil
}

// maxOccurrences bounds the expansion of one recurring event, counted from
// its first occurrence, so a rule like every minute since 2010 cannot stall
// an update.
const maxOccurrences = 100_000

// expand returns the occurrences of cal's events that overlap [from, to),
// in loc. Times without a zone, and in a zone neither Go nor the feed's
// VTIMEZONE X-LIC-LOCATION names, are in loc. An event that fails to parse
// is skipped and its error returned in skipped.
func expand(cal *ical.Calendar, from, to time.Time, loc *time.Location) (events []Event, skipped []error) {
	z := newZones(cal, loc)
	var masters, singles []*vevent
	// overrides are the RECURRENCE-ID instances per UID, which replace the
	// master's occurrence at that time.
	overrides := map[string][]when{}
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		v, err := parseEvent(comp, z)
		if err != nil {
			skipped = append(skipped, err)
			continue
		}
		switch {
		case v.recurrenceID != nil:
			overrides[v.uid] = append(overrides[v.uid], *v.recurrenceID)
			singles = append(singles, v)
		case v.rule != "" || len(v.rdates) > 0:
			masters = append(masters, v)
		default:
			singles = append(singles, v)
		}
	}
	events = []Event{}
	for _, v := range singles {
		if e := v.at(v.start.t, loc); !v.cancelled && overlaps(e, from, to) {
			events = append(events, e)
		}
	}
	for _, v := range masters {
		if v.cancelled {
			continue
		}
		occ, err := v.occurrences(from, to, overrides[v.uid], loc)
		if err != nil {
			skipped = append(skipped, err)
			continue
		}
		events = append(events, occ...)
	}
	return events, skipped
}

// when is a DATE or DATE-TIME value. A date is midnight UTC of that day.
type when struct {
	t      time.Time
	allDay bool
}

// matches reports whether occurrence t of an event is the one w names. A
// date names a day of a timed event in the event's zone.
func (w when) matches(t time.Time, allDay bool) bool {
	if w.allDay && !allDay {
		y, m, d := t.Date()
		wy, wm, wd := w.t.Date()
		return y == wy && m == wm && d == wd
	}
	return w.t.Equal(t)
}

type vevent struct {
	uid          string
	title        string
	location     string
	start        when
	end          when
	recurrenceID *when
	cancelled    bool
	rule         string
	rdates       []when
	exdates      []when
}

func parseEvent(comp *ical.Component, z *zones) (*vevent, error) {
	v := &vevent{}
	v.uid, _ = comp.Props.Text(ical.PropUID)
	v.title, _ = comp.Props.Text(ical.PropSummary)
	v.location, _ = comp.Props.Text(ical.PropLocation)
	if s, _ := comp.Props.Text(ical.PropStatus); strings.EqualFold(s, string(ical.EventCancelled)) {
		v.cancelled = true
	}
	fail := func(err error) (*vevent, error) {
		return nil, fmt.Errorf("event %q: %w", v.uid, err)
	}
	p := comp.Props.Get(ical.PropDateTimeStart)
	if p == nil {
		return fail(errors.New("no DTSTART"))
	}
	var err error
	if v.start, err = z.parse(p.Value, p.Params); err != nil {
		return fail(fmt.Errorf("DTSTART: %w", err))
	}
	switch {
	case comp.Props.Get(ical.PropDateTimeEnd) != nil:
		p := comp.Props.Get(ical.PropDateTimeEnd)
		if v.end, err = z.parse(p.Value, p.Params); err != nil {
			return fail(fmt.Errorf("DTEND: %w", err))
		}
	case comp.Props.Get(ical.PropDuration) != nil:
		d, err := comp.Props.Get(ical.PropDuration).Duration()
		if err != nil {
			return fail(fmt.Errorf("DURATION: %w", err))
		}
		v.end = when{t: v.start.t.Add(d), allDay: v.start.allDay}
	case v.start.allDay:
		v.end = when{t: v.start.t.AddDate(0, 0, 1), allDay: true}
	default:
		v.end = v.start
	}
	// A DTEND on or before DTSTART (some clients write all-day events with
	// equal ones) is read as no DTEND.
	if !v.end.t.After(v.start.t) {
		v.end = v.start
		if v.start.allDay {
			v.end.t = v.start.t.AddDate(0, 0, 1)
		}
	}
	v.end.allDay = v.start.allDay
	if p := comp.Props.Get(ical.PropRecurrenceID); p != nil {
		w, err := z.parse(p.Value, p.Params)
		if err != nil {
			return fail(fmt.Errorf("RECURRENCE-ID: %w", err))
		}
		v.recurrenceID = &w
	}
	if p := comp.Props.Get(ical.PropRecurrenceRule); p != nil {
		v.rule = p.Value
	}
	for _, p := range comp.Props.Values(ical.PropRecurrenceDates) {
		ws, err := z.parseList(p)
		if err != nil {
			return fail(fmt.Errorf("RDATE: %w", err))
		}
		v.rdates = append(v.rdates, ws...)
	}
	for _, p := range comp.Props.Values(ical.PropExceptionDates) {
		ws, err := z.parseList(p)
		if err != nil {
			return fail(fmt.Errorf("EXDATE: %w", err))
		}
		v.exdates = append(v.exdates, ws...)
	}
	return v, nil
}

// at returns the occurrence of the event that starts at t.
func (v *vevent) at(t time.Time, loc *time.Location) Event {
	e := Event{Title: v.title, Location: v.location, AllDay: v.start.allDay}
	if e.AllDay {
		days := int(v.end.t.Sub(v.start.t).Round(24*time.Hour) / (24 * time.Hour))
		y, m, d := t.Date()
		e.Start = time.Date(y, m, d, 0, 0, 0, 0, loc)
		e.End = time.Date(y, m, d+days, 0, 0, 0, 0, loc)
	} else {
		e.Start = t.In(loc)
		e.End = t.Add(v.end.t.Sub(v.start.t)).In(loc)
	}
	return e
}

// overlaps reports whether e overlaps [from, to). An event without duration
// overlaps when it starts in the range.
func overlaps(e Event, from, to time.Time) bool {
	if !e.Start.Before(to) {
		return false
	}
	return e.End.After(from) || e.End.Equal(e.Start) && !e.Start.Before(from)
}

// occurrences expands the recurring event over [from, to), leaving out the
// EXDATEs and the occurrences an override replaces.
func (v *vevent) occurrences(from, to time.Time, overrides []when, loc *time.Location) ([]Event, error) {
	set := &rrule.Set{}
	if v.rule != "" {
		opt, err := rrule.StrToROptionInLocation(v.rule, v.start.t.Location())
		if err != nil {
			return nil, fmt.Errorf("event %q: RRULE: %w", v.uid, err)
		}
		opt.Dtstart = v.start.t
		r, err := rrule.NewRRule(*opt)
		if err != nil {
			return nil, fmt.Errorf("event %q: RRULE: %w", v.uid, err)
		}
		set.RRule(r)
	}
	// DTSTART is the first occurrence even when the rule does not match it.
	set.RDate(v.start.t)
	for _, w := range v.rdates {
		set.RDate(w.t)
	}
	excluded := func(t time.Time) bool {
		for _, w := range v.exdates {
			if w.matches(t, v.start.allDay) {
				return true
			}
		}
		for _, w := range overrides {
			if w.matches(t, v.start.allDay) {
				return true
			}
		}
		return false
	}
	var out []Event
	next := set.Iterator()
	for n := 0; ; n++ {
		t, ok := next()
		if !ok {
			break
		}
		if n == maxOccurrences {
			return nil, fmt.Errorf("event %q: more than %d occurrences", v.uid, maxOccurrences)
		}
		e := v.at(t, loc)
		if !e.Start.Before(to) {
			break
		}
		if overlaps(e, from, to) && !excluded(t) {
			out = append(out, e)
		}
	}
	return out, nil
}

// zones resolves TZIDs. Google and Apple use IANA names; Outlook uses
// Windows names, which the feed's VTIMEZONE may map with X-LIC-LOCATION.
type zones struct {
	fallback *time.Location
	lic      map[string]string
	byID     map[string]*time.Location
}

func newZones(cal *ical.Calendar, fallback *time.Location) *zones {
	z := &zones{fallback: fallback, lic: map[string]string{}, byID: map[string]*time.Location{}}
	for _, comp := range cal.Children {
		if comp.Name != ical.CompTimezone {
			continue
		}
		id, _ := comp.Props.Text(ical.PropTimezoneID)
		if p := comp.Props.Get("X-LIC-LOCATION"); p != nil && id != "" {
			z.lic[id] = p.Value
		}
	}
	return z
}

func (z *zones) location(tzid string) *time.Location {
	if loc, ok := z.byID[tzid]; ok {
		return loc
	}
	loc := z.fallback
	// Some clients prefix the name, e.g. /freeassociation.sourceforge.net/Europe/Amsterdam.
	for _, name := range append(suffixes(tzid), suffixes(z.lic[tzid])...) {
		if l, err := time.LoadLocation(name); err == nil && name != "" && name != "Local" {
			loc = l
			break
		}
	}
	z.byID[tzid] = loc
	return loc
}

// suffixes returns name and its suffixes after each slash, longest first.
func suffixes(name string) []string {
	out := []string{name}
	for i, c := range name {
		if c == '/' {
			out = append(out, name[i+1:])
		}
	}
	return out
}

const (
	dateFormat     = "20060102"
	dateTimeFormat = "20060102T150405"
)

// parse reads one DATE or DATE-TIME value; params give its TZID and VALUE.
func (z *zones) parse(value string, params ical.Params) (when, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(params.Get(ical.ParamValue), string(ical.ValueDate)) || len(value) == len(dateFormat) {
		t, err := time.ParseInLocation(dateFormat, value, time.UTC)
		return when{t: t, allDay: true}, err
	}
	if v, ok := strings.CutSuffix(value, "Z"); ok {
		t, err := time.ParseInLocation(dateTimeFormat, v, time.UTC)
		return when{t: t}, err
	}
	loc := z.fallback
	if tzid := params.Get(ical.PropTimezoneID); tzid != "" {
		loc = z.location(tzid)
	}
	t, err := time.ParseInLocation(dateTimeFormat, value, loc)
	return when{t: t}, err
}

// parseList reads a comma-separated list of values, as EXDATE and RDATE
// allow. A PERIOD counts by its start.
func (z *zones) parseList(p ical.Prop) ([]when, error) {
	var out []when
	for v := range strings.SplitSeq(p.Value, ",") {
		v, _, _ = strings.Cut(v, "/")
		w, err := z.parse(v, p.Params)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}
