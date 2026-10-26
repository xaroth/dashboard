package esi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"testing"
)

// FakeSSOConfig is an eveonline_esi config with SSO set up for FakeSSO.
const FakeSSOConfig = `eveonline_esi: {contact: [x], client_id: app, client_secret: shh, callback_url: "http://admin/eveonline_esi/callback"}`

// FakeSSO is the EVE SSO token and revoke endpoints, for
// moduletest.HTTPClient. Login codes are set up with ExpectCharacter. Each
// token request issues access and refresh tokens numbered n: the access
// token's signature is "sig-<n>", the refresh token "refresh-<n>". A refresh
// token works once, and not after it is revoked.
type FakeSSO struct {
	mu     sync.Mutex
	logins map[string]fakeGrant
	// refreshes are the unused refresh tokens.
	refreshes map[string]fakeGrant
	issued    int
	serving   bool
	forms     []url.Values
	revokes   []url.Values
	revokeErr bool
	during    func()
}

type fakeGrant struct {
	char   Character
	scopes []string
}

// FakeOption sets up a FakeSSO.
type FakeOption func(testing.TB, *FakeSSO)

// NewFake returns a FakeSSO without characters, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeSSO {
	tb.Helper()
	f := &FakeSSO{logins: map[string]fakeGrant{}, refreshes: map[string]fakeGrant{}}
	for _, opt := range opts {
		opt(tb, f)
	}
	f.serving = true
	return f
}

// ExpectCharacter makes login code log in char, granting scopes.
func ExpectCharacter(code string, char Character, scopes ...string) FakeOption {
	return func(_ testing.TB, f *FakeSSO) { f.logins[code] = fakeGrant{char, scopes} }
}

// ExpectToken_Revoked rejects the refresh tokens issued so far to the
// character with id, as when its owner revokes the app. Logging in again
// issues working ones. It is for Set: NewFake has issued no tokens.
func ExpectToken_Revoked(id int64) FakeOption {
	return func(tb testing.TB, f *FakeSSO) {
		if !f.serving {
			tb.Fatalf("ExpectToken_Revoked(%d) before any login: pass it to Set", id)
		}
		maps.DeleteFunc(f.refreshes, func(_ string, g fakeGrant) bool { return g.char.ID == id })
	}
}

// ExpectRefresh_Interleaved runs fn once, while answering the next refresh
// after issuing its tokens.
func ExpectRefresh_Interleaved(fn func()) FakeOption {
	return func(_ testing.TB, f *FakeSSO) { f.during = fn }
}

// ExpectRevoke_Error answers revoke requests with a 503.
func ExpectRevoke_Error() FakeOption {
	return func(_ testing.TB, f *FakeSSO) { f.revokeErr = true }
}

// Set applies opts to a serving FakeSSO.
func (f *FakeSSO) Set(tb testing.TB, opts ...FakeOption) {
	tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range opts {
		opt(tb, f)
	}
}

// Forms returns the forms of the token requests.
func (f *FakeSSO) Forms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forms)
}

// Revokes returns the forms of the revoke requests.
func (f *FakeSSO) Revokes() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revokes)
}

const (
	fakeTokenURL  = "login.eveonline.com/v2/oauth/token"
	fakeRevokeURL = "login.eveonline.com/v2/oauth/revoke"
)

func (f *FakeSSO) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Host + r.URL.Path
	if path != fakeTokenURL && path != fakeRevokeURL {
		http.NotFound(w, r)
		return
	}
	if id, secret, ok := r.BasicAuth(); !ok || id != "app" || secret != "shh" {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"Client authentication failed."}`)
		return
	}
	r.ParseForm()
	if path == fakeRevokeURL {
		f.revokes = append(f.revokes, r.PostForm)
		if f.revokeErr {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		delete(f.refreshes, r.PostForm.Get("token"))
		return
	}
	f.forms = append(f.forms, r.PostForm)
	refresh := r.PostForm.Get("grant_type") == "refresh_token"
	var g fakeGrant
	var ok bool
	if refresh {
		token := r.PostForm.Get("refresh_token")
		g, ok = f.refreshes[token]
		delete(f.refreshes, token)
	} else {
		g, ok = f.logins[r.PostForm.Get("code")]
	}
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Invalid refresh token."}`)
		return
	}
	f.issued++
	token := fmt.Sprintf("refresh-%d", f.issued)
	f.refreshes[token] = g
	claims, _ := json.Marshal(map[string]any{"sub": fmt.Sprintf("CHARACTER:EVE:%d", g.char.ID), "name": g.char.Name, "scp": g.scopes})
	enc := base64.RawURLEncoding.EncodeToString
	body := map[string]any{
		"access_token":  enc([]byte(`{"alg":"RS256"}`)) + "." + enc(claims) + "." + enc(fmt.Appendf(nil, "sig-%d", f.issued)),
		"expires_in":    1199,
		"token_type":    "Bearer",
		"refresh_token": token,
	}
	if during := f.during; during != nil && refresh {
		f.during = nil
		f.mu.Unlock()
		during()
		f.mu.Lock()
	}
	json.NewEncoder(w).Encode(body)
}
