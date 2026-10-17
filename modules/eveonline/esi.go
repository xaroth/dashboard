// Package eveonline holds what the EVE Online modules share: the ESI client.
// The modules are its subpackages.
package eveonline

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"runtime/debug"

	defaults "github.com/eve-online-tools/lib-esi-go"
	"github.com/eve-online-tools/lib-esi-go/middleware/authentication"
	"github.com/eve-online-tools/lib-esi-go/middleware/compatibilitydate"
	"github.com/eve-online-tools/lib-esi-go/middleware/language"
	"github.com/eve-online-tools/lib-esi-go/middleware/tenant"
	"github.com/eve-online-tools/lib-esi-go/middleware/useragent"
	"github.com/eve-online-tools/lib-esi-go/transport"

	"github.com/xaroth/dashboard/module"
)

type config struct {
	// Contact goes in the User-Agent so CCP can reach the operator, e.g.
	// mailto:you@example.com.
	Contact []string `yaml:"contact"`
}

// ESI is the ESI client, configured under modules.eveonline. Modules declare it
// with module.WithShared(eveonline.ESI) and read it with ESI.Get(ctx).
var ESI = module.DefineShared("eveonline", func() *config { return &config{} }, newClient)

func newClient(ctx context.Context, cfg *config) (*http.Client, error) {
	if len(cfg.Contact) == 0 {
		return nil, errors.New("contact is required")
	}
	base := module.HTTPClient(ctx)
	// transport.New ignores WithTransport and always sends through
	// http.DefaultTransport; NewChain keeps the service's traced transport.
	rt := transport.NewChain(cmp.Or(base.Transport, http.DefaultTransport),
		useragent.Middleware("dashboard", version(), cfg.Contact...),
		compatibilitydate.Middleware(defaults.CompatibilityDate),
		language.Middleware(defaults.Language),
		tenant.Middleware(defaults.Tenant),
		authentication.Middleware,
	)
	return &http.Client{Timeout: base.Timeout, Transport: rt}, nil
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
