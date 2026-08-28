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
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
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

// TestResolveWorkspaceIDForContinuation_NoSession_ReturnsEmpty exercises the
// real, fixed production function (loop.go) for the "legitimate empty" case
// that must remain silent and unchanged: an inbound message carrying a
// SessionID that resolves to no session anywhere resolves to "" via the
// existing metadata/channel fallbacks.
func TestResolveWorkspaceIDForContinuation_NoSession_ReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: "session-that-does-not-exist",
		Channel:   "telegram",
		ChatID:    "999",
	})
	if got != "" {
		t.Fatalf("expected empty workspace id for a nonexistent session, got %q", got)
	}
}

// TestResolveWorkspaceIDForContinuation_SessionWithWorkspace_ReturnsIt proves
// the surviving happy path still works: a real session whose meta carries a
// WorkspaceID is resolved and returned.
func TestResolveWorkspaceIDForContinuation_SessionWithWorkspace_ReturnsIt(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	meta, err := store.NewChannelSession("telegram", "telegram", "888", "mia", "test session")
	if err != nil {
		t.Fatalf("NewChannelSession: %v", err)
	}
	wantWorkspace := "workspace-xyz"
	if setErr := store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wantWorkspace}); setErr != nil {
		t.Fatalf("SetMeta: %v", setErr)
	}

	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: meta.ID,
		Channel:   "telegram",
		ChatID:    "888",
	})
	if got != wantWorkspace {
		t.Fatalf("expected workspace id %q, got %q", wantWorkspace, got)
	}
}

// TestResolveSessionStore_CorruptMeta_ReturnsStoreNotNil is the fix's core
// regression test. Before the fix, ResolveSessionStore accepted a probed
// store only on GetMeta returning err == nil, so a corrupt-but-present
// session took the IDENTICAL path as "this session lives in a different
// store (or nowhere)" — and after exhausting every probe the function
// returned nil, indistinguishable from a session that never existed. The fix
// returns the owning store immediately (after logging its own WARN) whenever
// the error is anything other than os.ErrNotExist. Uses a completely fresh
// UnifiedStore (via a freshly constructed AgentLoop) with a hand-corrupted
// meta.json written directly to disk, mirroring
// TestUnifiedStoreGetMeta_CorruptMetaJSON_ReturnsNonNotExistError above.
func TestResolveSessionStore_CorruptMeta_ReturnsStoreNotNil(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "resolve-corrupt.log")

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	const sessionID = "corrupt-meta-resolve-test"
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	// Same hand-corrupted file shape as the GetMeta-level test above —
	// never went through writeMetaLocked's atomic-write path.
	metaPath := filepath.Join(sessionDir, "meta.json")
	if writeErr := os.WriteFile(metaPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}

	got := al.ResolveSessionStore(sessionID)
	if got == nil {
		t.Fatal("ResolveSessionStore must return the owning store for a corrupt-meta session, not nil — " +
			"corruption is not the same thing as 'session does not exist'")
	}
	if got != store {
		t.Fatalf("expected the shared store to be returned, got a different store (BaseDir=%q)", got.BaseDir())
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if !strings.Contains(logged, "ResolveSessionStore") {
		t.Errorf("log file missing the ResolveSessionStore WARN marker; got:\n%s", logged)
	}
	if !strings.Contains(logged, sessionID) {
		t.Errorf("log file missing the session id %q; got:\n%s", sessionID, logged)
	}
	if !strings.Contains(logged, store.BaseDir()) {
		t.Errorf("log file missing the owning store's BaseDir %q; got:\n%s", store.BaseDir(), logged)
	}
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Errorf("log file missing the warn level; got:\n%s", logged)
	}
}

// TestResolveSessionStore_MissingSession_StaysSilent is the control test:
// a session that genuinely does not exist anywhere (the frequent, legitimate
// case — e.g. a brand-new channel conversation with no session yet) must
// keep resolving to nil with NO warning logged. Over-correcting into a WARN
// on every not-yet-created session would be worse than the bug this fix
// closes.
func TestResolveSessionStore_MissingSession_StaysSilent(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "resolve-missing.log")

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	got := al.ResolveSessionStore("session-that-truly-does-not-exist-anywhere")
	if got != nil {
		t.Fatalf("expected nil for a genuinely missing session, got a store (BaseDir=%q)", got.BaseDir())
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	if strings.Contains(logged, "ResolveSessionStore") {
		t.Errorf("expected NO WARN for a legitimately missing session (unchanged behavior), but got:\n%s", logged)
	}
}

// TestResolveWorkspaceIDForContinuation_CorruptMeta_WarnsDownstream is the
// reachability proof the earlier fix pass could not produce (see this file's
// header comment): with ResolveSessionStore now returning the owning store
// for a corrupt-meta session instead of nil, resolveWorkspaceIDForContinuation
// gets a non-nil store, re-reads GetMeta itself, hits the identical
// non-ErrNotExist error, and its own "continuation: could not read session
// meta while resolving workspace; workspace unresolved" WARN (loop.go, ~3145)
// actually fires. Also asserts the function still returns "" rather than
// fabricating a workspace id — the corruption must be surfaced, not papered
// over with a guessed value.
func TestResolveWorkspaceIDForContinuation_CorruptMeta_WarnsDownstream(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "continuation-corrupt.log")

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "scripted-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), testutil.NewScenario())
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("expected a shared session store on a freshly constructed AgentLoop")
	}

	const sessionID = "corrupt-meta-continuation-test"
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	if mkErr := os.MkdirAll(sessionDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	metaPath := filepath.Join(sessionDir, "meta.json")
	if writeErr := os.WriteFile(metaPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}

	// Channel/ChatID chosen so no other fallback (channel-instance binding,
	// inbound metadata) can accidentally produce a non-empty workspace id —
	// this test isolates the corrupt-meta path specifically.
	got := al.resolveWorkspaceIDForContinuation(bus.InboundMessage{
		SessionID: sessionID,
		Channel:   "telegram",
		ChatID:    "no-such-instance-binding",
	})
	if got != "" {
		t.Fatalf("expected empty workspace id (no fabricated fallback) when session meta is corrupt, got %q", got)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)
	const wantMarker = "continuation: could not read session meta while resolving workspace; workspace unresolved"
	if !strings.Contains(logged, wantMarker) {
		t.Errorf(
			"downstream WARN not reached — this is exactly the reachability regression the fix closes; log:\n%s",
			logged,
		)
	}
	if !strings.Contains(logged, sessionID) {
		t.Errorf("log file missing the session id %q; got:\n%s", sessionID, logged)
	}
}
