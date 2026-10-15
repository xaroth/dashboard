package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/service"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// loadConfig reads the YAML file at path, substitutes ${VAR} references from
// the environment and validates the result.
func loadConfig(path string) (service.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return service.Config{}, err
	}
	return parseConfig(raw, os.LookupEnv)
}

func parseConfig(raw []byte, lookup func(string) (string, bool)) (service.Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return service.Config{}, fmt.Errorf("parsing config: %w", err)
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
		return service.Config{}, fmt.Errorf("environment variables not set: %v", missing)
	}

	// Re-encode so the decoder can reject unknown keys; Node.Decode cannot.
	expanded, err := yaml.Marshal(&root)
	if err != nil {
		return service.Config{}, fmt.Errorf("encoding config: %w", err)
	}
	cfg := service.DefaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return service.Config{}, fmt.Errorf("decoding config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return service.Config{}, err
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
