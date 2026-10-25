package sde_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/eveonline/sde"
	"github.com/xaroth/dashboard/storage"
)

// newFake serves build with Ship > Frigate > Rifter and Reaper, and Jita in
// Kimotoro, The Forge, then opts.
func newFake(tb testing.TB, build int64, opts ...sde.FakeOption) *sde.Fake {
	tb.Helper()
	return sde.NewFake(tb, append([]sde.FakeOption{
		sde.ExpectBuild(build),
		sde.ExpectFile("categories.jsonl", `{"_key": 6, "name": {"de": "Schiff", "en": "Ship"}, "published": true}`),
		sde.ExpectFile("groups.jsonl", `{"_key": 25, "anchorable": false, "categoryID": 6, "name": {"en": "Frigate"}, "published": true}`),
		sde.ExpectFile("types.jsonl", `{"_key": 587, "groupID": 25, "mass": 1067000.0, "name": {"en": "Rifter"}, "portionSize": 1, "published": true, "description": {"en": "x"}}
{"_key": 588, "groupID": 25, "name": {"en": "Reaper"}, "portionSize": 1, "published": false}`),
		sde.ExpectFile("mapRegions.jsonl", `{"_key": 10000002, "constellationIDs": [20000020], "name": {"en": "The Forge"}, "nebulaID": 1}`),
		sde.ExpectFile("mapConstellations.jsonl", `{"_key": 20000020, "regionID": 10000002, "name": {"en": "Kimotoro"}, "solarSystemIDs": [30000142]}`),
		sde.ExpectFile("mapSolarSystems.jsonl", `{"_key": 30000142, "constellationID": 20000020, "regionID": 10000002, "name": {"en": "Jita"}, "securityStatus": 0.945913, "securityClass": "B", "position": {"x": 1, "y": 2, "z": 3}}`),
	}, opts...)...)
}

// reader looks up the SDE in its query.
type reader struct{}

func (*reader) Initialize(context.Context, *struct{}, module.Env) error { return nil }
func (*reader) Enabled(context.Context, module.Query) (bool, error)     { return false, nil }
func (*reader) Update(context.Context, *module.Update) error            { return nil }

type lookup struct {
	Types   map[int64]sde.Type
	Systems map[int64]sde.SolarSystem
}

func (*reader) lookup(ctx context.Context, q module.Query, _ struct{}) (lookup, error) {
	s := sde.Shared.Get(q)
	types, err := s.Types(ctx, q, []int64{587, 588, 999})
	if err != nil {
		return lookup{}, err
	}
	systems, err := s.SolarSystems(ctx, q, []int64{30000142})
	return lookup{types, systems}, err
}

// load loads the reader module, declaring datasets, on db; loading again on
// the same db is a restart.
func load(tb testing.TB, db *storage.DB, handler http.Handler, datasets ...sde.Dataset) *module.Set {
	tb.Helper()
	reg := module.Define[reader, struct{}]("reader", func() *struct{} { return &struct{}{} },
		module.WithShared(sde.Shared, datasets...), module.WithQuery("lookup", (*reader).lookup))
	return moduletest.Load(tb, db, "", module.Options{
		Registrations: []module.Registration{reg},
		HTTPClient:    moduletest.HTTPClient(handler),
		Now:           func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) },
	})
}

func update(tb testing.TB, set *module.Set) {
	tb.Helper()
	if err := set.Update(tb.Context(), "eveonline_sde"); err != nil {
		tb.Fatalf("Update: %v", err)
	}
}

func resolve(tb testing.TB, set *module.Set) lookup {
	tb.Helper()
	res := set.Resolve(tb.Context(), "reader", "lookup", nil)
	if res.Error != "" {
		tb.Fatalf("lookup: %s", res.Error)
	}
	return res.Value.(lookup)
}

