// Package modules lists the data source modules linked into the binary.
package modules

import (
	"github.com/xaroth/dashboard/module"
	evestatus "github.com/xaroth/dashboard/modules/eveonline/status"
	"github.com/xaroth/dashboard/modules/weather"
)

// All returns every module's registration.
func All() []module.Registration {
	return []module.Registration{evestatus.Module, weather.Module}
}
