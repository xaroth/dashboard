// Package screen holds what device and render share: who a page is rendered
// for, the device's latest wake, and the finished image.
package screen

import "time"

// Target is who a page is rendered for: a device, or a panel in a preview.
type Target struct {
	Name     string
	Model    string
	Rotation int
	Wake     *Wake
	// Params overrides query params by "page.key", merged over the page's own.
	Params map[string]map[string]any
}

// Wake is what the device reported on its latest request.
type Wake struct {
	Source   string    `json:"source,omitempty"`
	Battery  *float64  `json:"battery,omitempty"`
	RSSI     *int      `json:"rssi,omitempty"`
	Firmware string    `json:"firmware,omitempty"`
	At       time.Time `json:"at"`
	// USB is whether the device counts as on USB power, decided by PowerSource.
	USB         bool        `json:"usb"`
	PowerSource PowerSource `json:"powerSource"`
}

// PowerSource is what decided Wake.USB.
type PowerSource string

const (
	// PowerSourceConfig is the device's power setting.
	PowerSourceConfig PowerSource = "config"
	// PowerSourceHeader is the USB-Connected header.
	PowerSourceHeader PowerSource = "header"
	// PowerSourceDefault is battery, for a device on auto without the header.
	PowerSourceDefault PowerSource = "default"
)

// PowerSources is every PowerSource; gentypes emits them as a union.
var PowerSources = []PowerSource{PowerSourceConfig, PowerSourceHeader, PowerSourceDefault}

// Image is a finished render.
type Image struct {
	PNG      []byte
	Filename string
}
