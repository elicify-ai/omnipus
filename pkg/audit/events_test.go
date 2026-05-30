// Tests for the Tool Registry redesign (Wave A2) audit event emitters.

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestLogger spins up a Logger writing to a temp directory; tests then
// read back the JSONL to assert wire shape.
func newTestLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	lg, err := NewLogger(LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
		RedactEnabled: false,
	})
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	t.Cleanup(func() { lg.Close() })
	return lg, filepath.Join(dir, "audit.jsonl")
}

// readRecords drains all JSONL records from path into typed Records.
func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	out := []Record{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// TestPolicyEngine_AuditEmissions exercises every event constant with its
// typed emitter and asserts:
//   - the resulting record event-name matches the constant
//   - severity is set per the spec table
//   - the documented field set is present
//
// This is the FR-038 / FR-047 / FR-054 / FR-066 / FR-074 contract test.
func TestPolicyEngine_AuditEmissions(t *testing.T) {
	t.Parallel()
	lg, path := newTestLogger(t)
	ctx := context.Background()

	EmitToolPolicyDenyAttempted(ctx, lg, "agent-1", "system.config.set", "agent",
		"sess-1", "turn-1", "tc-1", "mid_turn_policy_change")
	EmitToolPolicyAskRequested(ctx, lg, "appr-1", "tc-2", "system.exec",
		"agent-1", "sess-1", "turn-1", map[string]any{"cmd": "ls"})
	EmitToolPolicyAskGranted(ctx, lg, "appr-1", "user-1", "system.exec",
		"agent-1", "sess-1", "turn-1", 124, "ab12")
	EmitToolPolicyAskDenied(ctx, lg, "appr-1", "user-1", "system.exec",
		"agent-1", "sess-1", "turn-1", AskDenyReasonUser, "ab12", nil)
	EmitToolPolicyAskDenied(ctx, lg, "appr-2", "", "system.exec",
		"agent-1", "sess-1", "turn-1", AskDenyReasonBatchShortCircuit, "ab12",
		[]string{"tc-3", "tc-4"})
	EmitToolCollisionMCPRejected(ctx, lg, "srv-A", "web_fetch", ConflictWithBuiltin)
	EmitToolCollisionMCPRejected(ctx, lg, "srv-B", "system.foo", ConflictWithReservedPrefix)
	EmitToolCollisionMCPRejected(ctx, lg, "srv-C", "search", ConflictWithMCPPrefix+"srv-A")
	EmitAgentConfigCorrupt(ctx, lg, "ava", "core",
		"/tmp/ava/agent.json", os.ErrInvalid)
	EmitAgentConfigInvalidPolicyValue(ctx, lg, "billy", "custom",
		"/tmp/billy/agent.json", []InvalidPolicyEntry{
			{Field: "default_policy", Value: "banana", Reason: "not in {allow,ask,deny}"},
		})
	EmitAgentConfigUnknownToolInPolicy(ctx, lg, "billy", "/tmp/billy/agent.json",
		[]string{"ghost.tool"})
	EmitToolAssemblyDuplicateName(ctx, lg, "web_fetch",
		[]string{"builtin", "mcp:srv-A"}, "builtin")
	EmitMCPServerRenamed(ctx, lg, "old", "new", "stdio", "stdio:///cmd")
	EmitGatewayStartupGuardDisabled(ctx, lg, "gateway.tool_approval_max_pending")
	EmitGatewayConfigInvalidValue(ctx, lg, "gateway.tool_approval_max_pending",
		"-1", "negative")
	EmitTurnAbortedSyntheticLoop(ctx, lg, "agent-1", "sess-1", "turn-1", 8)

	// Force flush via Close.
	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readRecords(t, path)

	// Expected events in emission order, paired with mandated severity and
	// at least one required field per the spec table + FR refs.
	type expect struct {
		event       string
		sev         Severity
		mustHaveKey string
	}
	want := []expect{
		{EventToolPolicyDenyAttempted, SeverityWarn, "agent_id"},
		{EventToolPolicyAskRequested, SeverityInfo, "args_hash"},
		{EventToolPolicyAskGranted, SeverityInfo, "approver_user_id"},
		{EventToolPolicyAskDenied, SeverityInfo, "reason"},
		{EventToolPolicyAskDenied, SeverityInfo, "canceled_tool_call_ids"},
		{EventToolCollisionMCPRejected, SeverityWarn, "conflict_with"},
		{EventToolCollisionMCPRejected, SeverityWarn, "conflict_with"},
		{EventToolCollisionMCPRejected, SeverityWarn, "conflict_with"},
		{EventAgentConfigCorrupt, SeverityHigh, "path"},
		{EventAgentConfigInvalidPolicyValue, SeverityHigh, "entries"},
		{EventAgentConfigUnknownToolInPolicy, SeverityWarn, "tool_names"},
		{EventToolAssemblyDuplicateName, SeverityHigh, "kept"},
		{EventMCPServerRenamed, SeverityHigh, "new_name"},
		{EventGatewayStartupGuardDisabled, SeverityWarn, "config_key"},
		{EventGatewayConfigInvalidValue, SeverityHigh, "value"},
		{EventTurnAbortedSyntheticLoop, SeverityWarn, "synthetic_error_count"},
	}
	if len(got) != len(want) {
		t.Fatalf("record count: got %d, want %d", len(got), len(want))
	}
	for i, exp := range want {
		if got[i].Event != exp.event {
			t.Errorf("rec %d event: got %q, want %q", i, got[i].Event, exp.event)
		}
		if got[i].Severity != exp.sev {
			t.Errorf("rec %d severity: got %q, want %q", i, got[i].Severity, exp.sev)
		}
		if _, ok := got[i].Fields[exp.mustHaveKey]; !ok {
			t.Errorf("rec %d (%s) missing required field %q; have %v",
				i, exp.event, exp.mustHaveKey, got[i].Fields)
		}
	}
}

// TestEmit_NilLoggerIsNoOp — emitting against a nil logger must be a safe
// no-op (audit subsystem disabled). FR-038 best-effort contract.
func TestEmit_NilLoggerIsNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-logger emit panicked: %v", r)
		}
	}()
	EmitToolPolicyDenyAttempted(context.Background(), nil, "a", "t", "agent", "s", "t", "tc", "")
}

