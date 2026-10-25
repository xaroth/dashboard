// Package yamlconfig decodes the modules.<name> sections of a YAML config
// file for module.Load.
package yamlconfig

import (
	"bytes"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
)

// Sections returns a module.Config per section. A section must be a
// mapping, or empty ("weather:"), which means defaults.
func Sections(nodes map[string]yaml.Node) (map[string]module.Config, error) {
	out := make(map[string]module.Config, len(nodes))
	for name, node := range nodes {
		cfg, err := section(node)
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", name, err)
		}
		out[name] = cfg
	}
	return out, nil
}

// Parse is Sections of src, a mapping of sections; for tests.
func Parse(src []byte) (map[string]module.Config, error) {
	var nodes map[string]yaml.Node
	if err := yaml.Unmarshal(src, &nodes); err != nil {
		return nil, err
	}
	return Sections(nodes)
}

func section(node yaml.Node) (module.Config, error) {
	var cfg module.Config
	if node.Kind == 0 || (node.Kind == yaml.ScalarNode && node.Tag == "!!null") {
		return cfg, nil
	}
	if node.Kind != yaml.MappingNode {
		return cfg, errors.New("must be a mapping")
	}
	rest := node
	rest.Content = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Value == "schedule" {
			cfg.Schedule = func(s *module.Schedule) error { return strictDecode(v, s) }
			continue
		}
		rest.Content = append(rest.Content, k, v)
	}
	cfg.Decode = func(v any) error { return strictDecode(&rest, v) }
	return cfg, nil
}

// strictDecode decodes n into v, rejecting unknown keys; Node.Decode cannot.
func strictDecode(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
}
