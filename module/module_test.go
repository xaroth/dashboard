package module_test

import (
	"context"
	"database/sql"
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

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/storage"
)

type testConfig struct {
	Key string `yaml:"key"`
	// Fail makes Initialize ("init") or Update ("update", "auth") fail.
	Fail string `yaml:"fail"`
	// FailAfter makes updates fail after this many successes.
	FailAfter int `yaml:"fail_after"`
	// FailUntil limits Fail to the first this many updates; 0 is all of them.
	FailUntil int `yaml:"fail_until"`
	// SkipSave makes updates succeed without saving a snapshot.
	SkipSave bool `yaml:"skip_save"`
	// Panic makes Initialize ("init"), Enabled ("enabled") or Update ("update") panic.
	Panic string `yaml:"panic"`
}

type testModule struct {
	cfg     *testConfig
	updates int
}

var errBadInit = errors.New("bad init")

func (m *testModule) Initialize(_ context.Context, cfg *testConfig) error {
	if cfg.Fail == "init" {
		return errBadInit
	}
	if cfg.Panic == "init" {
		panic("boom")
	}
	m.cfg = cfg
	return nil
}

func (m *testModule) Enabled(context.Context) bool {
	if m.cfg.Panic == "enabled" {
		panic("boom")
	}
	return m.cfg.Key != ""
}

func (m *testModule) Update(ctx context.Context) error {
	m.updates++
	if m.cfg.Panic == "update" {
		panic("boom")
	}
	if !m.cfg.SkipSave || m.updates == 1 {
		if err := module.SaveSnapshot(ctx, map[string]int{"updates": m.updates}); err != nil {
			return err
		}
	}
	if m.cfg.FailAfter > 0 && m.updates > m.cfg.FailAfter {
		return errors.New("source down")
	}
	if m.cfg.FailUntil > 0 && m.updates > m.cfg.FailUntil {
		return nil
	}
	switch m.cfg.Fail {
	case "update":
		return errors.New("source down")
	case "auth":
		return fmt.Errorf("token refused: %w", module.ErrAuth)
	}
	return nil
}

var migrations = fstest.MapFS{"sql/migrations/00001_init.sql": {Data: []byte(
	"-- +goose Up\nCREATE TABLE test_items (id INTEGER PRIMARY KEY);\n")}}

func registration() module.Registration {
	return namedRegistration("test")
}

