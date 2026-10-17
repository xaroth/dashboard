package module_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/storage"
)

type greetingConfig struct {
	Text string `yaml:"text"`
	Fail bool   `yaml:"fail"`
}

// greeting greets the names the modules declare. As an http.Handler it
// serves its text and the number of migration rows it can see.
type greeting struct{ Text string }

// Version reports the number of migration rows the reader can see.
func (g *greeting) Version(ctx context.Context) (string, error) {
	var n int
	err := module.Database(ctx).QueryRowContext(ctx, "SELECT COUNT(*) FROM greeting_log").Scan(&n)
	return fmt.Sprintf("v%d", n), err
}

func (g *greeting) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var n int
	err := module.Database(r.Context()).QueryRowContext(r.Context(), "SELECT COUNT(*) FROM greeting_log").Scan(&n)
	fmt.Fprintf(w, "%s %s %d %v", r.URL.Path, g.Text, n, err)
}

var greetingMigrations = fstest.MapFS{"sql/migrations/00001_init.sql": {Data: []byte(
	"-- +goose Up\nCREATE TABLE greeting_log (id INTEGER PRIMARY KEY);\n")}}

var greetingShared = module.DefineShared("greeting", func() *greetingConfig { return &greetingConfig{Text: "hello"} },
	func(_ context.Context, cfg *greetingConfig, names []string) (*greeting, error) {
		if cfg.Fail {
			return nil, errors.New("no words")
		}
		return &greeting{Text: strings.Join(append([]string{cfg.Text}, names...), " ")}, nil
	},
	module.SharedMigrations(greetingMigrations))

// greeter reads the shared greeting in Initialize and in its query.
type greeter struct{}

type greeterConfig struct{}

func (*greeter) Initialize(ctx context.Context, _ *greeterConfig) error {
	greetingShared.Get(ctx)
	return nil
}
func (*greeter) Enabled(context.Context) bool { return false }
func (*greeter) Update(context.Context) error { return nil }

func (*greeter) greeting(ctx context.Context, _ struct{}) (*greeting, error) {
	return greetingShared.Get(ctx), nil
}

func greeterRegistration(name string, opts ...module.Option) module.Registration {
	opts = append(opts, module.WithQuery("greeting", (*greeter).greeting))
	return module.Define[greeter, greeterConfig](name, func() *greeterConfig { return &greeterConfig{} }, opts...)
}

func tryLoad(tb testing.TB, src string, regs ...module.Registration) (*module.Set, error) {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return module.Load(tb.Context(), module.Options{
		Registrations: regs,
		Configs:       parse(tb, src),
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		Now:           time.Now,
	})
}

