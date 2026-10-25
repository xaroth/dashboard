// Package series stores time series: samples are buffered in memory, flushed
// once a minute and rolled up into 5 minute, hourly and daily buckets.
package series

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"
)

// Migrations are the series tables, in sql/migrations.
//
//go:embed sql/migrations/*.sql
var Migrations embed.FS

// Kind decides how samples combine into a bucket.
type Kind int

const (
	// Rate buckets hold the average of their samples, e.g. power.
	Rate Kind = iota + 1
	// Counter buckets hold their last sample, e.g. a meter reading.
	Counter
)

// Point is one bucket of a query; V is null for a bucket without samples.
type Point struct {
	T time.Time `json:"t"`
	V *float64  `json:"v"`
}

const (
	FlushInterval = time.Minute
	pruneInterval = time.Hour
	rawRetention  = 7 * 24 * time.Hour
	// Bounds a query's result: an E1001 is 800 pixels wide.
	maxPoints = 2000
	// Rolled up again at startup: samples flushed before a crash may have
	// missed their rollup.
	startupRollup = 24 * time.Hour
)

type level struct {
	res time.Duration
	// keep is the retention; zero keeps forever.
	keep time.Duration
}

var levels = []level{
	{res: 5 * time.Minute, keep: 90 * 24 * time.Hour},
	{res: time.Hour},
	{res: 24 * time.Hour},
}

// Options are what Open needs.
type Options struct {
	Writer *sql.DB
	Reader *sql.DB
	// Location aligns buckets of an hour or longer.
	Location *time.Location
	Now      func() time.Time
	Log      *slog.Logger
	// Series are the names that may be written, with their kind.
	Series map[string]Kind
}

// Store buffers, stores and queries series.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	loc    *time.Location
	now    func() time.Time
	log    *slog.Logger
	series map[string]*def

	mu      sync.Mutex
	pending []sample
	// dirty is the earliest sample per series flushed since its last rollup.
	dirty map[*def]int64
}

type def struct {
	name string
	id   int64
	kind Kind
}

type sample struct {
	def   *def
	ts    int64
	value float64
}

// Open registers the series names. Migrations have run.
func Open(ctx context.Context, o Options) (*Store, error) {
	s := &Store{writer: o.Writer, reader: o.Reader, loc: o.Location, now: o.Now, log: o.Log, series: map[string]*def{}, dirty: map[*def]int64{}}
	q := New(o.Writer)
	since := o.Now().Add(-startupRollup).Unix()
	for name, kind := range o.Series {
		if kind != Rate && kind != Counter {
			return nil, fmt.Errorf("series %s: unknown kind %d", name, kind)
		}
		if err := q.AddName(ctx, name); err != nil {
			return nil, fmt.Errorf("adding series %s: %w", name, err)
		}
		id, err := q.GetName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("reading series %s: %w", name, err)
		}
		d := &def{name: name, id: id, kind: kind}
		s.series[name] = d
		s.dirty[d] = since
	}
	return s, nil
}

// Declared reports whether any series may be written.
func (s *Store) Declared() bool { return len(s.series) > 0 }

// Add buffers a sample; it is stored with the next flush. A second sample
// in the same second replaces the first.
func (s *Store) Add(name string, t time.Time, v float64) error {
	d, ok := s.series[name]
	if !ok {
		return fmt.Errorf("series %s is not declared", name)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("series %s: sample %v is not a number", name, v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, sample{def: d, ts: t.Unix(), value: v})
	return nil
}

// Run flushes and rolls up every minute and prunes every hour until ctx is
// cancelled, then flushes once more.
func (s *Store) Run(ctx context.Context) {
	tick := time.NewTicker(FlushInterval)
	defer tick.Stop()
	var pruned time.Time
	for {
		select {
		case <-ctx.Done():
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := s.Flush(ctx); err != nil {
				s.log.Error("flushing series at shutdown", "err", err)
			}
			return
		case <-tick.C:
		}
		if err := s.Flush(ctx); err != nil {
			s.log.Warn("flushing series", "err", err)
		}
		if now := s.now(); now.Sub(pruned) >= pruneInterval {
			if err := s.Prune(ctx); err != nil {
				s.log.Warn("pruning series", "err", err)
			}
			pruned = now
		}
	}
}

// Flush writes the buffered samples in one transaction and rolls up the
// buckets they fall in. Samples of a failed write stay buffered.
func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(batch) > 0 {
		if err := s.write(ctx, batch); err != nil {
			s.mu.Lock()
			s.pending = append(batch, s.pending...)
			s.mu.Unlock()
			return err
		}
	}
	return s.rollup(ctx)
}

