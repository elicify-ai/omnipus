// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test
// 28.
//
// Oracle: US-7 AS-2 / FD-2 ("Given a trashed view is restored, When the
// restore lands and its name collides with an existing view, Then FD-2's
// auto-rename applies... the restored copy gets a unique suffixed name, not
// a rejection").
//
// BLOCKED: (*Trasher).Restore has no per-file view-identity rewrite step —
// same root cause as TDD tests 27/29/52 (handleLibraryTransfer/
// handleLibraryUpload/library.CopyInto), and the planned shared helper
// (pkg/records::RewriteCopiedViewIdentity, D-DUPLICATE) does not exist
// anywhere (`grep -rn "RewriteCopiedViewIdentity" pkg/` — zero matches).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryRestore_AutoRenamesCollidingViewName$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

func TestLibraryRestore_AutoRenamesCollidingViewName(t *testing.T) {
	t.Fatal("BLOCKED: (*Trasher).Restore has no per-file view-identity rewrite step, and the planned " +
		"shared helper (pkg/records::RewriteCopiedViewIdentity, D-DUPLICATE) does not exist — " +
		"required before restoring a trashed view whose name now collides can be shown to " +
		"auto-suffix rather than reject, per FD-2 / Dataset G-2 / TDD test 28.")
}
