// gentypes is package main; its generator is tested from inside.
package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/modules"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.ts")

type cfg struct{}

type mod struct{}

func (*mod) Initialize(context.Context, *cfg) error { return nil }
func (*mod) Enabled(context.Context) bool           { return false }
func (*mod) Update(context.Context) error           { return nil }

type Base struct {
	ID string `json:"id"`
}

type Item struct {
	Base
	At       time.Time        `json:"at"`
	Window   modules.Duration `json:"window"`
	Note     *string          `json:"note"`
	Tags     []string         `json:"tags,omitempty"`
	Counts   map[string]int   `json:"counts"`
	Optional []*float64       `json:"optional"`
	Nested   struct{ X int }  `json:"nested"`
	Hidden   string           `json:"-"`
	hidden   string
	Any      any    `json:"any"`
	Raw      []byte `json:"raw"`
	Untagged bool
}

type listParams struct {
	Limit  int              `json:"limit"`
	Window modules.Duration `json:"window"`
}

func (*mod) list(context.Context, listParams) ([]Item, error) { return nil, nil }
func (*mod) one(context.Context, struct{}) (*Item, error)     { return nil, nil }

func TestGenerate(t *testing.T) {
	t.Parallel()
	reg := modules.Define[mod, cfg]("demo_mod", func() *cfg { return &cfg{} },
		modules.WithQuery("list", (*mod).list),
		modules.WithQuery("one", (*mod).one),
	)
	got, err := generate([]modules.Registration{reg})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if *update {
		if err := os.WriteFile("testdata/golden.ts", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/golden.ts")
	if err != nil {
		t.Fatalf("reading golden (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("generated (-want +got):\n%s", diff)
	}
}

type badMap struct {
	M map[int]string `json:"m"`
}

func (*mod) bad(context.Context, struct{}) (badMap, error)  { return badMap{}, nil }
func (*mod) badParams(context.Context, int) (string, error) { return "", nil }

func TestGenerateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		opt  modules.Option
		want string
	}{
		{"non-string map key", modules.WithQuery("bad", (*mod).bad), "map keys"},
		{"params not a struct", modules.WithQuery("bad_params", (*mod).badParams), "params must be a struct"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reg := modules.Define[mod, cfg]("demo", func() *cfg { return &cfg{} }, testCase.opt)
			_, err := generate([]modules.Registration{reg})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}
