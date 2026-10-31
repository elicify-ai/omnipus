// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"errors"
	"os"
	"path/filepath"
	"regexp/syntax"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// WriteAuthority is the current source of every agent write authority the
// AD-4 writer exclusion must see. It is read at every check (each launch and
// each registration), so configuration reloads and newly approved grants
// apply without a restart.
type WriteAuthority struct {
	// Config returns the current configuration (nil when unknown).
	Config func() *config.Config
	// PathGrants returns every approved filesystem widening of every
	// session (nil when the gateway records none).
	PathGrants func() []fspolicy.PathGrant
}

// ConfiguredWriteRoots gathers every agent write authority known from the
// home folder, configuration and approved grants, for the AD-4 writer
// exclusion: agent homes and workspace trees under $OMNIPUS_HOME, every
// workspace mount, the operator's write allowances (cfg.Tools.AllowWritePaths,
// every pattern — one Omnipus cannot reduce to a folder disables preview),
// the kernel sandbox's read-write paths (cfg.Sandbox.AllowedPaths), and every
// session's approved write grants. A source that cannot be read fails closed.
// The calling turn's own policy is added per call (TurnWriteRoots).
func ConfiguredWriteRoots(home string, cfg *config.Config, grants []fspolicy.PathGrant) []component.WriteRoot {
	roots := []component.WriteRoot{
		{Path: filepath.Join(home, "agents"), Grant: "agent home folders"},
		{Path: filepath.Join(home, "workspaces"), Grant: "workspace folders"},
	}
	roots = append(roots, mountRoots(home)...)
	if cfg != nil {
		for _, pattern := range cfg.Tools.AllowWritePaths {
			roots = append(roots, allowanceRoot(pattern))
		}
		for _, p := range cfg.Sandbox.AllowedPaths {
			roots = append(roots, sandboxPathRoot(p))
		}
	}
	for _, g := range grants {
		if g.Access&fspolicy.PathGrantAccessWrite != 0 {
			roots = append(roots, component.WriteRoot{Path: g.Path, Grant: "approved write access to " + g.Path})
		}
	}
	return roots
}

func mountRoots(home string) []component.WriteRoot {
	entries, err := os.ReadDir(filepath.Join(home, "entities", "mounts"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []component.WriteRoot{{All: true, Grant: "workspace mounts (the mount list could not be read: " + err.Error() + ")"}}
	}
	var roots []component.WriteRoot
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		// An unreadable mount record grants no write access to any turn
		// either (pkg/workspace::AllowedMountRoots reads the same store), so
		// it adds no authority here.
		mounts, _ := workspace.LoadMounts(home, id)
		for _, m := range mounts {
			roots = append(roots, component.WriteRoot{Path: m.HostPath, Grant: "mount " + m.Name + " of workspace " + id})
		}
	}
	return roots
}

// sandboxPathRoot maps one cfg.Sandbox.AllowedPaths entry (absolute or
// "~/..."; the kernel policy grants it read and write).
func sandboxPathRoot(p string) component.WriteRoot {
	grant := "sandbox allowed path " + p
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return component.WriteRoot{All: true, Grant: grant + " (home folder unknown)"}
		}
		p = filepath.Join(userHome, rest)
	}
	if !filepath.IsAbs(p) {
		return component.WriteRoot{All: true, Grant: grant + " (not an absolute path)"}
	}
	return component.WriteRoot{Path: filepath.Clean(p), Grant: grant}
}

// allowanceRoot maps one write-allowance regex (pkg/tools/filesystem.go::
// matchesAllowedPath: an unanchored regexp match on the cleaned path, plus
// the literal-root rule) to the authority it grants:
//   - "^<literal>(?:/|$)" (or the Windows separator form): that folder;
//   - "^<literal>..." otherwise: every path whose text starts with the
//     literal part (a quantified last character is not part of it);
//   - anything else (unanchored, alternation at the top, case-insensitive,
//     no absolute literal start, unparsable): All — preview stays off.
func allowanceRoot(pattern string) component.WriteRoot {
	grant := "operator write allowance " + pattern
	for _, suffix := range []string{"(?:/|$)", `(?:\\|$)`} {
		if body, ok := strings.CutSuffix(pattern, suffix); ok {
			if root, literal := literalPrefix(body); literal {
				return component.WriteRoot{Path: root, Grant: grant}
			}
		}
	}
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return component.WriteRoot{All: true, Grant: grant + " (not a valid pattern)"}
	}
	re = re.Simplify()
	var start *syntax.Regexp
	switch {
	case re.Op == syntax.OpConcat && len(re.Sub) >= 2 && re.Sub[0].Op == syntax.OpBeginText:
		start = re.Sub[1]
	default:
		return component.WriteRoot{All: true, Grant: grant + " (Omnipus cannot tell which folders it covers)"}
	}
	if start.Op != syntax.OpLiteral || start.Flags&syntax.FoldCase != 0 {
		return component.WriteRoot{All: true, Grant: grant + " (Omnipus cannot tell which folders it covers)"}
	}
	lit := string(start.Rune)
	if !filepath.IsAbs(lit) {
		return component.WriteRoot{All: true, Grant: grant + " (does not start with an absolute folder)"}
	}
	return component.WriteRoot{Path: lit, Prefix: true, Grant: grant}
}

// literalPrefix extracts the folder a "^<literal>" pattern body names: any
// unescaped regex operator means the body is not a literal.
func literalPrefix(pattern string) (string, bool) {
	raw, ok := strings.CutPrefix(pattern, "^")
	if !ok {
		return "", false
	}
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
