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
