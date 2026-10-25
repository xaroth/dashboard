package sde

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Fake serves the SDE downloads, latest.jsonl and the archive of its build,
// for tests of modules using the SDE. Route requests to
// developers.eveonline.com to it.
type Fake struct {
	mu        sync.Mutex
	build     int64
	files     map[string]string
	downloads int
}

// FakeOption sets up a Fake.
type FakeOption func(testing.TB, *Fake)

// NewFake returns a Fake serving build 1 with no records, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *Fake {
	tb.Helper()
	f := &Fake{build: 1, files: map[string]string{}}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectBuild serves build number n.
func ExpectBuild(n int64) FakeOption {
	return func(_ testing.TB, f *Fake) { f.build = n }
}

// ExpectFile adds a JSON Lines file to the archive, e.g. "types.jsonl".
// _sde.jsonl is generated from the build unless set.
func ExpectFile(name, jsonl string) FakeOption {
	return func(_ testing.TB, f *Fake) { f.files[name] = jsonl }
}

// ExpectFile_Missing leaves a file out of the archive.
func ExpectFile_Missing(name string) FakeOption {
	return func(_ testing.TB, f *Fake) { delete(f.files, name) }
}

// Set applies opts to a serving Fake, e.g. to publish a new build.
func (f *Fake) Set(tb testing.TB, opts ...FakeOption) {
	tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range opts {
		opt(tb, f)
	}
}

// Downloads is how many times the archive was downloaded.
func (f *Fake) Downloads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := fmt.Sprintf(`{"_key": "sde", "buildNumber": %d, "releaseDate": "2026-10-12T11:08:38Z"}`+"\n", f.build)
	switch r.URL.Path {
	case "/static-data/tranquility/latest.jsonl":
		fmt.Fprint(w, record)
	case fmt.Sprintf("/static-data/tranquility/eve-online-static-data-%d-jsonl.zip", f.build):
		f.downloads++
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		files := map[string]string{"_sde.jsonl": record}
		for name, data := range f.files {
			files[name] = data
		}
		for name, data := range files {
			fw, err := zw.Create(name)
			if err == nil {
				_, err = fw.Write([]byte(data))
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := zw.Close(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(buf.Bytes())
	default:
		http.NotFound(w, r)
	}
}