func (s *Store) write(ctx context.Context, batch []sample) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("writing samples: %w", err)
	}
	defer tx.Rollback()
	q := New(tx)
	for _, smp := range batch {
		if err := q.AddSample(ctx, AddSampleParams{Series: smp.def.id, Ts: smp.ts, Value: smp.value}); err != nil {
			return fmt.Errorf("writing %s: %w", smp.def.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("writing samples: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, smp := range batch {
		s.markDirty(smp.def, smp.ts)
	}
	return nil
}

// markDirty is called with s.mu held.
func (s *Store) markDirty(d *def, ts int64) {
	if prev, ok := s.dirty[d]; !ok || ts < prev {
		s.dirty[d] = ts
	}
}

// bucket is a row of a level, or a raw sample with n 1.
type bucket struct {
	ts    int64
	value float64
	n     int64
}

// rollup recomputes, for every series with new samples, each level's
// buckets from the one holding the earliest new sample up to now.
func (s *Store) rollup(ctx context.Context) error {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = map[*def]int64{}
	s.mu.Unlock()
	var errs []error
	for d, from := range dirty {
		if err := s.rollupSeries(ctx, d, from); err != nil {
			errs = append(errs, fmt.Errorf("rolling up %s: %w", d.name, err))
			s.mu.Lock()
			s.markDirty(d, from)
			s.mu.Unlock()
		}
	}
	return errors.Join(errs...)
}

func (s *Store) rollupSeries(ctx context.Context, d *def, from int64) error {
	// No upper bound: samples stamped ahead of the clock roll up too.
	const end = math.MaxInt64
	// 5-minute buckets older than the raw retention would be rebuilt from
	// what is left of their samples.
	if cutoff := s.bucketStart(s.now().Add(-rawRetention), levels[0].res).Add(levels[0].res).Unix(); from < cutoff {
		from = cutoff
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := New(tx)
	start := time.Unix(from, 0)
	for i, lv := range levels {
		start = s.bucketStart(start, lv.res)
		var src []bucket
		if i == 0 {
			rows, err := q.ListSamples(ctx, ListSamplesParams{Series: d.id, From: start.Unix(), To: end})
			if err != nil {
				return err
			}
			for _, r := range rows {
				src = append(src, bucket{ts: r.Ts, value: r.Value, n: 1})
			}
		} else if src, err = s.buckets(ctx, q, d, levels[i-1].res, start.Unix(), end); err != nil {
			return err
		}
		for _, b := range s.group(d.kind, src, lv.res) {
			if err := q.SetBucket(ctx, SetBucketParams{Series: d.id, Res: int64(lv.res / time.Second), Ts: b.ts, Value: b.value, N: b.n}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) buckets(ctx context.Context, q *Queries, d *def, res time.Duration, from, to int64) ([]bucket, error) {
	rows, err := q.ListBuckets(ctx, ListBucketsParams{Series: d.id, Res: int64(res / time.Second), From: from, To: to})
	if err != nil {
		return nil, err
	}
	out := make([]bucket, len(rows))
	for i, r := range rows {
		out[i] = bucket{ts: r.Ts, value: r.Value, n: r.N}
	}
	return out, nil
}

// group combines rows sorted by time into buckets of size d.
func (s *Store) group(kind Kind, rows []bucket, d time.Duration) []bucket {
	var out []bucket
	for _, r := range rows {
		ts := s.bucketStart(time.Unix(r.ts, 0), d).Unix()
		if len(out) == 0 || out[len(out)-1].ts != ts {
			out = append(out, bucket{ts: ts})
		}
		b := &out[len(out)-1]
		switch kind {
		case Rate:
			b.value = (b.value*float64(b.n) + r.value*float64(r.n)) / float64(b.n+r.n)
		case Counter:
			b.value = r.value
		}
		b.n += r.n
	}
	return out
}

// Prune deletes samples and buckets past their retention.
func (s *Store) Prune(ctx context.Context) error {
	now := s.now()
	q := New(s.writer)
	for _, d := range s.series {
		if err := q.DeleteSamples(ctx, DeleteSamplesParams{Series: d.id, Before: now.Add(-rawRetention).Unix()}); err != nil {
			return fmt.Errorf("pruning %s: %w", d.name, err)
		}
		for _, lv := range levels {
			if lv.keep == 0 {
				continue
			}
			err := q.DeleteBuckets(ctx, DeleteBucketsParams{Series: d.id, Res: int64(lv.res / time.Second), Before: now.Add(-lv.keep).Unix()})
			if err != nil {
				return fmt.Errorf("pruning %s: %w", d.name, err)
			}
		}
	}
	return nil
}

// Query returns name in buckets of step from the bucket holding from up to
// to, reading the coarsest stored level that is no coarser than step and
// still holds from. step is at least a minute and divides a day, or is a
// whole number of days; steps of an hour or more align to local time.
func (s *Store) Query(ctx context.Context, name string, from, to time.Time, step time.Duration) ([]Point, error) {
	d, ok := s.series[name]
	if !ok {
		return nil, fmt.Errorf("series %s is not declared", name)
	}
	if step < time.Minute || step%time.Second != 0 || (step < 24*time.Hour && (24*time.Hour)%step != 0) || (step >= 24*time.Hour && step%(24*time.Hour) != 0) {
		return nil, fmt.Errorf("step %v: want at least 1m, dividing a day or a whole number of days", step)
	}
	if !to.After(from) {
		return nil, fmt.Errorf("range %v to %v is empty", from, to)
	}
	start := s.bucketStart(from, step)
	if n := to.Sub(start) / step; n > maxPoints {
		return nil, fmt.Errorf("%d points over %v in steps of %v, want at most %d", n, to.Sub(from), step, maxPoints)
	}

	q := New(s.reader)
	var rows []bucket
	var err error
	if src := s.source(start, step); src == 0 {
		var raw []ListSamplesRow
		raw, err = q.ListSamples(ctx, ListSamplesParams{Series: d.id, From: start.Unix(), To: to.Unix()})
		for _, r := range raw {
			rows = append(rows, bucket{ts: r.Ts, value: r.Value, n: 1})
		}
	} else {
		rows, err = s.buckets(ctx, q, d, src, start.Unix(), to.Unix())
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	grouped := s.group(d.kind, rows, step)
	var out []Point
	for t := start; t.Before(to); t = s.nextBucket(t, step) {
		p := Point{T: t}
		if i, ok := slices.BinarySearchFunc(grouped, t.Unix(), func(b bucket, ts int64) int { return int(b.ts - ts) }); ok {
			v := grouped[i].value
			p.V = &v
		}
		out = append(out, p)
	}
	return out, nil
}

// source is the resolution to read for buckets of step from start on: zero
// for raw samples.
func (s *Store) source(start time.Time, step time.Duration) time.Duration {
	age := s.now().Sub(start)
	src := time.Duration(-1)
	if age <= rawRetention {
		src = 0
	}
	for _, lv := range levels {
		holds := lv.keep == 0 || age <= lv.keep
		if holds && (lv.res <= step || src < 0) {
			src = lv.res
		}
	}
	return src
}

// bucketStart is the start of the bucket of size d holding t: Unix time
// multiples under an hour, local hours at an hour, local wall-clock
// multiples above it and local midnights from a day.
func (s *Store) bucketStart(t time.Time, d time.Duration) time.Time {
	size := int64(d / time.Second)
	l := t.In(s.loc)
	y, m, day := l.Date()
	switch {
	case d >= 24*time.Hour:
		days := time.Date(y, m, day, 0, 0, 0, 0, time.UTC).Unix() / 86400
		days -= mod(days, int64(d/(24*time.Hour)))
		y, m, day = time.Unix(days*86400, 0).UTC().Date()
		return time.Date(y, m, day, 0, 0, 0, 0, s.loc)
	case d > time.Hour:
		// Wall clock, so a bucket spanning a DST change keeps one start.
		sec := int64(l.Hour()*3600 + l.Minute()*60 + l.Second())
		return s.earliest(time.Date(y, m, day, 0, 0, int(sec-mod(sec, size)), 0, s.loc))
	case d == time.Hour:
		// By instant: both hours of a DST fall back are buckets.
		_, off := l.Zone()
		u := t.Unix()
		return time.Unix(u-mod(u+int64(off), size), 0).In(s.loc)
	}
	u := t.Unix()
	return time.Unix(u-mod(u, size), 0).In(s.loc)
}

func (s *Store) nextBucket(t time.Time, d time.Duration) time.Time {
	l := t.In(s.loc)
	y, m, day := l.Date()
	switch {
	case d >= 24*time.Hour:
		return time.Date(y, m, day+int(d/(24*time.Hour)), 0, 0, 0, 0, s.loc)
	case d > time.Hour:
		wall := time.Date(y, m, day, l.Hour(), l.Minute(), l.Second()+int(d/time.Second), 0, s.loc)
		if next := s.bucketStart(wall, d); next.After(t) {
			return next
		}
	}
	return t.Add(d)
}

// earliest is t, or the instant an hour before when that reads the same on
// the wall clock: the first of a repeated hour at a DST fall back.
func (s *Store) earliest(t time.Time) time.Time {
	if prev := t.Add(-time.Hour); prev.In(s.loc).Format(time.DateTime) == t.In(s.loc).Format(time.DateTime) {
		return prev
	}
	return t
}

func mod(a, b int64) int64 {
	return ((a % b) + b) % b
}
