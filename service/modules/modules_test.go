package modules_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/service/modules"
	"github.com/xaroth/dashboard/service/storage"
)

type testConfig struct {
	Key string `yaml:"key"`
	// Fail makes Initialize ("init") or Update ("update", "auth") fail.
	Fail string `yaml:"fail"`
}

type testModule struct {
	cfg     *testConfig
	updates int
}

func (m *testModule) Initialize(_ context.Context, cfg *testConfig) error {
	if cfg.Fail == "init" {
		return errors.New("bad init")
	}
	m.cfg = cfg
	return nil
}

func (m *testModule) Enabled(context.Context) bool { return m.cfg.Key != "" }

func (m *testModule) Update(ctx context.Context) error {
	m.updates++
	if err := modules.SaveSnapshot(ctx, map[string]int{"updates": m.updates}); err != nil {
		return err
	}
	switch m.cfg.Fail {
	case "update":
		return errors.New("source down")
	case "auth":
		return fmt.Errorf("token refused: %w", modules.ErrAuth)
	}
	return nil
}

var migrations = fstest.MapFS{"sql/migrations/00001_init.sql": {Data: []byte(
	"-- +goose Up\nCREATE TABLE test_items (id INTEGER PRIMARY KEY);\n")}}

func registration() modules.Registration {
	return modules.Define[testModule, testConfig]("test", func() *testConfig { return &testConfig{} },
		modules.WithUpdateInterval(10*time.Minute),
		modules.WithStaleAfter(time.Hour),
		modules.WithMigrations(migrations),
	)
}

func parse(tb testing.TB, src string) map[string]yaml.Node {
	tb.Helper()
	var out map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(src), &out); err != nil {
		tb.Fatalf("parsing yaml: %v", err)
	}
	return out
}

func load(tb testing.TB, ctx context.Context, src string) (*modules.Set, *storage.DB) {
	tb.Helper()
	db, err := storage.Open(ctx, tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	set, err := modules.Load(ctx, modules.Options{
		Registrations: []modules.Registration{registration()},
		Configs:       parse(tb, src),
		DB:            db,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    http.DefaultClient,
		Now:           time.Now,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set, db
}

func status(tb testing.TB, set *modules.Set) modules.Status {
	tb.Helper()
	st, err := set.Statuses(context.Background())
	if err != nil || len(st) != 1 {
		tb.Fatalf("Statuses: %v, %v", st, err)
	}
	return st[0]
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown module", "nope: {}", "no such module"},
		{"unknown key", "test: {bogus: 1}", "bogus"},
		{"not a mapping", "test: [1]", "mapping"},
		{"bad schedule", "test: {schedule: {update_interval: soon}}", "schedule"},
		{"unknown schedule key", "test: {schedule: {every: 1m}}", "every"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db, err := storage.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatalf("storage: %v", err)
			}
			defer db.Close()
			_, err = modules.Load(t.Context(), modules.Options{
				Registrations: []modules.Registration{registration()},
				Configs:       parse(t, testCase.yaml),
				DB:            db,
				Log:           slog.New(slog.DiscardHandler),
				Now:           time.Now,
			})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	set, db := load(t, t.Context(), "test: {key: k, schedule: {update_interval: 1m}}")
	if got := status(t, set).Interval; got != time.Minute {
		t.Errorf("got interval %v, want the schedule override 1m", got)
	}
	var n int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM goose_test").Scan(&n); err != nil || n == 0 {
		t.Errorf("module migrations not applied: %d, %v", n, err)
	}
}

func TestLoadInitializeFailure(t *testing.T) {
	t.Parallel()
	set, _ := load(t, t.Context(), "test: {key: k, fail: init}")
	got := status(t, set)
	if got.Phase != modules.PhaseInitFail || got.Error != "bad init" {
		t.Errorf("got %s %q, want %s \"bad init\"", got.Phase, got.Error, modules.PhaseInitFail)
	}
	// Run returns at once: nothing to schedule.
	set.Run(t.Context())
}

// run starts the schedule in a synctest bubble and stops it at the end.
func run(t *testing.T, src string, check func(set *modules.Set, db *storage.DB)) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		set, db := load(t, ctx, src)
		done := make(chan struct{})
		go func() {
			set.Run(ctx)
			close(done)
		}()
		check(set, db)
		cancel()
		<-done
	})
}

func snapshot(tb testing.TB, db *storage.DB) map[string]int {
	tb.Helper()
	st, err := storage.New(db.Reader).GetState(context.Background(), "test")
	if err != nil {
		tb.Fatalf("GetState: %v", err)
	}
	var out map[string]int
	if err := yaml.Unmarshal([]byte(*st.Data), &out); err != nil {
		tb.Fatalf("snapshot: %v", err)
	}
	return out
}

func TestScheduleSuccess(t *testing.T) {
	run(t, "test: {key: k}", func(set *modules.Set, db *storage.DB) {
		time.Sleep(31 * time.Second) // past the start jitter
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseWaiting || got.FetchedAt == nil || got.Error != "" || got.Stale {
			t.Fatalf("after first update: got %+v", got)
		}
		if want := got.FetchedAt.Add(10 * time.Minute); !got.NextRun.Equal(want) {
			t.Errorf("got next run %v, want %v", got.NextRun, want)
		}
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if diff := cmp.Diff(map[string]int{"updates": 2}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
	})
}

func TestScheduleDisabled(t *testing.T) {
	run(t, "test: {}", func(set *modules.Set, db *storage.DB) {
		time.Sleep(31 * time.Second)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseDisabled || got.AttemptedAt != nil || !got.Stale {
			t.Errorf("got %+v, want disabled, never attempted, stale", got)
		}
	})
}

func TestScheduleBackoff(t *testing.T) {
	run(t, "test: {key: k, fail: update}", func(set *modules.Set, db *storage.DB) {
		time.Sleep(31 * time.Second)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseFailed || got.Error != "source down" || got.FetchedAt != nil {
			t.Fatalf("after first failure: got %+v", got)
		}
		if d := got.NextRun.Sub(*got.AttemptedAt); d != 30*time.Second {
			t.Errorf("got first retry in %v, want 30s", d)
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		got = status(t, set)
		if d := got.NextRun.Sub(*got.AttemptedAt); d != time.Minute {
			t.Errorf("got second retry in %v, want 1m", d)
		}
		if n := snapshot(t, db)["updates"]; n != 2 {
			t.Errorf("got %d updates, want 2", n)
		}
	})
}

func TestScheduleAuthStops(t *testing.T) {
	run(t, "test: {key: k, fail: auth}", func(set *modules.Set, db *storage.DB) {
		time.Sleep(31 * time.Second)
		synctest.Wait()
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseAuth || !strings.Contains(got.Error, "token refused") {
			t.Errorf("got %+v, want auth phase with the error", got)
		}
		if n := snapshot(t, db)["updates"]; n != 1 {
			t.Errorf("got %d updates, want 1: no retries after an auth error", n)
		}
	})
}
