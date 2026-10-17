package esi_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline/esi"
)

const ssoConfig = `eveonline_esi: {contact: [x], client_id: app, client_secret: shh, callback_url: "http://admin/eveonline_esi/callback"}`

// fakeEVE is EVE SSO and ESI. Code "skills" logs in Alice with the skills
// scope, "all" with both scopes. Each token request issues access-<n> and
// refresh-<n>; a refresh must present the latest refresh token.
type fakeEVE struct {
	mu       sync.Mutex
	issued   int
	refresh  string
	revoked  bool
	forms    []url.Values
	bearers  []string
	badBasic bool
}

func (f *fakeEVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Host + r.URL.Path {
	case "login.eveonline.com/v2/oauth/token":
		if id, secret, ok := r.BasicAuth(); !ok || id != "app" || secret != "shh" {
			f.badBasic = true
		}
		r.ParseForm()
		f.forms = append(f.forms, r.PostForm)
		var scopes []string
		switch {
		case r.PostForm.Get("code") == "skills":
			scopes = []string{skillsScope}
		case r.PostForm.Get("code") == "all":
			scopes = []string{skillsScope, walletScope}
		case r.PostForm.Get("grant_type") == "refresh_token" && !f.revoked && r.PostForm.Get("refresh_token") == f.refresh:
			scopes = []string{skillsScope}
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Invalid refresh token."}`)
			return
		}
		f.issued++
		f.refresh = fmt.Sprintf("refresh-%d", f.issued)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  jwt(map[string]any{"sub": "CHARACTER:EVE:90000001", "name": "Alice", "scp": scopes}, f.issued),
			"expires_in":    1199,
			"token_type":    "Bearer",
			"refresh_token": f.refresh,
		})
	case "esi.evetech.net/status":
		f.bearers = append(f.bearers, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func jwt(claims map[string]any, n int) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(b) + "." + enc(fmt.Appendf(nil, "sig-%d", n))
}

func (f *fakeEVE) tokenForms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forms
}

// accessTokens returns the tokens ESI requests carried.
func (f *fakeEVE) accessTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Named by their signature part, which numbers them.
	var out []string
	for _, b := range f.bearers {
		parts := strings.Split(strings.TrimPrefix(b, "Bearer "), ".")
		sig, _ := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
		out = append(out, string(sig))
	}
	return out
}

func get(tb testing.TB, mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	tb.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(tb.Context(), http.MethodGet, target, nil))
	return rec
}

// login runs the SSO flow with code and returns the admin mux.
func login(tb testing.TB, set *module.Set, code string) *http.ServeMux {
	tb.Helper()
	mux := http.NewServeMux()
	set.RegisterShared(mux)
	rec := get(tb, mux, "/eveonline_esi/login")
	if rec.Code != http.StatusFound {
		tb.Fatalf("login: got %d %s, want 302", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		tb.Fatalf("login redirect: %v", err)
	}
	rec = get(tb, mux, "/eveonline_esi/callback?code="+code+"&state="+loc.Query().Get("state"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/eveonline_esi/" {
		tb.Fatalf("callback: got %d %s, want 303 to /eveonline_esi/", rec.Code, rec.Body)
	}
	return mux
}

func TestLoginRedirect(t *testing.T) {
	t.Parallel()
	set := load(t, ssoConfig, &fakeEVE{}, probeRegistration("probe", skillsScope), probeRegistration("wallet", walletScope, skillsScope))
	mux := http.NewServeMux()
	set.RegisterShared(mux)
	rec := get(t, mux, "/eveonline_esi/login")
	loc, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil {
		t.Fatalf("got %d %v, want a redirect", rec.Code, err)
	}
	q := loc.Query()
	if q.Get("state") == "" {
		t.Errorf("got no state")
	}
	q.Del("state")
	want := url.Values{
		"response_type": {"code"},
		"client_id":     {"app"},
		"redirect_uri":  {"http://admin/eveonline_esi/callback"},
		"scope":         {skillsScope + " " + walletScope},
	}
	if diff := cmp.Diff(want, q); diff != "" {
		t.Errorf("authorize query (-want +got):\n%s", diff)
	}
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != "https://login.eveonline.com/v2/oauth/authorize" {
		t.Errorf("got authorize URL %s", got)
	}
}

func TestLoginAndRefresh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeEVE{}
		set := load(t, ssoConfig, fake, probeRegistration("probe", skillsScope), probeRegistration("wallet", walletScope))
		mux := login(t, set, "skills")

		if res := set.Resolve(t.Context(), "probe", "characters", nil); !cmp.Equal(res.Value, []esi.Character{{ID: 90000001, Name: "Alice"}}) {
			t.Errorf("probe characters: got %v %q, want Alice", res.Value, res.Error)
		}
		if res := set.Resolve(t.Context(), "wallet", "characters", nil); !cmp.Equal(res.Value, []esi.Character{}) {
			t.Errorf("wallet characters: got %v %q, want none: Alice did not grant the wallet scope", res.Value, res.Error)
		}
		if page := get(t, mux, "/eveonline_esi/").Body.String(); !strings.Contains(page, "Alice") || !strings.Contains(page, walletScope) {
			t.Errorf("page lacks Alice or her missing wallet scope:\n%s", page)
		}

		// Fresh token; then expired twice, so the second refresh must use
		// the rotated refresh token.
		for range 3 {
			if err := set.Update(t.Context(), "probe"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			time.Sleep(20 * time.Minute)
		}
		if diff := cmp.Diff([]string{"sig-1", "sig-2", "sig-3"}, fake.accessTokens()); diff != "" {
			t.Errorf("access tokens sent to ESI (-want +got):\n%s", diff)
		}
		wantForms := []url.Values{
			{"grant_type": {"authorization_code"}, "code": {"skills"}},
			{"grant_type": {"refresh_token"}, "refresh_token": {"refresh-1"}},
			{"grant_type": {"refresh_token"}, "refresh_token": {"refresh-2"}},
		}
		if diff := cmp.Diff(wantForms, fake.tokenForms()); diff != "" {
			t.Errorf("token requests (-want +got):\n%s", diff)
		}
		if fake.badBasic {
			t.Errorf("a token request lacked the client credentials")
		}
	})
}

