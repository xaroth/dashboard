package modules_test

import (
	"context"
	"encoding/json"
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
	// FailAfter makes updates fail after this many successes.
	FailAfter int `yaml:"fail_after"`
	// SkipSave makes updates succeed without saving a snapshot.
	SkipSave bool `yaml:"skip_save"`
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
	if !m.cfg.SkipSave || m.updates == 1 {
		if err := modules.SaveSnapshot(ctx, map[string]int{"updates": m.updates}); err != nil {
			return err
		}
	}
	if m.cfg.FailAfter > 0 && m.updates > m.cfg.FailAfter {
		return errors.New("source down")
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

func load(ctx context.Context, tb testing.TB, src string) (*modules.Set, *storage.DB) {
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
	st, err := set.Statuses(tb.Context())
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
		{"negative interval", "test: {schedule: {update_interval: -1m}}", "positive"},
		{"negative stale after", "test: {schedule: {stale_after: -1m}}", "positive"},
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
	set, db := load(t.Context(), t, "test: {key: k, schedule: {update_interval: 1m}}")
	if got := status(t, set).Interval; got != time.Minute {
		t.Errorf("got interval %v, want the schedule override 1m", got)
	}
	var n int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM goose_test").Scan(&n); err != nil || n == 0 {
		t.Errorf("module migrations not applied: %d, %v", n, err)
	}
}

func TestLoadEmptySection(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test:")
	if got := status(t, set); got.Phase != modules.PhaseWaiting || got.Interval != 10*time.Minute {
		t.Errorf("got %+v, want defaults", got)
	}
}

func TestDefineRejectsZeroInterval(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("Define accepted a zero update interval")
		}
	}()
	modules.Define[testModule, testConfig]("zero", func() *testConfig { return &testConfig{} }, modules.WithUpdateInterval(0))
}

func TestLoadInitializeFailure(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test: {key: k, fail: init}")
	got := status(t, set)
	if got.Phase != modules.PhaseInitFail || got.Error != "bad init" {
		t.Errorf("got %s %q, want %s \"bad init\"", got.Phase, got.Error, modules.PhaseInitFail)
	}
	// Run returns at once: nothing to schedule.
	set.Run(t.Context())
}

// run starts the schedule in a synctest bubble and stops it at the end.
func run(t *testing.T, src string, check func(t *testing.T, set *modules.Set, db *storage.DB)) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		set, db := load(ctx, t, src)
		done := make(chan struct{})
		go func() {
			set.Run(ctx)
			close(done)
		}()
		check(t, set, db)
		cancel()
		<-done
	})
}

// waitFirstAttempt advances the fake clock in 1 s steps until the first
// update ran, so the start jitter does not matter.
func waitFirstAttempt(t *testing.T, set *modules.Set) {
	t.Helper()
	for status(t, set).AttemptedAt == nil {
		time.Sleep(time.Second)
		synctest.Wait()
	}
}

func snapshot(tb testing.TB, db *storage.DB) map[string]int {
	tb.Helper()
	st, err := storage.New(db.Reader).GetState(tb.Context(), "test")
	if err != nil {
		tb.Fatalf("GetState: %v", err)
	}
	var out map[string]int
	if err := json.Unmarshal([]byte(*st.Data), &out); err != nil {
		tb.Fatalf("snapshot: %v", err)
	}
	return out
}

func TestScheduleSuccess(t *testing.T) {
	run(t, "test: {key: k}", func(t *testing.T, set *modules.Set, db *storage.DB) {
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
	run(t, "test: {}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		time.Sleep(31 * time.Second)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseDisabled || got.AttemptedAt != nil || !got.Stale {
			t.Errorf("got %+v, want disabled, never attempted, stale", got)
		}
	})
}

func TestScheduleBackoff(t *testing.T) {
	run(t, "test: {key: k, fail: update}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
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
		if got.Error != "source down" || got.FetchedAt != nil {
			t.Errorf("got %+v, want still failing, never fetched", got)
		}
	})
}

func TestScheduleRecovers(t *testing.T) {
	// Fails from the second update on; then fail_after is the only knob, so
	// recovery is shown by the snapshot of the first update surviving.
	run(t, "test: {key: k, fail_after: 1}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		first := status(t, set)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseFailed || !got.FetchedAt.Equal(*first.FetchedAt) {
			t.Errorf("got %+v, want failed with the first fetch time", got)
		}
		// The failed update's snapshot is dropped; the first one stays.
		if diff := cmp.Diff(map[string]int{"updates": 1}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
	})
}

func TestScheduleKeepsSnapshotWhenNotSaved(t *testing.T) {
	run(t, "test: {key: k, skip_save: true}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if diff := cmp.Diff(map[string]int{"updates": 1}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
		if got := status(t, set); got.Error != "" || got.Phase != modules.PhaseWaiting {
			t.Errorf("got %+v, want a clean second update", got)
		}
	})
}

func TestBackoffCap(t *testing.T) {
	run(t, "test: {key: k, fail: update}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		// 30s, 1m, 2m, ... reaches the 6h cap after 11 failures.
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		got := status(t, set)
		if d := got.NextRun.Sub(*got.AttemptedAt); d != 6*time.Hour {
			t.Errorf("got retry in %v, want the 6h cap", d)
		}
	})
}

func TestScheduleAuthStops(t *testing.T) {
	run(t, "test: {key: k, fail: auth}", func(t *testing.T, set *modules.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		first := *status(t, set).AttemptedAt
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != modules.PhaseAuth || !strings.Contains(got.Error, "token refused") {
			t.Errorf("got %+v, want auth phase with the error", got)
		}
		if !got.AttemptedAt.Equal(first) {
			t.Errorf("got attempt at %v, want only the first at %v: no retries after an auth error", got.AttemptedAt, first)
		}
	})
}
