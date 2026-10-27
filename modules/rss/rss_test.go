package rss_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/rss"
)

var testNow = time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)

const twoFeeds = `feeds: {news: {url: "` + rss.FakeNewsURL + `"}, tech: {url: "` + rss.FakeTechURL + `"}}`

// clock is a settable time for module.Options.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func load(tb testing.TB, dir string, fake *rss.FakeFeeds, cfg string, c *clock) *module.Set {
	tb.Helper()
	return moduletest.Load(tb, moduletest.DB(tb, dir), "rss: {"+cfg+"}", module.Options{
		Registrations: []module.Registration{rss.Module},
		HTTPClient:    moduletest.HTTPClient(fake),
		Now:           c.now,
	})
}

func update(tb testing.TB, set *module.Set) {
	tb.Helper()
	if err := set.Update(tb.Context(), "rss"); err != nil {
		tb.Fatalf("Update: %v", err)
	}
}

func items(tb testing.TB, set *module.Set, params map[string]any) *rss.Feed {
	tb.Helper()
	res := set.Resolve(tb.Context(), "rss", "items", params)
	if res.Error != "" {
		tb.Fatalf("items: %s", res.Error)
	}
	f, _ := res.Value.(*rss.Feed)
	return f
}

func titles(f *rss.Feed) []string {
	var out []string
	for _, it := range f.Items {
		out = append(out, it.Title)
	}
	return out
}

func TestUpdateAndItems(t *testing.T) {
	t.Parallel()
	set := load(t, t.TempDir(), rss.NewFake(t), twoFeeds, &clock{testNow})
	if f := items(t, set, map[string]any{"feed": "news"}); f != nil {
		t.Errorf("before any update: got %+v, want no value", f)
	}
	update(t, set)

	news := items(t, set, map[string]any{"feed": "news", "limit": 2})
	published := time.Date(2026, 10, 16, 9, 30, 0, 0, time.FixedZone("", 2*3600))
	want := &rss.Feed{
		Name: "news", Title: "Example News", Link: "https://news.example.com/", FetchedAt: &testNow,
		Items: []rss.Item{
			{Title: "Newest story", Link: "https://news.example.com/3", Published: &published, Summary: "Plain text summary."},
			{Title: "Second story", Link: "https://news.example.com/2", Published: ptr(published.Add(-90 * time.Minute)), Summary: "Rain all day. Bring an umbrella & boots."},
		},
	}
	if diff := cmp.Diff(want, news); diff != "" {
		t.Errorf("news (-want +got):\n%s", diff)
	}
	// Newest first; undated items last, in feed order.
	if diff := cmp.Diff([]string{"Newest story", "Second story", "Oldest story", "Undated story"}, titles(items(t, set, map[string]any{"feed": "news"}))); diff != "" {
		t.Errorf("all news (-want +got):\n%s", diff)
	}

	// Atom: the updated time stands in for published, content for the summary.
	tech := items(t, set, map[string]any{"feed": "tech"})
	if len(tech.Items) != 1 || tech.Title != "Example Tech" || tech.Items[0].Published == nil ||
		!tech.Items[0].Published.Equal(time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC)) || tech.Items[0].Summary != "Version 2 is out." {
		t.Errorf("got tech %+v", tech)
	}

	// An empty feed is the first by name.
	if f := items(t, set, nil); f.Name != "news" || len(f.Items) != 4 {
		t.Errorf("default feed: got %s with %d items, want news with 4", f.Name, len(f.Items))
	}

	b, err := json.Marshal(news)
	if err != nil || !strings.Contains(string(b), `"published":"2026-10-16T07:30:00Z"`) {
		t.Errorf("got %s (%v), want RFC 3339 times", b, err)
	}
}

