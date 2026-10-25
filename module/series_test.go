package module_test

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/series"
	"github.com/xaroth/dashboard/storage"
)

type meterConfig struct{}

type meter struct{ reading float64 }

func (*meter) Initialize(context.Context, *meterConfig) error { return nil }
func (*meter) Enabled(context.Context) (bool, error)          { return true, nil }

func (m *meter) Update(ctx context.Context) error {
	m.reading++
	return module.Series(ctx).Add("power", module.Clock(ctx)(), m.reading)
}

type powerParams struct{}

func (*meter) power(ctx context.Context, _ powerParams) ([]series.Point, error) {
	now := module.Clock(ctx)()
	if err := module.Series(ctx).Add("power", now, 1); err == nil {
		return nil, errorString("Add worked in a query")
	}
	return module.Series(ctx).Query(ctx, "power", now.Add(-2*time.Minute), now.Add(time.Minute), time.Minute)
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestSeries(t *testing.T) {
	t.Parallel()
	db, err := storage.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 7, 1, 12, 0, 30, 0, time.UTC)
	set, err := module.Load(t.Context(), module.Options{
		Registrations: []module.Registration{module.Define[meter, meterConfig]("meter", func() *meterConfig { return &meterConfig{} },
			module.WithSeries("power", series.Rate),
			module.WithQuery("power", (*meter).power),
		)},
		Migrator: db,
		Writer:   db.Writer,
		Reader:   db.Reader,
		Log:      slog.New(slog.DiscardHandler),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for range 2 {
		if err := set.Update(t.Context(), "meter"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		now = now.Add(time.Minute)
	}
	if err := set.FlushSeries(t.Context()); err != nil {
		t.Fatalf("FlushSeries: %v", err)
	}
	res := set.Resolve(t.Context(), "meter", "power", nil)
	if res.Error != "" {
		t.Fatalf("power: %s", res.Error)
	}
	var got []string
	for _, p := range res.Value.([]series.Point) {
		v := "-"
		if p.V != nil {
			v = strconv.FormatFloat(*p.V, 'f', -1, 64)
		}
		got = append(got, p.T.UTC().Format("15:04")+" "+v)
	}
	if want := "12:00 1 12:01 2 12:02 - 12:03 -"; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestReservedName(t *testing.T) {
	t.Parallel()
	db, err := storage.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = module.Load(t.Context(), module.Options{
		Registrations: []module.Registration{module.Define[meter, meterConfig]("series", func() *meterConfig { return &meterConfig{} })},
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		Now:           time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), `module name "series" is reserved`) {
		t.Errorf("got %v, want the name rejected", err)
	}
}

// TestSeriesShutdown checks that Run flushes the samples of the last update
// when it stops.
func TestSeriesShutdown(t *testing.T) {
	t.Parallel()
	db, err := storage.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 7, 1, 12, 0, 30, 0, time.UTC)
	set, err := module.Load(t.Context(), module.Options{
		Registrations: []module.Registration{module.Define[meter, meterConfig]("meter", func() *meterConfig { return &meterConfig{} },
			module.WithSeries("power", series.Rate),
			module.WithQuery("power", (*meter).power),
			module.WithUpdateInterval(time.Hour),
		)},
		Migrator: db,
		Writer:   db.Writer,
		Reader:   db.Reader,
		Log:      slog.New(slog.DiscardHandler),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		set.Run(ctx)
		close(done)
	}()
	if err := set.Update(t.Context(), "meter"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	cancel()
	<-done
	res := set.Resolve(t.Context(), "meter", "power", nil)
	if pts, _ := res.Value.([]series.Point); res.Error != "" || len(pts) == 0 || pts[2].V == nil {
		t.Errorf("got %v %s, want the sample at 12:00", res.Value, res.Error)
	}
}
