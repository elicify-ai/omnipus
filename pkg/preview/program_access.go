// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"fmt"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

// RefuseNewProgramAccess checks candidate's entries that are added or changed
// relative to published, in the fields ConfiguredWriteRoots treats as write
// access. An entry that overlaps a program folder, or that Omnipus cannot
// reduce to a folder, is refused with the text the person sees (spec §3.2). An
// unchanged entry was in force already and
// is left to the per-call check and doctor (§3.2 point 4). A nil published
// means nothing is in force, so every entry counts as new.
func RefuseNewProgramAccess(home string, published, candidate *config.Config) error {
	var oldWrite, oldSandbox []string
	if published != nil {
		oldWrite, oldSandbox = published.Tools.AllowWritePaths, published.Sandbox.AllowedPaths
	}
	folders := component.ProtectedFolders(home)
	for _, entry := range candidate.Tools.AllowWritePaths {
		if !slices.Contains(oldWrite, entry) {
			if err := refuseEntry("tools.allow_write_paths", entry, allowanceRoots(entry), folders); err != nil {
				return err
			}
		}
	}
	for _, entry := range candidate.Sandbox.AllowedPaths {
		if !slices.Contains(oldSandbox, entry) {
			if err := refuseEntry("sandbox.allowed_paths", entry, []component.WriteRoot{sandboxPathRoot(entry)}, folders); err != nil {
				return err
			}
		}
	}
	return nil
}

func refuseEntry(field, entry string, roots []component.WriteRoot, folders []component.ProtectedFolder) error {
	for _, root := range roots {
		if root.All {
			return fmt.Errorf("config error: %s entry %q: Omnipus cannot tell which folders it covers, "+
				"so it may include a document preview program folder; write it as ^/<folder>(?:/\\|$).", field, entry)
		}
		if hit := component.FirstProtectedOverlap(folders, root.Path); hit != nil {
			return fmt.Errorf("config error: %s entry %q would give agents write access to the %s program folder %s; "+
				"agents can never get write access there.", field, entry, hit.Name, hit.Path)
		}
	}
	return nil
}
