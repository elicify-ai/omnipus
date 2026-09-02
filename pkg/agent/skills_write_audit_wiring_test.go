// Omnipus — integration test for ADR-072 R4 fix: wiring the process-wide
// skills write-audit logger at gateway boot.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestNewAgentLoop_WiresSkillsWriteAuditLogger is the R4 regression test:
// before this fix, tools.SetSkillsWriteAuditLogger (pkg/tools/resolvepath.go)
// — whose own doc comment says "a nil logger is a silent no-op" — was never
// called anywhere in production. Confirmed live in
// docs/internal/qa/uat-report-skill-activation-batch2-groupD-2026-09-02.md
// (S30: write_file successfully overwrote a mounted skill file, but
// GET /api/v1/audit-log returned zero entries for a write that should have
// produced exactly one skill.write record).
//
// This builds a REAL AgentLoop via NewAgentLoop with sandbox.audit_log
// enabled — the exact production boot path (mirrors
// memory_reload_wiring_test.go's memReloadTestLoop fixture, which already
// proves the analogous audit-logger wiring for remember/run_retrospective) —
// and proves tools.EmitSkillWriteAudit, the SAME call both ResolvePath's
// write hook (write_file/edit_file) and the R3-fixed authoring tools
// (edit_skill/remove_skill) use, now reaches the real audit.jsonl file on
// disk instead of being a permanent no-op.
func TestNewAgentLoop_WiresSkillsWriteAuditLogger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Sandbox.AuditLog = true

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	require.NotNil(t, al.auditLogger, "sandbox.audit_log=true must construct a real audit logger")

	// Exercise the exact hook every skill-write path reaches: ResolvePath's
	// own write hook for write_file/edit_file (emitSkillPathWriteAudit,
	// unexported) and the R3-fixed authoring tools (edit_skill/remove_skill,
	// which write via pkg/skills' raw I/O and so call this exported wrapper
	// directly) both funnel into the SAME process-wide logger this call
	// reads.
	tools.EmitSkillWriteAudit("project", "edit_skill", "ava", "sess-1", "ws-1", "/mnt/acme/.claude/skills/db-migrate/SKILL.md")

	auditPath := filepath.Join(home, "system", "audit.jsonl")
	events := readAuditEvents(t, auditPath)

	var found map[string]any
	for _, e := range events {
		if e["event"] == "skill.write" {
			found = e
			break
		}
	}
	require.NotNil(t, found, "expected a skill.write audit entry after NewAgentLoop wired tools.SetSkillsWriteAuditLogger; got events: %v", events)

	details, ok := found["details"].(map[string]any)
	require.True(t, ok, "skill.write entry must carry a details object; got %v", found)
	assert.Equal(t, "project", details["shelf"])
	assert.Equal(t, "/mnt/acme/.claude/skills/db-migrate/SKILL.md", details["path"])
	assert.Equal(t, "ws-1", details["workspace_id"])
	assert.Equal(t, "edit_skill", found["tool"])
	assert.Equal(t, "ava", found["agent_id"])
}

// TestReloadProviderAndConfig_ReassertsSkillsWriteAuditLogger proves the
// reload path (pkg/agent/loop.go's al.auditLogger != nil block that also
// calls al.wireMemoryAuditLoggerOn) re-asserts
// tools.SetSkillsWriteAuditLogger too, so a hot config reload can never leave
// the process-wide skills audit hook silently unset even though it started
// wired at boot.
func TestReloadProviderAndConfig_ReassertsSkillsWriteAuditLogger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Sandbox.AuditLog = true

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	reloadWithSameAgents(t, al)

	tools.EmitSkillWriteAudit("registry", "write_file", "jim", "sess-2", "ws-2", "/home/.omnipus/skills/some-skill/SKILL.md")

	auditPath := filepath.Join(home, "system", "audit.jsonl")
	events := readAuditEvents(t, auditPath)
	found := false
	for _, e := range events {
		if e["event"] == "skill.write" {
			details, _ := e["details"].(map[string]any)
			if details != nil && details["workspace_id"] == "ws-2" {
				found = true
			}
		}
	}
	assert.True(t, found, "the skills write-audit logger must still be wired after a config reload; got events: %v", events)
}
