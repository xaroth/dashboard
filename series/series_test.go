package series_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xaroth/dashboard/series"
	"github.com/xaroth/dashboard/storage"
)

var amsterdam = must(time.LoadLocation("Europe/Amsterdam"))

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func open(tb testing.TB, c *clock) *series.Store {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	s, err := series.Open(tb.Context(), series.Options{
		Migrator: db,
		Writer:   db.Writer,
		Reader:   db.Reader,
		Location: amsterdam,
		Now:      c.now,
		Log:      slog.New(slog.DiscardHandler),
		Series:   map[string]series.Kind{"power": series.Rate, "meter": series.Counter},
	})
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	return s
}

func add(tb testing.TB, s *series.Store, name string, t time.Time, v float64) {
	tb.Helper()
	if err := s.Add(name, t, v); err != nil {
		tb.Fatalf("Add: %v", err)
	}
}

func query(tb testing.TB, s *series.Store, name string, from, to time.Time, step time.Duration) []series.Point {
	tb.Helper()
	pts, err := s.Query(tb.Context(), name, from, to, step)
	if err != nil {
		tb.Fatalf("Query: %v", err)
	}
	return pts
}

// values lists the values of pts, -1 for a gap.
func values(pts []series.Point) []float64 {
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = -1
		if p.V != nil {
			out[i] = *p.V
		}
	}
	return out
}

func at(s string) time.Time { return must(time.ParseInLocation(time.DateTime, s, amsterdam)) }

