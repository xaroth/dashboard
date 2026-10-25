package module_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/storage"
)

type countParams struct {
	Limit int `json:"limit"`
}

type countResult struct {
	Updates int `json:"updates"`
	Limit   int `json:"limit"`
}

// count reads the snapshot on the query connection.
func (m *testModule) count(ctx context.Context, q module.Query, p countParams) (countResult, error) {
	var snap map[string]int
	if _, err := q.LoadSnapshot(ctx, &snap); err != nil {
		return countResult{}, err
	}
	if p.Limit < 0 {
		return countResult{}, errors.New("limit must not be negative")
	}
	return countResult{Updates: snap["updates"], Limit: p.Limit}, nil
}

// boom panics.
func (m *testModule) boom(context.Context, module.Query, struct{}) (int, error) { panic("kaboom") }

// write tries to write through the query connection.
func (m *testModule) write(ctx context.Context, q module.Query, _ struct{}) (int, error) {
	_, err := q.DB.ExecContext(ctx, "INSERT INTO test_items (id) VALUES (1)")
	return 0, err
}

func queryRegistration() module.Registration {
	return module.Define[testModule, testConfig]("test", func() *testConfig { return &testConfig{} },
		module.WithUpdateInterval(10*time.Minute),
		module.WithMigrations(migrations),
		module.WithQuery("count", (*testModule).count),
		module.WithQuery("write", (*testModule).write),
		module.WithQuery("boom", (*testModule).boom),
	)
}

func TestCheck(t *testing.T) {
	t.Parallel()
	set, _ := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	testCases := []struct {
		name    string
		module  string
		query   string
		params  map[string]any
		wantErr error
		wantMsg string
	}{
		{"valid", "test", "count", map[string]any{"limit": 3}, nil, ""},
		{"no params", "test", "count", nil, nil, ""},
		{"unknown module", "nope", "count", nil, module.ErrUnknownModule, ""},
		{"unknown query", "test", "nope", nil, nil, "unknown query test.nope (have boom, count, write)"},
		{"unknown param", "test", "count", map[string]any{"bogus": 1}, nil, "bogus"},
		{"wrong type", "test", "count", map[string]any{"limit": "three"}, nil, "limit"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := set.Check(testCase.module, testCase.query, testCase.params)
			wantErr := testCase.wantErr != nil || testCase.wantMsg != ""
			if (err != nil) != wantErr {
				t.Fatalf("got %v, want error %v", err, wantErr)
			}
			if testCase.wantErr != nil && !errors.Is(err, testCase.wantErr) {
				t.Errorf("got %v, want %v", err, testCase.wantErr)
			}
			if testCase.wantMsg != "" && !strings.Contains(err.Error(), testCase.wantMsg) {
				t.Errorf("got %v, want it to mention %q", err, testCase.wantMsg)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		set, _ := loadWith(ctx, t, "test: {key: k}", queryRegistration())
		params := map[string]any{"limit": 3}

		before := set.Resolve(ctx, "test", "count", params)
		if diff := cmp.Diff(module.Result{Value: countResult{Limit: 3}, Stale: true}, before); diff != "" {
			t.Errorf("before any update (-want +got):\n%s", diff)
		}

		done := make(chan struct{})
		go func() {
			set.Run(ctx)
			close(done)
		}()
		waitFirstAttempt(t, set)
		after := set.Resolve(ctx, "test", "count", params)
		if after.FetchedAt == nil || after.Stale || after.Error != "" {
			t.Errorf("after an update: got %+v, want fresh", after)
		}
		if diff := cmp.Diff(countResult{Updates: 1, Limit: 3}, after.Value); diff != "" {
			t.Errorf("value (-want +got):\n%s", diff)
		}
		cancel()
		<-done
	})
}

func TestResolveStateUnreadable(t *testing.T) {
	t.Parallel()
	set, _ := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := set.Resolve(ctx, "test", "count", nil)
	if !got.Stale || got.Error == "" {
		t.Errorf("got %+v, want stale with an error", got)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	set, db := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	testCases := []struct {
		name   string
		module string
		query  string
		params map[string]any
		want   string
	}{
		{"query error", "test", "count", map[string]any{"limit": -1}, "must not be negative"},
		{"bad params", "test", "count", map[string]any{"bogus": 1}, "bogus"},
		{"unknown module", "nope", "count", nil, "unknown module"},
		{"read only", "test", "write", nil, "readonly"},
		{"panic", "test", "boom", nil, "query panicked: kaboom"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := set.Resolve(t.Context(), testCase.module, testCase.query, testCase.params)
			if got.Value != nil || !strings.Contains(strings.ToLower(got.Error), testCase.want) {
				t.Errorf("got %+v, want no value and an error containing %q", got, testCase.want)
			}
		})
	}
	var n int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM test_items").Scan(&n); err != nil || n != 0 {
		t.Errorf("got %d rows (%v), want the query's write rejected", n, err)
	}
}

type otherModule struct{ testModule }

func (m *otherModule) count(context.Context, module.Query, struct{}) (int, error) { return 0, nil }

func TestDefineRejectsForeignQuery(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("got no panic for a query method of another module type, want one")
		}
	}()
	module.Define[testModule, testConfig]("test", func() *testConfig { return &testConfig{} },
		module.WithQuery("count", (*otherModule).count))
}

func loadWith(ctx context.Context, tb testing.TB, src string, regs ...module.Registration) (*module.Set, *storage.DB) {
	tb.Helper()
	return loadRegs(ctx, tb, src, regs)
}
