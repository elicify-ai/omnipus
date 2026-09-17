// Package agent — ScenarioProvider runTurn integration test.
//
// This file bridges the ScenarioProvider harness (pkg/agent/testutil) into
// pkg/agent's runTurn end-to-end path. It was flagged as Rank-10 (most critical
// test gap) by the PR-test-analyzer in the 14-agent audit: the harness existed
// since commit 3961021 but no test inside pkg/agent/ actually drove runTurn
// with a scripted provider — every existing test bypassed runTurn and exercised
// leaf helpers directly.
//
// The bridge established here unblocks V2.D (RecoverOrphanedToolCalls integration
// test) and the Plan-3 Layer-3/Axis-3 e2e scenarios (their placeholder t.Skip
// stubs were deliberately removed 2026-07-19 — write those tests against this
// bridge when implementing them; the Plan 3 doc remains the spec).
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G step 1

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// dangerousStubTool is a test-only tool that records whether Execute was called.
// It implements tools.Tool so it can be registered in the agent's tool registry.
// If the policy deny path works correctly, Execute must NEVER be called.
type dangerousStubTool struct {
	tools.BaseTool
	wasCalled atomic.Bool
}

func (d *dangerousStubTool) Name() string        { return "dangerous_tool" }
func (d *dangerousStubTool) Description() string { return "Dangerous test stub — must never execute" }
func (d *dangerousStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *dangerousStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (d *dangerousStubTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	d.wasCalled.Store(true)
	return &tools.ToolResult{ForLLM: "executed — this should never happen", IsError: false}
}

// readAuditEntries parses a JSONL audit file and returns all entries as
// map[string]any. Lines that fail to parse are silently skipped (matching
// the pattern used in session_recovery_test.go).
func readAuditEntries(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &r); jsonErr != nil {
			continue
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

// TestRunTurn_ScriptedToolCall_PolicyDeniesAndAudits drives runTurn end-to-end
// using the ScenarioProvider harness. The scenario:
//
//	Step 1: LLM emits a tool call to "dangerous_tool" (denied by agent policy).
//	Step 2: LLM emits a plain text reply after receiving the synthetic denial result.
//
// Assertions (per quizzical-marinating-frog.md V2.G step 1):
//
//	(a) The stub tool was NOT executed — wasCalled must stay false.
//	(b) The audit log contains at least one entry with event="tool.policy.deny.attempted"
//	    and decision="deny" referencing "dangerous_tool".
//	(c) Session history contains a role="tool" message with "permission_denied" in its
//	    content — the synthetic deny result was injected into history.
//	(d) ScenarioProvider.CallCount() == 2 — the loop did NOT bail after the deny;
//	    it gave the LLM a second turn to recover.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G step 1 (ScenarioProvider → runTurn bridge)
func TestRunTurn_ScriptedToolCall_PolicyDeniesAndAudits(t *testing.T) {
	// -----------------------------------------------------------------------
	// Arrange: workspace, audit dir, AgentLoop.
	// -----------------------------------------------------------------------
	//
	// Layout:
	//   tmpHome/
	//     workspace/    ← cfg.Agents.Defaults.Home
	//     system/       ← audit dir (derived from filepath.Dir(workspace) = tmpHome)
	//     sessions/     ← shared session store
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Step 1: tool_call → dangerous_tool (will be denied by policy).
	// Step 2: plain text recovery message.
	provider := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("I cannot proceed without that tool.")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// Enable audit logging so emitPolicyDenyAudit writes to audit.jsonl.
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	// -----------------------------------------------------------------------
	// Wire: register dangerousStubTool and set its policy to deny.
	// -----------------------------------------------------------------------

	stub := &dangerousStubTool{}
	al.RegisterTool(stub)

	// Apply deny policy for dangerous_tool on every agent in the registry.
	// StoreToolPolicy is the atomic pointer setter used by the TOCTOU re-check.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		agent, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"dangerous_tool": "deny",
			},
		})
	}

	// -----------------------------------------------------------------------
	// Act: drive runTurn end-to-end via the public ProcessDirect entry point.
	//
	// ProcessDirect → ProcessDirectWithChannel → processMessage → runAgentLoop
	// → runTurn  ← this is what the whole test is designed to exercise.
	// -----------------------------------------------------------------------
	const sessionKey = "test-session-policy-deny"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please run dangerous_tool for me", sessionKey)
	require.NoError(
		t,
		err,
		"ProcessDirect must not return an error — policy deny is a loop-level action, not an agent error",
	)

	// -----------------------------------------------------------------------
	// Assert (a): tool was NOT executed.
	//
	// Differentiation test: the stub records execution. If the code silently
	// bypasses the policy path and calls Execute anyway, this fails.
	// -----------------------------------------------------------------------
	assert.False(t, stub.wasCalled.Load(),
		"CRITICAL: dangerous_tool.Execute was called — policy deny path did NOT block execution")

	// -----------------------------------------------------------------------
	// Assert (b): audit log contains a tool.policy.deny.attempted entry.
	//
	// The audit dir is tmpHome/system (derived in NewAgentLoop as
	// filepath.Dir(cfg.AgentHomeBasePath()) + "/system").
	// -----------------------------------------------------------------------
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")

	// Flush the audit logger before reading (Close/reopen not needed — the loop
	// is still running, but Log() calls writer.Flush() after each write).
	// Give a brief window for any deferred flushes if needed.
	require.FileExists(t, auditPath,
		"audit.jsonl must exist — AuditLog=true and at least one audit event was expected")

	entries, err := readAuditEntries(auditPath)
	require.NoError(t, err, "audit.jsonl must be readable and contain valid JSONL")
	require.NotEmpty(t, entries, "audit.jsonl must contain at least one entry")

	var denyEntries []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventToolPolicyDenyAttempted && e["decision"] == audit.DecisionDeny {
			denyEntries = append(denyEntries, e)
		}
	}
	require.NotEmpty(t, denyEntries,
		"audit log must contain at least one %q (decision=%q) entry; got entries: %v",
		audit.EventToolPolicyDenyAttempted, audit.DecisionDeny, entries)

	// Content test: the deny entry must name dangerous_tool — not a hardcoded stub.
	found := false
	for _, e := range denyEntries {
		if e["tool"] == "dangerous_tool" {
			found = true
			break
		}
	}
	assert.True(t, found,
		`deny audit entry must have tool="dangerous_tool"; deny entries: %v`, denyEntries)

	// -----------------------------------------------------------------------
	// Assert (c): session history contains a role=tool message with
	// "permission_denied" — the synthetic deny result was persisted.
	//
	// Persistence test: write-then-read-back confirms the deny result was
	// actually injected into history (not just emitted as an event and dropped).
	//
	// Note: ProcessDirect uses channel="cli", chatID="direct" with no peer or
	// session ID. The routing layer resolves this to DMScopeMain (default),
	// which produces session key "agent:<defaultAgent.ID>:main" via
	// BuildAgentMainSessionKey. This is the key under which history is
	// stored — not the caller-supplied key. (":main" here is
	// routing.DefaultMainKey, the session-key SUFFIX for an agent's
	// non-peer scope — an unrelated concept that survived the "main"
	// sentinel's removal; it is not the agent id.)
	//
	// ADR-058 (tool-denial semantics) strengthening: this denial is emitted by
	// loop.go's TOCTOU policy-deny site (site 1), which is uniformly rewired
	// through ClassifyDenial/denialPayloadJSON (spec §4.1 row 9, "policy_denied").
	// The original assertion only checked for the substring "permission_denied"
	// — that would pass even if the payload were malformed or a hardcoded
	// stub. Parsing the JSON and asserting every field against the ADR-058
	// classification table (rather than a re-derived call to ClassifyDenial,
	// which would let a broken classifier grade its own homework) proves the
	// real, honest, classified content reaches persisted history.
	// -----------------------------------------------------------------------
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must exist after boot")

	// "mia" is the only agent this test's cfg.Agents.List registers, so it is
	// what GetDefaultAgent() resolves to (no "main" sentinel to fall back to
	// anymore).
	const resolvedSessionKey = "agent:mia:main" // BuildAgentMainSessionKey("mia")
	history := defaultAgent.Sessions.GetHistory(resolvedSessionKey)
	require.NotEmpty(
		t,
		history,
		"session history must not be empty after a completed turn (checked key: %q)",
		resolvedSessionKey,
	)

	var toolDenyMsg *struct{ Role, Content string }
	for _, msg := range history {
		if msg.Role == "tool" && strings.Contains(msg.Content, "permission_denied") {
			toolDenyMsg = &struct{ Role, Content string }{Role: msg.Role, Content: msg.Content}
			break
		}
	}
	require.NotNil(t, toolDenyMsg,
		"session history must contain a role=tool message with 'permission_denied' in content; history: %v", history)
	assert.Equal(t, "tool", toolDenyMsg.Role,
		"synthetic deny result must have role=tool (not system or assistant)")

	// ADR-058 FR-058-05/06 content test: decode the persisted payload and
	// assert on every field's ACTUAL value, not just substring presence.
	var denyPayload struct {
		Error     string `json:"error"`
		Message   string `json:"message"`
		Tool      string `json:"tool"`
		Reason    string `json:"reason"`
		Permanent bool   `json:"permanent"`
	}
	require.NoError(t, json.Unmarshal([]byte(toolDenyMsg.Content), &denyPayload),
		"the persisted permission_denied message must be valid JSON, not a truncated/malformed string: %q",
		toolDenyMsg.Content)

	assert.Equal(t, "permission_denied", denyPayload.Error)
	assert.Equal(t, "dangerous_tool", denyPayload.Tool,
		"payload must name the ACTUAL denied tool — a hardcoded tool name would fail this")
	assert.Equal(t, "policy_denied", denyPayload.Reason,
		"ADR-058 spec §4.1 row 9: the TOCTOU policy-deny site's loop-pseudo-reason")
	assert.True(t, denyPayload.Permanent,
		"ADR-058 D1 row 8: a policy deny is PERMANENT — this is the pre-ADR-058 control case "+
			"(§1.2 of the ADR) that already behaved correctly; it must keep doing so")
	assert.Equal(t, "Tool execution denied by policy.", denyPayload.Message,
		"ADR-058 D2's pinned literal for the policy-deny row — 'unchanged, already true' per the ADR; "+
			"a paraphrase or drifted string must fail this")

	// FR-058-06 negative guard: this reason must never claim a human denied
	// anything (no human was ever asked — the policy pointer just flipped).
	assert.NotContains(t, strings.ToLower(denyPayload.Message), denialUserMarker,
		"a policy_denied message must never contain %q — nobody was asked, so nobody could have denied it",
		denialUserMarker)

	// Differentiation guard (anti-stub): if every denial reason rendered the
	// SAME hardcoded message, this test's exact-match assertions above could
	// still be satisfied by a classifier that ignores its input entirely.
	// Cross-check against a DIFFERENT reason's pinned message to prove the
	// classification is reason-specific, not a shared constant.
	userCls, _ := ClassifyDenial("user")
	assert.NotEqual(t, userCls.ModelMessage, denyPayload.Message,
		"policy_denied and user must render DIFFERENT messages — an implementation that returns "+
			"the same message for every reason would falsely pass every assertion above")

	// -----------------------------------------------------------------------
	// Assert (d): ScenarioProvider.CallCount() == 2 — both LLM steps fired.
	//
	// This confirms the loop did NOT bail after the policy deny; it continued
	// to give the LLM a second chance to recover (the recovery message in step 2).
	// If the loop had bailed, CallCount would be 1.
	// -----------------------------------------------------------------------
	assert.Equal(t, 2, provider.CallCount(),
		"ScenarioProvider must have been called exactly 2 times — step 1 (tool call) "+
			"and step 2 (recovery text); a count of 1 means the loop bailed after deny")

	// -----------------------------------------------------------------------
	// Differentiation bonus: the final content matches step 2's scripted text,
	// not step 1's empty content or any hardcoded fallback.
	// -----------------------------------------------------------------------
	assert.Equal(t, "I cannot proceed without that tool.", finalContent,
		"final content must be the step-2 recovery text from ScenarioProvider, not a hardcoded fallback")
}
