// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Skills & Tools C-1, C-15: the stored levels decide what each turn
// advertises. One authority (ToolSurface) answers, per tool, whether its
// definition is sent, which manifest line lists it and whether ToolSearch
// can find it. A home the tool-loading conversion has not reached yet (no
// sandbox.tool_visibility stored) keeps the pre-conversion tiers until the
// boot upgrade writes the levels.

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ToolSurface is one turn's stored tool levels. Levels is nil before the
// conversion (legacy tiers apply).
type ToolSurface struct {
	Levels map[string]string
}

// ReadToolSurface reads the stored tool levels under home.
func ReadToolSurface(home string) (ToolSurface, error) {
	v, err := config.ReadPersistedVisibility(home)
	if err != nil {
		return ToolSurface{}, err
	}
	return ToolSurface{Levels: v.Tools}, nil
}

// Leveled reports whether stored levels drive this surface.
func (s ToolSurface) Leveled() bool { return s.Levels != nil }

// LevelOf returns the stored level of name ("" before the conversion).
func (s ToolSurface) LevelOf(name string) string {
	if s.Levels == nil {
		return ""
	}
	return config.ResolveToolVisibility(name, s.Levels)
}

// TierOf returns the manifest tier name has on this surface: ToolSearch is
// infrastructure; a full level is sent every turn; every other level is
// loaded on demand.
func (s ToolSurface) TierOf(name string) ManifestTier {
	if s.Levels == nil {
		return ToolManifestTier(name)
	}
	if slices.Contains(InfraManifestToolNames(), name) {
		return ManifestInfra
	}
	if s.LevelOf(name) == config.VisibilityFull {
		return ManifestFull
	}
	return ManifestLazy
}

// Searchable reports whether ToolSearch finds a core tool on this surface:
// every tool whose level is not full, ToolSearch excluded (C-15).
func (s ToolSurface) Searchable(name string) bool {
	return s.TierOf(name) == ManifestLazy
}

// Stamp is a short fingerprint of the stored levels ("" before the
// conversion), for caches keyed by what the levels say.
func (s ToolSurface) Stamp() string {
	if s.Levels == nil {
		return ""
	}
	keys := make([]string, 0, len(s.Levels))
	for k := range s.Levels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := fnv.New64a()
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s=%s\n", k, s.Levels[k])
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// maxLevelDescriptionRunes is the C-1 cap of a name_and_description line.
const maxLevelDescriptionRunes = 140

// LevelManifestLine renders one tool's manifest line at its level (C-1):
// name and the first line of its description cut to 140 characters at
// name_and_description (the name alone when the description is empty);
// the name alone at name_only; "" at hidden and full (full is sent as a
// definition, hidden is not listed).
func LevelManifestLine(level, name, description string) string {
	switch level {
	case config.VisibilityNameOnly:
		return name
	case config.VisibilityNameAndDescription:
		line := description
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return name
		}
		if r := []rune(line); len(r) > maxLevelDescriptionRunes {
			line = string(r[:maxLevelDescriptionRunes]) + "..."
		}
		return name + " — " + line
	}
	return ""
}

// LevelPlan is what one turn does with one tool on this surface.
type LevelPlan struct {
	InRequest    bool   // the definition is sent every turn
	ManifestLine string // the More tools line when not sent ("" = none)
	Searchable   bool   // ToolSearch finds and loads it
}

// Plan returns the turn plan of one tool (C-1): denied is the effective
// deny of the tool for the agent, which overrides every level.
func (s ToolSurface) Plan(name, description string, denied bool) LevelPlan {
	if denied {
		return LevelPlan{}
	}
	switch s.TierOf(name) {
	case ManifestFull, ManifestInfra:
		return LevelPlan{InRequest: true}
	}
	return LevelPlan{
		ManifestLine: LevelManifestLine(s.LevelOf(name), name, description),
		Searchable:   true,
	}
}

// BuildManifest renders the "More tools" block for this surface: the
// pre-conversion tiers through BuildCompressedManifest, else one line per
// not-loaded tool at name_only or name_and_description, grouped by
// category. "" when nothing is listed.
func (s ToolSurface) BuildManifest(available []Tool, loaded map[string]bool) string {
	if s.Levels == nil {
		return BuildCompressedManifest(available, loaded)
	}
	grouped := make(map[ToolCategory][]string)
	for _, t := range available {
		n := t.Name()
		if loaded[n] {
			continue
		}
		line := s.Plan(n, t.Description(), false).ManifestLine
		if line == "" {
			continue
		}
		grouped[t.Category()] = append(grouped[t.Category()], line)
	}
	if len(grouped) == 0 {
		return ""
	}
	cats := make([]string, 0, len(grouped))
	for c := range grouped {
		cats = append(cats, string(c))
	}
	sort.Strings(cats)
	var sb strings.Builder
	sb.WriteString(manifestHeader)
	for _, cat := range cats {
		lines := grouped[ToolCategory(cat)]
		sort.Strings(lines)
		fmt.Fprintf(&sb, "\n## %s\n", cat)
		for _, l := range lines {
			fmt.Fprintf(&sb, "  - %s\n", l)
		}
	}
	return sb.String()
}
