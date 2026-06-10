// Package config carries the in-tree default models.yaml, embedded into
// the binary so a bare `frugal` — npm wrapper, MCP-registry install,
// GUI-spawned process with no shell environment — starts with the
// free-first routing defaults instead of dying on a missing file.
//
// This is the same file the curl installer drops at
// ~/.frugal/config/models.yaml; internal/config.LoadAuto prefers an
// on-disk copy when one exists so operator edits keep winning.
package config

import _ "embed"

// DefaultModelsYAML is the embedded copy of config/models.yaml.
//
//go:embed models.yaml
var DefaultModelsYAML []byte