func TestConditionalGet(t *testing.T) {
	t.Parallel()
	fake := rss.NewFake(t)
	c := &clock{testNow}
	set := load(t, t.TempDir(), fake, twoFeeds, c)
	update(t, set)

	c.t = testNow.Add(15 * time.Minute)
	update(t, set)
	if h := fake.LastHeader(rss.FakeNewsURL); h.Get("If-None-Match") == "" {
		t.Errorf("news: got headers %v, want If-None-Match", h)
	}
	if h := fake.LastHeader(rss.FakeTechURL); h.Get("If-Modified-Since") != "Fri, 16 Oct 2026 07:30:00 GMT" {
		t.Errorf("tech: got headers %v, want If-Modified-Since", h)
	}
	for _, url := range []string{rss.FakeNewsURL, rss.FakeTechURL} {
		if full, unmod := fake.Served(url); full != 1 || unmod != 1 {
			t.Errorf("%s: got %d full, %d not modified, want 1 each", url, full, unmod)
		}
	}
	// Not modified keeps the items, fetched now.
	f := items(t, set, map[string]any{"feed": "news"})
	if len(f.Items) != 4 || !f.FetchedAt.Equal(c.t) {
		t.Errorf("after 304: got %d items fetched at %v, want 4 at %v", len(f.Items), f.FetchedAt, c.t)
	}

	fake.Publish(rss.FakeNewsURL, []byte(`<rss version="2.0"><channel><title>Example News</title><item><title>Only story</title></item></channel></rss>`), testNow.Add(time.Hour))
	update(t, set)
	if diff := cmp.Diff([]string{"Only story"}, titles(items(t, set, map[string]any{"feed": "news"}))); diff != "" {
		t.Errorf("after publishing (-want +got):\n%s", diff)
	}
}

func TestChangedURL(t *testing.T) {
	t.Parallel()
	fake := rss.NewFake(t)
	dir := t.TempDir()
	c := &clock{testNow}
	update(t, load(t, dir, fake, `feeds: {news: {url: "`+rss.FakeNewsURL+`"}}`, c))

	set := load(t, dir, fake, `feeds: {news: {url: "`+rss.FakeTechURL+`"}}`, c)
	if f := items(t, set, map[string]any{"feed": "news"}); f != nil {
		t.Errorf("before fetching the new URL: got %+v, want no value", f)
	}
	update(t, set)
	if h := fake.LastHeader(rss.FakeTechURL); h.Get("If-None-Match") != "" || h.Get("If-Modified-Since") != "" {
		t.Errorf("got headers %v, want no validators from the old URL", h)
	}
	if f := items(t, set, map[string]any{"feed": "news"}); f.Title != "Example Tech" {
		t.Errorf("got title %q, want the new URL's", f.Title)
	}
}

func TestFeedOrder(t *testing.T) {
	t.Parallel()
	fake := rss.NewFake(t)
	dir := t.TempDir()
	c := &clock{testNow}
	set := load(t, dir, fake, `feeds: {news: {url: "`+rss.FakeNewsURL+`", order: feed}}`, c)
	update(t, set)
	if diff := cmp.Diff([]string{"Second story", "Undated story", "Newest story", "Oldest story"}, titles(items(t, set, map[string]any{"feed": "news"}))); diff != "" {
		t.Errorf("feed order (-want +got):\n%s", diff)
	}

	// Another order fetches without validators: a 304 would keep the old order.
	set = load(t, dir, fake, `feeds: {news: {url: "`+rss.FakeNewsURL+`"}}`, c)
	update(t, set)
	if h := fake.LastHeader(rss.FakeNewsURL); h.Get("If-None-Match") != "" {
		t.Errorf("got headers %v, want no validators after changing the order", h)
	}
	if diff := cmp.Diff([]string{"Newest story", "Second story", "Oldest story", "Undated story"}, titles(items(t, set, map[string]any{"feed": "news"}))); diff != "" {
		t.Errorf("published order (-want +got):\n%s", diff)
	}
}

func TestFeedFails(t *testing.T) {
	t.Parallel()
	fake := rss.NewFake(t)
	c := &clock{testNow}
	set := load(t, t.TempDir(), fake, twoFeeds, c)
	update(t, set)

	// One feed failing keeps its items and does not fail the update.
	fake.Fail(rss.FakeNewsURL, http.StatusBadGateway)
	c.t = testNow.Add(15 * time.Minute)
	update(t, set)
	news := items(t, set, map[string]any{"feed": "news"})
	if !strings.Contains(news.Error, "502") || len(news.Items) != 4 || !news.FetchedAt.Equal(testNow) {
		t.Errorf("got news error %q, %d items fetched at %v, want 502, 4 items at %v", news.Error, len(news.Items), news.FetchedAt, testNow)
	}
	if tech := items(t, set, map[string]any{"feed": "tech"}); tech.Error != "" || !tech.FetchedAt.Equal(c.t) {
		t.Errorf("got tech error %q fetched at %v, want none at %v", tech.Error, tech.FetchedAt, c.t)
	}

	// All feeds failing fails the update.
	fake.Fail(rss.FakeTechURL, http.StatusNotFound)
	if err := set.Update(t.Context(), "rss"); err == nil || !strings.Contains(err.Error(), "news: 502") || !strings.Contains(err.Error(), "tech: 404") {
		t.Errorf("got %v, want both feeds' errors", err)
	}

	// Recovering clears the error.
	fake.Fail(rss.FakeNewsURL, 0)
	fake.Fail(rss.FakeTechURL, 0)
	update(t, set)
	if news := items(t, set, map[string]any{"feed": "news"}); news.Error != "" {
		t.Errorf("got error %q after recovering, want none", news.Error)
	}
}

