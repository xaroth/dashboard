// Package rss reads named RSS, Atom and JSON feeds.
package rss

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xaroth/dashboard/module"
)

type feedConfig struct {
	URL string `yaml:"url"`
	// Order is "published" (newest first) or "feed" (as the feed lists them,
	// for ranked feeds such as Hacker News).
	Order string `yaml:"order"`
}

const (
	orderPublished = "published"
	orderFeed      = "feed"
)

type config struct {
	// Feeds by name; pages select one with the items query's feed param.
	Feeds map[string]feedConfig `yaml:"feeds"`
}

func defaultConfig() *config {
	return &config{}
}

type rss struct {
	cfg *config
}

// Module is the rss registration.
var Module = module.Define[rss, config](
	"rss",
	defaultConfig,
	module.WithUpdateInterval(15*time.Minute),
	module.WithStaleAfter(2*time.Hour),
	module.WithQuery("items", (*rss).items),
)

// snapshot is what one update stores, by feed name.
type snapshot struct {
	Feeds map[string]stored `json:"feeds"`
}

type stored struct {
	// URL is the one fetched; validators and items of another URL are not reused.
	URL string `json:"url"`
	// Order is the one items are stored in; a 304 under another order would
	// keep them in the old one.
	Order        string `json:"order"`
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
	// FetchedAt is the last successful fetch; Error is why the latest one
	// failed, which keeps the previous items.
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     string     `json:"error"`
	Title     string     `json:"title"`
	Link      string     `json:"link"`
	Items     []Item     `json:"items"`
}

func (m *rss) Initialize(_ context.Context, cfg *config, _ module.Env) error {
	for _, name := range slices.Sorted(maps.Keys(cfg.Feeds)) {
		if name == "" {
			return errors.New("feeds: empty feed name")
		}
		u, err := url.Parse(cfg.Feeds[name].URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("feeds: %s: url must be an http or https URL, got %q", name, cfg.Feeds[name].URL)
		}
		f := cfg.Feeds[name]
		f.Order = cmp.Or(f.Order, orderPublished)
		if f.Order != orderPublished && f.Order != orderFeed {
			return fmt.Errorf("feeds: %s: order must be %s or %s, got %q", name, orderPublished, orderFeed, f.Order)
		}
		cfg.Feeds[name] = f
	}
	m.cfg = cfg
	return nil
}

func (m *rss) Enabled(context.Context, module.Query) (bool, error) {
	return len(m.cfg.Feeds) > 0, nil
}

// Update fetches every feed. A feed that fails keeps its previous items with
// the error; the update fails only when all do.
func (m *rss) Update(ctx context.Context, u *module.Update) error {
	var prev snapshot
	if _, err := u.LoadSnapshot(ctx, &prev); err != nil {
		return err
	}
	names := slices.Sorted(maps.Keys(m.cfg.Feeds))
	feeds := make([]stored, len(names))
	errs := make([]error, len(names))
	now := u.Now()
	// Concurrently: a few unreachable feeds, each up to the HTTP timeout,
	// would otherwise use up the update's timeout.
	var wg sync.WaitGroup
	for i, name := range names {
		s := prev.Feeds[name]
		// Snapshots from before Order had none, and were stored by published.
		if fc := m.cfg.Feeds[name]; s.URL != fc.URL || cmp.Or(s.Order, orderPublished) != fc.Order {
			s = stored{URL: fc.URL, Order: fc.Order}
		}
		wg.Go(func() { feeds[i], errs[i] = refresh(ctx, u.HTTP, s, now) })
	}
	wg.Wait()

	out := snapshot{Feeds: make(map[string]stored, len(names))}
	var failed []error
	for i, name := range names {
		out.Feeds[name] = feeds[i]
		if errs[i] != nil {
			failed = append(failed, fmt.Errorf("%s: %w", name, errs[i]))
		}
	}
	if len(names) > 0 && len(failed) == len(names) {
		return errors.Join(failed...)
	}
	// Logged only here: the scheduler logs a failed update's error.
	for _, err := range failed {
		u.Log.Warn("fetching feed", "err", err)
	}
	return u.SaveSnapshot(out)
}

const maxLimit = 50

type itemsParams struct {
	// Feed is a configured feed's name; empty is the first by name.
	Feed string `json:"feed"`
	// Limit is how many items to return, newest first; default 10, at most 50.
	Limit int `json:"limit"`
}

// Feed is a feed's latest items.
type Feed struct {
	// Name is the feed's name in the config.
	Name string `json:"name"`
	// Title and Link are the feed's own, as it publishes them.
	Title string `json:"title"`
	Link  string `json:"link"`
	// FetchedAt is the last successful fetch of this feed; Error is why the
	// latest one failed, in which case Items are from FetchedAt.
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     string     `json:"error"`
	Items     []Item     `json:"items"`
}

// items returns the newest items of one feed. The value is null before the
// feed's first fetch.
func (m *rss) items(ctx context.Context, q module.Query, p itemsParams) (*Feed, error) {
	if p.Limit < 0 || p.Limit > maxLimit {
		return nil, fmt.Errorf("limit must be 0 to %d, got %d", maxLimit, p.Limit)
	}
	names := slices.Sorted(maps.Keys(m.cfg.Feeds))
	if len(names) == 0 {
		return nil, errors.New("no feeds configured")
	}
	name := cmp.Or(p.Feed, names[0])
	if !slices.Contains(names, name) {
		return nil, fmt.Errorf("no feed %q (have %s)", name, strings.Join(names, ", "))
	}
	var snap snapshot
	if _, err := q.LoadSnapshot(ctx, &snap); err != nil {
		return nil, err
	}
	s, ok := snap.Feeds[name]
	if !ok || s.URL != m.cfg.Feeds[name].URL {
		return nil, nil
	}
	items := s.Items[:min(cmp.Or(p.Limit, 10), len(s.Items))]
	return &Feed{
		Name: name, Title: s.Title, Link: s.Link, FetchedAt: s.FetchedAt, Error: s.Error,
		Items: append([]Item{}, items...),
	}, nil
}
