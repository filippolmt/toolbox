package configedit

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// UserMountNames returns the name of every named entry in path's mounts:
// list — the candidate set for remove-time suggestions. A missing file
// yields an empty list.
func UserMountNames(path string) ([]string, error) {
	var doc struct {
		Mounts []struct {
			Name string `yaml:"name"`
		} `yaml:"mounts"`
	}
	if err := readYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Mounts))
	for _, m := range doc.Mounts {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// UserShells reads the shells: block of one config file (name → path) —
// the candidate set for remove-time existence checks and suggestions. A
// missing file yields an empty map.
func UserShells(path string) (map[string]string, error) {
	var doc struct {
		Shells map[string]struct {
			Path string `yaml:"path"`
		} `yaml:"shells"`
	}
	if err := readYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(doc.Shells))
	for n, s := range doc.Shells {
		out[n] = s.Path
	}
	return out, nil
}

// readYAMLFile is the shared single-file reader behind UserMountNames /
// UserShells: missing file decodes as the zero value, anything else
// unmarshals into out.
func readYAMLFile(path string, out any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
