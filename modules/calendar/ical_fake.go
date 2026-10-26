package calendar

import (
	_ "embed"
	"net/http"
	"sync"
	"testing"

	"github.com/xaroth/dashboard/module"
)

// The feeds a FakeFeeds serves by default, at secret-looking addresses like
// Google Calendar's.
const (
	FakeFamilyURL = "https://calendar.example.com/ical/family%40example.com/private-f4m1ly5ecret/basic.ics"
	FakeWorkURL   = "https://calendar.example.com/ical/work%40example.com/private-w0rk5ecret/basic.ics"
)

var (
	//go:embed testdata/family.ics
	fakeFamily []byte
	//go:embed testdata/work.ics
	fakeWork []byte
)

// FakeFeeds serves iCalendar feeds by URL, for moduletest.HTTPClient: the
// recorded family and work feeds at FakeFamilyURL and FakeWorkURL, and
// those set with ExpectFeed. It counts requests per URL and those not
// marked with module.WithSecretURL.
type FakeFeeds struct {
	mu       sync.Mutex
	feeds    map[string][]byte
	status   map[string]int
	calls    map[string]int
	unmarked int
}

// FakeOption sets up a FakeFeeds.
type FakeOption func(testing.TB, *FakeFeeds)

// NewFake returns a FakeFeeds serving the recorded feeds, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeFeeds {
	tb.Helper()
	f := &FakeFeeds{
		feeds:  map[string][]byte{FakeFamilyURL: fakeFamily, FakeWorkURL: fakeWork},
		status: map[string]int{},
		calls:  map[string]int{},
	}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectFeed serves body at url.
func ExpectFeed(url string, body []byte) FakeOption {
	return func(_ testing.TB, f *FakeFeeds) { f.feeds[url] = body }
}

// ExpectStatus makes url answer with code and no feed.
func ExpectStatus(url string, code int) FakeOption {
	return func(_ testing.TB, f *FakeFeeds) { f.status[url] = code }
}

// SetStatus makes url answer with code from now on; 0 serves its feed again.
func (f *FakeFeeds) SetStatus(url string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[url] = code
}

// Calls returns how many requests url got.
func (f *FakeFeeds) Calls(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[url]
}

// Unmarked returns how many requests were not marked as going to a secret URL.
func (f *FakeFeeds) Unmarked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unmarked
}

func (f *FakeFeeds) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !module.SecretURL(r.Context()) {
		f.unmarked++
	}
	u := r.URL.String()
	f.calls[u]++
	if code := f.status[u]; code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	body, ok := f.feeds[u]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=UTF-8")
	w.Write(body)
}
