package module_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/storage"
)

type greetingConfig struct {
	Text string `yaml:"text"`
	Fail bool   `yaml:"fail"`
}

type greeting struct{ Text string }

var greetingShared = module.DefineShared("greeting", func() *greetingConfig { return &greetingConfig{Text: "hello"} },
	func(_ context.Context, cfg *greetingConfig) (*greeting, error) {
		if cfg.Fail {
			return nil, errors.New("no words")
		}
		return &greeting{Text: cfg.Text}, nil
	})

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
		greeterRegistration("a", module.WithShared(greetingShared)),
		greeterRegistration("b", module.WithShared(greetingShared)),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := set.Resolve(t.Context(), "a", "greeting", nil)
	b := set.Resolve(t.Context(), "b", "greeting", nil)
	if a.Error != "" || b.Error != "" {
		t.Fatalf("got errors %q, %q, want none", a.Error, b.Error)
	}
	if diff := cmp.Diff(&greeting{Text: "hi"}, a.Value); diff != "" {
		t.Errorf("greeting (-want +got):\n%s", diff)
	}
	if a.Value != b.Value {
		t.Errorf("got two greetings, want one built once and shared")
	}
}

func TestSharedFailures(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		yaml string
		regs []module.Registration
		// Either Load fails with wantLoad, or module "a" fails init with wantInit.
		wantLoad string
		wantInit string
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
					func(context.Context, *greetingConfig) (int, error) { return 0, nil }))),
			},
			wantLoad: "shared greeting: defined twice",
		},
		{
			name:     "build fails",
			yaml:     "greeting: {fail: true}",
			regs:     []module.Registration{greeterRegistration("a", module.WithShared(greetingShared))},
			wantInit: "greeting: no words",
		},
		{
			name:     "not declared",
			regs:     []module.Registration{greeterRegistration("a")},
			wantInit: "panicked: module: context has no shared greeting; declare it with WithShared",
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
		func(context.Context, *greetingConfig) (int, error) { return 0, nil })
}
