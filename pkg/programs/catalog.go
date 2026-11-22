// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// presetFiles are the shipped presets, one JSON data file per preset
// (FR-001: adding a preset is adding a data file, no Go change).
//
//go:embed presets/*.json
var presetFiles embed.FS

// Presets returns the presets shipped in the binary, ordered by id.
func Presets() ([]ProgramEntry, error) {
	sub, err := fs.Sub(presetFiles, "presets")
	if err != nil {
		return nil, fmt.Errorf("programs: open embedded presets: %w", err)
	}
	return CatalogFromFS(sub)
}

// CatalogFromFS loads the preset catalog from the *.json data files at the
// root of fsys. Every file must be a complete, valid preset: kind "preset",
// a unique id and the same field rules as a custom entry. Entries are
// returned ordered by id.
func CatalogFromFS(fsys fs.FS) ([]ProgramEntry, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, fmt.Errorf("programs: list presets: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("programs: the preset catalog is empty")
	}
	seen := map[string]bool{}
	out := make([]ProgramEntry, 0, len(names))
	for _, name := range names {
		e, err := loadPreset(fsys, name)
		if err != nil {
			return nil, err
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("programs: preset %s: duplicate id %q", name, e.ID)
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadPreset(fsys fs.FS, name string) (ProgramEntry, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return ProgramEntry{}, fmt.Errorf("programs: read preset %s: %w", name, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var e ProgramEntry
	if err := dec.Decode(&e); err != nil {
		return ProgramEntry{}, fmt.Errorf("programs: parse preset %s: %w", name, err)
	}
	if e.Kind != KindPreset {
		return ProgramEntry{}, fmt.Errorf("programs: preset %s: kind must be %q", name, KindPreset)
	}
	if e.ID == "" || e.ID != strings.TrimSuffix(path.Base(name), ".json") {
		return ProgramEntry{}, fmt.Errorf("programs: preset %s: id must equal the file name", name)
	}
	req := CustomRequest{
		DisplayName: e.DisplayName, ProgramWords: e.ProgramWords, TokenVariables: e.TokenVariables,
		NetworkSites: e.NetworkSites, Git: e.Git, IdentityCheck: e.IdentityCheck,
	}
	if err := validateFields(&req); err != nil {
		return ProgramEntry{}, fmt.Errorf("programs: preset %s: %w", name, err)
	}
	e.Git, e.IdentityCheck = req.Git, req.IdentityCheck
	if e.NetworkSites == nil {
		e.NetworkSites = []string{}
	}
	return e, nil
}
