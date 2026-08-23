// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_sweep_unified_meta_test.go covers a code-review finding on
// PlanEngine.reconcileUnifiedMetaStatus (boot_sweep.go): every SetMeta
// failure used to be logged identically, at Debug, with reassuring "expected"
// wording — collapsing the genuinely-benign "no meta.json at all" case
// (delegate/subturn sessions never get one) together with a corrupted
// meta.json, a permission error, or a disk-full atomic-write failure on a
// REAL chat/task session that DOES have one. Because pkg/logger initializes
// to INFO by default (see logger.go's init()), Debug is silent in production
// — a genuine failure produced NO output at all, and GET /api/v1/sessions
// would report a swept session as "active" forever (the durable
// LifecycleRecord flips to failed(interrupted) correctly, but the OTHER
// status store, UnifiedMeta.Status, silently never followed).
//
// The fix distinguishes the two cases via errors.Is(setErr, os.ErrNotExist):
// readUnifiedMeta (pkg/session/unified.go) wraps os.ReadFile's error with
// %w, and os.ReadFile's underlying *fs.PathError unwraps to a sentinel that
// satisfies errors.Is(_, os.ErrNotExist) — the standard, idiomatic Go
// file-not-found check. The not-exist case stays at Debug (unchanged
// wording); every OTHER error (parse failure, permission, I/O) now logs at
// Warn with distinct wording making clear the session is left inconsistent.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newMetaReconcileTestAgentLoop builds a minimal real *AgentLoop with one
// registered agent, so al.GetAgentStore(agentID) returns a real
// *session.UnifiedStore backed by an on-disk temp dir — reconcileUnifiedMetaStatus
// requires a concrete *AgentLoop (pe.agentLoop is typed *AgentLoop, not an
// interface), so a bare struct-literal PlanEngine (as most of this package's
// other tests use) cannot exercise this method at all.
func newMetaReconcileTestAgentLoop(t *testing.T) (al *AgentLoop, agentID string) {
	t.Helper()
	agentID = "meta-reconcile-agent"
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: agentID, Name: "Meta Reconcile Test Agent", Type: config.AgentTypeWorker, Home: t.TempDir()},
			},
		},
	}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &scriptedProvider{responseBody: successMarkerBody})
	t.Cleanup(func() { al.Close() })
	return al, agentID
}

// captureLogFile redirects package logger output to a temp file at the given
// level for the rest of the test (mirrors the established pattern in
// loop_unroutable_panic_test.go: DisableConsole + SetLevel + EnableFileLogging,
// restoring only the level on cleanup — console is not re-enabled, matching
// that file's own precedent). Returns a function that reads back everything
// logged so far.
func captureLogFile(t *testing.T, level logger.LogLevel) func() string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "reconcile-meta.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(level)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	return func() string {
		data, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read captured log file: %v", err)
		}
		return string(data)
	}
}

// TestReconcileUnifiedMetaStatus_MissingMetaJSON_StaysDebugAtDefaultLevel
// proves the benign, EXPECTED case (a delegate/subturn session_id that never
// had a UnifiedMeta record at all — SetMeta's readMetaLocked hits
// os.ReadFile's not-exist error) produces NO log output at the
// production-default INFO level. This is the "keep the benign case quiet"
// half of the fix.
func TestReconcileUnifiedMetaStatus_MissingMetaJSON_StaysDebugAtDefaultLevel(t *testing.T) {
	al, agentID := newMetaReconcileTestAgentLoop(t)
	pe := &PlanEngine{agentLoop: al}

	readLog := captureLogFile(t, logger.INFO) // the production default level

	// No session was ever created for this session id — SetMeta must hit the
	// not-exist path (delegate/subturn sessions never get a meta.json at all).
	pe.reconcileUnifiedMetaStatus(&session.LifecycleRecord{
		SessionID: "never-created-session-id",
		AgentID:   agentID,
	})

	if logged := readLog(); strings.Contains(logged, "could not reconcile UnifiedMeta status") {
		t.Fatalf("the benign not-exist case must stay silent at the production-default INFO level "+
			"(Debug-only) — got log output:\n%s", logged)
	}
}

// TestReconcileUnifiedMetaStatus_CorruptMetaJSON_IsWarnAtDefaultLevel proves
// the OTHER half: a genuine SetMeta failure on a session that DOES have a
// UnifiedMeta record (simulated here via a corrupted meta.json — readUnifiedMeta's
// json.Unmarshal fails with a parse error, NOT a not-exist error) is visible
// (WRN) at the production-default INFO level, with wording distinct from the
// benign message, making clear the session was left inconsistent.
func TestReconcileUnifiedMetaStatus_CorruptMetaJSON_IsWarnAtDefaultLevel(t *testing.T) {
	al, agentID := newMetaReconcileTestAgentLoop(t)
	sessStore := al.GetAgentStore(agentID)
	if sessStore == nil {
		t.Fatal("GetAgentStore returned nil — test harness misconfigured")
	}

	// Write a corrupted meta.json directly to disk for a session id that was
	// NEVER touched through the store's own API (NewSession/SetMeta). This is
	// essential: going through NewSession first would populate the store's
	// in-process metaCache, and SetMeta's cache-hit fast path
	// (readMetaLocked) would then return the cached value WITHOUT ever
	// touching the (corrupted) file on disk — masking the very error this
	// test needs to produce.
	sessionID := "corrupt-meta-session-id"
	sessionDir := filepath.Join(sessStore.BaseDir(), sessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "meta.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt meta.json: %v", err)
	}

	pe := &PlanEngine{agentLoop: al}
	readLog := captureLogFile(t, logger.INFO) // the production default level

	pe.reconcileUnifiedMetaStatus(&session.LifecycleRecord{
		SessionID: sessionID,
		AgentID:   agentID,
	})

	logged := readLog()
	if !strings.Contains(logged, "could not reconcile UnifiedMeta status") {
		t.Fatalf("a genuine (non-not-exist) SetMeta error must be logged and visible at the "+
			"production-default INFO level — got:\n%s", logged)
	}
	// The file logger emits structured JSON lines (`"level":"warn"`), unlike
	// the console writer's abbreviated "WRN" — assert on the JSON form.
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Fatalf("a genuine SetMeta error must log at WARN (not Debug/Info) so a disk-pressure or "+
			"corruption failure on a real chat/task session is never silent — got:\n%s", logged)
	}
}
