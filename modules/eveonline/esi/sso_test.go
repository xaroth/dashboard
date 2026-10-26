package esi_test

import (
	"context"
	"encoding/base64"
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
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/eveonline/esi"
)

// fakeEVE is EVE SSO and ESI's /status. Code "skills" logs in Alice with
// the skills scope, "all" with both scopes.
type fakeEVE struct {
	*esi.FakeSSO
	mu      sync.Mutex
	bearers []string
}

func newFake(tb testing.TB) *fakeEVE {
	tb.Helper()
	alice := esi.Character{ID: 90000001, Name: "Alice"}
	return &fakeEVE{FakeSSO: esi.NewFake(tb,
		esi.ExpectCharacter("skills", alice, skillsScope),
		esi.ExpectCharacter("all", alice, skillsScope, walletScope))}
}

func (f *fakeEVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host+r.URL.Path != "esi.evetech.net/status" {
		f.FakeSSO.ServeHTTP(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bearers = append(f.bearers, r.Header.Get("Authorization"))
	fmt.Fprint(w, `{}`)
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
	set.RegisterAdmin(mux)
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
	set := load(t, esi.FakeSSOConfig, newFake(t), probeRegistration("probe", skillsScope), probeRegistration("wallet", walletScope, skillsScope))
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	rec := get(t, mux, "/eveonline_esi/login")
	loc, err := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || err != nil {
		t.Fatalf("got %d %v, want a redirect", rec.Code, err)
	}
	q := loc.Query()
	if q.Get("state") == "" {
		t.Errorf("state: got none, want one")
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
	const wantURL = "https://login.eveonline.com/v2/oauth/authorize"
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != wantURL {
		t.Errorf("authorize URL: got %s, want %s", got, wantURL)
	}
}

func TestLoginAndRefresh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := newFake(t)
		set := load(t, esi.FakeSSOConfig, fake, probeRegistration("probe", skillsScope), probeRegistration("wallet", walletScope))
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
		if diff := cmp.Diff(wantForms, fake.Forms()); diff != "" {
			t.Errorf("token requests (-want +got):\n%s", diff)
		}
	})
}

func TestRefreshRejected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := newFake(t)
		set := load(t, esi.FakeSSOConfig, fake, probeRegistration("probe", skillsScope))
		mux := login(t, set, "skills")
		fake.Set(t, esi.ExpectToken_Revoked(90000001))
		time.Sleep(20 * time.Minute)

		err := set.Update(t.Context(), "probe")
		if !errors.Is(err, module.ErrAuth) {
			t.Fatalf("got %v, want ErrAuth", err)
		}
		if page := get(t, mux, "/eveonline_esi/").Body.String(); !strings.Contains(page, "Invalid refresh token.") {
			t.Errorf("page lacks the refresh error:\n%s", page)
		}
		// The dead refresh token is not sent again.
		sent := len(fake.Forms())
		if err := set.Update(t.Context(), "probe"); !errors.Is(err, module.ErrAuth) {
			t.Errorf("second Update: got %v, want ErrAuth", err)
		}
		if got := len(fake.Forms()); got != sent {
			t.Errorf("got %d token requests after the rejection, want none", got-sent)
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

func TestClientRejected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := newFake(t)
		db := moduletest.DB(t, t.TempDir())
		opts := module.Options{Registrations: []module.Registration{probeRegistration("probe", skillsScope)}, HTTPClient: moduletest.HTTPClient(fake)}
		set := moduletest.Load(t, db, esi.FakeSSOConfig, opts)
		login(t, set, "skills")
		wrong := moduletest.Load(t, db, strings.Replace(esi.FakeSSOConfig, "shh", "wrong", 1), opts)
		time.Sleep(20 * time.Minute)

		err := wrong.Update(t.Context(), "probe")
		if !errors.Is(err, esi.ErrClientRejected) || !errors.Is(err, module.ErrAuth) {
			t.Fatalf("got %v, want ErrClientRejected and ErrAuth", err)
		}
		// A config error is not the character's: with the right secret its
		// token still works.
		mux := http.NewServeMux()
		set.RegisterAdmin(mux)
		if page := get(t, mux, "/eveonline_esi/").Body.String(); strings.Contains(page, "Client authentication failed.") {
			t.Errorf("page shows the client error on the character:\n%s", page)
		}
		// One recorded before error codes were stored does not block either.
		if _, err := db.Writer.ExecContext(t.Context(), `UPDATE eveonline_esi_characters SET auth_error = 'SSO token: 401 Unauthorized: Client authentication failed.: authentication rejected'`); err != nil {
			t.Fatalf("recording a legacy error: %v", err)
		}
		if err := set.Update(t.Context(), "probe"); err != nil {
			t.Errorf("Update with the right secret: %v", err)
		}
	})
}

func TestRefreshSavedAfterDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := newFake(t)
		db := moduletest.DB(t, t.TempDir())
		set := moduletest.Load(t, db, esi.FakeSSOConfig, module.Options{
			Registrations: []module.Registration{probeRegistration("probe", skillsScope)},
			HTTPClient:    moduletest.HTTPClient(fake),
		})
		login(t, set, "skills")
		time.Sleep(20 * time.Minute)
		// SSO answers while another transaction holds the writer past the
		// request's 10 s timeout.
		fake.Set(t, esi.ExpectRefresh_Interleaved(func() {
			conn, err := db.Writer.Conn(context.Background())
			if err != nil {
				t.Errorf("holding the writer: %v", err)
				return
			}
			go func() {
				time.Sleep(15 * time.Second)
				conn.Close()
			}()
		}))

		if err := set.Update(t.Context(), "probe"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Update: got %v, want the request's deadline", err)
		}
		// The rotated token was stored all the same.
		if err := set.Update(t.Context(), "probe"); err != nil {
			t.Fatalf("Update after the deadline: %v", err)
		}
		wantForms := []url.Values{
			{"grant_type": {"authorization_code"}, "code": {"skills"}},
			{"grant_type": {"refresh_token"}, "refresh_token": {"refresh-1"}},
		}
		if diff := cmp.Diff(wantForms, fake.Forms()); diff != "" {
			t.Errorf("token requests (-want +got):\n%s", diff)
		}
		if got := fake.accessTokens(); len(got) == 0 || got[len(got)-1] != "sig-2" {
			t.Errorf("got access tokens %v, want the refreshed sig-2 last", got)
		}
	})
}

func TestLoginDuringRefresh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := newFake(t)
		set := load(t, esi.FakeSSOConfig, fake, probeRegistration("probe", skillsScope))
		login(t, set, "skills")
		time.Sleep(20 * time.Minute)
		fake.Set(t, esi.ExpectRefresh_Interleaved(func() { login(t, set, "all") }))

		// The refresh gets access-2 from refresh-1; the login saved access-3
		// meanwhile, which must win.
		if err := set.Update(t.Context(), "probe"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		time.Sleep(20 * time.Minute)
		if err := set.Update(t.Context(), "probe"); err != nil {
			t.Fatalf("Update after the login's token expired: %v", err)
		}
		if diff := cmp.Diff([]string{"sig-3", "sig-4"}, fake.accessTokens()); diff != "" {
			t.Errorf("access tokens sent to ESI (-want +got):\n%s", diff)
		}
		forms := fake.Forms()
		if got := forms[len(forms)-1].Get("refresh_token"); got != "refresh-3" {
			t.Errorf("got last refresh with %q, want the login's refresh-3", got)
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
				set := load(t, esi.FakeSSOConfig, newFake(t))
				mux := http.NewServeMux()
				set.RegisterAdmin(mux)
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
	set := load(t, "eveonline_esi: {contact: [x]}", newFake(t))
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	if rec := get(t, mux, "/eveonline_esi/login"); rec.Code != http.StatusNotFound {
		t.Errorf("login: got %d, want 404", rec.Code)
	}
	if page := get(t, mux, "/eveonline_esi/").Body.String(); !strings.Contains(page, "SSO is not configured") {
		t.Errorf("page does not say SSO is not configured:\n%s", page)
	}
}
