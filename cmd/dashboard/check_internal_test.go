// package main cannot be imported, so its tests are internal.
package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/render"
)

type clockConfig struct{}

type clockModule struct{}

func (*clockModule) Initialize(context.Context, *clockConfig, module.Env) error { return nil }
func (*clockModule) Enabled(context.Context, module.Query) (bool, error)        { return false, nil }
func (*clockModule) Update(context.Context, *module.Update) error               { return nil }

type ticksParams struct {
	Limit int `json:"limit"`
}

func (*clockModule) ticks(_ context.Context, _ module.Query, p ticksParams) ([]int, error) {
	return make([]int, p.Limit), nil
}

func clockRegistration() module.Registration {
	return module.Define[clockModule, clockConfig]("clock", func() *clockConfig { return &clockConfig{} },
		module.WithQuery("ticks", (*clockModule).ticks))
}

func TestNewChecksQueries(t *testing.T) {
	t.Parallel()
	page := func(query string, params map[string]any) render.PageInfo {
		return render.PageInfo{Name: "e1001/hello", Data: map[string]render.QueryDescriptor{
			"ticks": {Module: "clock", Query: query, Params: params},
		}}
	}
	testCases := []struct {
		name      string
		page      render.PageInfo
		overrides map[string]map[string]any
		want      string // empty: New succeeds
	}{
		{"valid", page("ticks", map[string]any{"limit": 2}), nil, ""},
		{"valid override", page("ticks", nil), map[string]map[string]any{"hello.ticks": {"limit": 8}}, ""},
		{"unknown query", page("tocks", nil), nil, "page e1001/hello, data ticks"},
		{"bad param", page("ticks", map[string]any{"limit": "two"}), nil, "limit"},
		{"override for unknown key", page("ticks", nil), map[string]map[string]any{"hello.tocks": {"limit": 8}}, "not a page data key"},
		{"override without page", page("ticks", nil), map[string]map[string]any{"ticks": {"limit": 8}}, "not a page data key"},
		{"bad override value", page("ticks", nil), map[string]map[string]any{"hello.ticks": {"limit": "eight"}}, "devices.hallway.params.hello.ticks"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig(t, "", "")
			d := cfg.Devices["hallway"]
			d.Params = testCase.overrides
			cfg.Devices["hallway"] = d
			stub := render.NewFakeRenderer(t, render.ExpectPages(testCase.page))
			_, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler),
				WithRenderer(stub), WithRegistrations(clockRegistration()))
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}
