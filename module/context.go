package module

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type ctxKey int

const (
	keyDatabase ctxKey = iota
	keyLogger
	keyHTTPClient
	keyClock
	keyState
)

// Database is the module's connection: the writer in Initialize and Update,
// the read-only pool in queries.
func Database(ctx context.Context) *sql.DB { return value[*sql.DB](ctx, keyDatabase, "WithDatabase") }

// Logger is the module's logger.
func Logger(ctx context.Context) *slog.Logger {
	return value[*slog.Logger](ctx, keyLogger, "WithLogger")
}

// HTTPClient is the client for calls to the source; it has a timeout.
func HTTPClient(ctx context.Context) *http.Client {
	return value[*http.Client](ctx, keyHTTPClient, "WithHTTPClient")
}

// Clock returns the current time.
func Clock(ctx context.Context) func() time.Time {
	return value[func() time.Time](ctx, keyClock, "WithClock")
}

func value[T any](ctx context.Context, key ctxKey, setter string) T {
	v, ok := ctx.Value(key).(T)
	if !ok {
		panic("module: context has no dependency; set it with " + setter)
	}
	return v
}

// WithDatabase, WithLogger, WithHTTPClient and WithClock put dependencies on
// a context; the service uses them, and tests use them to pass fakes.
func WithDatabase(ctx context.Context, db *sql.DB) context.Context {
	return context.WithValue(ctx, keyDatabase, db)
}

func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, keyLogger, l)
}

func WithHTTPClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, keyHTTPClient, c)
}

func WithClock(ctx context.Context, now func() time.Time) context.Context {
	return context.WithValue(ctx, keyClock, now)
}

// StateStore keeps a module's snapshot: the one document a module like
// weather or calendar produces per update.
type StateStore interface {
	SaveSnapshot(ctx context.Context, data []byte) error
	LoadSnapshot(ctx context.Context) ([]byte, error)
}

// WithState puts the module's state store on a context.
func WithState(ctx context.Context, s StateStore) context.Context {
	return context.WithValue(ctx, keyState, s)
}

// SaveSnapshot sets v as the module's snapshot. Called from Update; it is
// stored together with the fetch time when Update returns nil, and dropped
// when Update fails.
func SaveSnapshot(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	return value[StateStore](ctx, keyState, "WithState").SaveSnapshot(ctx, b)
}

// LoadSnapshot reads the module's snapshot into v. ok is false when there is none yet.
func LoadSnapshot(ctx context.Context, v any) (ok bool, err error) {
	b, err := value[StateStore](ctx, keyState, "WithState").LoadSnapshot(ctx)
	if err != nil || b == nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("decoding snapshot: %w", err)
	}
	return true, nil
}
