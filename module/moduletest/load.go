package moduletest

import (
	"net/http"
	"testing"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/yamlconfig"
	"github.com/xaroth/dashboard/storage"
)

// DB opens the database in dir and closes it at cleanup.
func DB(tb testing.TB, dir string) *storage.DB {
	tb.Helper()
	db, err := storage.Open(tb.Context(), dir)
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return db
}

// Load loads o.Registrations on db, or a new database if nil, with config
// as the modules section of the config file. An unset HTTPClient answers
// 404.
func Load(tb testing.TB, db *storage.DB, config string, o module.Options) *module.Set {
	tb.Helper()
	if db == nil {
		db = DB(tb, tb.TempDir())
	}
	configs, err := yamlconfig.Parse([]byte(config))
	if err != nil {
		tb.Fatalf("config: %v", err)
	}
	o.Configs = configs
	o.Migrator, o.Writer, o.Reader = db, db.Writer, db.Reader
	if o.HTTPClient == nil {
		o.HTTPClient = HTTPClient(http.NotFoundHandler())
	}
	set, err := module.Load(tb.Context(), o)
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set
}
