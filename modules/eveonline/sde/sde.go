// Package sde is the shared EVE Online Static Data Export: names and
// definitions for the IDs ESI returns, imported into typed tables and kept at
// the latest build.
package sde

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/xaroth/dashboard/module"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// Dataset is a part of the SDE a module needs, declared with
// module.WithShared(sde.Shared, sde.Types, ...). Only declared datasets are
// imported.
type Dataset string

const (
	// Types is for Store.Types: categories, groups and types.
	Types Dataset = "types"
	// SolarSystems is for Store.SolarSystems: regions, constellations and
	// solar systems.
	SolarSystems Dataset = "solar_systems"
)

type config struct{}

// Shared is the static data, configured under modules.eveonline_sde. It
// checks for a new build every hour; latest.jsonl is cached for 5 minutes and
// builds come at unannounced times, often days apart. An update downloads
// about 100 MB when there is one. Modules declare it with the datasets they
// need and read it with Shared.Get(ctx).
var Shared = module.DefineShared("eveonline_sde", func() *config { return &config{} }, newStore,
	module.SharedMigrations(migrations),
	module.SharedSchedule(time.Hour, 6*time.Hour),
	module.SharedUpdateTimeout(10*time.Minute))

// Store imports the SDE and answers lookups from the imported build. Lookups
// use module.Database(ctx), so they work in Update and in queries.
type Store struct {
	// http is the service's client, for latest.jsonl.
	http *http.Client
	// download has no timeout of its own: an archive download is bounded by
	// the update's.
	download *http.Client
	// datasets is every declared dataset, sorted.
	datasets []Dataset
}

func newStore(ctx context.Context, _ *config, decls []Dataset) (*Store, error) {
	if len(decls) == 0 {
		return nil, errors.New("no datasets declared")
	}
	for _, d := range decls {
		if _, ok := importers[d]; !ok {
			return nil, fmt.Errorf("unknown dataset %q", d)
		}
	}
	base := module.HTTPClient(ctx)
	return &Store{
		http:     base,
		download: &http.Client{Transport: cmp.Or(base.Transport, http.DefaultTransport)},
		datasets: slices.Compact(slices.Sorted(slices.Values(decls))),
	}, nil
}

// imported returns the imported build. ok is false before the first import.
func (s *Store) imported(ctx context.Context) (b GetBuildRow, ok bool, err error) {
	b, err = New(module.Database(ctx)).GetBuild(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	if err != nil {
		return b, false, fmt.Errorf("reading SDE build: %w", err)
	}
	return b, true, nil
}

// Version reports the imported build, for the admin UI.
func (s *Store) Version(ctx context.Context) (string, error) {
	b, ok, err := s.imported(ctx)
	if err != nil || !ok {
		return "", err
	}
	return fmt.Sprintf("build %d, released %s", b.BuildNumber, b.ReleaseDate.Format(time.DateOnly)), nil
}

// Type is an inventory type with its group and category. Names are English.
type Type = ListTypesRow

// Types returns the types with the given IDs. An ID the imported build does
// not have is left out, as are all before the first import. It needs the
// Types dataset.
func (s *Store) Types(ctx context.Context, ids []int64) (map[int64]Type, error) {
	if err := s.check(Types); err != nil {
		return nil, err
	}
	rows, err := New(module.Database(ctx)).ListTypes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading SDE types: %w", err)
	}
	return byID(rows, func(r Type) int64 { return r.TypeID }), nil
}

// SolarSystem is a solar system with its constellation and region. Names are
// English. SecurityStatus is full precision; see docs/sde.md for display.
type SolarSystem = ListSolarSystemsRow

// SolarSystems returns the solar systems with the given IDs, as Types does.
// It needs the SolarSystems dataset.
func (s *Store) SolarSystems(ctx context.Context, ids []int64) (map[int64]SolarSystem, error) {
	if err := s.check(SolarSystems); err != nil {
		return nil, err
	}
	rows, err := New(module.Database(ctx)).ListSolarSystems(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading SDE solar systems: %w", err)
	}
	return byID(rows, func(r SolarSystem) int64 { return r.SolarSystemID }), nil
}

func (s *Store) check(d Dataset) error {
	if !slices.Contains(s.datasets, d) {
		return fmt.Errorf("SDE dataset %s not declared; declare it with module.WithShared", d)
	}
	return nil
}

func byID[T any](rows []T, id func(T) int64) map[int64]T {
	out := make(map[int64]T, len(rows))
	for _, r := range rows {
		out[id(r)] = r
	}
	return out
}
