package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/device"
)

// Config is the service configuration, parsed from the YAML file by
// parseConfig.
type Config struct {
	Listen    Listen
	PublicURL string
	Location  *time.Location
	DataDir   string
	// Schedule.Location is Location.
	Schedule device.Schedule
	Renderer RendererConfig
	Modules  map[string]yaml.Node
	Devices  map[string]Device
}

// fileConfig is the YAML file as written.
type fileConfig struct {
	Listen    Listen               `yaml:"listen"`
	PublicURL string               `yaml:"public_url"`
	Timezone  string               `yaml:"timezone"`
	DataDir   string               `yaml:"data_dir"`
	Schedule  fileSchedule         `yaml:"schedule"`
	Renderer  RendererConfig       `yaml:"renderer"`
	Modules   map[string]yaml.Node `yaml:"modules"`
	Devices   map[string]Device    `yaml:"devices"`
}

type Listen struct {
	Device string `yaml:"device"`
	Admin  string `yaml:"admin"`
}

// fileSchedule is when devices wake: every IntervalBattery or IntervalUSB
// from FirstWake up to and including LastWake.
type fileSchedule struct {
	FirstWake       string        `yaml:"first_wake"`
	LastWake        string        `yaml:"last_wake"`
	IntervalBattery time.Duration `yaml:"interval_battery"`
	IntervalUSB     time.Duration `yaml:"interval_usb"`
	DetailTimeout   time.Duration `yaml:"detail_timeout"`
}

// RendererConfig is how the Bun render process is started.
type RendererConfig struct {
	Bun string `yaml:"bun"`
	// Dir holds renderer/, pages/ and node_modules.
	Dir string `yaml:"dir"`
	// Watch rebuilds pages on change, for development.
	Watch bool `yaml:"watch"`
}

// defaults returns the values used for keys missing from the file.
func defaults() fileConfig {
	return fileConfig{
		Listen:   Listen{Device: ":8080", Admin: "127.0.0.1:8081"},
		Timezone: "Europe/Amsterdam",
		DataDir:  "/data",
		Schedule: fileSchedule{FirstWake: "07:00", LastWake: "00:00", IntervalBattery: time.Hour, IntervalUSB: 5 * time.Minute, DetailTimeout: 5 * time.Minute},
		Renderer: RendererConfig{Bun: "bun", Dir: "."},
	}
}

// parse reports the first configuration error or returns the typed config.
func (f fileConfig) parse() (Config, error) {
	if f.PublicURL == "" {
		return Config{}, fmt.Errorf("public_url is required")
	}
	if u, err := url.Parse(f.PublicURL); err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("public_url %q is not an absolute URL", f.PublicURL)
	}
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		return Config{}, fmt.Errorf("timezone: %w", err)
	}
	schedule, err := f.Schedule.parse(loc)
	if err != nil {
		return Config{}, fmt.Errorf("schedule: %w", err)
	}
	devices, err := parseDevices(f.Devices)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Listen:    f.Listen,
		PublicURL: f.PublicURL,
		Location:  loc,
		DataDir:   f.DataDir,
		Schedule:  schedule,
		Renderer:  f.Renderer,
		Modules:   f.Modules,
		Devices:   devices,
	}, nil
}

func (s fileSchedule) parse(loc *time.Location) (device.Schedule, error) {
	first, err := sinceMidnight(s.FirstWake)
	if err != nil {
		return device.Schedule{}, err
	}
	last, err := sinceMidnight(s.LastWake)
	if err != nil {
		return device.Schedule{}, err
	}
	for key, i := range map[string]time.Duration{"interval_battery": s.IntervalBattery, "interval_usb": s.IntervalUSB} {
		if i < time.Minute || i > 24*time.Hour || i%time.Minute != 0 {
			return device.Schedule{}, fmt.Errorf("%s must be whole minutes from 1m to 24h", key)
		}
	}
	if s.DetailTimeout <= 0 {
		return device.Schedule{}, fmt.Errorf("detail_timeout must be positive")
	}
	return device.Schedule{
		First:    first,
		Last:     last,
		Battery:  s.IntervalBattery,
		USB:      s.IntervalUSB,
		Detail:   s.DetailTimeout,
		Location: loc,
	}, nil
}

func sinceMidnight(hhmm string) (time.Duration, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, fmt.Errorf("%q is not HH:MM", hhmm)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// loadConfig reads the YAML file at path, substitutes ${VAR} references from
// the environment and parses the result.
func loadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return parseConfig(raw, os.LookupEnv)
}

func parseConfig(raw []byte, lookup func(string) (string, bool)) (Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	// Substitute in parsed scalars, not raw text, so values with YAML syntax stay values.
	var missing []string
	walkScalars(&root, func(n *yaml.Node) {
		v := envRef.ReplaceAllStringFunc(n.Value, func(ref string) string {
			name := envRef.FindStringSubmatch(ref)[1]
			v, ok := lookup(name)
			if !ok {
				missing = append(missing, name)
			}
			return v
		})
		if v != n.Value {
			// Let the substituted value resolve its own type, so ${PORT} can be an
			// int, but not to null: that would silently keep the default.
			n.Value, n.Tag, n.Style = v, "", 0
			if n.ShortTag() == "!!null" {
				n.Tag = "!!str"
			}
		}
	})
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("environment variables not set: %v", missing)
	}

	// Re-encode so the decoder can reject unknown keys; Node.Decode cannot.
	expanded, err := yaml.Marshal(&root)
	if err != nil {
		return Config{}, fmt.Errorf("encoding config: %w", err)
	}
	file := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return Config{}, fmt.Errorf("decoding config: %w", err)
	}
	return file.parse()
}

func walkScalars(n *yaml.Node, fn func(*yaml.Node)) {
	if n.Kind == yaml.ScalarNode {
		fn(n)
	}
	for _, c := range n.Content {
		walkScalars(c, fn)
	}
}
