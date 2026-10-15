package main

import (
	"fmt"
	"regexp"

	"github.com/xaroth/dashboard/device"
)

type Device struct {
	MAC      string   `yaml:"mac"`
	Rotation int      `yaml:"rotation"`
	Pages    []string `yaml:"pages"`
	// Params overrides query params per page data key, e.g. "overview.events".
	Params map[string]map[string]any `yaml:"params"`
}

// Device names appear in URL paths.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateDevices(devices map[string]Device) error {
	macs := map[string]string{}
	for name, d := range devices {
		if !deviceName.MatchString(name) {
			return fmt.Errorf("devices.%s: name may only contain letters, digits, - and _", name)
		}
		if name == "page" {
			// Taken by the admin route /preview/page/{page}.png.
			return fmt.Errorf("devices.page: the name page is reserved")
		}
		mac, err := device.NormalizeMAC(d.MAC)
		if err != nil {
			return fmt.Errorf("devices.%s: %w", name, err)
		}
		if other, ok := macs[mac]; ok {
			return fmt.Errorf("devices.%s: mac %s already used by %s", name, mac, other)
		}
		macs[mac] = name
		switch d.Rotation {
		case 0, 90, 180, 270:
		default:
			return fmt.Errorf("devices.%s: rotation must be 0, 90, 180 or 270", name)
		}
		if len(d.Pages) == 0 {
			return fmt.Errorf("devices.%s: pages is empty", name)
		}
	}
	return nil
}
