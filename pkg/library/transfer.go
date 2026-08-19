// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// Delete removes rel (already-cleaned, non-root). A directory is removed
// recursively with everything under it.
func (r *Root) Delete(rel string) error {
	if rel == "" {
		return ErrInvalidPath
	}
	// Refuse the mount's own entry BEFORE resolving: resolving it yields the
	// mount's root at ".", and the RemoveAll below would then empty the
	// operator's real folder. See ErrIsMountRoot.
	if r.isMountRootEntry(rel) {
		return ErrIsMountRoot
	}
	rt, sub := r.resolve(rel)
	fi, err := rt.Stat(sub)
	if err != nil {
		return translateErr(err)
	}
	if fi.IsDir() {
		if err := rt.RemoveAll(sub); err != nil {
			return fmt.Errorf("library: remove directory: %w", err)
		}
		return nil
	}
	if err := rt.Remove(sub); err != nil {
		return fmt.Errorf("library: remove file: %w", err)
	}
	return nil
}

// Rename renames/moves fromRel to toRel WITHIN this single Root (same
// workspace) — the same-workspace sugar renameLibraryEntry offers over
// POST /library/move. A no-op rename (fromRel == toRel after cleaning) is
// treated as an idempotent success rather than a spurious "already exists"
// conflict. Returns ErrNotFound if nothing exists at fromRel,
// ErrAlreadyExists if something already exists at toRel, or ErrNotDir if
// toRel's parent is not a directory.
func (r *Root) Rename(fromRel, toRel string) (os.FileInfo, error) {
	if fromRel == "" || toRel == "" {
		return nil, ErrInvalidPath
	}
	if fromRel == toRel {
		rtF0, subF0 := r.resolve(fromRel)
		fi, err := rtF0.Stat(subF0)
		if err != nil {
			return nil, translateErr(err)
		}
		return fi, nil
	}
	// A mounted folder's own entry is not an ordinary directory: its name is
	// held in the mount record, so renaming the symlink alone would leave the
	// two disagreeing, and renaming ONTO it would write through into the
	// operator's real folder. Both directions refused. See ErrIsMountRoot.
	if r.isMountRootEntry(fromRel) || r.isMountRootEntry(toRel) {
		return nil, ErrIsMountRoot
	}
	// Source and destination must live in the same root for a rename to be
	// expressible at all — see ErrCrossRootTransfer. Checked BEFORE any
	// filesystem work so the caller gets the real reason rather than a
	// containment error from whichever root it happened to be attempted in.
	if !r.sameRoot(fromRel, toRel) {
		return nil, ErrCrossRootTransfer
	}
	rtFrom, subFrom := r.resolve(fromRel)
	if _, err := rtFrom.Stat(subFrom); err != nil {
		return nil, translateErr(err)
	}
	// Case-insensitive collision backstop (see caseInsensitiveMatch's doc)
	// is the SOLE existence check for toRel, not a fallback reached only
	// after an exact-case Stat "miss". FIX (real data-loss/false-rejection
	// bug, not a test-platform assumption): on a genuinely case-insensitive
	// filesystem (macOS's default APFS, Windows/NTFS-typical) an exact-name
	// r.root.Stat(toRel) SUCCEEDS by case-folding onto a DIFFERENTLY-CASED
	// existing entry — indistinguishable from a true exact match by return
	// value alone. The previous version trusted that success as "toRel
	// already exists" and returned ErrAlreadyExists immediately, which
	// short-circuited PAST this backstop's sameEntry special case below —
	// rejecting a legitimate case-only relabel ("Report.txt" ->
	// "report.txt") with a spurious conflict before ever checking whether
	// the "existing" entry found IS the file being renamed. Confirmed by
	// the Cross-Platform CI matrix (macos-latest, arm64)'s
	// TestLibraryRename_CaseOnlyRelabel_Allowed failure. A directory
	// listing name scan (caseInsensitiveMatch) already subsumes an
	// exact-case existence check — an exact match is trivially fold-equal —
	// so no separate Stat(toRel) is needed to decide existence at all; the
	// sameEntry classification below (a) the very file being renamed — a
	// legitimate case-only relabel, which the OS's own Rename call already
	// handles correctly on every platform Omnipus targets (NTFS and
	// APFS/HFS+ rename a name to a different case of itself in place; ext4
	// sees two genuinely distinct names and performs an ordinary rename) —
	// or (b) some OTHER file, which must be rejected everywhere Omnipus
	// runs, not only on filesystems that happen to enforce it natively.
	toDir := dirParent(toRel)
	if _, found, ciErr := r.caseInsensitiveMatch(toDir, path.Base(toRel)); ciErr != nil {
		return nil, ciErr
	} else if found {
		sameEntry := dirParent(fromRel) == toDir && pathsafe.SameName(path.Base(fromRel), path.Base(toRel))
		if !sameEntry {
			return nil, ErrAlreadyExists
		}
	}
	if err := r.requireParentDir(toRel); err != nil {
		return nil, err
	}
	rtTo, subTo := r.resolve(toRel)
	if err := rtTo.Rename(subFrom, subTo); err != nil {
		return nil, fmt.Errorf("library: rename: %w", err)
	}
	fi, err := rtTo.Stat(subTo)
	if err != nil {
		return nil, fmt.Errorf("library: stat renamed entry: %w", err)
	}
	return fi, nil
}

