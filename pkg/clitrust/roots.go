// Package clitrust decides whether a program path typed by a person may be
// started as a third-party CLI (T11 spec docs/internal/specs/
// t11-documents-and-3p-agents-spec.md §3.2). Callers run CheckProgramPath
// against AgentWritableRoots before starting anything, and launch only the
// resolved file it returns.
package clitrust

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agenthome"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// AgentWritableRoots returns the folders agents can write, which a candidate
// program must therefore not live in or be linked through: the Omnipus home,
// every agent home cfg configures (resolved exactly as the runtime resolves
// it, through agenthome.Resolve), every workspace folder and every workspace
// mount root.
//
// The list is complete or it is an error. A workspace listing that fails, a
// workspace id that is unsafe, or a mount record that cannot be read or that
// had entries dropped is an error, never a shorter list: a missing root would
// silently turn "refuse" into "allow" for whatever it covers. Callers refuse
// the candidate on error.
func AgentWritableRoots(home string, cfg *config.Config) ([]string, error) {
	if strings.TrimSpace(home) == "" {
		return nil, fmt.Errorf("the Omnipus data folder is not known, so the folders agents can write cannot be listed")
	}
	var roots []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		roots = append(roots, filepath.Clean(p))
	}
	// addHome adds a runtime-resolved agent home and, when it differs, its
	// whitespace-trimmed spelling: the runtime trims a per-agent home but uses
	// the default home as written, and other components read the trimmed
	// form, so both spellings are refused (extra roots only ever refuse more).
	addHome := func(p string) {
		add(p)
		if t := strings.TrimSpace(p); t != p {
			add(agenthome.ExpandHome(t))
		}
	}
	add(home)
	if cfg != nil {
		addHome(agenthome.Resolve(nil, &cfg.Agents.Defaults))
		for i := range cfg.Agents.List {
			addHome(agenthome.Resolve(&cfg.Agents.List[i], &cfg.Agents.Defaults))
		}
	}
	ids, err := workspace.ListWorkspaceIDs(home)
	if err != nil {
		return nil, fmt.Errorf("the workspaces could not be listed, so their folders and mounts cannot be excluded: %w", err)
	}
	for _, id := range ids {
		dir, derr := workspace.SafeWorkspaceDir(home, id)
		if derr != nil {
			return nil, fmt.Errorf("workspace %q has an unsafe id, so its folders cannot be excluded: %w", id, derr)
		}
		add(dir)
		mounts, ok, dropped := workspace.LoadMountsWithDropStatus(home, id)
		if !ok || dropped {
			return nil, fmt.Errorf("the mount record of workspace %q could not be read completely, so its mounts cannot be excluded; repair the record and retry (the gateway log names the cause)", id)
		}
		for _, m := range mounts {
			add(m.HostPath)
		}
	}
	return roots, nil
}
