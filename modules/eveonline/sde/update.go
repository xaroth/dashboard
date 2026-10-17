package sde

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/xaroth/dashboard/module"
)

const baseURL = "https://developers.eveonline.com/static-data/tranquility"

// Update imports the latest build when it is not the imported one, or when
// the imported build lacks a declared dataset.
func (s *Store) Update(ctx context.Context) error {
	latest, err := s.latest(ctx)
	if err != nil {
		return err
	}
	cur, ok, err := s.imported(ctx)
	if err != nil {
		return err
	}
	have, err := New(module.Database(ctx)).ListDatasets(ctx)
	if err != nil {
		return fmt.Errorf("reading SDE datasets: %w", err)
	}
	if ok && cur.BuildNumber == latest.BuildNumber && slices.Equal(have, datasetNames(s.datasets)) {
		return nil
	}
	start := time.Now()
	if err := s.importBuild(ctx, latest.BuildNumber); err != nil {
		return fmt.Errorf("importing build %d: %w", latest.BuildNumber, err)
	}
	module.Logger(ctx).Info("imported SDE", "build", latest.BuildNumber, "previous", cur.BuildNumber, "datasets", s.datasets, "took", time.Since(start))
	return nil
}

// buildRecord is the record with key "sde" in latest.jsonl and _sde.jsonl.
type buildRecord struct {
	Key         string    `json:"_key"`
	BuildNumber int64     `json:"buildNumber"`
	ReleaseDate time.Time `json:"releaseDate"`
}

func (s *Store) latest(ctx context.Context) (buildRecord, error) {
	body, err := get(ctx, s.http, baseURL+"/latest.jsonl")
	if err != nil {
		return buildRecord{}, fmt.Errorf("requesting latest build: %w", err)
	}
	defer body.Close()
	b, err := findBuild(body)
	if err != nil {
		return buildRecord{}, fmt.Errorf("latest build: %w", err)
	}
	return b, nil
}

// get returns the body of a 200 response to a GET of url.
func get(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New(resp.Status)
	}
	return resp.Body, nil
}

// findBuild returns the "sde" record of a JSON Lines stream.
func findBuild(r io.Reader) (buildRecord, error) {
	dec := json.NewDecoder(r)
	for {
		var b buildRecord
		if err := dec.Decode(&b); err == io.EOF {
			return buildRecord{}, errors.New(`no "sde" record`)
		} else if err != nil {
			return buildRecord{}, err
		}
		if b.Key == "sde" {
			if b.BuildNumber <= 0 {
				return buildRecord{}, fmt.Errorf("invalid build number %d", b.BuildNumber)
			}
			return b, nil
		}
	}
}

// importBuild downloads the build's archive to a temporary file, since zip
// needs random access, and replaces the tables with it.
func (s *Store) importBuild(ctx context.Context, build int64) error {
	f, err := os.CreateTemp("", "eveonline-sde-*.zip")
	if err != nil {
		return fmt.Errorf("creating archive file: %w", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	body, err := get(ctx, s.download, fmt.Sprintf("%s/eve-online-static-data-%d-jsonl.zip", baseURL, build))
	if err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}
	size, err := io.Copy(f, body)
	body.Close()
	if err != nil {
		return fmt.Errorf("downloading archive: %w", err)
	}
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}
	return s.replace(ctx, zr, build)
}

// replace replaces the tables with the archive's build of the declared
// datasets in one transaction, so readers see either the previous build or
// this one. Tables of other datasets are emptied.
func (s *Store) replace(ctx context.Context, zr *zip.Reader, want int64) (err error) {
	f, err := zr.Open("_sde.jsonl")
	if err != nil {
		return err
	}
	build, err := findBuild(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("_sde.jsonl: %w", err)
	}
	if build.BuildNumber != want {
		return fmt.Errorf("archive has build %d", build.BuildNumber)
	}

	tx, err := module.Database(ctx).BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	q := New(tx)
	if err := q.DeleteDatasets(ctx); err != nil {
		return err
	}
	for _, imp := range importers {
		if err := imp.clear(ctx, q); err != nil {
			return err
		}
	}
	for _, d := range s.datasets {
		if err := importers[d].load(ctx, q, zr); err != nil {
			return err
		}
		if err := q.InsertDataset(ctx, string(d)); err != nil {
			return err
		}
	}
	if err := q.SetBuild(ctx, SetBuildParams{BuildNumber: build.BuildNumber, ReleaseDate: build.ReleaseDate}); err != nil {
		return err
	}
	return tx.Commit()
}

