package agent

// drift_audit_entry_test.go — ADR-029 F-13: drift audit event read-back.
//
// Extends the drift-drop test to assert the audit log contains a
// "channel.routing.drift_drop" entry with the correct fields after a drift drop,
// and that exactly ONE entry is emitted per dropped message (the single-emission
// fix verified by TestDriftDrop_SingleEmission_ViaProcessMessage).
//
// This test complements TestDriftDrop_SingleEmission_ViaProcessMessage
// (bound_drift_route_test.go) which only checks the counter, not the log entry.
//
// Traces to: channel-instance-workspace-binding-spec.md §7 TDD #23b (F-13 aspect),
//            FR-028, MAJ-003, SC-004, BDD "Drift drops-and-alerts, never global default".

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// readAllAuditEntries reads all JSON lines from audit.jsonl in dir and returns
// them as decoded maps.  Empty lines are skipped.
func readAllAuditEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil // no entries yet is valid (pre-emission check)
	}
	require.NoError(t, err, "failed to read audit.jsonl")

	var entries []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"audit.jsonl line must be valid JSON: %s", line)
		entries = append(entries, entry)
	}
	return entries
}

// findDriftDropEntries filters entries to those with event=="channel.routing.drift_drop".
func findDriftDropEntries(entries []map[string]any) []map[string]any {
	var out []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventChannelRoutingDriftDrop {
			out = append(out, e)
		}
	}
	return out
}

// TestDriftDrop_EmitsAuditEntry_WithRequiredFields verifies FR-028:
// after a processMessage call on a bound instance with a deleted agent,
// the audit log contains exactly ONE "channel.routing.drift_drop" entry
// with the required details fields: instance_id, workspace_id,
// intended_agent_id, chat_id, reason.
//
// BDD: Given instance "whatsapp.eu" bound to workspace "sales" agent "ray" (deleted),
// When processMessage is called with an ordinary inbound,
// Then the audit log has exactly 1 "channel.routing.drift_drop" entry
//
//	with instance_id="whatsapp.eu", workspace_id="sales", intended_agent_id="ray",
//	chat_id=<the chat ID>, and a non-empty reason.
func TestDriftDrop_EmitsAuditEntry_WithRequiredFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Set up the audit logger writing to a temp dir so we can read it back.
	auditDir := filepath.Join(home, "audit")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		RetentionDays: 90,
	})
	require.NoError(t, err)
	defer auditLogger.Close()

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		// "ray" is intentionally absent (deleted) → drift condition.
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	// Inject the audit logger directly (same pattern as audit_user_test.go).
	al.auditLogger = auditLogger

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	const chatID = "15555550100@s.whatsapp.net"
	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     chatID,
		Content:    "hello drift",
	}

	// Precondition: no drift entries before the drop.
	entriesBefore := findDriftDropEntries(readAllAuditEntries(t, auditDir))
	assert.Empty(t, entriesBefore, "precondition: no drift_drop entries before processMessage")

	// Drive the full processMessage path (single emission point).
	preDrift := al.GetDriftDropped()
	_, _, processErr := al.processMessage(context.Background(), msg)
	postDrift := al.GetDriftDropped()

	require.Error(t, processErr, "processMessage must return an error for a drift drop")

	// ── Counter: exactly ONE increment per message ───────────────────────────
	require.Equal(t, preDrift+1, postDrift,
		"driftDropped counter must increment by exactly 1 per dropped message")

	// ── Audit log: exactly ONE drift_drop entry ───────────────────────────────
	// Flush the logger to disk.
	require.NoError(t, auditLogger.Close())

	allEntries := readAllAuditEntries(t, auditDir)
	driftEntries := findDriftDropEntries(allEntries)
	require.Len(
		t,
		driftEntries,
		1,
		"exactly 1 'channel.routing.drift_drop' audit entry must be emitted per dropped message (no double-emit); total entries: %d",
		len(allEntries),
	)

	entry := driftEntries[0]

	// ── Required fields on the entry ─────────────────────────────────────────
	assert.Equal(t, audit.EventChannelRoutingDriftDrop, entry["event"],
		"entry event must be %q", audit.EventChannelRoutingDriftDrop)
	assert.Equal(t, string(audit.DecisionDeny), entry["decision"],
		"entry decision must be %q", string(audit.DecisionDeny))

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "entry must have a non-nil 'details' object; got %T", entry["details"])

	assert.Equal(t, "whatsapp.eu", details["instance_id"],
		"details.instance_id must be 'whatsapp.eu'")
	assert.Equal(t, "sales", details["workspace_id"],
		"details.workspace_id must be 'sales'")
	assert.Equal(t, "ray", details["intended_agent_id"],
		"details.intended_agent_id must be 'ray'")
	assert.Equal(t, chatID, details["chat_id"],
		"details.chat_id must match the inbound ChatID")
	assert.NotEmpty(t, details["reason"],
		"details.reason must be non-empty (explains why the agent is unresolvable)")
}

// TestDriftDrop_ExactlyOneEntryPerMessage verifies the no-double-emit property
// at the audit-entry level (not just the counter).  Two separate drift messages
// on the same bound instance must produce exactly two drift_drop entries — no more,
// no fewer.
//
// This is the audit-layer counterpart to the counter-only check in
// TestDriftDrop_SingleEmission_ViaProcessMessage (bound_drift_route_test.go).
//
// Traces to: FR-028, MAJ-003 (single-emission fix, no double-count).
func TestDriftDrop_ExactlyOneEntryPerMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	auditDir := filepath.Join(home, "audit")
	require.NoError(t, os.MkdirAll(auditDir, 0o700))
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		RetentionDays: 90,
	})
	require.NoError(t, err)
	defer auditLogger.Close()

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		// "ray" absent → drift.
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	al.auditLogger = auditLogger
	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "chat-777",
		Content:    "first message",
	}

	// Send the SAME message twice (simulates two separate inbounds).
	_, _, err1 := al.processMessage(context.Background(), msg)
	require.Error(t, err1)

	msg2 := msg
	msg2.Content = "second message"
	_, _, err2 := al.processMessage(context.Background(), msg2)
	require.Error(t, err2)

	// Flush.
	require.NoError(t, auditLogger.Close())

	driftEntries := findDriftDropEntries(readAllAuditEntries(t, auditDir))
	assert.Len(t, driftEntries, 2,
		"two separate drift-drop messages must produce exactly 2 audit entries (one per message, no double-emit)")
}
