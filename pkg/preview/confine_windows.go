// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package preview

// officeFontFolders is empty: Windows has no file confinement (Q5), so no
// folder needs a rule.
var officeFontFolders []string

// hooksFor: Windows confines the programs with the launcher's Job Object
// only (Q5: no file confinement). That is the documented platform boundary,
// not a failed sandbox set-up, so no "converted without sandbox" disclosure
// is attached (AD-7 limits the disclosure to macOS and Linux).
func hooksFor(confinement) spawnHooks {
	return spawnHooks{}
}
