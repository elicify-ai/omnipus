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
	// pinned maps "<base rel path>\x00<view name>" to the slug that pair
	// MUST keep — set by a re-derivation (rederive.go) for a view whose file
	// already exists on disk, so re-translating its base cannot move it to a
	// different name and strand the old file. Empty in a whole-vault import,
	// which has nothing to preserve.
	pinned map[string]string
}

// NewSlugRegistry returns an empty registry.
func NewSlugRegistry() *SlugRegistry {
	return &SlugRegistry{used: map[string]int{}}
}

// Slug returns a unique slug for one base's view.
func (r *SlugRegistry) Slug(baseRelPath, viewName string) string {
	if slug, ok := r.pinned[baseRelPath+"\x00"+viewName]; ok {
		return slug
	}
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

// Reserve marks slug as already taken, so a later Slug call for a name that
// would derive it is handed a suffixed variant instead of overwriting
// another view's file. It reconstructs the exact counter state handing that
// slug out would have produced, which is what makes a reserved registry
// behave identically to one that really issued the slug: reserving
// "projects--all" sets the counter to 1 (the next collision suffixes -2),
// and reserving "projects--all-2" sets it to 2 (the next suffixes -3).
//
// A re-derivation reserves every slug the vault already holds that does NOT
// belong to the base being re-derived (RederiveBase, rederive.go).
func (r *SlugRegistry) Reserve(slug string) {
	stem, n := splitSlugSuffix(slug)
	if r.used[stem] < n {
		r.used[stem] = n
	}
}

// Pin fixes the slug one (base, view name) pair must keep, and reserves it
// against every other pair. Used by a re-derivation to preserve the file
// name of a view whose source is the base being re-derived, even when the
// deterministic slug would have differed (an import-time collision left the
// file holding a suffixed name; the operator's view is that file, and
// re-editing its base must not move it).
func (r *SlugRegistry) Pin(baseRelPath, viewName, slug string) {
	if r.pinned == nil {
		r.pinned = map[string]string{}
	}
	r.pinned[baseRelPath+"\x00"+viewName] = slug
	r.Reserve(slug)
}

// splitSlugSuffix splits a slug Slug could have handed out into its stem and
// the ordinal position it occupied (1 for the unsuffixed form, N for a
// "-N" suffix). Anything that is not that shape is reserved whole at
// position 1 — a hand-renamed view file nobody should be silently moved.
func splitSlugSuffix(slug string) (string, int) {
	if i := strings.LastIndex(slug, "-"); i > 0 {
		if n, err := strconv.Atoi(slug[i+1:]); err == nil && n >= 2 {
			return slug[:i], n
		}
	}
	return slug, 1
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
