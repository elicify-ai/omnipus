// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"strings"
)

// secretBackupPaths lists the existing entries under home whose basename
// matches a secretGlobPrefixes entry.
//
// Split from SecretPaths so the pure, always-correct part of the secret set
// (the exact names) stays free of I/O and cannot be weakened by a filesystem
// error. A listing failure here returns nothing rather than an error, and that
// is deliberate: the exact names are the load-bearing protection and are
// already in the caller's slice by this point. The Linux caller fails the spawn
// on its own listing errors, so an unreadable home still fails closed there.
//
// # This is NOT how the app layer recognises a backup, and must not become so
//
// An enumeration answers "which backups exist right now", which is the wrong
// question for a set that must cover files created later — including by the
// gateway itself, mid-turn. IsCarveOut therefore matches the PREFIXES directly
// (fspolicy.CoversSecretBackup) and does not depend on this listing at all.
//
// What still needs the listing is the two consumers that require a finite path
// LIST rather than a predicate: the kernel deny list (a Seatbelt profile is
// fixed at exec and cannot evaluate a prefix over paths that do not exist) and
// the Linux sibling-granting walk. Both are bounded by their own freshness
// contracts — and the Linux walk additionally applies IsSecretName per
// directory ENTRY, which is prefix-based, so a backup created later is never
// granted there in the first place.
func secretBackupPaths(home string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		for _, prefix := range secretGlobPrefixes {
			if strings.HasPrefix(name, prefix) {
				out = append(out, filepath.Join(home, name))
				break
			}
		}
	}
	return out
}
