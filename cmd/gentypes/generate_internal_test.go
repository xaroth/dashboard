// gentypes is package main; its generator is tested from inside.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
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
	At       time.Time       `json:"at"`
	Window   module.Duration `json:"window"`
	Note     *string         `json:"note"`
	Tags     []string        `json:"tags,omitempty"`
	Counts   map[string]int  `json:"counts"`
	Optional []*float64      `json:"optional"`
	Nested   struct{ X int } `json:"nested"`
	Hidden   string          `json:"-"`
	hidden   string
	Any      any    `json:"any"`
	Raw      []byte `json:"raw"`
	Untagged bool
}

type listParams struct {
	Limit  int             `json:"limit"`
	Window module.Duration `json:"window"`
}

type Page[T any] struct {
	Items []T `json:"items"`
}

type Extra struct {
	Ptr    *int            `json:"ptr,omitempty"`
	AsText int64           `json:"asText,string"`
	Fixed  [2]byte         `json:"fixed"`
	JSON   json.RawMessage `json:"json"`
	Paged  Page[int]       `json:"paged"`
	Hyphen int             `json:"with-hyphen"`
	Tricky Tricky          `json:"tricky"`
}

// shared is both a params and a result type.
type shared struct {
	V int `json:"v"`
}

func (*mod) list(context.Context, listParams) ([]Item, error) { return nil, nil }
func (*mod) extra(context.Context, struct{}) (Extra, error)   { return Extra{}, nil }
func (*mod) echo(context.Context, shared) (shared, error)     { return shared{}, nil }
func (*mod) one(context.Context, struct{}) (*Item, error)     { return nil, nil }

func TestGenerate(t *testing.T) {
	t.Parallel()
	reg := module.Define[mod, cfg]("demo_mod", func() *cfg { return &cfg{} },
		module.WithQuery("list", (*mod).list),
		module.WithQuery("one", (*mod).one),
		module.WithQuery("extra", (*mod).extra),
		module.WithQuery("echo", (*mod).echo),
	)
	got, err := generate([]module.Registration{reg})
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
func (*mod) marshaler(context.Context, struct{}) (struct {
	At net.IP `json:"at"`
}, error) {
	return struct {
		At net.IP `json:"at"`
	}{}, nil
}

func TestGenerateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		opt  module.Option
		want string
	}{
		{"non-string map key", module.WithQuery("bad", (*mod).bad), "map keys"},
		{"params not a struct", module.WithQuery("bad_params", (*mod).badParams), "params must be a struct"},
		{"own JSON encoding", module.WithQuery("marshaler", (*mod).marshaler), "own JSON encoding"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reg := module.Define[mod, cfg]("demo", func() *cfg { return &cfg{} }, testCase.opt)
			_, err := generate([]module.Registration{reg})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}
