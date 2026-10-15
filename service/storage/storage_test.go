package storage_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/storage"
)

func open(tb testing.TB) *storage.DB {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return db
}

func TestOpenPragmas(t *testing.T) {
	t.Parallel()
	db := open(t)
	testCases := []struct {
		name   string
		conn   *sql.DB
		pragma string
		want   string
	}{
		{"page size", db.Writer, "page_size", "16384"},
		{"writer wal", db.Writer, "journal_mode", "wal"},
		{"reader wal", db.Reader, "journal_mode", "wal"},
		{"synchronous normal", db.Writer, "synchronous", "1"},
		{"writer foreign keys", db.Writer, "foreign_keys", "1"},
		{"reader foreign keys", db.Reader, "foreign_keys", "1"},
		{"busy timeout", db.Reader, "busy_timeout", "5000"},
		{"cache size", db.Reader, "cache_size", "-32768"},
		{"mmap size", db.Writer, "mmap_size", "268435456"},
		{"reader query only", db.Reader, "query_only", "1"},
		{"writer not query only", db.Writer, "query_only", "0"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var got string
			if err := testCase.conn.QueryRowContext(t.Context(), "PRAGMA "+testCase.pragma).Scan(&got); err != nil {
				t.Fatalf("PRAGMA %s: %v", testCase.pragma, err)
			}
			if got != testCase.want {
				t.Errorf("got %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestOpenReopens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for range 2 {
		db, err := storage.Open(t.Context(), dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		db.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, storage.File)); err != nil {
		t.Errorf("database file: %v", err)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	t.Parallel()
	db := open(t)
	_, err := db.Reader.ExecContext(t.Context(), "INSERT INTO module_state (module) VALUES ('x')")
	if err == nil {
		t.Error("got nil error writing through the reader, want one")
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	good := fstest.MapFS{"sql/migrations/00001_notes.sql": {Data: []byte(
		"-- +goose Up\nCREATE TABLE test_notes (id INTEGER PRIMARY KEY);\n-- +goose Down\nDROP TABLE test_notes;\n")}}
	bad := fstest.MapFS{"sql/migrations/00001_broken.sql": {Data: []byte("-- +goose Up\nCREATE TABLE test_broken (;\n")}}
	testCases := []struct {
		name    string
		fsys    fstest.MapFS
		dir     string
		wantErr bool
	}{
		{"applies", good, "sql/migrations", false},
		{"bad sql", bad, "sql/migrations", true},
		{"missing dir", good, "nope", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db := open(t)
			err := db.Migrate(t.Context(), "test", testCase.fsys, testCase.dir)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error %v", err, testCase.wantErr)
			}
			if testCase.wantErr {
				return
			}
			// Idempotent, and tracked in the owner's own table.
			if err := db.Migrate(t.Context(), "test", testCase.fsys, testCase.dir); err != nil {
				t.Fatalf("second Migrate: %v", err)
			}
			var version int
			if err := db.Reader.QueryRowContext(t.Context(), "SELECT MAX(version_id) FROM goose_test").Scan(&version); err != nil {
				t.Fatalf("reading goose_test: %v", err)
			}
			if version != 1 {
				t.Errorf("got version %d, want 1", version)
			}
		})
	}
}

func TestModuleState(t *testing.T) {
	t.Parallel()
	db := open(t)
	q := db.Queries()
	ctx := t.Context()
	first := time.Date(2026, 10, 16, 7, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	second := first.Add(15 * time.Minute)
	data := `{"temp":12.5}`
	boom := "boom"

	if err := q.RecordSuccess(ctx, storage.RecordSuccessParams{Module: "weather", At: &first, Data: &data}); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if err := q.RecordFailure(ctx, storage.RecordFailureParams{Module: "weather", At: &second, Error: &boom}); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	got, err := storage.New(db.Reader).GetState(ctx, "weather")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	// A failure keeps the last good data and fetch time; times come back in UTC.
	want := storage.ModuleState{
		Module:      "weather",
		AttemptedAt: ptr(second.UTC()),
		FetchedAt:   ptr(first.UTC()),
		Error:       &boom,
		Data:        &data,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("state (-want +got):\n%s", diff)
	}

	var raw string
	if err := db.Reader.QueryRowContext(ctx, "SELECT fetched_at || '' FROM module_state").Scan(&raw); err != nil {
		t.Fatalf("raw fetched_at: %v", err)
	}
	if raw != "2026-10-16 05:00:00+00:00" {
		t.Errorf("got stored time %q, want SQLite date text in UTC", raw)
	}

	if _, err := q.GetState(ctx, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("got %v for an unknown module, want sql.ErrNoRows", err)
	}
}

func ptr[T any](v T) *T { return &v }
