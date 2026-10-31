// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ConfiguredWriteRoots gathers every agent write root known from the home
// folder and configuration, for the AD-4 writer exclusion: agent homes and
// workspace trees under $OMNIPUS_HOME, every workspace mount, and the
// operator's write allowances (cfg.Tools.AllowWritePaths, literal-prefix
// patterns). cfg may be nil. Per-turn grants are checked per call from the
// calling turn's policy (TurnWriteRoots).
func ConfiguredWriteRoots(home string, cfg *config.Config) []component.WriteRoot {
	roots := []component.WriteRoot{
		{Path: filepath.Join(home, "agents"), Grant: "agent home folders"},
		{Path: filepath.Join(home, "workspaces"), Grant: "workspace folders"},
	}
	if entries, err := os.ReadDir(filepath.Join(home, "entities", "mounts")); err == nil {
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || e.IsDir() {
				continue
			}
			mounts, _ := workspace.LoadMounts(home, id)
			for _, m := range mounts {
				roots = append(roots, component.WriteRoot{Path: m.HostPath, Grant: "mount " + m.Name + " of workspace " + id})
			}
		}
	}
	if cfg != nil {
		for _, pattern := range cfg.Tools.AllowWritePaths {
			if root, ok := literalPrefix(pattern); ok {
				roots = append(roots, component.WriteRoot{Path: root, Grant: "operator write allowance " + pattern})
			}
		}
	}
	return roots
}

// TurnWriteRoots lists the calling turn's write roots: its work folder,
// mounts, writable allowances and write path grants.
func TurnWriteRoots(p fspolicy.FSPolicy) []component.WriteRoot {
	var roots []component.WriteRoot
	if p.WorkDir != "" {
		roots = append(roots, component.WriteRoot{Path: p.WorkDir, Grant: "this agent's work folder"})
	}
	for _, r := range p.AllowedRoots {
		roots = append(roots, component.WriteRoot{Path: r, Grant: "mount " + r})
	}
	for _, a := range p.HomeGuard.Allow {
		if a.Writable {
			roots = append(roots, component.WriteRoot{Path: a.Path, Grant: "write allowance " + a.Path})
		}
	}
	for _, g := range p.PathGrants {
		if g.Access&fspolicy.PathGrantAccessWrite != 0 {
			roots = append(roots, component.WriteRoot{Path: g.Path, Grant: "approved write access to " + g.Path})
		}
	}
	return roots
}

// literalPrefix extracts the directory a "^<literal>" or "^<literal>(?:/|$)"
// allowance pattern names, with the same acceptance rule as
// pkg/tools/filesystem.go::extractAllowedPathRoot: any unescaped regex
// operator left after the anchors means the pattern is not a literal root
// and is skipped.
func literalPrefix(pattern string) (string, bool) {
	raw, ok := strings.CutPrefix(pattern, "^")
	if !ok {
		return "", false
	}
	raw = strings.TrimSuffix(raw, "(?:/|$)")
	raw = strings.TrimSuffix(raw, `(?:\\|$)`)
	var b strings.Builder
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if strings.ContainsRune(".*+?()|[]{}^$", r) {
			return "", false
		}
		b.WriteRune(r)
	}
	lit := b.String()
	if escaped || lit == "" || !filepath.IsAbs(lit) {
		return "", false
	}
	return filepath.Clean(lit), true
}
