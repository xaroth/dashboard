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
func Database(ctx context.Context) *sql.DB { return value[*sql.DB](ctx, keyDatabase) }

// Logger is the module's logger.
func Logger(ctx context.Context) *slog.Logger {
	return value[*slog.Logger](ctx, keyLogger)
}

// HTTPClient is the client for calls to the source; it has a timeout.
func HTTPClient(ctx context.Context) *http.Client {
	return value[*http.Client](ctx, keyHTTPClient)
}

// Clock returns the current time.
func Clock(ctx context.Context) func() time.Time {
	return value[func() time.Time](ctx, keyClock)
}

// value panics outside a context from Set: modules only run inside one.
func value[T any](ctx context.Context, key ctxKey) T {
	v, ok := ctx.Value(key).(T)
	if !ok {
		panic("module: context has no module dependencies")
	}
	return v
}

// SaveSnapshot sets v as the module's snapshot. Called from Update; it is
// stored together with the fetch time when Update returns nil, and dropped
// when Update fails.
func SaveSnapshot(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	return value[*stateStore](ctx, keyState).save(b)
}

// LoadSnapshot reads the module's snapshot into v. ok is false when there is none yet.
func LoadSnapshot(ctx context.Context, v any) (ok bool, err error) {
	b, err := value[*stateStore](ctx, keyState).load(ctx)
	if err != nil || b == nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("decoding snapshot: %w", err)
	}
	return true, nil
}
