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

// Config is the service configuration, decoded from the YAML file.
type Config struct {
	Listen    Listen               `yaml:"listen"`
	PublicURL string               `yaml:"public_url"`
	Timezone  string               `yaml:"timezone"`
	DataDir   string               `yaml:"data_dir"`
	Schedule  Schedule             `yaml:"schedule"`
	Renderer  RendererConfig       `yaml:"renderer"`
	Modules   map[string]yaml.Node `yaml:"modules"`
	Devices   map[string]Device    `yaml:"devices"`
}

type Listen struct {
	Device string `yaml:"device"`
	Admin  string `yaml:"admin"`
}

// Schedule is when devices wake: every Interval from FirstWake up to and
// including LastWake.
type Schedule struct {
	FirstWake     string        `yaml:"first_wake"`
	LastWake      string        `yaml:"last_wake"`
	Interval      time.Duration `yaml:"interval"`
	DetailTimeout time.Duration `yaml:"detail_timeout"`
}

// RendererConfig is how the Bun render process is started.
type RendererConfig struct {
	Bun string `yaml:"bun"`
	// Dir holds renderer/, pages/ and node_modules.
	Dir string `yaml:"dir"`
	// Watch rebuilds pages on change, for development.
	Watch bool `yaml:"watch"`
}

// DefaultConfig returns the values used for keys missing from the file.
func DefaultConfig() Config {
	return Config{
		Listen:   Listen{Device: ":8080", Admin: "127.0.0.1:8081"},
		Timezone: "Europe/Amsterdam",
		DataDir:  "/data",
		Schedule: Schedule{FirstWake: "07:00", LastWake: "00:00", Interval: time.Hour, DetailTimeout: 5 * time.Minute},
		Renderer: RendererConfig{Bun: "bun", Dir: "."},
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
	if i := c.Schedule.Interval; i < time.Minute || i > 24*time.Hour || i%time.Minute != 0 {
		return fmt.Errorf("schedule: interval must be whole minutes from 1m to 24h")
	}
	if c.Schedule.DetailTimeout <= 0 {
		return fmt.Errorf("schedule: detail_timeout must be positive")
	}
	return validateDevices(c.Devices)
}

// device converts the schedule for the device package. Call after Validate.
func (s Schedule) device(loc *time.Location) device.Schedule {
	return device.Schedule{
		First:    sinceMidnight(s.FirstWake),
		Last:     sinceMidnight(s.LastWake),
		Interval: s.Interval,
		Detail:   s.DetailTimeout,
		Location: loc,
	}
}

func sinceMidnight(hhmm string) time.Duration {
	t, _ := time.Parse("15:04", hhmm)
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// loadConfig reads the YAML file at path, substitutes ${VAR} references from
// the environment and validates the result.
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
			// Let the substituted value resolve its own type, so ${PORT} can be an int.
			n.Value, n.Tag, n.Style = v, "", 0
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
	cfg := DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decoding config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func walkScalars(n *yaml.Node, fn func(*yaml.Node)) {
	if n.Kind == yaml.ScalarNode {
		fn(n)
	}
	for _, c := range n.Content {
		walkScalars(c, fn)
	}
}
