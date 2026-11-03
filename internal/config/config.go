// Package config loads archdrift.json, the optional per-repo configuration.
// It is JSON rather than YAML because the stdlib has no YAML parser and the
// project keeps the Anthropic SDK as its only dependency.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/stmic168/arch-drift/internal/codemodel"
	"github.com/stmic168/arch-drift/internal/drift"
)

// FileName is the default config file at the repo root.
const FileName = "archdrift.json"

// Config is the file format.
type Config struct {
	// Components map directories to component IDs. A path matches itself and
	// everything below it; the longest matching path wins.
	Components []struct {
		ID    string   `json:"id"`
		Paths []string `json:"paths"`
	} `json:"components"`
	// External maps import-path prefixes to ext:* IDs, checked before the built-in catalog.
	External []struct {
		ID       string   `json:"id"`
		Prefixes []string `json:"prefixes"`
	} `json:"external"`
	// Exclude lists directories to skip entirely (generated code, tools, ...).
	Exclude []string `json:"exclude"`
	// Shared lists directories of shared code inside component directories
	// (protocol types, small helpers): no component, and imports of them
	// create no edge.
	Shared []string `json:"shared"`
	// SharedProtocolPackages treats generated protobuf packages (directory
	// name ending in "pb") as shared. Defaults to true.
	SharedProtocolPackages *bool `json:"shared_protocol_packages"`
	// DefaultLayout keeps the cmd/x, internal/x, pkg/x rule for directories no
	// configured component matches. Defaults to true.
	DefaultLayout *bool `json:"default_layout"`
	// UndocumentedComponents controls UNDOCUMENTED_COMPONENT findings.
	UndocumentedComponents struct {
		Fail   bool     `json:"fail"`   // fail the check (default: list only)
		Ignore []string `json:"ignore"` // component ID patterns to skip, e.g. "pkg/ext*"
	} `json:"undocumented_components"`
	// StrictProtocols: a runtime protocol on a relation counts only when the
	// doc quote names it; protocols the model inferred are ignored and the
	// relation is checked by imports. Recommended for rules docs.
	StrictProtocols bool `json:"strict_protocols"`
	// UndocumentedEdges controls UNDOCUMENTED_EDGE findings.
	UndocumentedEdges struct {
		// Fail the check (default true). Set false for overview docs that
		// state few relations: the edges are then listed but do not fail.
		Fail *bool `json:"fail"`
	} `json:"undocumented_edges"`
	// Transitive lets import paths through other components count.
	Transitive struct {
		Required  bool `json:"required"`
		Forbidden bool `json:"forbidden"`
	} `json:"transitive"`
}

// Load reads and validates a config file.
func Load(p string) (*Config, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", p, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for i, comp := range c.Components {
		switch {
		case comp.ID == "":
			return fmt.Errorf("components[%d]: empty id", i)
		case strings.HasPrefix(comp.ID, "ext:"):
			return fmt.Errorf("components[%d]: id %q must not start with ext: (use external)", i, comp.ID)
		case len(comp.Paths) == 0:
			return fmt.Errorf("components[%d] %q: no paths", i, comp.ID)
		case seen[comp.ID]:
			return fmt.Errorf("components[%d]: duplicate id %q", i, comp.ID)
		}
		seen[comp.ID] = true
		for _, p := range comp.Paths {
			if strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Trim(p, "/") == "" {
				return fmt.Errorf("components[%d] %q: path %q must be relative to the repo root", i, comp.ID, p)
			}
		}
	}
	for i, e := range c.External {
		if !strings.HasPrefix(e.ID, "ext:") || len(e.ID) == 4 {
			return fmt.Errorf("external[%d]: id %q must look like ext:<name>", i, e.ID)
		}
		if len(e.Prefixes) == 0 {
			return fmt.Errorf("external[%d] %q: no prefixes", i, e.ID)
		}
	}
	for i, p := range c.UndocumentedComponents.Ignore {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("undocumented_components.ignore[%d]: bad pattern %q", i, p)
		}
	}
	for i, p := range c.Exclude {
		if strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Trim(p, "/") == "" {
			return fmt.Errorf("exclude[%d]: path %q must be relative to the repo root", i, p)
		}
	}
	for i, p := range c.Shared {
		if strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.Trim(p, "/") == "" {
			return fmt.Errorf("shared[%d]: path %q must be relative to the repo root", i, p)
		}
	}
	return nil
}

// ScanOptions converts the config for the scanner.
func (c *Config) ScanOptions() codemodel.Options {
	var o codemodel.Options
	for _, comp := range c.Components {
		o.Components = append(o.Components, codemodel.ComponentRule{ID: comp.ID, Paths: comp.Paths})
	}
	for _, e := range c.External {
		o.External = append(o.External, codemodel.ExternalRule{ID: e.ID, Prefixes: e.Prefixes})
	}
	o.Exclude = c.Exclude
	o.NoDefaultLayout = c.DefaultLayout != nil && !*c.DefaultLayout
	o.Shared = c.Shared
	o.NoSharedProtocol = c.SharedProtocolPackages != nil && !*c.SharedProtocolPackages
	return o
}

// DiffOptions converts the config for the diff.
func (c *Config) DiffOptions() drift.Options {
	return drift.Options{
		TransitiveRequired:         c.Transitive.Required,
		TransitiveForbidden:        c.Transitive.Forbidden,
		FailUndocumentedComponents: c.UndocumentedComponents.Fail,
		IgnoreUndocumented:         c.UndocumentedComponents.Ignore,
		ReportUndocumentedEdges:    c.UndocumentedEdges.Fail != nil && !*c.UndocumentedEdges.Fail,
		StrictProtocols:            c.StrictProtocols,
	}
}
