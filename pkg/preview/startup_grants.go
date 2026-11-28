// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"fmt"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// RecordStartupGrants records, for office-preview spec §3.8 BR-2.5, what the
// confined Linux startup rules in policy did with each of grants (the list
// component.ConfinementStartupGrants returned and
// sandbox.DefaultBootPolicyForModel was given): a granted folder with its
// identity now, a refused one with the reason class the sandbox's own rule
// gives (sandbox.ClassifyStartupGrant). Call it after building policy and
// before applying it, so the identities are the ones the rules bind to; then
// pass the record to component.SetStartupGrants once the rules are
// enforcing. notes names every entry that is refused or could not be
// recorded, for the sandbox.component_grants log line.
func RecordStartupGrants(policy sandbox.SandboxPolicy, grants []string) (*component.StartupGrantRecord, []string) {
	rec := &component.StartupGrantRecord{}
	writable := sandbox.WritableRoots(policy)
	var notes []string
	for _, g := range grants {
		if refusal := sandbox.ClassifyStartupGrant(g, writable); refusal.Refused() {
			rec.AddRefused(g, refusal.WritableFolder)
			notes = append(notes, fmt.Sprintf("%s refused: %s", g, refusal.Reason))
			continue
		}
		if !hasStartupGrantRule(policy, g) {
			notes = append(notes, fmt.Sprintf("%s not recorded: the startup rules carry no read+execute rule for it", g))
			continue
		}
		if err := rec.AddGranted(g); err != nil {
			notes = append(notes, fmt.Sprintf("%s not recorded: %v", g, err))
		}
	}
	return rec, notes
}

// hasStartupGrantRule reports whether policy grants exactly read+execute on
// path (the shape sandbox.DefaultBootPolicyForModel gives an accepted grant).
func hasStartupGrantRule(policy sandbox.SandboxPolicy, path string) bool {
	clean := filepath.Clean(path)
	for _, r := range policy.FilesystemRules {
		if r.Path == clean && r.Access == sandbox.AccessRead|sandbox.AccessExecute {
			return true
		}
	}
	return false
}
