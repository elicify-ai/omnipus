//go:build darwin || linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// t11RootIsWritable is the spec's own effective-access oracle for §3.1
// condition 5, computed in the test — not read off the guard.
//
// Spec: "On Unix this uses the effective-access check from
// golang.org/x/sys/unix." AT_EACCESS is that check (effective uid/gid, not
// the real ids). Nil means the effective user can write the filesystem root,
// so the stand-alone-literal rule is off. EACCES means it cannot, so the
// rule is on. Any other error is an instrument failure, not "not writable".
func t11RootIsWritable(t *testing.T) bool {
	t.Helper()
	err := unix.Faccessat(unix.AT_FDCWD, "/", unix.W_OK, unix.AT_EACCESS)
	if err == nil {
		return true
	}
	// EACCES: the effective user is not allowed to write. EROFS: the filesystem
	// itself rejects the write (macOS sealed system volume returns this for "/").
	// Both mean condition 5 holds — the effective user cannot write the root —
	// so the stand-alone rule stays on. Any other error is an instrument failure.
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EROFS) {
		return false
	}
	t.Fatalf("effective-access check unix.Faccessat(\"/\", W_OK, AT_EACCESS) = %v; "+
		"spec §3.1 condition 5 requires this check, so the test will not guess writability", err)
	return false
}
