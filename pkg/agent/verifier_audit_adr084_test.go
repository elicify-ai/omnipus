// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_audit_adr084_test.go — ADR-084 revision 9, JUDGE-FR-084 (wave
// T4, round 7 of the ADR-084/085/086 joint delivery plan §3). This test
// lives here, not in wave E0 (which owns the actual emit sites in
// pkg/tools/filesystem.go and pkg/tools/resolvepath.go), because "E0 must
// not touch pkg/agent/**" — see this wave's row's own Must-NOT-touch
// column. E0 builds the mechanism (emitFileReadAudit, emitPathAccessDenied
// Correlated, the tools.WithVerifierAdjudicationID ctx seam) and unit-tests
// it in pkg/tools; this file proves it end to end through a REAL Judge
// turn, dispatched via the same runVerifierAdjudication path production
// uses.
//
// FR-084: "A Judge that now reads files MUST emit audit entries for those
// reads — read and path.access_denied — attributed to the Judge's agent id
// and correlated to the adjudication id, so an operator can answer 'what
// did the verifier open' from the audit log."
//
// Oracle discipline: every expected value is derived from FR-084's own text
// and from the judge spec's BDD scenario ("Given an adjudication in which
// the Judge read two files and was refused a third … it holds two read
// entries and one path.access_denied entry … all three attributed to the
// Judge's agent id and carry the adjudication id"), never from reading
// runVerifierAdjudication's behaviour back. "Two read entries" is the
// spec's own informal phrasing for two file-read audit rows (Event ==
// audit.EventFileOp, Details["op"] == "read" — pkg/tools/filesystem.go's
// own emitFileReadAudit, which this file deliberately does not import the
// unexported constant from; it asserts on the exact Details["op"] value the
// spec's prose names instead).
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Entry read-back reuses the package's own shared readAuditJSONL helper
// (pkg/agent/tool_approver_failclosed_test.go, []map[string]any) rather
// than defining a second, typed one — the two files would otherwise
// collide on the name.

