// gentypes is package main; its generator is tested from inside.
package main

import (
	"context"
	"flag"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/series"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type cfg struct{}

type mod struct{}

func (*mod) Initialize(context.Context, *cfg, module.Env) error  { return nil }
func (*mod) Enabled(context.Context, module.Query) (bool, error) { return false, nil }
func (*mod) Update(context.Context, *module.Update) error        { return nil }

type Item struct {
	ID       string          `json:"id"`
	At       time.Time       `json:"at"`
	Until    *time.Time      `json:"until"`
	Note     *string         `json:"note"`
	Tags     []string        `json:"tags,omitempty"`
	Optional []*float64      `json:"optional"`
	Nested   struct{ X int } `json:"nested"`
	Hidden   string          `json:"-"`
	hidden   string
	Untagged bool
}

type listParams struct {
	Limit int  `json:"limit"`
	Since *int `json:"since"`
}

type Page[T any] struct {
	Items []T `json:"items"`
}

type Extra struct {
	Ptr    *int      `json:"ptr,omitempty"`
	Paged  Page[int] `json:"paged"`
	Hyphen int       `json:"with-hyphen"`
	Dash   int       `json:"-,"`
	// From another package: named for it.
	Point series.Point `json:"point"`
}

// shared is both a params and a result type.
type shared struct {
	V int `json:"v"`
}

func (*mod) list(context.Context, module.Query, listParams) ([]Item, error) { return nil, nil }
func (*mod) extra(context.Context, module.Query, struct{}) (Extra, error)   { return Extra{}, nil }
func (*mod) echo(context.Context, module.Query, shared) (shared, error)     { return shared{}, nil }
func (*mod) one(context.Context, module.Query, struct{}) (*Item, error)     { return nil, nil }

// Params from another package: named for it.
func (*mod) history(context.Context, module.Query, series.HistoryParams) ([]series.Point, error) {
	return nil, nil
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	reg := module.Define[mod, cfg]("demo_mod", func() *cfg { return &cfg{} },
		module.WithQuery("list", (*mod).list),
		module.WithQuery("one", (*mod).one),
		module.WithQuery("extra", (*mod).extra),
		module.WithQuery("echo", (*mod).echo),
		module.WithQuery("history", (*mod).history),
	)
	got, err := generate([]module.Registration{reg})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	checkGolden(t, "testdata/golden.ts", got)
}

func checkGolden(t *testing.T, file string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading golden (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("generated (-want +got):\n%s", diff)
	}
}

type level string

type envelope struct {
	Value any      `json:"value"`
	Inner *inner   `json:"inner,omitempty"`
	Level level    `json:"level"`
	All   []level  `json:"all"`
	Anon  struct{} `json:"anon"`
}

type inner struct {
	N int `json:"n"`
}

func TestGenerateProps(t *testing.T) {
	t.Parallel()
	got, err := generateProps([]tsType{
		{t: reflect.TypeFor[envelope](), name: "Envelope<T>", fields: map[string]string{"value": "T"}},
		{t: reflect.TypeFor[inner](), name: "Inner"},
	}, map[reflect.Type][]string{reflect.TypeFor[level](): {"low", "high"}})
	if err != nil {
		t.Fatalf("generateProps: %v", err)
	}
	checkGolden(t, "testdata/props.ts", got)
}

func TestGeneratePropsErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		types []tsType
		want  string
	}{
		{"unnamed struct", []tsType{{t: reflect.TypeFor[envelope](), name: "Envelope", fields: map[string]string{"value": "unknown"}}}, "inner is not named"},
		{"any without a type", []tsType{{t: reflect.TypeFor[envelope](), name: "Envelope"}, {t: reflect.TypeFor[inner](), name: "Inner"}}, "unsupported type interface {}"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := generateProps(testCase.types, map[reflect.Type][]string{reflect.TypeFor[level](): {"low"}})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
		})
	}
}

// The committed props are what gentypes writes now.
func TestPropsUpToDate(t *testing.T) {
	t.Parallel()
	got, err := generateProps(propsTypes, propsEnums)
	if err != nil {
		t.Fatalf("generateProps: %v", err)
	}
	want, err := os.ReadFile("../../pages/generated/props.ts")
	if err != nil {
		t.Fatalf("reading props: %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("pages/generated/props.ts is stale; run go run ./cmd/gentypes (-want +got):\n%s", diff)
	}
}

type base struct {
	ID string `json:"id"`
}

type embedded struct {
	base
}

type mapped struct {
	M map[string]int `json:"m"`
}

type asString struct {
	N int `json:"n,string"`
}

type raw struct {
	B []byte `json:"b"`
}

type marshaler struct {
	At net.IP `json:"at"`
}

func (*mod) embedded(context.Context, module.Query, struct{}) (embedded, error) {
	return embedded{}, nil
}
func (*mod) mapped(context.Context, module.Query, struct{}) (mapped, error) { return mapped{}, nil }
func (*mod) asString(context.Context, module.Query, struct{}) (asString, error) {
	return asString{}, nil
}
func (*mod) raw(context.Context, module.Query, struct{}) (raw, error) { return raw{}, nil }
func (*mod) marshaler(context.Context, module.Query, struct{}) (marshaler, error) {
	return marshaler{}, nil
}
func (*mod) badParams(context.Context, module.Query, int) (string, error) { return "", nil }

func TestGenerateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		opt  module.Option
		want string
	}{
		{"embedded field", module.WithQuery("embedded", (*mod).embedded), "embedded fields are not supported"},
		{"map", module.WithQuery("mapped", (*mod).mapped), "unsupported type map[string]int"},
		{"string option", module.WithQuery("as_string", (*mod).asString), `json option "string" is not supported`},
		{"bytes", module.WithQuery("raw", (*mod).raw), "unsupported type []uint8"},
		{"own JSON encoding", module.WithQuery("marshaler", (*mod).marshaler), "own JSON encoding"},
		{"params not a struct", module.WithQuery("bad_params", (*mod).badParams), "params must be a struct"},
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
