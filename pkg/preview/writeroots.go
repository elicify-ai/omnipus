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

// ConfiguredWriteRoots gathers the agent write authorities Omnipus can see
// from the home folder, configuration and recorded grants, for the AD-4
// writer exclusion: agent homes and workspace trees under $OMNIPUS_HOME,
// every workspace mount, the operator's write allowances
// (cfg.Tools.AllowWritePaths; a pattern Omnipus cannot reduce to folders
// disables preview), the kernel sandbox's read-write paths
// (cfg.Sandbox.AllowedPaths), and every session's recorded write grants. A
// source that cannot be read — including an unknown (nil) configuration and
// a single unreadable or partly invalid mount record — fails closed with an
// All root. The calling turn's own policy is added per call (TurnWriteRoots).
func ConfiguredWriteRoots(home string, cfg *config.Config, grants []fspolicy.PathGrant) []component.WriteRoot {
	roots := []component.WriteRoot{
		{Path: filepath.Join(home, "agents"), Grant: "agent home folders"},
		{Path: filepath.Join(home, "workspaces"), Grant: "workspace folders"},
	}
	roots = append(roots, mountRoots(home)...)
	if cfg == nil {
		roots = append(roots, component.WriteRoot{All: true, Grant: "agent write settings (the configuration is not known)"})
	} else {
		for _, pattern := range cfg.Tools.AllowWritePaths {
			roots = append(roots, allowanceRoots(pattern)...)
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
		// A record that cannot be read now (or whose entries were partly
		// dropped) may still have granted write access to a turn that is
		// running: "no new grants" is not "no writers", so it fails closed.
		mounts, readable, dropped := workspace.LoadMountsWithDropStatus(home, id)
		if !readable {
			roots = append(roots, component.WriteRoot{All: true, Grant: "mounts of workspace " + id + " (the mount record could not be read)"})
			continue
		}
		if dropped {
			roots = append(roots, component.WriteRoot{All: true, Grant: "mounts of workspace " + id + " (the mount record has invalid entries)"})
		}
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

// allowanceRoots maps one write-allowance regex to folders that contain
// every path the file tool lets it write (pkg/tools/filesystem.go::
// matchesAllowedPath: an unanchored regexp match on the cleaned path, OR the
// tool's own literal-root rule). Both halves are covered:
//   - the regexp itself, reduced from its parsed form (regexp/syntax, the
//     parser regexp.Compile uses) — "^<literal>(?:/|$)" is that folder; any
//     other "^<literal>..." is the folder holding the literal's last
//     separator (a text prefix also matches sibling names); anything else
//     (unanchored, top-level alternation, case-insensitive, a non-literal
//     start, a relative start, unparsable) is All — preview stays off;
//   - the tool's literal-root rule (toolLiteralRoot), which reads escapes
//     differently from the regexp ("\d" is the letter d there, a digit in
//     the regexp), so it can name a different folder.
func allowanceRoots(pattern string) []component.WriteRoot {
	grant := "operator write allowance " + pattern
	folder, reason := regexFolder(pattern)
	if reason != "" {
		return []component.WriteRoot{{All: true, Grant: grant + " (" + reason + ")"}}
	}
	roots := []component.WriteRoot{{Path: folder, Grant: grant}}
	if lit, ok := toolLiteralRoot(pattern); ok && lit != folder {
		roots = append(roots, component.WriteRoot{Path: lit, Grant: grant})
	}
	return roots
}

// regexFolder reduces a pattern to one folder containing every string the
// pattern matches (as an unanchored search on a cleaned absolute path), or
// returns why it cannot.
func regexFolder(pattern string) (folder, reason string) {
	const unknown = "Omnipus cannot tell which folders it covers"
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", "not a valid pattern"
	}
	re = re.Simplify()
	if re.Op != syntax.OpConcat || len(re.Sub) < 2 || re.Sub[0].Op != syntax.OpBeginText {
		return "", unknown
	}
	start := re.Sub[1]
	if start.Op != syntax.OpLiteral || start.Flags&syntax.FoldCase != 0 {
		return "", unknown
	}
	lit := string(start.Rune)
	if !filepath.IsAbs(lit) {
		return "", "does not start with an absolute folder"
	}
	if len(re.Sub) == 3 && isFolderEnd(re.Sub[2]) {
		// "^<lit>(?:/|$)": <lit> itself or anything below it.
		return filepath.Clean(lit), ""
	}
	// Every match starts with the text <lit>, so it lies in the folder
	// holding <lit>'s last separator ("^/srv/data" also matches
	// "/srv/data2/...": the folder is /srv).
	cut := strings.LastIndexAny(lit, "/"+string(filepath.Separator))
	dir := lit[:cut+1]
	if !filepath.IsAbs(dir) {
		return "", "does not start with an absolute folder"
	}
	return filepath.Clean(dir), ""
}

// isFolderEnd reports whether re is exactly "(?:<sep>|$)" for a separator a
// cleaned path on this platform can contain.
func isFolderEnd(re *syntax.Regexp) bool {
	if re.Op != syntax.OpAlternate || len(re.Sub) != 2 {
		return false
	}
	sep, end := false, false
	for _, sub := range re.Sub {
		switch {
		case sub.Op == syntax.OpEndText:
			end = true
		case sub.Op == syntax.OpLiteral && sub.Flags&syntax.FoldCase == 0 && len(sub.Rune) == 1 &&
			(sub.Rune[0] == '/' || sub.Rune[0] == filepath.Separator):
			sep = true
		}
	}
	return sep && end
}

// toolLiteralRoot is the file tool's literal-root rule, reproduced exactly
// (pkg/tools/filesystem.go::extractAllowedPathRoot,
// ::containsUnescapedRegexMeta, ::unescapeRegexLiteral; pkg/tools imports
// this package, so it cannot be called directly). The tool grants
// everything at or below the returned folder.
func toolLiteralRoot(pattern string) (string, bool) {
	literal, ok := strings.CutPrefix(pattern, "^")
	if !ok {
		return "", false
	}
	literal = strings.TrimSuffix(literal, "(?:/|$)")
	literal = strings.TrimSuffix(literal, `(?:\\|$)`)
	var b strings.Builder
	escaped := false
	for _, r := range literal {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '|':
			return "", false
		}
		b.WriteRune(r)
	}
	unescaped := b.String()
	if escaped || unescaped == "" || !filepath.IsAbs(unescaped) {
		return "", false
	}
	return filepath.Clean(unescaped), true
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
