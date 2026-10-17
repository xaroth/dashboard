package eveonline

import (
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

const (
	ssoURL = "https://login.eveonline.com"
	// stateTTL is how long a login may take from redirect to callback.
	stateTTL = 10 * time.Minute
)

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("page").Parse(pageHTML))

// ServeHTTP serves the characters page, the SSO login and its callback,
// under /eveonline/ on the admin listener.
func (c *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		c.page(w, r)
	case "/login":
		c.login(w, r)
	case "/callback":
		c.callback(w, r)
	default:
		http.NotFound(w, r)
	}
}

type characterView struct {
	Name      string
	ID        int64
	Scopes    []string
	Missing   []string
	ExpiresAt time.Time
	AuthError string
}

func (c *Client) page(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := New(module.Database(ctx)).ListCharacters(ctx)
	if err != nil {
		c.fail(w, r, http.StatusInternalServerError, fmt.Errorf("listing characters: %w", err))
		return
	}
	views := make([]characterView, 0, len(rows))
	for _, row := range rows {
		granted := strings.Fields(row.Scopes)
		v := characterView{Name: row.Name, ID: row.CharacterID, Scopes: granted, ExpiresAt: row.ExpiresAt}
		v.Missing = missing(granted, c.scopes)
		if row.AuthError != nil {
			v.AuthError = *row.AuthError
		}
		views = append(views, v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = pageTmpl.Execute(w, map[string]any{
		"Configured": c.cfg.ClientID != "",
		"Scopes":     c.scopes,
		"Characters": views,
	})
	if err != nil {
		module.Logger(ctx).Error("rendering characters page", "err", err)
	}
}

func (c *Client) login(w http.ResponseWriter, r *http.Request) {
	if c.cfg.ClientID == "" {
		c.fail(w, r, http.StatusNotFound, errors.New("SSO is not configured: set client_id, client_secret and callback_url"))
		return
	}
	state := rand.Text()
	now := module.Clock(r.Context())()
	c.mu.Lock()
	for s, exp := range c.states {
		if now.After(exp) {
			delete(c.states, s)
		}
	}
	c.states[state] = now.Add(stateTTL)
	c.mu.Unlock()
	v := url.Values{
		"response_type": {"code"},
		"redirect_uri":  {c.cfg.CallbackURL},
		"client_id":     {c.cfg.ClientID},
		"scope":         {strings.Join(c.scopes, " ")},
		"state":         {state},
	}
	http.Redirect(w, r, ssoURL+"/v2/oauth/authorize?"+v.Encode(), http.StatusFound)
}

func (c *Client) callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		c.fail(w, r, http.StatusBadRequest, fmt.Errorf("SSO login failed: %s", cmp.Or(q.Get("error_description"), e)))
		return
	}
	now := module.Clock(ctx)()
	c.mu.Lock()
	exp, ok := c.states[q.Get("state")]
	delete(c.states, q.Get("state"))
	c.mu.Unlock()
	if !ok || now.After(exp) {
		c.fail(w, r, http.StatusBadRequest, errors.New("unknown or expired login; start again"))
		return
	}
	tok, err := c.requestToken(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {q.Get("code")}})
	if err != nil {
		c.fail(w, r, http.StatusBadGateway, err)
		return
	}
	ch, err := parseAccessToken(tok.AccessToken)
	if err != nil {
		c.fail(w, r, http.StatusBadGateway, err)
		return
	}
	err = New(module.Database(ctx)).SaveLogin(ctx, SaveLoginParams{
		CharacterID: ch.id, Name: ch.name, Scopes: strings.Join(ch.scopes, " "),
		RefreshToken: tok.RefreshToken, AccessToken: tok.AccessToken, ExpiresAt: tok.expiresAt(now),
	})
	if err != nil {
		c.fail(w, r, http.StatusInternalServerError, fmt.Errorf("saving character: %w", err))
		return
	}
	module.Logger(ctx).Info("character logged in", "character", ch.name, "id", ch.id, "scopes", len(ch.scopes))
	http.Redirect(w, r, "/eveonline/", http.StatusSeeOther)
}

func (c *Client) fail(w http.ResponseWriter, r *http.Request, code int, err error) {
	module.Logger(r.Context()).Warn("eveonline page", "path", r.URL.Path, "err", err)
	http.Error(w, err.Error(), code)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (t tokenResponse) expiresAt(now time.Time) time.Time {
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// requestToken posts form to the SSO token endpoint. A rejected grant or
// client is module.ErrAuth: retrying will not help, logging in again may.
func (c *Client) requestToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	var tok tokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ssoURL+"/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tok, err
	}
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.sso.Do(req)
	if err != nil {
		return tok, fmt.Errorf("SSO token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tok, fmt.Errorf("SSO token: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		err := fmt.Errorf("SSO token: %s: %s", resp.Status, cmp.Or(e.Description, e.Error, strings.TrimSpace(string(body))))
		if e.Error == "invalid_grant" || e.Error == "invalid_client" {
			err = fmt.Errorf("%w: %w", err, module.ErrAuth)
		}
		return tok, err
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return tok, fmt.Errorf("SSO token: decoding: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return tok, errors.New("SSO token: response has no access or refresh token")
	}
	return tok, nil
}

type accessClaims struct {
	id     int64
	name   string
	scopes []string
}

// parseAccessToken reads the character from an access token without
// checking its signature: it comes straight from the token endpoint over
// TLS, authenticated with the client secret.
func parseAccessToken(token string) (accessClaims, error) {
	var out accessClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return out, errors.New("access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return out, fmt.Errorf("access token payload: %w", err)
	}
	var claims struct {
		Sub  string          `json:"sub"`
		Name string          `json:"name"`
		Scp  json.RawMessage `json:"scp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return out, fmt.Errorf("access token claims: %w", err)
	}
	id, ok := strings.CutPrefix(claims.Sub, "CHARACTER:EVE:")
	if !ok {
		return out, fmt.Errorf("access token subject %q is not a character", claims.Sub)
	}
	if out.id, err = strconv.ParseInt(id, 10, 64); err != nil {
		return out, fmt.Errorf("access token subject %q: %w", claims.Sub, err)
	}
	out.name = claims.Name
	// scp is a string for one scope, an array for more, absent for none.
	if len(claims.Scp) > 0 {
		var one string
		if json.Unmarshal(claims.Scp, &one) == nil {
			out.scopes = []string{one}
		} else if err := json.Unmarshal(claims.Scp, &out.scopes); err != nil {
			return out, fmt.Errorf("access token scopes: %w", err)
		}
	}
	slices.Sort(out.scopes)
	return out, nil
}

// missing returns the scopes in want that granted lacks.
func missing(granted, want []string) []string {
	var out []string
	for _, s := range want {
		if !slices.Contains(granted, s) {
			out = append(out, s)
		}
	}
	return out
}