func datasetNames(ds []Dataset) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = string(d)
	}
	return out
}

// importer empties and fills the tables of a dataset.
type importer struct {
	clear func(context.Context, *Queries) error
	load  func(context.Context, *Queries, *zip.Reader) error
}

var importers = map[Dataset]importer{
	Types: {
		clear: func(ctx context.Context, q *Queries) error {
			return errors.Join(q.DeleteCategories(ctx), q.DeleteGroups(ctx), q.DeleteTypes(ctx))
		},
		load: loadTypes,
	},
	SolarSystems: {
		clear: func(ctx context.Context, q *Queries) error {
			return errors.Join(q.DeleteRegions(ctx), q.DeleteConstellations(ctx), q.DeleteSolarSystems(ctx))
		},
		load: loadSolarSystems,
	},
}

// localized is a name in every client language.
type localized map[string]string

func (l localized) en() string { return l["en"] }

// record has the fields every imported SDE record has.
type record struct {
	Key  int64     `json:"_key"`
	Name localized `json:"name"`
}

func loadTypes(ctx context.Context, q *Queries, zr *zip.Reader) error {
	if err := each(zr, "categories.jsonl", func(c record) error {
		return q.InsertCategory(ctx, InsertCategoryParams{CategoryID: c.Key, Name: c.Name.en()})
	}); err != nil {
		return err
	}
	type group struct {
		record
		CategoryID int64 `json:"categoryID"`
	}
	if err := each(zr, "groups.jsonl", func(g group) error {
		return q.InsertGroup(ctx, InsertGroupParams{GroupID: g.Key, CategoryID: g.CategoryID, Name: g.Name.en()})
	}); err != nil {
		return err
	}
	type invType struct {
		record
		GroupID   int64 `json:"groupID"`
		Published bool  `json:"published"`
	}
	return each(zr, "types.jsonl", func(t invType) error {
		return q.InsertType(ctx, InsertTypeParams{TypeID: t.Key, GroupID: t.GroupID, Name: t.Name.en(), Published: t.Published})
	})
}

func loadSolarSystems(ctx context.Context, q *Queries, zr *zip.Reader) error {
	if err := each(zr, "mapRegions.jsonl", func(r record) error {
		return q.InsertRegion(ctx, InsertRegionParams{RegionID: r.Key, Name: r.Name.en()})
	}); err != nil {
		return err
	}
	type constellation struct {
		record
		RegionID int64 `json:"regionID"`
	}
	if err := each(zr, "mapConstellations.jsonl", func(c constellation) error {
		return q.InsertConstellation(ctx, InsertConstellationParams{ConstellationID: c.Key, RegionID: c.RegionID, Name: c.Name.en()})
	}); err != nil {
		return err
	}
	type solarSystem struct {
		record
		ConstellationID int64   `json:"constellationID"`
		SecurityStatus  float64 `json:"securityStatus"`
	}
	return each(zr, "mapSolarSystems.jsonl", func(s solarSystem) error {
		return q.InsertSolarSystem(ctx, InsertSolarSystemParams{
			SolarSystemID: s.Key, ConstellationID: s.ConstellationID, Name: s.Name.en(), SecurityStatus: s.SecurityStatus,
		})
	})
}

// each calls fn with every record of the archive's JSON Lines file.
func each[T any](zr *zip.Reader, file string, fn func(T) error) error {
	f, err := zr.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var v T
		if err := dec.Decode(&v); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if err := fn(v); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
}
