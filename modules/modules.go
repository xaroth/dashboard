// Package modules lists the data source modules linked into the binary.
package modules

import (
	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/car"
	"github.com/xaroth/dashboard/modules/eveonline/skills"
	"github.com/xaroth/dashboard/modules/eveonline/status"
	"github.com/xaroth/dashboard/modules/p1"
	"github.com/xaroth/dashboard/modules/rss"
	"github.com/xaroth/dashboard/modules/solar"
	"github.com/xaroth/dashboard/modules/weather"
)

// All returns every module's registration.
func All() []module.Registration {
	return []module.Registration{car.Module, p1.Module, rss.Module, skills.Module, solar.Module, status.Module, weather.Module}
}
