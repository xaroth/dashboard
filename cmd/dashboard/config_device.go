package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/render"
)

type Device struct {
	// MAC is normalized by parseConfig, as device.NormalizeMAC returns it.
	MAC      string `yaml:"mac"`
	Rotation int    `yaml:"rotation"`
	// Layout is a folder under pages/layouts/. Pages are files in it or,
	// when it has none of that name, in pages/shared/.
	Layout string   `yaml:"layout"`
	Pages  []string `yaml:"pages"`
	// Power is auto (the USB-Connected header, else battery), usb or battery.
	Power device.Power `yaml:"power"`
	// Params overrides query params per page data key, e.g. "weather.forecast".
	Params map[string]map[string]any `yaml:"params"`
}

// logoPage is the page setup renders, when the layout has one.
const logoPage = "logo"

// Device and layout names appear in URL paths.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// resolve returns the name of the page a device page refers to: the layout's
// "<layout>/<page>" if pages has it, else the shared "<page>".
func (d Device) resolve(pages map[string]render.PageInfo, page string) (string, bool) {
	if name := d.Layout + "/" + page; hasPage(pages, name) {
		return name, true
	}
	// A slash would reach into another layout.
	if !strings.Contains(page, "/") && hasPage(pages, page) {
		return page, true
	}
	return "", false
}

func hasPage(pages map[string]render.PageInfo, name string) bool {
	_, ok := pages[name]
	return ok
}

// pages returns the device's pages as page names, resolved in pages.
func (d Device) pages(pages map[string]render.PageInfo) []string {
	out := make([]string, len(d.Pages))
	for i, p := range d.Pages {
		out[i], _ = d.resolve(pages, p)
	}
	return out
}

// params returns Params keyed by page name and key, "<name>.<key>".
func (d Device) params(pages map[string]render.PageInfo) map[string]map[string]any {
	out := make(map[string]map[string]any, len(d.Params))
	for ref, v := range d.Params {
		page, key, _ := strings.Cut(ref, ".")
		name, _ := d.resolve(pages, page)
		out[name+"."+key] = v
	}
	return out
}

// parseDevices checks devices and returns them with normalized MACs.
func parseDevices(devices map[string]Device) (map[string]Device, error) {
	out := make(map[string]Device, len(devices))
	macs := map[string]string{}
	for name, d := range devices {
		if !deviceName.MatchString(name) {
			return nil, fmt.Errorf("devices.%s: name may only contain letters, digits, - and _", name)
		}
		if name == "page" {
			// Taken by the admin route /preview/page/{page}.png.
			return nil, fmt.Errorf("devices.page: the name page is reserved")
		}
		mac, err := device.NormalizeMAC(d.MAC)
		if err != nil {
			return nil, fmt.Errorf("devices.%s: %w", name, err)
		}
		if other, ok := macs[mac]; ok {
			return nil, fmt.Errorf("devices.%s: mac %s already used by %s", name, mac, other)
		}
		macs[mac] = name
		d.MAC = mac
		switch d.Rotation {
		case 0, 90, 180, 270:
		default:
			return nil, fmt.Errorf("devices.%s: rotation must be 0, 90, 180 or 270", name)
		}
		if !deviceName.MatchString(d.Layout) {
			return nil, fmt.Errorf("devices.%s: layout is required and may only contain letters, digits, - and _", name)
		}
		if len(d.Pages) == 0 {
			return nil, fmt.Errorf("devices.%s: pages is empty", name)
		}
		switch d.Power {
		case "", device.PowerAuto, device.PowerUSB, device.PowerBattery:
		default:
			return nil, fmt.Errorf("devices.%s: power must be auto, usb or battery", name)
		}
		out[name] = d
	}
	return out, nil
}
