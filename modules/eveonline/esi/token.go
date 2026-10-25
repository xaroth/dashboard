package esi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xaroth/dashboard/module"
)

// refreshMargin is how long before expiry a token is refreshed, so it does
// not expire in flight.
const refreshMargin = time.Minute

// Character is a character logged in through SSO.
type Character struct {
	ID   int64
	Name string
}

// Characters returns the logged-in characters that granted every scope in
// need, by name. A module is typically enabled when there is one.
func (c *Client) Characters(ctx context.Context, q module.Query, need Scopes) ([]Character, error) {
	rows, err := New(q.DB).ListCharacters(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing characters: %w", err)
	}
	out := []Character{}
	for _, row := range rows {
		if len(missing(strings.Fields(row.Scopes), need)) == 0 {
			out = append(out, Character{ID: row.CharacterID, Name: row.Name})
		}
	}
	return out, nil
}

// Token returns the character's token, for authentication.WithToken. It
// refreshes itself before a request when it is about to expire and stores
// the result, so it takes Update's writer. A rejected refresh fails the
// request with module.ErrAuth.
func (c *Client) Token(ctx context.Context, u *module.Update, characterID int64) (*Token, error) {
	row, err := New(u.DB).GetCharacter(ctx, characterID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("character %d is not logged in", characterID)
	}
	if err != nil {
		return nil, fmt.Errorf("reading character %d: %w", characterID, err)
	}
	t := &Token{c: c, db: u.DB, now: u.Now, log: u.Log, id: characterID}
	t.set(row)
	return t, nil
}

// Token is a character's access token. It implements lib-esi-go's
// authentication.RefreshableToken and ScopedToken.
type Token struct {
	c   *Client
	db  *sql.DB
	now func() time.Time
	log *slog.Logger
	id  int64

	mu      sync.Mutex
	access  string
	expires time.Time
	scopes  []string
}

func (t *Token) set(row EveonlineEsiCharacter) {
	t.access, t.expires, t.scopes = row.AccessToken, row.ExpiresAt, strings.Fields(row.Scopes)
}

func (t *Token) Owner() int64 { return t.id }

func (t *Token) Token() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.access
}

func (t *Token) Scopes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scopes
}

// RefreshIfNeeded refreshes the token when it expires within refreshMargin.
func (t *Token) RefreshIfNeeded(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.now().Add(refreshMargin).Before(t.expires) {
		return nil
	}
	lock := t.c.refreshLock(t.id)
	lock.Lock()
	defer lock.Unlock()
	q := New(t.db)
	// Another module may have refreshed it while this one waited.
	row, err := q.GetCharacter(ctx, t.id)
	if err != nil {
		return fmt.Errorf("reading character %d: %w", t.id, err)
	}
	if t.now().Add(refreshMargin).Before(row.ExpiresAt) {
		t.set(row)
		return nil
	}
	old := row.RefreshToken
	tok, err := t.c.requestToken(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old}})
	if errors.Is(err, module.ErrAuth) {
		msg := err.Error()
		if serr := q.SaveAuthError(ctx, SaveAuthErrorParams{AuthError: &msg, CharacterID: t.id}); serr != nil {
			t.log.Error("recording rejected refresh", "character", row.Name, "err", serr)
		}
	}
	if err != nil {
		return fmt.Errorf("refreshing token of %s: %w", row.Name, err)
	}
	row.AccessToken, row.RefreshToken, row.ExpiresAt = tok.AccessToken, tok.RefreshToken, tok.expiresAt(t.now())
	n, err := q.SaveRefresh(ctx, SaveRefreshParams{
		RefreshToken: row.RefreshToken, AccessToken: row.AccessToken, ExpiresAt: row.ExpiresAt,
		CharacterID: t.id, OldRefreshToken: old,
	})
	if err != nil {
		return fmt.Errorf("saving refreshed token of %s: %w", row.Name, err)
	}
	if n == 0 {
		// A login saved newer tokens, possibly with more scopes, while this
		// refresh was in flight: keep those.
		return t.reload(ctx, q)
	}
	t.set(row)
	return nil
}

func (t *Token) reload(ctx context.Context, q *Queries) error {
	row, err := q.GetCharacter(ctx, t.id)
	if err != nil {
		return fmt.Errorf("reading character %d: %w", t.id, err)
	}
	t.set(row)
	return nil
}

func (c *Client) refreshLock(id int64) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.refreshing[id]
	if !ok {
		l = &sync.Mutex{}
		c.refreshing[id] = l
	}
	return l
}
