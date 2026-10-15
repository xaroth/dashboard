package service

import (
	"fmt"
	"net/url"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the service configuration, decoded from the YAML file.
type Config struct {
	Listen    Listen               `yaml:"listen"`
	PublicURL string               `yaml:"public_url"`
	Timezone  string               `yaml:"timezone"`
	DataDir   string               `yaml:"data_dir"`
	Schedule  Schedule             `yaml:"schedule"`
	Modules   map[string]yaml.Node `yaml:"modules"`
	Devices   map[string]Device    `yaml:"devices"`
}

type Listen struct {
	Device string `yaml:"device"`
	Admin  string `yaml:"admin"`
}

type Schedule struct {
	FirstWake     string        `yaml:"first_wake"`
	LastWake      string        `yaml:"last_wake"`
	DetailTimeout time.Duration `yaml:"detail_timeout"`
}

type Device struct {
	MAC      string   `yaml:"mac"`
	Rotation int      `yaml:"rotation"`
	Pages    []string `yaml:"pages"`
	// Params overrides query params per page data key, e.g. "overview.events".
	Params map[string]map[string]any `yaml:"params"`
}

// DefaultConfig returns the values used for keys missing from the file.
func DefaultConfig() Config {
	return Config{
		Listen:   Listen{Device: ":8080", Admin: "127.0.0.1:8081"},
		Timezone: "Europe/Amsterdam",
		DataDir:  "/data",
		Schedule: Schedule{FirstWake: "07:00", LastWake: "00:00", DetailTimeout: 5 * time.Minute},
	}
}

// Validate reports the first configuration error.
func (c *Config) Validate() error {
	if c.PublicURL == "" {
		return fmt.Errorf("public_url is required")
	}
	if u, err := url.Parse(c.PublicURL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("public_url %q is not an absolute URL", c.PublicURL)
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	for _, s := range []string{c.Schedule.FirstWake, c.Schedule.LastWake} {
		if _, err := time.Parse("15:04", s); err != nil {
			return fmt.Errorf("schedule: %q is not HH:MM", s)
		}
	}
	macs := map[string]string{}
	for name, d := range c.Devices {
		mac, err := NormalizeMAC(d.MAC)
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
