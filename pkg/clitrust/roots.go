// Package clitrust decides whether a program path typed by a person may be
// started as a third-party CLI (T11 spec docs/internal/specs/
// t11-documents-and-3p-agents-spec.md §3.2). Callers run CheckProgramPath
// against AgentWritableRoots before starting anything, and launch only the
// resolved file it returns.
package clitrust

import (
	"log/slog"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// AgentWritableRoots returns the folders agents can write and a candidate
// program must therefore not live in or be linked through: the Omnipus home,
// every workspace folder and every workspace mount root, plus any agent
// working folder cfg configures outside the home. Empty entries are skipped.
//
// A workspace listing failure is logged and the home (which contains every
// workspace folder) still covers the workspaces themselves; only mount roots
// of unlisted workspaces could be missed, which the log names.
func AgentWritableRoots(home string, cfg *config.Config) []string {
	var roots []string
	add := func(p string) {
		if p == "" {
			return
		}
		roots = append(roots, filepath.Clean(p))
	}
	add(home)
	if cfg != nil {
		add(cfg.Agents.Defaults.Home)
		for _, a := range cfg.Agents.List {
			add(a.Home)
		}
	}
	if home == "" {
		return roots
	}
	ids, err := workspace.ListWorkspaceIDs(home)
	if err != nil {
		slog.Warn("clitrust: workspace listing failed; mount roots of unlisted workspaces are not in the forbidden set",
			"home", home, "error", err)
		return roots
	}
	for _, id := range ids {
		dir, derr := workspace.SafeWorkspaceDir(home, id)
		if derr != nil {
			slog.Warn("clitrust: skipping workspace with an unsafe id", "id", id, "error", derr)
			continue
		}
		add(dir)
		for _, m := range workspace.AllowedMountRoots(home, id) {
			add(m)
		}
	}
	return roots
}
