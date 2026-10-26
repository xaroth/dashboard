package calendar

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/go-cmp/cmp"
)

func mustLoad(tb testing.TB, name string) *time.Location {
	tb.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		tb.Fatal(err)
	}
	return loc
}

// parseCal parses VEVENTs, given one property per line, into a calendar.
func parseCal(tb testing.TB, body string) *ical.Calendar {
	tb.Helper()
	src := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:test\n" + strings.TrimSpace(body) + "\nEND:VCALENDAR\n"
	cal, err := ical.NewDecoder(strings.NewReader(strings.ReplaceAll(src, "\n", "\r\n"))).Decode()
	if err != nil {
		tb.Fatalf("parsing: %v", err)
	}
	return cal
}

func ev(props ...string) string {
	return "BEGIN:VEVENT\n" + strings.Join(props, "\n") + "\nEND:VEVENT\n"
}

func TestExpand(t *testing.T) {
	t.Parallel()
	ams := mustLoad(t, "Europe/Amsterdam")
	// A week around the end of summer time on Sunday 2026-10-25.
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, ams)
	to := time.Date(2026, 10, 29, 0, 0, 0, 0, ams)
	cases := []struct {
		name   string
		events string
		want   []string
	}{
		{
			name: "weekly keeps the wall clock across DST",
			events: ev("UID:a", "SUMMARY:Yoga", "DTSTART;TZID=Europe/Amsterdam:20260101T190000",
				"DTEND;TZID=Europe/Amsterdam:20260101T200000", "RRULE:FREQ=WEEKLY;BYDAY=TH,MO"),
			want: []string{"10-22 19:00 +02 - 10-22 20:00 +02 Yoga", "10-26 19:00 +01 - 10-26 20:00 +01 Yoga"},
		},
		{
			name: "UTC rule keeps UTC",
			events: ev("UID:a", "SUMMARY:Sync", "DTSTART:20261001T080000Z", "DURATION:PT30M",
				"RRULE:FREQ=DAILY;INTERVAL=3"),
			want: []string{"10-22 10:00 +02 - 10-22 10:30 +02 Sync", "10-25 09:00 +01 - 10-25 09:30 +01 Sync", "10-28 09:00 +01 - 10-28 09:30 +01 Sync"},
		},
		{
			name: "EXDATE list, in another zone, and as a date",
			events: ev("UID:a", "SUMMARY:Standup", "DTSTART;TZID=Europe/Amsterdam:20261019T093000",
				"DTEND;TZID=Europe/Amsterdam:20261019T094500", "RRULE:FREQ=DAILY",
				"EXDATE;TZID=Europe/Amsterdam:20261022T093000,20261023T093000",
				"EXDATE:20261024T073000Z", "EXDATE;VALUE=DATE:20261025,20261026"),
			want: []string{"10-27 09:30 +01 - 10-27 09:45 +01 Standup", "10-28 09:30 +01 - 10-28 09:45 +01 Standup"},
		},
		{
			name: "COUNT includes excluded occurrences",
			events: ev("UID:a", "SUMMARY:Cursus", "DTSTART;TZID=Europe/Amsterdam:20261020T200000",
				"RRULE:FREQ=DAILY;COUNT=5", "EXDATE;TZID=Europe/Amsterdam:20261023T200000"),
			want: []string{"10-22 20:00 +02 - 10-22 20:00 +02 Cursus", "10-24 20:00 +02 - 10-24 20:00 +02 Cursus"},
		},
		{
			name: "UNTIL is inclusive",
			events: ev("UID:a", "SUMMARY:Sprint", "DTSTART;TZID=Europe/Amsterdam:20261020T100000",
				"RRULE:FREQ=DAILY;UNTIL=20261024T080000Z"),
			want: []string{"10-22 10:00 +02 - 10-22 10:00 +02 Sprint", "10-23 10:00 +02 - 10-23 10:00 +02 Sprint", "10-24 10:00 +02 - 10-24 10:00 +02 Sprint"},
		},
		{
			name: "overrides: moved, moved in, moved out, cancelled",
			events: ev("UID:a", "SUMMARY:Les", "DTSTART;TZID=Europe/Amsterdam:20260901T150000",
				"DTEND;TZID=Europe/Amsterdam:20260901T160000", "RRULE:FREQ=DAILY") +
				ev("UID:a", "SUMMARY:Les verzet", "RECURRENCE-ID;TZID=Europe/Amsterdam:20261022T150000",
					"DTSTART;TZID=Europe/Amsterdam:20261022T170000", "DTEND;TZID=Europe/Amsterdam:20261022T180000") +
				ev("UID:a", "SUMMARY:Les weg", "RECURRENCE-ID:20261023T130000Z",
					"DTSTART;TZID=Europe/Amsterdam:20261105T150000", "DTEND;TZID=Europe/Amsterdam:20261105T160000") +
				ev("UID:a", "SUMMARY:Les erin", "RECURRENCE-ID;TZID=Europe/Amsterdam:20261102T150000",
					"DTSTART;TZID=Europe/Amsterdam:20261024T150000", "DTEND;TZID=Europe/Amsterdam:20261024T160000") +
				ev("UID:a", "SUMMARY:Les", "RECURRENCE-ID;TZID=Europe/Amsterdam:20261025T150000", "STATUS:CANCELLED",
					"DTSTART;TZID=Europe/Amsterdam:20261025T150000", "DTEND;TZID=Europe/Amsterdam:20261025T160000"),
			want: []string{
				"10-22 17:00 +02 - 10-22 18:00 +02 Les verzet",
				"10-24 15:00 +02 - 10-24 16:00 +02 Les",
				"10-24 15:00 +02 - 10-24 16:00 +02 Les erin",
				"10-26 15:00 +01 - 10-26 16:00 +01 Les",
				"10-27 15:00 +01 - 10-27 16:00 +01 Les",
				"10-28 15:00 +01 - 10-28 16:00 +01 Les",
			},
		},
		{
			name: "overrides of all-day events",
			events: ev("UID:a", "SUMMARY:Container", "DTSTART;VALUE=DATE:20260105", "RRULE:FREQ=WEEKLY;INTERVAL=2") +
				ev("UID:a", "SUMMARY:Container (later)", "RECURRENCE-ID;VALUE=DATE:20261026", "DTSTART;VALUE=DATE:20261027"),
			want: []string{"10-27 00:00 +01 - 10-28 00:00 +01 Container (later) all-day"},
		},
		{
			name: "monthly by weekday and set position",
			events: ev("UID:a", "SUMMARY:Vierde maandag", "DTSTART;TZID=Europe/Amsterdam:20260126T200000", "RRULE:FREQ=MONTHLY;BYDAY=4MO") +
				ev("UID:b", "SUMMARY:Laatste woensdag", "DTSTART;TZID=Europe/Amsterdam:20260128T160000", "RRULE:FREQ=MONTHLY;BYDAY=-1WE") +
				ev("UID:c", "SUMMARY:Laatste werkdag", "DTSTART;TZID=Europe/Amsterdam:20260102T120000", "RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1"),
			want: []string{"10-23 12:00 +02 - 10-23 12:00 +02 Laatste werkdag", "10-26 20:00 +01 - 10-26 20:00 +01 Vierde maandag", "10-28 16:00 +01 - 10-28 16:00 +01 Laatste woensdag"},
		},
		{
			name: "RDATE adds, and DTSTART counts though the rule skips it",
			events: ev("UID:a", "SUMMARY:Extra", "DTSTART;TZID=Europe/Amsterdam:20261022T120000", "RRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=1",
				"RDATE;TZID=Europe/Amsterdam:20261024T120000,20261027T120000"),
			want: []string{"10-22 12:00 +02 - 10-22 12:00 +02 Extra", "10-24 12:00 +02 - 10-24 12:00 +02 Extra", "10-26 12:00 +01 - 10-26 12:00 +01 Extra", "10-27 12:00 +01 - 10-27 12:00 +01 Extra"},
		},
		{
			name: "yearly all-day with a date UNTIL",
			events: ev("UID:a", "SUMMARY:Jarig", "DTSTART;VALUE=DATE:19801025", "DTEND;VALUE=DATE:19801026", "RRULE:FREQ=YEARLY;UNTIL=20261025") +
				ev("UID:b", "SUMMARY:Was jarig", "DTSTART;VALUE=DATE:19801025", "RRULE:FREQ=YEARLY;UNTIL=20251025"),
			want: []string{"10-25 00:00 +02 - 10-26 00:00 +01 Jarig all-day"},
		},
		{
			name: "all-day: started before the range, equal DTEND, no DTEND",
			events: ev("UID:a", "SUMMARY:Vakantie", "DTSTART;VALUE=DATE:20261017", "DTEND;VALUE=DATE:20261023") +
				ev("UID:b", "SUMMARY:Gelijk", "DTSTART;VALUE=DATE:20261023", "DTEND;VALUE=DATE:20261023") +
				ev("UID:c", "SUMMARY:Zonder einde", "DTSTART:20261028") +
				ev("UID:d", "SUMMARY:Voorbij", "DTSTART;VALUE=DATE:20261017", "DTEND;VALUE=DATE:20261022"),
			want: []string{"10-17 00:00 +02 - 10-23 00:00 +02 Vakantie all-day", "10-23 00:00 +02 - 10-24 00:00 +02 Gelijk all-day", "10-28 00:00 +01 - 10-29 00:00 +01 Zonder einde all-day"},
		},
		{
			name: "zones: floating, UTC, other, prefixed, unknown, VTIMEZONE alias",
			events: "BEGIN:VTIMEZONE\nTZID:Custom Pacific\nX-LIC-LOCATION:America/Los_Angeles\nEND:VTIMEZONE\n" +
				ev("UID:a", "SUMMARY:Zwevend", "DTSTART:20261022T090000") +
				ev("UID:b", "SUMMARY:UTC", "DTSTART:20261022T090000Z") +
				ev("UID:c", "SUMMARY:New York", "DTSTART;TZID=America/New_York:20261022T090000") +
				ev("UID:d", "SUMMARY:Evolution", "DTSTART;TZID=/freeassociation.sourceforge.net/Tzfile/America/New_York:20261023T090000") +
				ev("UID:e", "SUMMARY:Outlook", "DTSTART;TZID=W. Europe Standard Time:20261023T090000") +
				ev("UID:f", "SUMMARY:Alias", "DTSTART;TZID=Custom Pacific:20261023T090000"),
			want: []string{
				"10-22 09:00 +02 - 10-22 09:00 +02 Zwevend",
				"10-22 11:00 +02 - 10-22 11:00 +02 UTC",
				"10-22 15:00 +02 - 10-22 15:00 +02 New York",
				"10-23 09:00 +02 - 10-23 09:00 +02 Outlook",
				"10-23 15:00 +02 - 10-23 15:00 +02 Evolution",
				"10-23 18:00 +02 - 10-23 18:00 +02 Alias",
			},
		},
		{
			name: "range edges: ends at from, starts at to, zero length at from",
			events: ev("UID:a", "SUMMARY:Ends", "DTSTART;TZID=Europe/Amsterdam:20261021T230000", "DTEND;TZID=Europe/Amsterdam:20261022T000000") +
				ev("UID:b", "SUMMARY:Starts", "DTSTART;TZID=Europe/Amsterdam:20261029T000000", "DTEND;TZID=Europe/Amsterdam:20261029T010000") +
				ev("UID:c", "SUMMARY:Point", "DTSTART;TZID=Europe/Amsterdam:20261022T000000") +
				ev("UID:d", "SUMMARY:Across", "DTSTART;TZID=Europe/Amsterdam:20261021T230000", "DTEND;TZID=Europe/Amsterdam:20261022T010000"),
			want: []string{"10-21 23:00 +02 - 10-22 01:00 +02 Across", "10-22 00:00 +02 - 10-22 00:00 +02 Point"},
		},
		{
			name: "cancelled series",
			events: ev("UID:a", "SUMMARY:Weg", "STATUS:CANCELLED", "DTSTART;TZID=Europe/Amsterdam:20261001T100000", "RRULE:FREQ=DAILY"),
			want: []string{},
		},
		{
			name: "text is unescaped",
			events: ev("UID:a", `SUMMARY:Borrel\, met taart\; of niet`, `LOCATION:Café "De Hoek"\nAmsterdam`, "DTSTART:20261023T150000Z"),
			want: []string{"10-23 17:00 +02 - 10-23 17:00 +02 Borrel, met taart; of niet @ Café \"De Hoek\"\nAmsterdam"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			events, skipped := expand(parseCal(t, c.events), from, to, ams)
			if len(skipped) > 0 {
				t.Errorf("skipped %v", skipped)
			}
			slices.SortStableFunc(events, compareEvents)
			got := []string{}
			for _, e := range events {
				got = append(got, format(e))
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func format(e Event) string {
	s := e.Start.Format("01-02 15:04 -07") + " - " + e.End.Format("01-02 15:04 -07") + " " + e.Title
	if e.Location != "" {
		s += " @ " + e.Location
	}
	if e.AllDay {
		s += " all-day"
	}
	return s
}

func TestExpandSkipsBadEvents(t *testing.T) {
	t.Parallel()
	ams := mustLoad(t, "Europe/Amsterdam")
	from := time.Date(2026, 10, 22, 0, 0, 0, 0, ams)
	cal := parseCal(t, ev("UID:no-start", "SUMMARY:x")+
		ev("UID:bad-start", "DTSTART:2026-10-22")+
		ev("UID:bad-rule", "DTSTART:20261022T100000Z", "RRULE:FREQ=SOMETIMES")+
		ev("UID:bad-exdate", "DTSTART:20261022T100000Z", "RRULE:FREQ=DAILY", "EXDATE:tomorrow")+
		ev("UID:too-many", "DTSTART:20200101T000000Z", "RRULE:FREQ=MINUTELY")+
		ev("UID:ok", "SUMMARY:OK", "DTSTART:20261022T100000Z"))
	events, skipped := expand(cal, from, from.AddDate(0, 0, 1), ams)
	if len(events) != 1 || events[0].Title != "OK" {
		t.Errorf("got %v, want the good event only", events)
	}
	var uids []string
	for _, err := range skipped {
		uids = append(uids, strings.SplitN(err.Error(), `"`, 3)[1])
	}
	if diff := cmp.Diff([]string{"no-start", "bad-start", "bad-exdate", "bad-rule", "too-many"}, uids); diff != "" {
		t.Errorf("skipped (-want +got):\n%s", diff)
	}
}
