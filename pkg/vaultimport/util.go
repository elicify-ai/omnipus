// Omnipus — small shared helpers for the importer.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// kebab lowercases and collapses everything that is not a-z0-9 into a single
// hyphen, trimmed — used to derive a filesystem-safe, human-legible view
// filename from a Base filename and a view's display name.
func kebab(s string) string {
	s = strings.ToLower(s)
	s = reSlugStrip.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// SlugRegistry hands out a globally unique view slug per (base, view name)
// pair, appending a numeric suffix on the rare collision rather than
// silently overwriting one view's file with another's.
type SlugRegistry struct {
	used map[string]int
}

// NewSlugRegistry returns an empty registry.
func NewSlugRegistry() *SlugRegistry {
	return &SlugRegistry{used: map[string]int{}}
}

// Slug returns a unique slug for one base's view.
func (r *SlugRegistry) Slug(baseRelPath, viewName string) string {
	stem := strings.TrimSuffix(filepath.Base(baseRelPath), filepath.Ext(baseRelPath))
	base := kebab(stem) + "--" + kebab(viewName)
	if base == "" {
		base = "view"
	}
	n := r.used[base]
	r.used[base] = n + 1
	if n == 0 {
		return base
	}
	return base + "-" + strconv.Itoa(n+1)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