func version(tb testing.TB, set *module.Set) string {
	tb.Helper()
	for _, s := range set.Shared(tb.Context()) {
		if s.Name == "eveonline_sde" {
			if s.Error != "" {
				tb.Errorf("got version error %q, want none", s.Error)
			}
			return s.Version
		}
	}
	tb.Fatal("eveonline_sde is not a shared value")
	return ""
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	fake := newFake(t, 100)
	set := load(t, moduletest.DB(t, t.TempDir()), fake, sde.Types, sde.SolarSystems)

	if got := resolve(t, set); len(got.Types) != 0 || len(got.Systems) != 0 {
		t.Errorf("got %+v before the first import, want nothing", got)
	}
	if got := version(t, set); got != "" {
		t.Errorf("got version %q before the first import, want none", got)
	}

	update(t, set)
	want := lookup{
		Types: map[int64]sde.Type{
			587: {TypeID: 587, Name: "Rifter", Published: true, GroupID: 25, GroupName: "Frigate", CategoryID: 6, CategoryName: "Ship"},
			588: {TypeID: 588, Name: "Reaper", GroupID: 25, GroupName: "Frigate", CategoryID: 6, CategoryName: "Ship"},
		},
		Systems: map[int64]sde.SolarSystem{
			30000142: {
				SolarSystemID: 30000142, Name: "Jita", SecurityStatus: 0.945913,
				ConstellationID: 20000020, ConstellationName: "Kimotoro", RegionID: 10000002, RegionName: "The Forge",
			},
		},
	}
	if diff := cmp.Diff(want, resolve(t, set)); diff != "" {
		t.Errorf("lookup (-want +got):\n%s", diff)
	}
	if got, want := version(t, set), "build 100, released 2026-10-12"; got != want {
		t.Errorf("got version %q, want %q", got, want)
	}

	update(t, set)
	if got := fake.Downloads(); got != 1 {
		t.Errorf("got %d downloads, want 1: the build did not change", got)
	}

	fake.Set(t, sde.ExpectBuild(101), sde.ExpectFile("types.jsonl", `{"_key": 587, "groupID": 25, "name": {"en": "Rifter II"}, "published": true}`))
	update(t, set)
	want.Types = map[int64]sde.Type{
		587: {TypeID: 587, Name: "Rifter II", Published: true, GroupID: 25, GroupName: "Frigate", CategoryID: 6, CategoryName: "Ship"},
	}
	if diff := cmp.Diff(want, resolve(t, set)); diff != "" {
		t.Errorf("lookup after build 101 (-want +got):\n%s", diff)
	}
	if got, want := version(t, set), "build 101, released 2026-10-12"; got != want {
		t.Errorf("got version %q, want %q", got, want)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		// handler serves the failing update when set; else the fake serves
		// build 101 with opts.
		handler http.HandlerFunc
		opts    []sde.FakeOption
		wantErr string
	}{
		{
			name:    "latest unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusBadGateway) },
			wantErr: "requesting latest build: 502 Bad Gateway",
		},
		{
			name:    "no sde record",
			handler: func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, `{"_key": "other", "buildNumber": 1}`) },
			wantErr: `latest build: no "sde" record`,
		},
		{
			name:    "archive of another build",
			opts:    []sde.FakeOption{sde.ExpectFile("_sde.jsonl", `{"_key": "sde", "buildNumber": 100, "releaseDate": "2026-10-12T11:08:38Z"}`)},
			wantErr: "importing build 101: archive has build 100",
		},
		{
			name:    "missing file",
			opts:    []sde.FakeOption{sde.ExpectFile_Missing("mapSolarSystems.jsonl")},
			wantErr: "importing build 101: open mapSolarSystems.jsonl: file does not exist",
		},
		{
			name:    "bad record",
			opts:    []sde.FakeOption{sde.ExpectFile("groups.jsonl", `{"_key": "x"}`)},
			wantErr: "importing build 101: groups.jsonl: json: cannot unmarshal string into Go struct field group._key of type int64",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db := moduletest.DB(t, t.TempDir())
			fake := newFake(t, 100)
			update(t, load(t, db, fake, sde.Types, sde.SolarSystems))

			var handler http.Handler = testCase.handler
			if testCase.handler == nil {
				fake.Set(t, append([]sde.FakeOption{sde.ExpectBuild(101)}, testCase.opts...)...)
				handler = fake
			}
			set := load(t, db, handler, sde.Types, sde.SolarSystems)
			if err := set.Update(t.Context(), "eveonline_sde"); err == nil || err.Error() != testCase.wantErr {
				t.Errorf("got Update error %v, want %q", err, testCase.wantErr)
			}
			if got := resolve(t, set); got.Types[587].Name != "Rifter" || len(got.Systems) != 1 {
				t.Errorf("got %+v, want build 100 kept", got)
			}
			if got, want := version(t, set), "build 100, released 2026-10-12"; got != want {
				t.Errorf("got version %q, want %q", got, want)
			}
		})
	}
}

func TestDatasets(t *testing.T) {
	t.Parallel()
	fake := newFake(t, 100)
	db := moduletest.DB(t, t.TempDir())

	set := load(t, db, fake, sde.Types)
	update(t, set)
	res := set.Resolve(t.Context(), "reader", "lookup", nil)
	if want := "SDE dataset solar_systems not declared; declare it with module.WithShared"; res.Error != want {
		t.Errorf("got lookup error %q, want %q", res.Error, want)
	}

	// Declaring another dataset imports the same build again, with it.
	set = load(t, db, fake, sde.SolarSystems, sde.Types, sde.Types)
	if got := resolve(t, set); len(got.Types) != 2 || len(got.Systems) != 0 {
		t.Errorf("got %+v before the update, want types only", got)
	}
	update(t, set)
	if got := resolve(t, set); len(got.Types) != 2 || len(got.Systems) != 1 {
		t.Errorf("got %+v, want types and solar systems", got)
	}
	update(t, set)
	if got := fake.Downloads(); got != 2 {
		t.Errorf("got %d downloads, want 2: one per set of datasets", got)
	}

	// A dataset no longer declared is emptied by the next import.
	fake.Set(t, sde.ExpectBuild(101))
	update(t, load(t, db, fake, sde.SolarSystems))
	if got := resolve(t, load(t, db, fake, sde.SolarSystems, sde.Types)); len(got.Types) != 0 || len(got.Systems) != 1 {
		t.Errorf("got %+v, want solar systems only", got)
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		datasets []sde.Dataset
		want     string
	}{
		{"unknown dataset", []sde.Dataset{sde.Types, "moons"}, `eveonline_sde: unknown dataset "moons"`},
		{"no datasets", nil, "eveonline_sde: no datasets declared"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, moduletest.DB(t, t.TempDir()), http.NotFoundHandler(), testCase.datasets...)
			st, err := set.Statuses(t.Context())
			if err != nil {
				t.Fatalf("Statuses: %v", err)
			}
			var got []string
			for _, s := range st {
				got = append(got, fmt.Sprintf("%s %s %s", s.Name, s.Phase, s.Error))
			}
			want := []string{
				fmt.Sprintf("eveonline_sde %s %s", module.PhaseInitFail, testCase.want),
				fmt.Sprintf("reader %s %s", module.PhaseInitFail, testCase.want),
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("statuses (-want +got):\n%s", diff)
			}
		})
	}
}
