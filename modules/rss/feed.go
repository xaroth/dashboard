package rss

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

const (
	maxBody    = 5 << 20
	maxItems   = maxLimit
	maxSummary = 300 // runes
)

// Item is one feed entry.
type Item struct {
	Title string `json:"title"`
	Link  string `json:"link"`
	// Published falls back to the updated time; null when the feed gives neither.
	Published *time.Time `json:"published"`
	// Summary is the description, or else the content, as plain text, cut
	// at about 300 characters.
	Summary string `json:"summary"`
}

// refresh fetches s.URL, conditionally when s has validators, and returns s
// updated. On failure it returns s with the error and its previous items.
func refresh(ctx context.Context, c *http.Client, s stored, now time.Time) (stored, error) {
	err := fetch(ctx, c, &s)
	if err != nil {
		s.Error = err.Error()
		return s, err
	}
	s.FetchedAt, s.Error = &now, ""
	return s, nil
}

func fetch(ctx context.Context, c *http.Client, s *stored) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	// Some hosts reject Go's default User-Agent.
	req.Header.Set("User-Agent", "dashboard (+https://github.com/xaroth/dashboard)")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, */*;q=0.8")
	if s.ETag != "" {
		req.Header.Set("If-None-Match", s.ETag)
	}
	if s.LastModified != "" {
		req.Header.Set("If-Modified-Since", s.LastModified)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		// A 304 may leave out validators that did not change.
		s.ETag = cmp.Or(resp.Header.Get("ETag"), s.ETag)
		s.LastModified = cmp.Or(resp.Header.Get("Last-Modified"), s.LastModified)
		return nil
	case http.StatusOK:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// Error pages are often HTML; the page shows this as text.
		return fmt.Errorf("%s: %s", resp.Status, summarize(string(body)))
	}
	f, err := gofeed.NewParser().Parse(http.MaxBytesReader(nil, resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("parsing feed: %w", err)
	}
	s.ETag, s.LastModified = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
	s.Title, s.Link = collapse(f.Title), f.Link
	s.Items = make([]Item, 0, len(f.Items))
	for _, it := range f.Items {
		summary := summarize(cmp.Or(it.Description, it.Content))
		title := collapse(it.Title)
		if title == "" {
			title, summary = summary, ""
		}
		s.Items = append(s.Items, Item{
			Title: title, Link: it.Link, Published: cmp.Or(it.PublishedParsed, it.UpdatedParsed), Summary: summary,
		})
	}
	// Newest first; undated items keep the feed's order, after the dated ones.
	slices.SortStableFunc(s.Items, func(a, b Item) int {
		if a.Published == nil || b.Published == nil {
			return boolCompare(a.Published == nil, b.Published == nil)
		}
		return b.Published.Compare(*a.Published)
	})
	s.Items = s.Items[:min(maxItems, len(s.Items))]
	return nil
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

// summarize returns HTML as plain text, cut at a word boundary within
// maxSummary runes.
func summarize(s string) string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	skip := false
	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			return cut(collapse(b.String()))
		case html.TextToken:
			if !skip {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if t := string(name); t == "script" || t == "style" {
				skip = tt == html.StartTagToken
			}
			// Tags separate words: "<p>a</p><p>b</p>" is "a b".
			b.WriteByte(' ')
		}
	}
}

func cut(s string) string {
	if utf8.RuneCountInString(s) <= maxSummary {
		return s
	}
	r := []rune(s)[:maxSummary]
	if i := strings.LastIndexByte(string(r), ' '); i > 0 {
		return string(r)[:i] + "…"
	}
	return string(r) + "…"
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
