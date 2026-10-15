package main

import (
	"fmt"
	"regexp"

	"github.com/xaroth/dashboard/device"
)

type Device struct {
	MAC      string `yaml:"mac"`
	Rotation int    `yaml:"rotation"`
	// Layout is a folder under pages/layouts/; Pages are files in it.
	Layout string   `yaml:"layout"`
	Pages  []string `yaml:"pages"`
	// Params overrides query params per page data key, e.g. "weather.forecast".
	Params map[string]map[string]any `yaml:"params"`
}

// Device and layout names appear in URL paths.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// pages returns the device's pages as page names, "<layout>/<page>".
func (d Device) pages() []string {
	out := make([]string, len(d.Pages))
	for i, p := range d.Pages {
		out[i] = d.Layout + "/" + p
	}
	return out
}

// params returns Params keyed by page name, "<layout>/<page>.<key>".
func (d Device) params() map[string]map[string]any {
	out := make(map[string]map[string]any, len(d.Params))
	for ref, v := range d.Params {
		out[d.Layout+"/"+ref] = v
	}
	return out
}

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
		if !deviceName.MatchString(d.Layout) {
			return fmt.Errorf("devices.%s: layout is required and may only contain letters, digits, - and _", name)
		}
		if len(d.Pages) == 0 {
			return fmt.Errorf("devices.%s: pages is empty", name)
		}
	}
	return nil
}
