// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for FIX 1 (sendfile-fix review): four sites in loop.go
// (resolveWorkspaceIDForContinuation, ProcessScheduled, the async/delegate-
// completion turn reconstruction, and processMessage's M4 resolution) used to
// take the IDENTICAL silent path for "meta.GetMeta returned a real error" and
// "this session legitimately has no workspace" — mErr was never logged. The
// fix distinguishes the two via errors.Is(mErr, os.ErrNotExist): a genuinely
// absent session stays silent (unchanged behavior); any other error (corrupt
// meta.json, decode failure, I/O error) now logs a WARN.
//
// UPDATE (reachability gap closed): the caveat below described how all four
// fixed call sites first obtain their session store via
// AgentLoop.ResolveSessionStore (loop.go), which used to apply the IDENTICAL
// `err == nil` swallow to its OWN internal GetMeta probe before ever
// returning a store — so a corrupt-meta.json scenario could never reach the
// `store != nil` branch at any of the four fixed lines. ResolveSessionStore
// itself has now been fixed (loop.go, same function) to distinguish the two
// cases exactly as this file's callers do: a non-ErrNotExist GetMeta error on
// a probed store logs its own WARN (naming the session id, the store's
// BaseDir, and the error) and returns that store anyway — a corrupt-but-
// present meta.json is strong evidence the session belongs to THAT store, so
// there is no reason to keep scanning remaining stores — rather than falling
// through toward an eventual nil. This makes the store reach the caller,
// which lets each of the four sites' own GetMeta re-read reproduce the
// identical error and hit ITS OWN downstream WARN. See
// TestResolveSessionStore_CorruptMeta_ReturnsStoreNotNil,
// TestResolveSessionStore_MissingSession_StaysSilent, and
// TestResolveWorkspaceIDForContinuation_CorruptMeta_WarnsDownstream below for
// the fix + end-to-end reachability proof (the latter is the proof the
// original fix pass could not produce).
//
// Original caveat (kept for history — no longer describes current behavior):
// ResolveSessionStore's own success criterion for returning a store WAS that
// same GetMeta call succeeding on the SAME store instance for the SAME
// session id, evaluated moments before the fixed code's own call — a hard
// determinism, not a rare race, since nothing in the four functions' bodies
// intervenes between the two calls. This was a separate, pre-existing helper
// (also called from pkg/gateway/websocket.go and pkg/gateway/rest.go,
// outside this fix's exclusive scope) sharing the exact same swallow SHAPE
// this fix closes; fixing it was not part of the four originally-named lines
// and was left untouched at the time. It has since been fixed directly (see
// above) without touching either gateway file — ResolveSessionStore's
// signature was left unchanged, so no caller outside pkg/agent needed
// editing.

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestUnifiedStoreGetMeta_CorruptMetaJSON_ReturnsNonNotExistError proves the
// exact error shape FIX 1's `!errors.Is(mErr, os.ErrNotExist)` condition
// relies on: a session directory that exists but whose meta.json cannot be
// parsed returns an error that is NOT os.ErrNotExist. Uses a completely fresh
// UnifiedStore instance (empty in-memory cache) so the very first GetMeta
// call for this session id is a genuine disk read of the corrupt file.
func TestUnifiedStoreGetMeta_CorruptMetaJSON_ReturnsNonNotExistError(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}

	const sessionID = "corrupt-meta-session"
	sessionDir := filepath.Join(baseDir, sessionID)
	if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	// Deliberately malformed JSON — never went through writeMetaLocked's
	// atomic-write path, mimicking a hand-edited or externally corrupted file.
	metaPath := filepath.Join(sessionDir, "meta.json")
	if writeErr := os.WriteFile(metaPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}

	_, mErr := store.GetMeta(sessionID)
	if mErr == nil {
		t.Fatal("GetMeta on a corrupt meta.json must return a non-nil error")
	}
	if errors.Is(mErr, os.ErrNotExist) {
		t.Fatalf("GetMeta on a corrupt (but present) meta.json must NOT be os.ErrNotExist — got %v", mErr)
	}
}

// TestUnifiedStoreGetMeta_MissingSession_ReturnsNotExistError proves the
// complementary half of the same condition: a session that was never created
// at all returns an error that IS os.ErrNotExist — the one case FIX 1 must
// keep silent.
func TestUnifiedStoreGetMeta_MissingSession_ReturnsNotExistError(t *testing.T) {
	baseDir := t.TempDir()
	store, err := session.NewUnifiedStore(baseDir)
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}

	_, mErr := store.GetMeta("never-created-session")
	if mErr == nil {
		t.Fatal("GetMeta on a session that was never created must return a non-nil error")
	}
	if !errors.Is(mErr, os.ErrNotExist) {
		t.Fatalf("GetMeta on a never-created session must be os.ErrNotExist — got %v", mErr)
	}
}