// TestVerifierAudit_JudgeReadsAndDenialsAreAttributedAndCorrelated is the
// Test Matrix's named oracle for FR-084
// (judge-active-reviewer-spec.md's "TestVerifierAudit_JudgeReadsAndDenials
// AreAttributedAndCorrelated").
func TestVerifierAudit_JudgeReadsAndDenialsAreAttributedAndCorrelated(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	allowReadFilePolicy(judgeInst)

	// Real audit.Logger, injected onto the Judge's REAL tool registry (the
	// default-registered *tools.ReadFileTool from instance.go, never
	// replaced by a double here — this test exercises E0's real emit
	// sites, not a stand-in for them). SetAuditLogger propagates to every
	// already-registered tool implementing the auditLoggerAware contract
	// (pkg/tools/registry.go), which *tools.ReadFileTool satisfies.
	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLogger.Close() })
	judgeInst.Tools.SetAuditLogger(auditLogger)

	// Two ordinary files the Judge is genuinely permitted to read, placed
	// INSIDE the work-under-review workspace's own work/ directory.
	//
	// RE-POINTED 2026-09-11 (review finding 4 fix, arrangement only — no
	// assertion changed). These two files used to live in a bare t.TempDir()
	// outside every workspace, on the strength of ADR-063 FR-2.2's "FSOpRead
	// is allowed anywhere outside the secret set". That stopped being the
	// Judge's posture when JUDGE-FR-060's read-confinement seam
	// (tools.WithReadConfined) was actually SET at the verifier dispatch, as
	// ADR-084 requires: a Judge turn's reads are now confined to its
	// effective working directory, so a file outside it is refused — which is
	// the whole point of FR-060 and exactly the bypass ADR-084 exists to
	// close. The oracle here is FR-084's audit contract (two reads + one
	// denial, attributed and correlated), not "the Judge can read /tmp", so
	// the fix is to put the readable evidence where a confined Judge can
	// legitimately reach it.
	//
	// The workspace RECORD has to exist on disk for the turn to root there:
	// WithSystemAgentWorkspaceOverride is a PREFERRING selector, so a
	// WorkspaceID with no workspaces/<id>.json falls through to the harness's
	// own seeded workspace and the reads land outside the confinement
	// boundary again.
	omnipusHome := config.OmnipusHomeDir() // newGoalLoopTestLoop already t.Setenv'd OMNIPUS_HOME for this test
	const auditWorkspaceID = "ws-adr084-t4-audit"
	workspacesDir := filepath.Join(omnipusHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspacesDir, auditWorkspaceID+".json"),
		[]byte(`{"id":"`+auditWorkspaceID+`","core_team":["judge","native-agent"]}`), 0o644))

	filesDir := workspace.WorkDir(omnipusHome, auditWorkspaceID)
	require.NoError(t, os.MkdirAll(filesDir, 0o755))
	goodFile1 := filepath.Join(filesDir, "evidence-a.txt")
	goodFile2 := filepath.Join(filesDir, "evidence-b.txt")
	require.NoError(t, os.WriteFile(goodFile1, []byte("evidence A: the change compiles"), 0o600))
	require.NoError(t, os.WriteFile(goodFile2, []byte("evidence B: the tests pass"), 0o600))

	// The refused path stays where it is: master.key sits in the
	// scope-independent carve-out set AND outside the work dir, so it is
	// refused either way — the entry this test needs is a path.access_denied
	// for read_file, which both mechanisms emit.
	masterKeyPath := filepath.Join(omnipusHome, "master.key")
	require.NoError(t, os.MkdirAll(omnipusHome, 0o755))
	require.NoError(t, os.WriteFile(masterKeyPath, []byte("carve-out-protected key material"), 0o600))

	callN := 0
	fake := &fakeJudgeProvider{}
	fake.chatFn = func(int) (*providers.LLMResponse, error) {
		callN++
		switch callN {
		case 1:
			return toolCallResponse("read-1", "read_file", map[string]any{"path": goodFile1}), nil
		case 2:
			return toolCallResponse("read-2", "read_file", map[string]any{"path": goodFile2}), nil
		case 3:
			return toolCallResponse("read-3", "read_file", map[string]any{"path": masterKeyPath}), nil
		default:
			return &providers.LLMResponse{
				Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"reviewed both evidence files; the third path was refused"}]}`,
			}, nil
		}
	}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-fr084-audit",
		AssigneeAgentID: "native-agent",
		WorkspaceID:     auditWorkspaceID,
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the change was reviewed")},
		Attempt:         1,
		ClaimText:       "done",
	})
	require.Falsef(t, result.Unavailable, "the verifier turn must complete (2 reads + 1 refusal + a final verdict), got Unavailable: %s", result.Reason)
	require.NotNil(t, result.Verdict, "a completed adjudication must produce a verdict")

	require.NoError(t, auditLogger.Close())
	entries := readAuditJSONL(t, auditDir) // shared package helper -> []map[string]any

	var reads, denials []map[string]any
	for _, e := range entries {
		if tool, _ := e["tool"].(string); tool != "read_file" {
			continue // ignore the registry's own generic "tool_call" SEC-15 entries and anything unrelated
		}
		details, _ := e["details"].(map[string]any)
		switch ev, _ := e["event"].(string); {
		case ev == "path.access_denied":
			denials = append(denials, e)
		case details != nil && details["op"] == "read":
			reads = append(reads, e)
		}
	}

	require.Lenf(t, reads, 2, "FR-084: exactly two successful read_file audit entries expected, got %d: %+v", len(reads), reads)
	require.Lenf(t, denials, 1, "FR-084: exactly one path.access_denied audit entry expected, got %d: %+v", len(denials), denials)

	all := append(append([]map[string]any{}, reads...), denials...)
	for _, e := range all {
		assert.Equalf(t, judgeInst.ID, e["agent_id"],
			"FR-084: every read/denial audit entry must be attributed to the Judge's own agent id, got %+v", e)
	}

	// Differentiation: the two successful reads name DIFFERENT resolved
	// paths — proves the audit trail records what was actually opened, not
	// a fixed/hardcoded row repeated twice.
	if len(reads) == 2 {
		d1, _ := reads[0]["details"].(map[string]any)
		d2, _ := reads[1]["details"].(map[string]any)
		p1, _ := d1["path"].(string)
		p2, _ := d2["path"].(string)
		assert.NotEmpty(t, p1)
		assert.NotEmpty(t, p2)
		assert.NotEqual(t, p1, p2, "the two read entries must name the two DIFFERENT files actually opened, not the same path twice")
	}

	// FR-084's correlation requirement: all three entries carry the SAME
	// adjudication id, and it is non-empty.
	adjIDs := make(map[string]int, 3)
	for _, e := range all {
		details, _ := e["details"].(map[string]any)
		id, _ := details["adjudication_id"].(string)
		adjIDs[id]++
	}
	_, sawEmpty := adjIDs[""]
	assert.Falsef(t, sawEmpty,
		"FR-084: every read/denial audit entry for this adjudication must carry a non-empty adjudication_id "+
			"details field, so an operator can answer 'what did the verifier open' for THIS adjudication — "+
			"got entries with no adjudication_id at all: %+v", all)
	assert.Lenf(t, adjIDs, 1,
		"FR-084: all three entries (two reads, one denial) from the SAME adjudication must carry the SAME "+
			"adjudication_id — got %d distinct value(s) across them: %v", len(adjIDs), adjIDs)
}

// toolCallResponse builds a single-tool-call LLMResponse using the same
// direct Name/Arguments construction cancel_cascade_grandchild_test.go's
// established real-dispatch pattern uses (proven to drive an actual tool
// execution through the standard turn loop, not merely a transcript
// fixture).
func toolCallResponse(id, toolName string, args map[string]any) *providers.LLMResponse {
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: id, Name: toolName, Arguments: args}},
	}
}