// namedRegistration is the test module under another name, for tests that
// read metrics or spans, which are shared by every test in the package.
func namedRegistration(name string) module.Registration {
	return module.Define[testModule, testConfig](name, func() *testConfig { return &testConfig{} },
		module.WithUpdateInterval(10*time.Minute),
		module.WithStaleAfter(time.Hour),
		module.WithMigrations(migrations),
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

func load(ctx context.Context, tb testing.TB, src string) (*module.Set, *storage.DB) {
	tb.Helper()
	return loadRegs(ctx, tb, src, []module.Registration{registration()})
}

func loadRegs(ctx context.Context, tb testing.TB, src string, regs []module.Registration) (*module.Set, *storage.DB) {
	tb.Helper()
	db, err := storage.Open(ctx, tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	set, err := module.Load(ctx, module.Options{
		Registrations: regs,
		Configs:       parse(tb, src),
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    http.DefaultClient,
		Now:           time.Now,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set, db
}

func status(tb testing.TB, set *module.Set) module.Status {
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
			_, err = module.Load(t.Context(), module.Options{
				Registrations: []module.Registration{registration()},
				Configs:       parse(t, testCase.yaml),
				Migrator:      db,
				Writer:        db.Writer,
				Reader:        db.Reader,
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
		t.Errorf("got %d module migration rows (%v), want at least 1", n, err)
	}
}

func TestLoadEmptySection(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test:")
	if got := status(t, set); got.Phase != module.PhaseWaiting || got.Interval != 10*time.Minute {
		t.Errorf("got %+v, want defaults", got)
	}
}

func TestDefineRejectsZeroInterval(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("got no panic for a zero update interval, want one")
		}
	}()
	module.Define[testModule, testConfig]("zero", func() *testConfig { return &testConfig{} }, module.WithUpdateInterval(0))
}

func TestLoadInitializeFailure(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test: {key: k, fail: init}")
	got := status(t, set)
	if got.Phase != module.PhaseInitFail || got.Error != "bad init" {
		t.Errorf("got %s %q, want %s \"bad init\"", got.Phase, got.Error, module.PhaseInitFail)
	}
	// Run returns at once: nothing to schedule.
	set.Run(t.Context())
}

func TestLoadInitializePanic(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test: {key: k, panic: init}")
	if got := status(t, set); got.Phase != module.PhaseInitFail || !strings.Contains(got.Error, "panicked: boom") {
		t.Errorf("got %s %q, want %s with the panic", got.Phase, got.Error, module.PhaseInitFail)
	}
}

func TestUpdatePanic(t *testing.T) {
	t.Parallel()
	set, _ := load(t.Context(), t, "test: {key: k, panic: update}")
	if err := set.Update(t.Context(), "test"); err == nil || !strings.Contains(err.Error(), "panicked: boom") {
		t.Errorf("got %v, want the panic as an error", err)
	}
	if got := status(t, set); got.Error != "panicked: boom" {
		t.Errorf("got error %q, want the panic recorded", got.Error)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		yaml   string
		module string
		want   error
	}{
		{"unknown module", "test: {key: k}", "nope", module.ErrUnknownModule},
		{"initialize failed", "test: {key: k, fail: init}", "test", errBadInit},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set, _ := load(t.Context(), t, testCase.yaml)
			if err := set.Update(t.Context(), testCase.module); !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestStaleness(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		set, _ := load(t.Context(), t, "test: {key: k, schedule: {stale_after: 2h}}")
		if err := set.Update(t.Context(), "test"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		// Sequential: the clock only moves forward.
		steps := []struct {
			advance   time.Duration
			wantStale []string
		}{
			{0, []string{}},
			{2 * time.Hour, []string{}}, // stale only past stale_after
			{time.Second, []string{"test"}},
		}
		for i, step := range steps {
			time.Sleep(step.advance)
			got, err := set.StaleModules(t.Context())
			if err != nil {
				t.Fatalf("step %d: StaleModules: %v", i, err)
			}
			if diff := cmp.Diff(step.wantStale, got); diff != "" {
				t.Errorf("step %d: stale modules (-want +got):\n%s", i, diff)
			}
			if got, want := status(t, set).Stale, len(step.wantStale) > 0; got != want {
				t.Errorf("step %d: got status stale %v, want %v", i, got, want)
			}
		}
	})
}

func TestSchedulePanics(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"enabled", "update"} {
		t.Run(in, func(t *testing.T) {
			run(t, "test: {key: k, panic: "+in+"}", func(t *testing.T, set *module.Set, db *storage.DB) {
				waitFirstAttempt(t, set)
				got := status(t, set)
				if got.Phase != module.PhaseFailed || !strings.Contains(got.Error, "panicked: boom") {
					t.Errorf("got %+v, want failed with the panic", got)
				}
				if d := got.NextRun.Sub(*got.AttemptedAt); d != 30*time.Second {
					t.Errorf("got retry in %v, want the 30s backoff", d)
				}
			})
		})
	}
}

// run starts the schedule in a synctest bubble and stops it at the end.
func run(t *testing.T, src string, check func(t *testing.T, set *module.Set, db *storage.DB)) {
	t.Helper()
	runRegs(t, src, []module.Registration{registration()}, check)
}

// runRegs is run with regs.
func runRegs(t *testing.T, src string, regs []module.Registration, check func(t *testing.T, set *module.Set, db *storage.DB)) {
	t.Helper()
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		set, db := loadRegs(ctx, t, src, regs)
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
func waitFirstAttempt(t *testing.T, set *module.Set) {
	t.Helper()
	for status(t, set).AttemptedAt == nil {
		time.Sleep(time.Second)
		synctest.Wait()
	}
}

func snapshot(tb testing.TB, db *storage.DB) map[string]int {
	tb.Helper()
	st, err := module.New(db.Reader).GetState(tb.Context(), "test")
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
	run(t, "test: {key: k}", func(t *testing.T, set *module.Set, db *storage.DB) {
		time.Sleep(31 * time.Second) // past the start jitter
		synctest.Wait()
		got := status(t, set)
		if got.Phase != module.PhaseWaiting || got.FetchedAt == nil || got.Error != "" || got.Stale {
			t.Fatalf("after first update: got %+v, want waiting, fetched, fresh", got)
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
	run(t, "test: {}", func(t *testing.T, set *module.Set, db *storage.DB) {
		time.Sleep(31 * time.Second)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != module.PhaseDisabled || got.AttemptedAt != nil || !got.Stale {
			t.Errorf("got %+v, want disabled, never attempted, stale", got)
		}
	})
}

func TestScheduleBackoff(t *testing.T) {
	run(t, "test: {key: k, fail: update}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		got := status(t, set)
		if got.Phase != module.PhaseFailed || got.Error != "source down" || got.FetchedAt != nil {
			t.Fatalf("after first failure: got %+v, want failed with the error, never fetched", got)
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
	run(t, "test: {key: k, fail_after: 1}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		first := status(t, set)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != module.PhaseFailed || !got.FetchedAt.Equal(*first.FetchedAt) {
			t.Errorf("got %+v, want failed with the first fetch time", got)
		}
		// The failed update's snapshot is dropped; the first one stays.
		if diff := cmp.Diff(map[string]int{"updates": 1}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
	})
}

func TestScheduleKeepsSnapshotWhenNotSaved(t *testing.T) {
	run(t, "test: {key: k, skip_save: true}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if diff := cmp.Diff(map[string]int{"updates": 1}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
		if got := status(t, set); got.Error != "" || got.Phase != module.PhaseWaiting {
			t.Errorf("got %+v, want a clean second update", got)
		}
	})
}

func TestBackoffCap(t *testing.T) {
	run(t, "test: {key: k, fail: update}", func(t *testing.T, set *module.Set, db *storage.DB) {
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
	run(t, "test: {key: k, fail: auth}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		first := *status(t, set).AttemptedAt
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		got := status(t, set)
		if got.Phase != module.PhaseAuth || !strings.Contains(got.Error, "token refused") {
			t.Errorf("got %+v, want auth phase with the error", got)
		}
		if !got.AttemptedAt.Equal(first) {
			t.Errorf("got attempt at %v, want only the first at %v: no retries after an auth error", got.AttemptedAt, first)
		}
	})
}

func TestManualUpdateResetsBackoff(t *testing.T) {
	run(t, "test: {key: k, fail: update, fail_until: 2}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		time.Sleep(30 * time.Second) // second failure, retry in 1m
		synctest.Wait()
		if err := set.Update(t.Context(), "test"); err != nil {
			t.Fatalf("manual update: %v", err)
		}
		synctest.Wait()
		got := status(t, set)
		if got.Phase != module.PhaseWaiting || !got.NextRun.Equal(time.Now().Add(10*time.Minute)) {
			t.Errorf("got %s next run %v, want waiting a full interval from now %v", got.Phase, got.NextRun, time.Now())
		}
	})
}

func TestManualUpdateResumesAfterAuth(t *testing.T) {
	run(t, "test: {key: k, fail: auth, fail_until: 1}", func(t *testing.T, set *module.Set, db *storage.DB) {
		waitFirstAttempt(t, set)
		if got := status(t, set).Phase; got != module.PhaseAuth {
			t.Fatalf("got phase %s, want %s", got, module.PhaseAuth)
		}
		if err := set.Update(t.Context(), "test"); err != nil {
			t.Fatalf("manual update: %v", err)
		}
		synctest.Wait()
		if got := status(t, set); got.Phase != module.PhaseWaiting || got.NextRun.IsZero() {
			t.Fatalf("got %+v, want the schedule resumed", got)
		}
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		// The auth failure, the manual update, then the resumed schedule.
		if diff := cmp.Diff(map[string]int{"updates": 3}, snapshot(t, db)); diff != "" {
			t.Errorf("snapshot (-want +got):\n%s", diff)
		}
	})
}

func TestModuleState(t *testing.T) {
	t.Parallel()
	_, db := loadRegs(t.Context(), t, "", nil)
	q := module.New(db.Writer)
	ctx := t.Context()
	first := time.Date(2026, 10, 16, 7, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	second := first.Add(15 * time.Minute)
	data := `{"temp":12.5}`
	boom := "boom"

	if err := q.RecordSuccess(ctx, module.RecordSuccessParams{Module: "weather", At: &first, Data: &data}); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if err := q.RecordFailure(ctx, module.RecordFailureParams{Module: "weather", At: &second, Error: &boom}); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	got, err := module.New(db.Reader).GetState(ctx, "weather")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	// A failure keeps the last good data and fetch time; times come back in UTC.
	want := module.ModuleState{
		Module:      "weather",
		AttemptedAt: ptr(second.UTC()),
		FetchedAt:   ptr(first.UTC()),
		Error:       &boom,
		Data:        &data,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("state (-want +got):\n%s", diff)
	}

	var raw string
	if err := db.Reader.QueryRowContext(ctx, "SELECT fetched_at || '' FROM module_state").Scan(&raw); err != nil {
		t.Fatalf("raw fetched_at: %v", err)
	}
	if raw != "2026-10-16 05:00:00+00:00" {
		t.Errorf("got stored time %q, want SQLite date text in UTC", raw)
	}

	if _, err := q.GetState(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("got %v for an unknown module, want sql.ErrNoRows", err)
	}
}

func ptr[T any](v T) *T { return &v }