func TestFormats(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		body string
		want []string
		err  string
	}{
		{"json feed", `{"version":"https://jsonfeed.org/version/1.1","title":"J","items":[{"id":"1","title":"From JSON","date_published":"2026-10-16T08:00:00Z"}]}`, []string{"From JSON"}, ""},
		{"rss 1.0", `<?xml version="1.0"?><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/"><channel><title>R</title></channel><item><title>From RDF</title><link>https://r.example/1</link></item></rdf:RDF>`, []string{"From RDF"}, ""},
		{"untitled item", `<rss version="2.0"><channel><item><description>&lt;p&gt;Just text&lt;/p&gt;</description></item></channel></rss>`, []string{"Just text"}, ""},
		{"not a feed", `<html><body>Moved</body></html>`, nil, "parsing feed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			const url = "https://feed.example.net/feed"
			set := load(t, t.TempDir(), rss.NewFake(t, rss.ExpectFeed(url, []byte(testCase.body))), `feeds: {x: {url: "`+url+`"}}`, &clock{testNow})
			err := set.Update(t.Context(), "rss")
			if testCase.err != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.err) {
					t.Errorf("got %v, want error containing %q", err, testCase.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if diff := cmp.Diff(testCase.want, titles(items(t, set, nil))); diff != "" {
				t.Errorf("titles (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLongSummary(t *testing.T) {
	t.Parallel()
	const url = "https://feed.example.net/feed"
	body := `<rss version="2.0"><channel><item><title>Long</title><description>` + strings.Repeat("word ", 100) + `</description></item></channel></rss>`
	set := load(t, t.TempDir(), rss.NewFake(t, rss.ExpectFeed(url, []byte(body))), `feeds: {x: {url: "`+url+`"}}`, &clock{testNow})
	update(t, set)
	s := items(t, set, nil).Items[0].Summary
	if want := strings.Repeat("word ", 59) + "word…"; s != want {
		t.Errorf("got summary %q, want %q", s, want)
	}
}

func TestItemsErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		cfg    string
		params map[string]any
		want   string
	}{
		{"unknown feed", twoFeeds, map[string]any{"feed": "sports"}, `no feed "sports" (have news, tech)`},
		{"limit too high", twoFeeds, map[string]any{"limit": 51}, "limit must be 0 to 50"},
		{"negative limit", twoFeeds, map[string]any{"limit": -1}, "limit must be 0 to 50"},
		{"no feeds", "", nil, "no feeds configured"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, t.TempDir(), rss.NewFake(t), testCase.cfg, &clock{testNow})
			res := set.Resolve(t.Context(), "rss", "items", testCase.params)
			if !strings.Contains(res.Error, testCase.want) || res.Value != nil {
				t.Errorf("got %+v, want error containing %q", res, testCase.want)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		cfg  string
		want module.Phase
	}{
		{"feeds", twoFeeds, module.PhaseWaiting},
		{"no feeds", "", module.PhaseWaiting},
		{"relative url", `feeds: {news: {url: "/rss"}}`, module.PhaseInitFail},
		{"other scheme", `feeds: {news: {url: "ftp://example.com/rss"}}`, module.PhaseInitFail},
		{"no url", `feeds: {news: {}}`, module.PhaseInitFail},
		{"feed order", `feeds: {news: {url: "` + rss.FakeNewsURL + `", order: feed}}`, module.PhaseWaiting},
		{"unknown order", `feeds: {news: {url: "` + rss.FakeNewsURL + `", order: rank}}`, module.PhaseInitFail},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, t.TempDir(), rss.NewFake(t), testCase.cfg, &clock{testNow})
			st, err := set.Statuses(t.Context())
			if err != nil || st[0].Phase != testCase.want {
				t.Errorf("got %+v, %v, want phase %s", st, err, testCase.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
