package rss

import (
	"bytes"
	_ "embed"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync"
	"testing"
	"time"
)

var (
	//go:embed testdata/rss.xml
	fakeRSS []byte
	//go:embed testdata/atom.xml
	fakeAtom []byte
)

// The feeds a FakeFeeds serves: an RSS 2.0 feed with an ETag and
// Last-Modified, and an Atom feed with Last-Modified only.
const (
	FakeNewsURL = "https://news.example.com/rss"
	FakeTechURL = "https://tech.example.org/atom.xml"
)

// fakeModified is when the fixtures were last modified.
var fakeModified = time.Date(2026, 10, 16, 7, 30, 0, 0, time.UTC)

// FakeFeeds serves feeds for moduletest.HTTPClient, answering conditional
// requests with 304 as a server does, and records the requests.
type FakeFeeds struct {
	mu    sync.Mutex
	feeds map[string]*fakeFeed
}

type fakeFeed struct {
	body     []byte
	etag     bool
	modified time.Time
	status   int
	served   int
	unmod    int
	last     http.Header
}

// FakeOption sets up a FakeFeeds.
type FakeOption func(testing.TB, *FakeFeeds)

// NewFake returns a FakeFeeds serving the fixtures in testdata at
// FakeNewsURL and FakeTechURL, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeFeeds {
	tb.Helper()
	f := &FakeFeeds{feeds: map[string]*fakeFeed{
		FakeNewsURL: {body: fakeRSS, etag: true, modified: fakeModified},
		FakeTechURL: {body: fakeAtom, modified: fakeModified},
	}}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectFeed serves body at url, without validators.
func ExpectFeed(url string, body []byte) FakeOption {
	return func(_ testing.TB, f *FakeFeeds) { f.feeds[url] = &fakeFeed{body: body} }
}

// Publish replaces the feed at url with body, modified at t.
func (f *FakeFeeds) Publish(url string, body []byte, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[url].body, f.feeds[url].modified = body, t
}

// Fail makes the feed at url answer with status; 0 serves it again.
func (f *FakeFeeds) Fail(url string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[url].status = status
}

// Served returns how many requests for url were answered with the feed, and
// how many with 304.
func (f *FakeFeeds) Served(url string) (full, notModified int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.feeds[url].served, f.feeds[url].unmod
}

// LastHeader returns the headers of the last request for url.
func (f *FakeFeeds) LastHeader(url string) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.feeds[url].last
}

func (f *FakeFeeds) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	feed, ok := f.feeds[r.URL.Scheme+"://"+r.URL.Host+r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	feed.last = r.Header.Clone()
	if feed.status != 0 {
		http.Error(w, "upstream down", feed.status)
		return
	}
	if feed.etag {
		h := fnv.New64a()
		h.Write(feed.body)
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, h.Sum64()))
	}
	// ServeContent answers If-None-Match and If-Modified-Since.
	rec := &statusRecorder{ResponseWriter: w}
	http.ServeContent(rec, r, "", feed.modified, bytes.NewReader(feed.body))
	if rec.status == http.StatusNotModified {
		feed.unmod++
	} else {
		feed.served++
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