func TestRefreshRejected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeEVE{}
		set := load(t, ssoConfig, fake, probeRegistration("probe", skillsScope))
		mux := login(t, set, "skills")
		fake.mu.Lock()
		fake.revoked = true
		fake.mu.Unlock()
		time.Sleep(20 * time.Minute)

		err := set.Update(t.Context(), "probe")
		if !errors.Is(err, module.ErrAuth) {
			t.Fatalf("got %v, want ErrAuth", err)
		}
		if page := get(t, mux, "/eveonline_esi/").Body.String(); !strings.Contains(page, "Invalid refresh token.") {
			t.Errorf("page lacks the refresh error:\n%s", page)
		}
		// Logging in again clears it.
		login(t, set, "skills")
		if page := get(t, mux, "/eveonline_esi/").Body.String(); strings.Contains(page, "Invalid refresh token.") {
			t.Errorf("page still shows the refresh error after logging in again")
		}
		if err := set.Update(t.Context(), "probe"); err != nil {
			t.Errorf("Update after logging in again: %v", err)
		}
	})
}

func TestCallbackErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		// query is the callback's query; STATE is replaced by a valid state.
		query    string
		wait     time.Duration
		wantCode int
		wantBody string
	}{
		{"unknown state", "code=skills&state=nope", 0, http.StatusBadRequest, "unknown or expired login; start again\n"},
		{"expired state", "code=skills&state=STATE", 11 * time.Minute, http.StatusBadRequest, "unknown or expired login; start again\n"},
		{"denied", "error=access_denied&state=STATE", 0, http.StatusBadRequest, "SSO login failed: access_denied\n"},
		{"code rejected", "code=bad&state=STATE", 0, http.StatusBadGateway, "SSO token: 400 Bad Request: Invalid refresh token.: authentication rejected\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				set := load(t, ssoConfig, &fakeEVE{})
				mux := http.NewServeMux()
				set.RegisterShared(mux)
				loc, _ := url.Parse(get(t, mux, "/eveonline_esi/login").Header().Get("Location"))
				time.Sleep(testCase.wait)
				rec := get(t, mux, "/eveonline_esi/callback?"+strings.ReplaceAll(testCase.query, "STATE", loc.Query().Get("state")))
				if rec.Code != testCase.wantCode || rec.Body.String() != testCase.wantBody {
					t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body, testCase.wantCode, testCase.wantBody)
				}
			})
		})
	}
}

func TestLoginWithoutSSO(t *testing.T) {
	t.Parallel()
	set := load(t, "eveonline_esi: {contact: [x]}", &fakeEVE{})
	mux := http.NewServeMux()
	set.RegisterShared(mux)
	if rec := get(t, mux, "/eveonline_esi/login"); rec.Code != http.StatusNotFound {
		t.Errorf("login: got %d, want 404", rec.Code)
	}
	if page := get(t, mux, "/eveonline_esi/").Body.String(); !strings.Contains(page, "SSO is not configured") {
		t.Errorf("page does not say SSO is not configured:\n%s", page)
	}
}