func TestShared(t *testing.T) {
	t.Parallel()
	set, err := tryLoad(t, "greeting: {text: hi}",
		greeterRegistration("b", module.WithShared(greetingShared, "bob")),
		greeterRegistration("a", module.WithShared(greetingShared, "alice", "ann")),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := set.Resolve(t.Context(), "a", "greeting", nil)
	b := set.Resolve(t.Context(), "b", "greeting", nil)
	if a.Error != "" || b.Error != "" {
		t.Fatalf("got errors %q, %q, want none", a.Error, b.Error)
	}
	if diff := cmp.Diff(&greeting{Text: "hi alice ann bob"}, a.Value); diff != "" {
		t.Errorf("greeting (-want +got):\n%s", diff)
	}
	if a.Value != b.Value {
		t.Errorf("got two greetings, want one built once and shared")
	}

	if diff := cmp.Diff([]module.SharedStatus{{Name: "greeting", Page: true, Version: "v0"}}, set.Shared(t.Context())); diff != "" {
		t.Errorf("shared (-want +got):\n%s", diff)
	}
	mux := http.NewServeMux()
	set.RegisterShared(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/greeting/x", nil))
	if got, want := rec.Body.String(), "/x hi alice ann bob 0 <nil>"; got != want {
		t.Errorf("got page %q, want %q", got, want)
	}
}

func TestSharedFailures(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		yaml string
		regs []module.Registration
		// Either Load fails with wantLoad, or module "a" fails init with
		// wantInit and the shared values are wantShared.
		wantLoad   string
		wantInit   string
		wantShared []module.SharedStatus
	}{
		{
			name:     "unknown key",
			yaml:     "greeting: {bogus: 1}",
			regs:     []module.Registration{greeterRegistration("a", module.WithShared(greetingShared))},
			wantLoad: "modules.greeting: yaml: unmarshal errors:\n  line 1: field bogus not found in type module_test.greetingConfig",
		},
		{
			name:     "schedule key",
			yaml:     "greeting: {schedule: {update_interval: 1m}}",
			regs:     []module.Registration{greeterRegistration("a", module.WithShared(greetingShared))},
			wantLoad: "modules.greeting: yaml: unmarshal errors:\n  line 1: field schedule not found in type module_test.greetingConfig",
		},
		{
			name:     "section without a declaring module",
			yaml:     "greeting: {text: hi}",
			regs:     []module.Registration{greeterRegistration("a")},
			wantLoad: "modules.greeting: no such module (have [a])",
		},
		{
			name: "name is also a module",
			regs: []module.Registration{
				greeterRegistration("a", module.WithShared(greetingShared)),
				greeterRegistration("greeting"),
			},
			wantLoad: "shared greeting: name is also a module",
		},
		{
			name: "defined twice",
			regs: []module.Registration{
				greeterRegistration("a", module.WithShared(greetingShared)),
				greeterRegistration("b", module.WithShared(module.DefineShared("greeting",
					func() *greetingConfig { return &greetingConfig{} },
					func(context.Context, *greetingConfig, []string) (int, error) { return 0, nil }))),
			},
			wantLoad: "shared greeting: defined twice",
		},
		{
			name:       "build fails",
			yaml:       "greeting: {fail: true}",
			regs:       []module.Registration{greeterRegistration("a", module.WithShared(greetingShared))},
			wantInit:   "greeting: no words",
			wantShared: []module.SharedStatus{{Name: "greeting", Error: "greeting: no words"}},
		},
		{
			name:       "not declared",
			regs:       []module.Registration{greeterRegistration("a")},
			wantInit:   "panicked: module: context has no shared greeting; declare it with WithShared",
			wantShared: []module.SharedStatus{},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set, err := tryLoad(t, testCase.yaml, testCase.regs...)
			if testCase.wantLoad != "" {
				if err == nil || err.Error() != testCase.wantLoad {
					t.Errorf("got Load error %v, want %q", err, testCase.wantLoad)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := status(t, set)
			if got.Phase != module.PhaseInitFail || got.Error != testCase.wantInit {
				t.Errorf("got %s %q, want %s %q", got.Phase, got.Error, module.PhaseInitFail, testCase.wantInit)
			}
			if diff := cmp.Diff(testCase.wantShared, set.Shared(t.Context())); diff != "" {
				t.Errorf("shared (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDefineSharedRejectsInvalidName(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "invalid shared name") {
			t.Errorf("got panic %v, want invalid shared name", r)
		}
	}()
	module.DefineShared("Bad-Name", func() *greetingConfig { return nil },
		func(context.Context, *greetingConfig, []string) (int, error) { return 0, nil })
}

// counter counts its updates in counter_log. An update blocks until its
// context is done while block is set.
type counter struct{ block bool }

func (c *counter) Update(ctx context.Context) error {
	if c.block {
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := module.Database(ctx).ExecContext(ctx, "INSERT INTO counter_log DEFAULT VALUES")
	return err
}

type counterConfig struct {
	Block bool `yaml:"block"`
}

var counterShared = module.DefineShared("counter", func() *counterConfig { return &counterConfig{} },
	func(_ context.Context, cfg *counterConfig, _ []struct{}) (*counter, error) {
		return &counter{block: cfg.Block}, nil
	},
	module.SharedMigrations(fstest.MapFS{"sql/migrations/00001_init.sql": {Data: []byte(
		"-- +goose Up\nCREATE TABLE counter_log (id INTEGER PRIMARY KEY);\n")}}),
	module.SharedSchedule(time.Hour, 3*time.Hour),
	module.SharedUpdateTimeout(5*time.Minute))

func TestSharedSchedule(t *testing.T) {
	regs := []module.Registration{greeterRegistration("z", module.WithShared(counterShared))}
	runRegs(t, "counter: {schedule: {update_interval: 2h}}", regs, func(t *testing.T, set *module.Set, db *storage.DB) {
		time.Sleep(5 * time.Hour)
		synctest.Wait()
		var n int
		if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM counter_log").Scan(&n); err != nil || n != 3 {
			t.Errorf("got %d updates (%v), want 3: one within the start jitter, then every 2h", n, err)
		}
		st, err := set.Statuses(t.Context())
		if err != nil {
			t.Fatalf("Statuses: %v", err)
		}
		var names []string
		for _, s := range st {
			names = append(names, s.Name)
		}
		if want := []string{"counter", "z"}; !slices.Equal(names, want) {
			t.Errorf("got statuses %v, want %v", names, want)
		}
		if got := st[0]; got.Interval != 2*time.Hour || got.Stale || got.Phase != module.PhaseWaiting {
			t.Errorf("got %+v, want waiting every 2h, fresh", got)
		}
	})
}

func TestSharedUpdateTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		set, _ := loadRegs(t.Context(), t, "counter: {block: true}",
			[]module.Registration{greeterRegistration("z", module.WithShared(counterShared))})
		start := time.Now()
		err := set.Update(t.Context(), "counter")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got Update error %v, want %v", err, context.DeadlineExceeded)
		}
		if got := time.Since(start); got != 5*time.Minute {
			t.Errorf("got Update after %v, want 5m", got)
		}
	})
}

func TestSharedScheduleBuildFails(t *testing.T) {
	t.Parallel()
	failing := module.DefineShared("failing", func() *struct{} { return &struct{}{} },
		func(context.Context, *struct{}, []struct{}) (*counter, error) { return nil, errors.New("no source") },
		module.SharedSchedule(time.Hour, time.Hour))
	set, err := tryLoad(t, "", greeterRegistration("a", module.WithShared(failing)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	st, err := set.Statuses(t.Context())
	if err != nil || len(st) != 2 {
		t.Fatalf("Statuses: %v, %v", st, err)
	}
	for _, s := range st {
		if s.Phase != module.PhaseInitFail || s.Error != "failing: no source" {
			t.Errorf("got %s %s %q, want %s %q", s.Name, s.Phase, s.Error, module.PhaseInitFail, "failing: no source")
		}
	}
	if err := set.Update(t.Context(), "failing"); err == nil || !strings.Contains(err.Error(), "no source") {
		t.Errorf("got Update error %v, want the build error", err)
	}
}

func TestDefineSharedRejectsSchedule(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		define  func()
		wantMsg string
	}{
		{
			name: "not an Updater",
			define: func() {
				module.DefineShared("x", func() *struct{} { return nil },
					func(context.Context, *struct{}, []struct{}) (int, error) { return 0, nil },
					module.SharedSchedule(time.Hour, time.Hour))
			},
			wantMsg: "module: shared x: scheduled but int does not implement Updater",
		},
		{
			name: "zero interval",
			define: func() {
				module.DefineShared("x", func() *struct{} { return nil },
					func(context.Context, *struct{}, []struct{}) (*counter, error) { return nil, nil },
					module.SharedSchedule(0, time.Hour))
			},
			wantMsg: "module: shared x: schedule durations must be positive",
		},
		{
			name: "negative timeout",
			define: func() {
				module.DefineShared("x", func() *struct{} { return nil },
					func(context.Context, *struct{}, []struct{}) (*counter, error) { return nil, nil },
					module.SharedSchedule(time.Hour, time.Hour), module.SharedUpdateTimeout(-time.Minute))
			},
			wantMsg: "module: shared x: schedule durations must be positive",
		},
		{
			name: "timeout without a schedule",
			define: func() {
				module.DefineShared("x", func() *struct{} { return nil },
					func(context.Context, *struct{}, []struct{}) (*counter, error) { return nil, nil },
					module.SharedUpdateTimeout(time.Minute))
			},
			wantMsg: "module: shared x: update timeout without a schedule",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != testCase.wantMsg {
					t.Errorf("got panic %v, want %q", r, testCase.wantMsg)
				}
			}()
			testCase.define()
		})
	}
}
