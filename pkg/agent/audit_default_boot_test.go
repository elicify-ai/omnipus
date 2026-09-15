// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// Audit logging is ON by default (founder decision, 2026-09-11), which means
// the audit-construction block in NewAgentLoop now runs on every install
// rather than only on those that opted in. These tests pin the consequence
// that decision turns on: whether a failure to build the audit logger aborts
// boot or degrades.
//
// The rule is consent-based. An operator who WROTE `"audit_log": true` asked
// for a compliance guarantee, so failing to deliver it is a fail-closed boot
// abort (unchanged from B1.2(b)). An install that has audit only because it is
// the default never asked for anything, and turning a default on must not
// convert a working install into one that refuses to start — so that case
// degrades loudly instead.
//
// Both tests break audit construction the same way: a regular FILE where the
// audit directory belongs, which makes audit.NewLogger's os.MkdirAll fail with
// ENOTDIR. That is a genuine construction failure at the real call site, not a
// stub.

// breakAuditDir plants a regular file at <home>/system so that
// audit.NewLogger's os.MkdirAll cannot create the audit directory.
func breakAuditDir(t *testing.T, home string) {
	t.Helper()
	systemPath := filepath.Join(home, "system")
	require.NoError(t, os.WriteFile(systemPath, []byte("not a directory"), 0o600))

	// Prove the sabotage is real before relying on it: if a later refactor
	// moved the audit directory, both tests below would pass vacuously.
	_, err := audit.NewLogger(audit.LoggerConfig{
		Dir:               systemPath,
		RetentionDays:     90,
		AuditLogRequested: true,
	})
	require.Error(t, err,
		"precondition: audit.NewLogger must fail against a file-where-a-directory-belongs; "+
			"without this the abort/degrade assertions below prove nothing")
}

// auditBootTestConfig returns a config that boots a real AgentLoop, with audit
// left at whatever the caller sets.
func auditBootTestConfig() *config.Config {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	return cfg
}

// TestNewAgentLoop_AuditFailure_DefaultOn_DegradesAndBoots covers the
// population created by the default flip: audit is on, but nobody asked for
// it. Boot must succeed with no audit logger rather than refusing to start.
//
// This is still strictly better than the behaviour it replaces. Before the
// default flip this install had audit off AND no error at all; it now has
// audit off, an ERROR naming the directory and the cause, and a /health
// endpoint that reads degraded (audit_logger unavailable while
// cfg.Sandbox.AuditLog reports configured).
func TestNewAgentLoop_AuditFailure_DefaultOn_DegradesAndBoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	breakAuditDir(t, home)

	cfg := auditBootTestConfig()
	require.True(t, cfg.Sandbox.AuditLog,
		"precondition: audit must be ON by default for this test to mean anything")
	require.True(t, cfg.Sandbox.AuditLogFromDefault,
		"precondition: DefaultConfig must mark audit as coming from the seed, not from an operator request")

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err,
		"a DEFAULT-enabled audit that cannot be constructed must degrade, not abort boot — "+
			"turning on a default must never convert a working install into one that refuses to start")
	require.NotNil(t, al)
	t.Cleanup(func() { al.Close() })

	assert.Nil(t, al.AuditLogger(),
		"audit construction failed, so the loop must carry NO audit logger — a non-nil logger here "+
			"would be the degraded-writer case the AuditLogRequested flag exists to prevent")

	// The health surface must be able to see this. It reports "broken" from
	// the pair (audit_logger unavailable, operator config says audit on), and
	// both halves of that pair have to hold for the ERROR to be visible
	// rather than look like a deliberate off-state.
	assert.True(t, al.GetConfig().Sandbox.AuditLog,
		"config must still report audit as configured so /health reads degraded rather than off-by-choice")
}

// TestNewAgentLoop_FreshInstall_WiresAuditLogger is the regression test for
// the finding that prompted the default flip.
//
// A live gateway logged "knowledge: no audit logger is wired to the record
// write door — no record write or refusal will be recorded for the lifetime of
// this process". That message fires from logRecordAuditDecision
// (pkg/gateway/rest_knowledge_record.go) when restAPI.auditor is nil, and
// restAPI.auditor is populated from exactly one place: agentLoop.AuditLogger()
// (pkg/gateway/gateway.go). So the nil auditor was never a knowledge-records
// bug — it was this loop booting with no audit logger because
// cfg.Sandbox.AuditLog defaulted to false on an install that has no "sandbox"
// block at all.
//
// This test pins the fix at its root: a fresh-install config must produce a
// REAL audit logger, writing to a real file. Assert on the logger and the file
// rather than on the absence of a log line — an absent ERROR proves nothing on
// its own, since it is also absent when nothing is wired up at all.
func TestNewAgentLoop_FreshInstall_WiresAuditLogger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Exactly what a freshly onboarded instance resolves to: DefaultConfig
	// with no operator "sandbox" block overriding anything.
	cfg := auditBootTestConfig()
	require.True(t, cfg.Sandbox.AuditLog,
		"a fresh install must resolve audit_log = true")

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	// The precondition pkg/gateway reads into restAPI.auditor. Non-nil here
	// is precisely what makes the reported ERROR unreachable.
	require.NotNil(t, al.AuditLogger(),
		"a fresh install must boot WITH an audit logger; a nil one here is what put "+
			"restAPI.auditor at nil and silenced every record write and refusal")

	// And it must be a working logger, not merely a non-nil pointer: boot
	// emits a startup entry, so the file exists and is non-empty.
	auditPath := filepath.Join(home, "system", "audit.jsonl")
	info, statErr := os.Stat(auditPath)
	require.NoError(t, statErr,
		"audit.jsonl must exist after boot at %s", auditPath)
	assert.Positive(t, info.Size(),
		"audit.jsonl exists but is empty — a non-nil logger that writes nothing is the "+
			"degraded-writer case, not a working audit trail")
}

// TestNewAgentLoop_AuditFailure_ExplicitOn_AbortsBoot pins the existing
// fail-closed contract, unchanged. An operator who wrote `"audit_log": true`
// in config.json gets a boot abort with the typed error the gateway maps to
// SandboxBootError + EX_CONFIG (78).
func TestNewAgentLoop_AuditFailure_ExplicitOn_AbortsBoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	breakAuditDir(t, home)

	cfg := auditBootTestConfig()
	cfg.Sandbox.AuditLog = true
	// What loadConfig sets when `audit_log` is physically present in the file.
	// Also the zero value, so every hand-built config.Config literal — every
	// existing test fixture included — keeps the fail-closed contract without
	// having to know this field exists.
	cfg.Sandbox.AuditLogFromDefault = false

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err == nil {
		if al != nil {
			al.Close()
		}
		t.Fatal("an EXPLICITLY-enabled audit that cannot be constructed must abort boot; " +
			"NewAgentLoop returned no error, which would leave the operator running " +
			"without the compliance trail they asked for")
	}

	var constructErr *audit.LoggerConstructionError
	require.True(t, errors.As(err, &constructErr),
		"the abort must carry a *audit.LoggerConstructionError — pkg/gateway matches on that "+
			"exact type to map the failure to EX_CONFIG (78); got %T: %v", err, err)
}
