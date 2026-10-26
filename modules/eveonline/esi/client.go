// Package esi is the shared EVE Online ESI client and the characters logged
// in through EVE SSO.
package esi

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"time"

	defaults "github.com/eve-online-tools/lib-esi-go"
	"github.com/eve-online-tools/lib-esi-go/middleware/ratelimiting"
	"github.com/eve-online-tools/lib-esi-go/middleware/ratelimiting/memory"
	"github.com/eve-online-tools/lib-esi-go/transport"

	"github.com/xaroth/dashboard/module"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// callbackPath is where the SSO callback is served on the admin listener.
const callbackPath = "/eveonline_esi/callback"

type config struct {
	// Contact goes in the User-Agent so CCP can reach the operator, e.g.
	// mailto:you@example.com.
	Contact []string `yaml:"contact"`
	// The SSO application from developers.eveonline.com. Empty disables
	// character logins.
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// CallbackURL is the application's callback: the admin listener's
	// address with path /eveonline_esi/callback.
	CallbackURL string `yaml:"callback_url"`
}

// Scopes are the ESI scopes a module needs, declared with
// module.WithShared(esi.Shared, esi.Scopes{...}).
type Scopes []string

// Shared is the ESI client, configured under modules.eveonline_esi. Modules
// declare it with module.WithShared and read it with Shared.Get.
var Shared = module.DefineShared("eveonline_esi", func() *config { return &config{} }, newClient,
	module.SharedMigrations(migrations))

// Load only detects these by type assertion.
var (
	_ module.Versioned    = (*Client)(nil)
	_ module.AdminHandler = (*Client)(nil)
)

// Client sends ESI requests and holds the logged-in characters' tokens.
type Client struct {
	esi *http.Client
	// sso is the service's client, for login.eveonline.com.
	sso *http.Client
	cfg *config
	// scopes is every scope a module declared, sorted; logins ask for all.
	scopes []string
	now    func() time.Time

	mu sync.Mutex
	// states are the pending logins' OAuth states, to when they expire.
	states map[string]time.Time
	// refreshing serializes refreshes per character: a refresh may rotate
	// the refresh token, so two at once would lose one.
	refreshing map[int64]*sync.Mutex
	// limitedUntil is when the last 429 or 420 lifts; no request is sent
	// before then.
	limitedUntil time.Time
}

func newClient(_ context.Context, cfg *config, decls []Scopes, env module.Env) (*Client, error) {
	if len(cfg.Contact) == 0 {
		return nil, errors.New("contact is required")
	}
	if err := checkSSO(cfg); err != nil {
		return nil, err
	}
	var scopes []string
	for _, d := range decls {
		scopes = append(scopes, d...)
	}
	slices.Sort(scopes)
	base := env.HTTP
	rt := transport.New("dashboard", version(), cfg.Contact, defaults.CompatibilityDate,
		transport.WithTransport(cmp.Or(base.Transport, http.DefaultTransport)),
		transport.WithMiddleware(ratelimiting.Middleware(memory.New())))
	return &Client{
		esi:        &http.Client{Timeout: base.Timeout, Transport: rt},
		sso:        base,
		cfg:        cfg,
		scopes:     slices.Compact(scopes),
		states:     map[string]time.Time{},
		refreshing: map[int64]*sync.Mutex{},
		now:        env.Now,
	}, nil
}

func checkSSO(cfg *config) error {
	if cfg.ClientID == "" && cfg.ClientSecret == "" && cfg.CallbackURL == "" {
		return nil
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.CallbackURL == "" {
		return errors.New("client_id, client_secret and callback_url are set together")
	}
	u, err := url.Parse(cfg.CallbackURL)
	if err != nil || !u.IsAbs() || u.Path != callbackPath {
		return fmt.Errorf("callback_url must be an absolute URL with path %s, got %q", callbackPath, cfg.CallbackURL)
	}
	return nil
}

// Version reports the ESI compatibility date requests are sent with: the
// version of the routes the client was built against.
func (c *Client) Version(context.Context, module.Query) (string, error) {
	return "compatibility date " + defaults.CompatibilityDate, nil
}

// defaultLimitReset is the pause after a 429 or 420 without a reset header.
const defaultLimitReset = time.Minute

// statusErrorLimited is ESI's legacy error limit response.
const statusErrorLimited = 420

// RateLimitError is a request ESI refused with 429 or 420, or one not sent
// because the limit of an earlier one has not reset yet.
type RateLimitError struct {
	// Status is the refused response's status; empty when not sent.
	Status string
	// Until is when requests may be sent again.
	Until time.Time
}

func (e *RateLimitError) Error() string {
	if e.Status == "" {
		return "ESI rate limited until " + e.Until.Format(time.RFC3339)
	}
	return fmt.Sprintf("ESI %s: rate limited until %s", e.Status, e.Until.Format(time.RFC3339))
}

// Do sends an ESI request; Client is the sender for lib-esi-go requests.
// After a 429 or 420 ESI wants all traffic stopped until the limit resets:
// later requests wait for that, or fail with a RateLimitError when their
// deadline comes first.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := c.waitLimit(req.Context()); err != nil {
		return nil, err
	}
	resp, err := c.esi.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != statusErrorLimited {
		return resp, nil
	}
	resp.Body.Close()
	until := c.now().Add(limitReset(resp))
	c.mu.Lock()
	if until.After(c.limitedUntil) {
		c.limitedUntil = until
	}
	c.mu.Unlock()
	return nil, &RateLimitError{Status: resp.Status, Until: until}
}

// waitLimit waits until no limit applies; a 429 or 420 meanwhile extends it.
func (c *Client) waitLimit(ctx context.Context) error {
	for {
		c.mu.Lock()
		until := c.limitedUntil
		c.mu.Unlock()
		d := until.Sub(c.now())
		if d <= 0 {
			return nil
		}
		if dl, ok := ctx.Deadline(); ok && dl.Before(until) {
			return &RateLimitError{Until: until}
		}
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

// limitReset is how long a 429 (Retry-After) or 420
// (X-Esi-Error-Limit-Reset) asks to wait. ESI sends seconds, so an HTTP-date
// Retry-After is not parsed and gets the default.
func limitReset(resp *http.Response) time.Duration {
	h := "Retry-After"
	if resp.StatusCode == statusErrorLimited {
		h = "X-Esi-Error-Limit-Reset"
	}
	if s, err := strconv.Atoi(resp.Header.Get(h)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return defaultLimitReset
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
