package esi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	defaults "github.com/eve-online-tools/lib-esi-go"
	"github.com/eve-online-tools/lib-esi-go/esi/getstatus"
	"github.com/eve-online-tools/lib-esi-go/middleware/authentication"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/eveonline/esi"
)

// probe sends ESI requests: one without a token when it needs no scopes,
// else one per character that granted them. Its characters query lists
// those characters.
type probe struct{ cfg *probeConfig }

type probeConfig struct {
	Need esi.Scopes `yaml:"need"`
}

func (m *probe) Initialize(_ context.Context, cfg *probeConfig, _ module.Env) error {
	m.cfg = cfg
	return nil
}

func (*probe) Enabled(context.Context, module.Query) (bool, error) { return false, nil }

func (m *probe) Update(ctx context.Context, u *module.Update) error {
	c := esi.Shared.Get(u.Query)
	if len(m.cfg.Need) == 0 {
		_, err := getstatus.Request(ctx, c)
		return err
	}
	chars, err := c.Characters(ctx, u.Query, m.cfg.Need)
	if err != nil {
		return err
	}
	for _, ch := range chars {
		tok, err := c.Token(ctx, u, ch.ID)
		if err != nil {
			return err
		}
		if _, err := getstatus.Request(ctx, c, authentication.WithToken(tok)); err != nil {
			return err
		}
	}
	return nil
}

func (m *probe) characters(ctx context.Context, q module.Query, _ struct{}) ([]esi.Character, error) {
	return esi.Shared.Get(q).Characters(ctx, q, m.cfg.Need)
}

const (
	skillsScope = "esi-skills.read_skills.v1"
	walletScope = "esi-wallet.read_character_wallet.v1"
)

func probeRegistration(name string, scopes ...string) module.Registration {
	return module.Define[probe, probeConfig](name, func() *probeConfig { return &probeConfig{Need: scopes} },
		module.WithShared(esi.Shared, esi.Scopes(scopes)),
		module.WithQuery("characters", (*probe).characters))
}

var probeModule = probeRegistration("probe")

func load(tb testing.TB, src string, handler http.Handler, regs ...module.Registration) *module.Set {
	tb.Helper()
	if len(regs) == 0 {
		regs = []module.Registration{probeModule}
	}
	return moduletest.Load(tb, nil, src, module.Options{Registrations: regs, HTTPClient: moduletest.HTTPClient(handler)})
}

func TestESIRequest(t *testing.T) {
	t.Parallel()
	var got http.Header
	var url string
	set := load(t, "eveonline_esi: {contact: [mailto:me@example.com, https://example.com]}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, url = r.Header, r.URL.String()
		fmt.Fprint(w, `{}`)
	}))
	if err := set.Update(t.Context(), "probe"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if want := "https://esi.evetech.net/status"; url != want {
		t.Errorf("got URL %s, want %s", url, want)
	}
	want := map[string]string{
		"User-Agent":           "dashboard/(devel) (mailto:me@example.com; https://example.com); " + defaults.UserAgent,
		"X-Compatibility-Date": defaults.CompatibilityDate,
		"Accept-Language":      defaults.Language,
		"X-Tenant":             defaults.Tenant,
	}
	gotHeaders := map[string]string{}
	for k := range want {
		gotHeaders[k] = got.Get(k)
	}
	if diff := cmp.Diff(want, gotHeaders); diff != "" {
		t.Errorf("headers (-want +got):\n%s", diff)
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var sent []time.Time
		set := load(t, "eveonline_esi: {contact: [x]}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sent = append(sent, time.Now())
			// A 4 token bucket refilling one token per second, used up.
			w.Header().Set("X-Ratelimit-Group", "status")
			w.Header().Set("X-Ratelimit-Limit", "4/4s")
			w.Header().Set("X-Ratelimit-Remaining", "0")
			fmt.Fprint(w, `{}`)
		}))
		for range 2 {
			if err := set.Update(t.Context(), "probe"); err != nil {
				t.Fatalf("Update: %v", err)
			}
		}
		if len(sent) != 2 {
			t.Fatalf("got %d requests, want 2", len(sent))
		}
		if gap := sent[1].Sub(sent[0]); gap < time.Second {
			t.Errorf("got %v between requests, want at least 1s: the second waits for the bucket to drain below 75%%", gap)
		}
	})
}

func TestRateLimited(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		status int
		header string
		value  string
		reset  time.Duration
	}{
		{"429", http.StatusTooManyRequests, "Retry-After", "50", 50 * time.Second},
		{"420", 420, "X-Esi-Error-Limit-Reset", "40", 40 * time.Second},
		{"no reset header", 420, "", "", time.Minute},
		// Longer than an update's timeout: the next update fails unsent.
		{"long reset", http.StatusTooManyRequests, "Retry-After", "300", 5 * time.Minute},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var sent []time.Time
				set := load(t, "eveonline_esi: {contact: [x]}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sent = append(sent, time.Now())
					if len(sent) == 1 {
						if testCase.header != "" {
							w.Header().Set(testCase.header, testCase.value)
						}
						w.WriteHeader(testCase.status)
						fmt.Fprint(w, `{"error":"limited"}`)
						return
					}
					fmt.Fprint(w, `{}`)
				}))
				start := time.Now()
				err := set.Update(t.Context(), "probe")
				rl, ok := errors.AsType[*esi.RateLimitError](err)
				if !ok || !rl.Until.Equal(start.Add(testCase.reset)) {
					t.Fatalf("got %v, want a RateLimitError until %v", err, start.Add(testCase.reset))
				}
				err = set.Update(t.Context(), "probe")
				if testCase.reset > 2*time.Minute {
					if rl, ok := errors.AsType[*esi.RateLimitError](err); !ok || rl.Status != "" || len(sent) != 1 {
						t.Fatalf("got %v after %d requests, want an unsent RateLimitError", err, len(sent))
					}
					time.Sleep(testCase.reset)
					err = set.Update(t.Context(), "probe")
				}
				if err != nil {
					t.Fatalf("Update after the limit: %v", err)
				}
				if len(sent) != 2 || sent[1].Sub(sent[0]) < testCase.reset {
					t.Errorf("got requests at %v, want 2 at least %v apart", sent, testCase.reset)
				}
			})
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	set := load(t, "eveonline_esi: {contact: [x]}", http.NotFoundHandler())
	want := []module.SharedStatus{{Name: "eveonline_esi", Page: true, Version: "compatibility date " + defaults.CompatibilityDate}}
	if diff := cmp.Diff(want, set.Shared(t.Context())); diff != "" {
		t.Errorf("shared (-want +got):\n%s", diff)
	}
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		yaml string
		want string
	}{
		{"no contact", "", "eveonline_esi: contact is required"},
		{
			"partial SSO",
			"eveonline_esi: {contact: [x], client_id: id}",
			"eveonline_esi: client_id, client_secret and callback_url are set together",
		},
		{
			"callback path",
			"eveonline_esi: {contact: [x], client_id: id, client_secret: s, callback_url: 'http://h/cb'}",
			`eveonline_esi: callback_url must be an absolute URL with path /eveonline_esi/callback, got "http://h/cb"`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, testCase.yaml, http.NotFoundHandler())
			st, err := set.Statuses(t.Context())
			if err != nil || len(st) != 1 {
				t.Fatalf("Statuses: %v, %v", st, err)
			}
			if st[0].Phase != module.PhaseInitFail || st[0].Error != testCase.want {
				t.Errorf("got %s %q, want %s %q", st[0].Phase, st[0].Error, module.PhaseInitFail, testCase.want)
			}
		})
	}
}