// TestEmit_InvalidAskDenyReason — emitter MUST refuse to write a record
// with an unknown reason; this is the boundary defense on FR-047's enum.
func TestEmit_InvalidAskDenyReason(t *testing.T) {
	t.Parallel()
	lg, path := newTestLogger(t)
	EmitToolPolicyAskDenied(context.Background(), lg, "appr", "u", "t", "a", "s", "tu",
		AskDenyReason("bogus"), "h", nil)
	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if recs := readRecords(t, path); len(recs) != 0 {
		t.Fatalf("expected zero records on invalid reason, got %d", len(recs))
	}
}

// TestBoot_AuditFailureStderrFallback exercises the FR-063 stderr-line
// path. Captures the output via the BootAbortWriter override and asserts
// shape and content.
func TestBoot_AuditFailureStderrFallback(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	prev := BootAbortWriter
	BootAbortWriter = &buf
	t.Cleanup(func() { BootAbortWriter = prev })

	n := EmitBootAbortStderr(EventAgentConfigCorrupt, "ava", "/tmp/ava/agent.json",
		os.ErrInvalid, []KV{{Key: "extra", Value: "with spaces"}})
	if n == 0 {
		t.Fatalf("expected non-zero bytes written")
	}
	out := buf.String()
	if !strings.HasPrefix(out, "BOOT_ABORT_REASON=") {
		t.Errorf("missing prefix: %q", out)
	}
	if !strings.Contains(out, "agent_id=ava") {
		t.Errorf("missing agent_id: %q", out)
	}
	if !strings.Contains(out, "path=/tmp/ava/agent.json") {
		t.Errorf("missing path: %q", out)
	}
	if !strings.Contains(out, `error="invalid argument"`) {
		t.Errorf("missing quoted error: %q", out)
	}
	if !strings.Contains(out, `extra="with spaces"`) {
		t.Errorf("missing quoted extra kv: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline: %q", out)
	}
}