func TestBuckets(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:30:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	// 12:00-12:09 every minute, 12:20 once: a gap from 12:10.
	for i := range 10 {
		add(t, s, "power", start.Add(time.Duration(i)*time.Minute+5*time.Second), float64(i))
		add(t, s, "meter", start.Add(time.Duration(i)*time.Minute+5*time.Second), float64(100+i))
	}
	add(t, s, "power", start.Add(20*time.Minute), 50)
	if got := query(t, s, "power", start, start.Add(time.Hour), 5*time.Minute); values(got)[0] != -1 {
		t.Fatalf("got %v before the flush, want nothing", values(got))
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := query(t, s, "power", start, start.Add(30*time.Minute), 5*time.Minute)
	if diff := cmp.Diff([]float64{2, 7, -1, -1, 50, -1}, values(got)); diff != "" {
		t.Errorf("rate in 5m (-want +got):\n%s", diff)
	}
	if !got[1].T.Equal(start.Add(5 * time.Minute)) {
		t.Errorf("got second bucket at %v, want 12:05", got[1].T)
	}
	got = query(t, s, "meter", start, start.Add(30*time.Minute), 10*time.Minute)
	if diff := cmp.Diff([]float64{109, -1, -1}, values(got)); diff != "" {
		t.Errorf("counter in 10m (-want +got):\n%s", diff)
	}
	// Raw, not 5m buckets: 12:05 holds samples of both the 12:00 and 12:06 steps.
	got = query(t, s, "power", start, start.Add(30*time.Minute), 6*time.Minute)
	if diff := cmp.Diff([]float64{2.5, 7.5, -1, 50, -1}, values(got)); diff != "" {
		t.Errorf("rate in 6m (-want +got):\n%s", diff)
	}
	got = query(t, s, "power", start.Add(8*time.Minute), start.Add(11*time.Minute), time.Minute)
	if diff := cmp.Diff([]float64{8, 9, -1}, values(got)); diff != "" {
		t.Errorf("raw in 1m (-want +got):\n%s", diff)
	}
	// Rolled up: 10 samples of 0-9 and one of 50, weighted by count.
	got = query(t, s, "power", start, start.Add(time.Hour), time.Hour)
	if diff := cmp.Diff([]float64{95.0 / 11}, values(got)); diff != "" {
		t.Errorf("rate in 1h (-want +got):\n%s", diff)
	}

	// 5m buckets, not hours: the 01:00 hour spans both 90m steps.
	day := at("2026-07-01 00:00:00")
	for i := range 120 {
		add(t, s, "power", day.Add(time.Duration(i)*time.Minute), float64(1+2*(i/60)))
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	c.t = c.t.AddDate(0, 0, 39)
	got = query(t, s, "power", day, day.Add(3*time.Hour), 90*time.Minute)
	if diff := cmp.Diff([]float64{5.0 / 3, 3}, values(got), cmpopts.EquateApprox(0, 1e-9)); diff != "" {
		t.Errorf("rate in 90m after 39 days (-want +got):\n%s", diff)
	}
}

// TestRollupAcrossFlushes adds to a bucket after it was rolled up.
func TestRollupAcrossFlushes(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:02:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	add(t, s, "power", start, 10)
	s.Flush(t.Context())
	c.t = c.t.Add(time.Minute)
	add(t, s, "power", start.Add(2*time.Minute), 20)
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for _, step := range []time.Duration{5 * time.Minute, time.Hour, 24 * time.Hour} {
		got := query(t, s, "power", start, start.Add(time.Minute), step)
		if diff := cmp.Diff([]float64{15}, values(got)); diff != "" {
			t.Errorf("step %v (-want +got):\n%s", step, diff)
		}
	}
}

func TestLocalDays(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-10-27 12:00:00")}
	s := open(t, c)
	// One sample per local hour from Oct 24 to Oct 26; Oct 25 has 25 hours.
	for ts := at("2026-10-24 00:30:00"); ts.Before(at("2026-10-27 00:00:00")); ts = ts.Add(time.Hour) {
		add(t, s, "meter", ts, float64(ts.Unix()))
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	got := query(t, s, "meter", at("2026-10-24 00:00:00"), at("2026-10-27 00:00:00"), 24*time.Hour)
	var days []string
	for _, p := range got {
		days = append(days, p.T.In(amsterdam).Format(time.DateTime))
	}
	want := []string{"2026-10-24 00:00:00", "2026-10-25 00:00:00", "2026-10-26 00:00:00"}
	if diff := cmp.Diff(want, days); diff != "" {
		t.Errorf("days (-want +got):\n%s", diff)
	}
	if last := *got[1].V; last != float64(at("2026-10-25 23:30:00").Unix()) {
		t.Errorf("Oct 25 ends with %v, want the 23:30 sample", time.Unix(int64(last), 0))
	}
	hours := query(t, s, "meter", at("2026-10-25 00:00:00"), at("2026-10-26 00:00:00"), time.Hour)
	if len(hours) != 25 {
		t.Errorf("got %d hours on Oct 25, want 25", len(hours))
	}
	for i, h := range hours {
		if h.V == nil {
			t.Errorf("hour %d (%v) has no value", i, h.T)
		}
	}
}

func TestRetention(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:10:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	for i := range 10 {
		add(t, s, "power", start.Add(time.Duration(i)*time.Minute), 10)
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	check := func(when string, step time.Duration, want []float64) {
		t.Helper()
		if err := s.Prune(t.Context()); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		got := query(t, s, "power", start, start.Add(10*time.Minute), step)
		if diff := cmp.Diff(want, values(got)); diff != "" {
			t.Errorf("%s, step %v (-want +got):\n%s", when, step, diff)
		}
	}
	gap := []float64{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
	full := []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10}
	check("now", time.Minute, full)
	c.t = start.Add(8 * 24 * time.Hour)
	// Raw samples are gone; 1m steps read 5m buckets, which fill 12:00 and 12:05.
	check("after 8 days", time.Minute, []float64{10, -1, -1, -1, -1, 10, -1, -1, -1, -1})
	check("after 8 days", 5*time.Minute, []float64{10, 10})
	c.t = start.Add(91 * 24 * time.Hour)
	// Only hours are left: 12:00 holds the hour.
	check("after 91 days", time.Minute, append([]float64{10}, gap[1:]...))
	check("after 91 days", 5*time.Minute, []float64{10, -1})
}

func TestFlushFailureKeepsSamples(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:10:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	add(t, s, "power", start, 7)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Flush(ctx); err == nil {
		t.Fatal("Flush with a cancelled context succeeded")
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := values(query(t, s, "power", start, start.Add(time.Minute), time.Minute)); got[0] != 7 {
		t.Errorf("got %v, want the sample kept and written", got)
	}
}

// TestFlushFailureCapsBuffer keeps a day of samples per series through a
// failed write and drops the oldest.
func TestFlushFailureCapsBuffer(t *testing.T) {
	t.Parallel()
	start := at("2026-07-01 00:00:00")
	c := &clock{start.Add(72 * time.Hour)}
	s := open(t, c)
	// Two declared series: room for 2*1440 samples.
	const n = 2*24*60 + 10
	for i := range n {
		add(t, s, "power", start.Add(time.Duration(i)*time.Minute), 1)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Flush(ctx); err == nil {
		t.Fatal("Flush with a cancelled context succeeded")
	}
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	got := values(query(t, s, "power", start, start.Add(20*time.Minute), time.Minute))
	want := []float64{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("oldest samples (-want +got):\n%s", diff)
	}
	last := start.Add((n - 1) * time.Minute)
	if got := values(query(t, s, "power", last, last.Add(time.Minute), time.Minute)); got[0] != 1 {
		t.Errorf("got %v, want the newest sample kept", got)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:00:00")}
	s := open(t, c)
	from := c.t.Add(-time.Hour)
	for _, testCase := range []struct {
		name    string
		err     error
		wantErr string
	}{
		{"add undeclared", s.Add("nope", c.t, 1), "series nope is not declared"},
		{"add NaN", s.Add("power", c.t, nan()), "series power: sample NaN is not a number"},
		{"query undeclared", second(s.Query(t.Context(), "nope", from, c.t, time.Minute)), "series nope is not declared"},
		{"odd step", second(s.Query(t.Context(), "power", from, c.t, 7*time.Minute)), "step 7m0s: want"},
		{"short step", second(s.Query(t.Context(), "power", from, c.t, time.Second)), "step 1s: want"},
		{"empty range", second(s.Query(t.Context(), "power", c.t, c.t, time.Minute)), "is empty"},
		{"too many points", second(s.Query(t.Context(), "power", c.t.Add(-48*time.Hour), c.t, time.Minute)), "2880 points"},
	} {
		if testCase.err == nil || !strings.Contains(testCase.err.Error(), testCase.wantErr) {
			t.Errorf("%s: got %v, want %q", testCase.name, testCase.err, testCase.wantErr)
		}
	}
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}

func second[T any](_ T, err error) error { return err }

// TestDSTSteps checks steps between an hour and a day across both DST
// changes: every bucket starts on the local wall clock and holds the samples
// up to the next one.
func TestDSTSteps(t *testing.T) {
	t.Parallel()
	for _, day := range []string{"2026-03-29", "2026-10-25"} {
		from := at(day + " 00:00:00")
		to := from.AddDate(0, 0, 1)
		c := &clock{to.Add(time.Hour)}
		s := open(t, c)
		var samples []time.Time
		for ts := from; ts.Before(to); ts = ts.Add(10 * time.Minute) {
			add(t, s, "meter", ts, float64(ts.Unix()))
			samples = append(samples, ts)
		}
		if err := s.Flush(t.Context()); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		for _, step := range []time.Duration{2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour} {
			got := query(t, s, "meter", from, to, step)
			if want := int(24 * time.Hour / step); len(got) != want {
				t.Errorf("%s step %v: got %d buckets, want %d", day, step, len(got), want)
				continue
			}
			for i, p := range got {
				if l := p.T.In(amsterdam); l.Minute() != 0 || l.Hour()%int(step/time.Hour) != 0 {
					// 02:00 does not exist on 2026-03-29; it starts at 03:00.
					if !(day == "2026-03-29" && l.Hour() == 3 && step == 2*time.Hour) {
						t.Errorf("%s step %v: bucket %d starts at %v", day, step, i, l)
					}
				}
				end := to
				if i+1 < len(got) {
					end = got[i+1].T
				}
				var last time.Time
				for _, ts := range samples {
					if ts.Before(end) {
						last = ts
					}
				}
				if p.V == nil || *p.V != float64(last.Unix()) {
					t.Errorf("%s step %v: bucket %v holds %v, want the sample at %v", day, step, p.T, p.V, last)
				}
			}
		}
	}
}

func TestConcurrentAdd(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 13:00:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 60 {
			add(t, s, "power", start.Add(time.Duration(i)*time.Minute), 1)
		}
	}()
	for range 5 {
		if err := s.Flush(t.Context()); err != nil {
			t.Errorf("Flush: %v", err)
		}
	}
	<-done
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	for _, p := range query(t, s, "power", start, start.Add(time.Hour), time.Minute) {
		if p.V == nil {
			t.Errorf("no sample at %v", p.T)
		}
	}
}

// TestOldSample writes a sample past the raw retention: its 5-minute bucket
// keeps the value it had.
func TestOldSample(t *testing.T) {
	t.Parallel()
	c := &clock{at("2026-07-01 12:10:00")}
	s := open(t, c)
	start := at("2026-07-01 12:00:00")
	add(t, s, "power", start, 10)
	add(t, s, "power", start.Add(time.Minute), 20)
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	c.t = start.Add(8 * 24 * time.Hour)
	if err := s.Prune(t.Context()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	add(t, s, "power", start.Add(2*time.Minute), 90)
	if err := s.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := values(query(t, s, "power", start, start.Add(5*time.Minute), 5*time.Minute)); got[0] != 15 {
		t.Errorf("got %v, want the bucket kept at 15", got)
	}
}

// TestHistoryLastBucket queries in the last second of a step: the bucket
// holding now is the last one and has the sample.
func TestHistoryLastBucket(t *testing.T) {
	t.Parallel()
	for _, frac := range []time.Duration{0, 500 * time.Millisecond} {
		now := at("2026-10-25 12:00:59").Add(frac)
		s := open(t, &clock{now})
		add(t, s, "power", now, 7)
		if err := s.Flush(t.Context()); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		from, to, step, err := series.HistoryParams{Hours: 1, StepMinutes: 1}.Range(now)
		if err != nil {
			t.Fatalf("Range: %v", err)
		}
		pts := query(t, s, "power", from, to, step)
		if last := pts[len(pts)-1]; !last.T.Equal(at("2026-10-25 12:00:00")) || last.V == nil || *last.V != 7 {
			t.Errorf("now %s: got last point at %v with %v, want 12:00 with 7", now.Format(time.StampMilli), last.T, values(pts[len(pts)-1:]))
		}
	}
}

func TestHistoryParams(t *testing.T) {
	t.Parallel()
	now := at("2026-10-25 12:00:00")
	type window struct {
		From, To time.Time
		Step     time.Duration
	}
	testCases := []struct {
		name    string
		params  series.HistoryParams
		want    window
		wantErr string
	}{
		{name: "defaults", want: window{now.Add(-24 * time.Hour), now.Add(time.Second), 15 * time.Minute}},
		{name: "90 days in days", params: series.HistoryParams{Hours: 2160, StepMinutes: 1440}, want: window{now.Add(-2160 * time.Hour), now.Add(time.Second), 24 * time.Hour}},
		{name: "negative hours", params: series.HistoryParams{Hours: -1}, wantErr: "hours must be 1 to 2160, got -1"},
		{name: "too long", params: series.HistoryParams{Hours: 2161}, wantErr: "hours must be 1 to 2160, got 2161"},
		{name: "negative step", params: series.HistoryParams{StepMinutes: -5}, wantErr: "stepMinutes must divide 1440, got -5"},
		{name: "step not dividing a day", params: series.HistoryParams{StepMinutes: 7}, wantErr: "stepMinutes must divide 1440, got 7"},
		{name: "step over a day", params: series.HistoryParams{StepMinutes: 2880}, wantErr: "stepMinutes must divide 1440, got 2880"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			from, to, step, err := testCase.params.Range(now)
			if testCase.wantErr != "" {
				if err == nil || err.Error() != testCase.wantErr {
					t.Errorf("got error %v, want %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Range: %v", err)
			}
			if diff := cmp.Diff(testCase.want, window{from, to, step}); diff != "" {
				t.Errorf("range (-want +got):\n%s", diff)
			}
		})
	}
}
