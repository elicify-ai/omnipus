// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"io/fs"
	"os"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// caseInsensitiveMatch scans dir's direct children (dir == "" means the
// work-tree root) for an entry whose name pathsafe.SameName considers
// equal to baseName, returning the ACTUAL on-disk name if found.
//
// This is the application-level backstop that makes Library collision
// detection identical regardless of the host OS's filesystem case
// sensitivity: ext4 (the common Linux root filesystem, and what this
// repository's own CI and dev pods run on) is case-sensitive, while NTFS
// and macOS's default APFS/HFS+ are not. An exact-case os.Root.Stat miss
// only proves no entry exists with THIS EXACT casing — it says nothing
// about whether a differently-cased sibling already occupies the same
// "slot" on a filesystem that does not distinguish case. Without this
// scan, a rename/move/copy/mkdir/write that Omnipus allows on a Linux dev
// or CI machine could silently create two files that collide (one
// silently overwriting the other) the instant the identical workspace is
// opened on Windows or a default macOS install — the one gap in this
// whole feature that can actually lose data, since every other check here
// merely rejects a name outright rather than risking an overwrite. See
// each caller (Root.Rename, CopyInto, Root.CreateUnique, Root.Mkdir,
// Root.WriteContent) for how the match is used once found — the
// appropriate response differs (allow a same-entry case-only rename,
// reject a genuine collision, defer to the next de-duplication candidate,
// …), so this helper only reports the fact, it never decides the outcome.
func (r *Root) caseInsensitiveMatch(dir, baseName string) (match string, found bool, err error) {
	name := dir
	if name == "" {
		name = "."
	}
	entries, readErr := fs.ReadDir(r.root.FS(), name)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return "", false, nil
		}
		return "", false, translateErr(readErr)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	match, found = pathsafe.FindFold(names, baseName)
	return match, found, nil
}