// CreateUnique creates a new, exclusive file at rel, or — if rel already
// exists — at a name with a numeric " (N)" suffix inserted before the
// extension, mirroring the exact de-duplication convention
// pkg/gateway/rest.go's chat-upload dual-write (stageWorkspaceUploadCopy)
// already uses, so an upload behaves identically whether it lands via chat
// or via the Library. Uses O_CREATE|O_EXCL in a retry loop rather than
// Stat-then-Create, closing the TOCTOU window between checking a name is
// free and claiming it.
//
// Each candidate is ALSO checked against its siblings case-insensitively
// (see caseInsensitiveMatch's doc) before the O_CREATE|O_EXCL attempt: an
// exact-case create can succeed even when a differently-cased sibling
// already occupies this "slot" on a case-sensitive host (e.g. uploading
// "report.txt" once "Report.txt" already exists), which would number two
// files identically here but collide the instant this same workspace is
// opened on Windows or default macOS. Checking first keeps the numbering
// — and which name ultimately wins — identical regardless of host OS.
func (r *Root) CreateUnique(rel string) (finalRel string, f *os.File, err error) {
	dir := dirParent(rel)
	base := path.Base(rel)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	const maxAttempts = 1000
	candidate := rel
	candidateBase := base
	for attempt := 0; ; attempt++ {
		if _, found, ciErr := r.caseInsensitiveMatch(dir, candidateBase); ciErr != nil {
			return "", nil, ciErr
		} else if !found {
			rtC, subC := r.resolve(candidate)
			file, openErr := rtC.OpenFile(subC, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if openErr == nil {
				return candidate, file, nil
			}
			if !os.IsExist(openErr) {
				return "", nil, translateErr(openErr)
			}
			// Exact-case race with a concurrent writer: fall through and
			// bump the suffix exactly as the case-insensitive-collision
			// path already does below.
		}
		if attempt >= maxAttempts {
			return "", nil, fmt.Errorf("library: too many filename collisions for %q", base)
		}
		candidateBase = fmt.Sprintf("%s (%d)%s", stem, attempt+1, ext)
		if dir == "" {
			candidate = candidateBase
		} else {
			candidate = dir + "/" + candidateBase
		}
	}
}

// CopyInto copies the file or directory at fromRel (within fromRoot) to
// toRel (within toRoot), recursively for a directory, leaving the source in
// place. toRoot may be the same *Root as fromRoot (same-workspace copy) or a
// different one (cross-workspace) — this function does not care either way,
// it always performs an explicit read-then-write rather than relying on any
// same-filesystem shortcut. Returns ErrAlreadyExists if something already
// exists at toRel, ErrNotFound if nothing exists at fromRel, or ErrNotDir if
// toRel's parent is not a directory.
func CopyInto(fromRoot, toRoot *Root, fromRel, toRel string) (os.FileInfo, error) {
	if fromRel == "" || toRel == "" {
		return nil, ErrInvalidPath
	}
	// Resolve BOTH sides through their own Root: either may sit inside a mount,
	// in which case the real source/destination is that mount's os.Root rather
	// than the work tree's. Copying is expressible across roots (unlike rename),
	// so no cross-root refusal belongs here.
	srcRt, srcSub := fromRoot.resolve(fromRel)
	dstRt, dstSub := toRoot.resolve(toRel)

	// Copying ONTO a mount's own entry would write through into the operator's
	// real folder under a name they never chose; copying FROM it means copying
	// the whole mounted tree, which is a duplication of their real data that no
	// caller asked for by naming a folder.
	if fromRoot.isMountRootEntry(fromRel) || toRoot.isMountRootEntry(toRel) {
		return nil, ErrIsMountRoot
	}

	srcInfo, err := srcRt.Stat(srcSub)
	if err != nil {
		return nil, translateErr(err)
	}
	if _, statErr := dstRt.Stat(dstSub); statErr == nil {
		return nil, ErrAlreadyExists
	} else if !os.IsNotExist(statErr) {
		return nil, translateErr(statErr)
	}
	// Case-insensitive collision backstop (see caseInsensitiveMatch's doc).
	// Unlike Rename, a Copy has no legitimate "same entry" exception: the
	// whole point of copying is to end up with two distinct filesystem
	// entries, so ANY case-insensitive sibling match at the destination is
	// a real collision — even when fromRoot == toRoot and the match
	// happens to literally BE the source (e.g. copying "Report.txt" onto
	// "report.txt" in the same directory), which would silently clobber
	// the very file being copied on a case-insensitive filesystem.
	if _, found, ciErr := toRoot.caseInsensitiveMatch(dirParent(toRel), path.Base(toRel)); ciErr != nil {
		return nil, ciErr
	} else if found {
		return nil, ErrAlreadyExists
	}
	if parentErr := toRoot.requireParentDir(toRel); parentErr != nil {
		return nil, parentErr
	}

	if srcInfo.IsDir() {
		if copyErr := copyDirRecursive(srcRt, dstRt, srcSub, dstSub); copyErr != nil {
			return nil, copyErr
		}
	} else {
		if copyErr := copyFile(srcRt, dstRt, srcSub, dstSub, srcInfo.Mode()); copyErr != nil {
			return nil, copyErr
		}
	}
	fi, err := dstRt.Stat(dstSub)
	if err != nil {
		return nil, fmt.Errorf("library: stat copy destination: %w", err)
	}
	return fi, nil
}

// errSourceCleanupFailed wraps a MoveInto failure that occurs AFTER the
// destination copy already succeeded — the move is not fully complete (the
// source still exists too), but nothing was lost. Callers/tests can
// errors.Is against this to distinguish "copy itself failed" (nothing
// written) from "copy succeeded, source cleanup failed" (a duplicate now
// exists, surfaced as an error rather than silently reported as a clean
// success).
var errSourceCleanupFailed = errors.New("library: move succeeded but removing the source failed")

// MoveInto moves the file or directory at fromRel (within fromRoot) to
// toRel (within toRoot). When fromRoot and toRoot are the SAME *Root
// (same-workspace transfer — REST callers only ever open one Root per
// workspace id per request, so pointer equality here reliably means "same
// workspace"), this delegates to the OS-atomic Root.Rename. Otherwise it
// performs a CopyInto followed by removing the source — os.Root cannot
// atomically rename across two independently-opened roots even when they
// happen to sit on the same physical disk. A cleanup failure after a
// successful cross-workspace copy is reported as an error (wrapping
// errSourceCleanupFailed) rather than silently swallowed or rolled back:
// the data is safe (now present at the destination), but the operation did
// not fully complete, and an operator should know a duplicate was left
// behind rather than have it silently reported as a clean success.
func MoveInto(fromRoot, toRoot *Root, fromRel, toRel string) (os.FileInfo, error) {
	// Pointer equality alone is no longer enough to mean "one atomic rename will
	// do". A single *Root now holds several os.Roots (the work tree plus one per
	// mount), so the same workspace can still be a CROSS-root move — work tree
	// into a mounted folder is exactly the case the Transfer dialog exists for.
	// Ask whether the two paths land in the same os.Root, not whether they came
	// from the same wrapper; otherwise this delegates to Rename, which correctly
	// refuses cross-root, and a legitimate move fails.
	if fromRoot == toRoot && fromRoot.sameRoot(fromRel, toRel) {
		return fromRoot.Rename(fromRel, toRel)
	}
	fi, err := CopyInto(fromRoot, toRoot, fromRel, toRel)
	if err != nil {
		return nil, err
	}
	if delErr := fromRoot.Delete(fromRel); delErr != nil {
		return fi, fmt.Errorf("%w: %v", errSourceCleanupFailed, delErr)
	}
	return fi, nil
}

func copyFile(fromRoot, toRoot *os.Root, fromRel, toRel string, mode os.FileMode) error {
	src, err := fromRoot.Open(fromRel)
	if err != nil {
		return translateErr(err)
	}
	defer src.Close()

	perm := mode.Perm()
	if perm == 0 {
		perm = 0o600
	}
	dst, err := toRoot.OpenFile(toRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("library: create copy destination: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		removeQuietRoot(toRoot, toRel)
		return fmt.Errorf("library: copy file contents: %w", err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		removeQuietRoot(toRoot, toRel)
		return fmt.Errorf("library: sync copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		removeQuietRoot(toRoot, toRel)
		return fmt.Errorf("library: close copy destination: %w", err)
	}
	return nil
}

func copyDirRecursive(fromRoot, toRoot *os.Root, fromRel, toRel string) error {
	if err := toRoot.MkdirAll(toRel, 0o700); err != nil {
		return fmt.Errorf("library: create destination directory: %w", err)
	}
	entries, err := fs.ReadDir(fromRoot.FS(), fromRel)
	if err != nil {
		return fmt.Errorf("library: read source directory: %w", err)
	}
	for _, e := range entries {
		childFrom := fromRel + "/" + e.Name()
		childTo := toRel + "/" + e.Name()
		if e.IsDir() {
			if err := copyDirRecursive(fromRoot, toRoot, childFrom, childTo); err != nil {
				return err
			}
			continue
		}
		mode := os.FileMode(0o600)
		if info, infoErr := e.Info(); infoErr == nil {
			mode = info.Mode()
		}
		if err := copyFile(fromRoot, toRoot, childFrom, childTo, mode); err != nil {
			return err
		}
	}
	return nil
}

// removeQuietRoot best-effort removes a partially-written copy destination
// after a mid-copy failure. Logged rather than silently discarded, but
// never overrides the caller's already-in-flight error.
func removeQuietRoot(root *os.Root, rel string) {
	if err := root.Remove(rel); err != nil && !os.IsNotExist(err) {
		logger.WarnCF("library", "failed to remove partial copy destination after error",
			map[string]any{"path": rel, "error": err.Error()})
	}
}
