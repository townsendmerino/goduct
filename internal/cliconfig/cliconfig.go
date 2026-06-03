// Package cliconfig loads goduct.json and exposes the parsed Config
// to cmd/goduct. Per ADR 0038: stdlib encoding/json with
// DisallowUnknownFields (a typo'd key is loud-fail per ADR 0007),
// no upward discovery, and no plumbing into the analyzer — the
// CLI overlays config values onto its flagset and stamps ir.API.Meta
// after analysis.
package cliconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// DefaultFilename is the file name auto-discovered relative to cwd
// when --config is not given (ADR 0038 §2).
const DefaultFilename = "goduct.json"

// Config mirrors the CLI flag set 1:1 plus the `openapi` block.
// Pointer fields (e.g. *bool) distinguish "key absent in JSON" from
// "key explicitly set to false"; the precedence overlay needs that
// distinction to know whether the config supplied a value.
type Config struct {
	Pattern    *string           `json:"pattern,omitempty"`
	Out        *string           `json:"out,omitempty"`
	Dir        *string           `json:"dir,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Tests      *bool             `json:"tests,omitempty"`
	Watch      *bool             `json:"watch,omitempty"`
	Framework  *string           `json:"framework,omitempty"`
	All        *bool             `json:"all,omitempty"`
	Generators []string          `json:"generators,omitempty"`
	Adapters   map[string]string `json:"adapters,omitempty"`
	OpenAPI    *OpenAPI          `json:"openapi,omitempty"`
}

// OpenAPI is the project-metadata block consumed by the openapi
// generator (ADR 0038 §5). Empty/nil fields fall back to the
// generator's built-in defaults (package name; "0.0.0"; no
// description; no servers).
type OpenAPI struct {
	Title       string   `json:"title,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Servers     []string `json:"servers,omitempty"`
}

// Load reads cfgPath and returns the parsed Config. When cfgPath is
// "", Load auto-discovers DefaultFilename in cwd: present → load;
// absent → return (nil, nil) so the caller runs flag-only. An
// explicit cfgPath that doesn't exist is an error, not a silent
// no-op (ADR 0038 §2).
func Load(cfgPath string) (*Config, error) {
	autoDiscovered := false
	if cfgPath == "" {
		cfgPath = DefaultFilename
		autoDiscovered = true
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if autoDiscovered && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cliconfig: read %s: %w", cfgPath, err)
	}
	return Parse(raw, cfgPath)
}

// Parse decodes a goduct.json byte slice with DisallowUnknownFields.
// pathForErr is woven into error messages so users see "goduct.json"
// (or their --config path) in the diagnostic. Exported because tests
// drive it directly from in-memory fixtures.
func Parse(raw []byte, pathForErr string) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cliconfig: parse %s: %w", pathForErr, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("cliconfig: parse %s: trailing data after JSON object", pathForErr)
	}
	return &cfg, nil
}
