//go:build goolm && stdjson

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestCrashRecovery_ActualDeniedApprovalRemainsDeny(t *testing.T) {
	gate := make(chan struct{})
	p := &crashScript{gate: gate, entered: make(chan int, 8)}
	h := crashNew(t, p)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	work, err := workspace.EnsureWorkDir(h.home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(work, "denied.txt")
	inst := h.al.GetRegistry().primaryChatAgentForTest()
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAsk}})
	approver := &recordingApprover{approved: false}
	h.al.SetToolApprover(approver)
	p.next = func(i int, _ []providers.Message) (*providers.LLMResponse, error) {
		if i == 0 {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{crashCall("actual-denial", "append_file", map[string]any{"path": path, "content": "DENIED\n"})}, FinishReason: "tool_calls"}, nil
		}
		return &providers.LLMResponse{Content: "reported the actual denial", FinishReason: "stop"}, nil
	}
	_, owner := crashChild(t, h, p)
	close(gate)
	crashAwaitDone(t, owner.done)
	approver.mu.Lock()
	cards := len(approver.reqs)
	approver.mu.Unlock()
	require.Equal(t, 1, cards, "instrument: a real Ask path reached the operator boundary")
	require.NoError(t, h.al.auditLogger.Close())
	entries, err := readAuditEntries(filepath.Join(h.home, "system", "audit.jsonl"))
	require.NoError(t, err)
	var denies int
	for _, entry := range entries {
		if entry["event"] == "tool.policy.ask.denied" && entry["tool"] == "append_file" {
			denies++
			assert.Equal(t, "deny", entry["decision"], "actual approval denial remains a policy Deny")
		}
	}
	assert.Equal(t, 1, denies)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "denied approval performed no mutation")
}

func TestCrashRecovery_SuccessfulReadbackAuditDoesNotGrantPolicyAllow(t *testing.T) {
	crashRequireFileProof(t)
	f := crashFileCut(t, "append_file")
	p := &crashScript{}
	f.h.restore(t, f.snapshot, p)
	f.h.recover(t)
	got := f.h.record(t, f.record.SessionID)
	if own := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(got)); own != nil {
		crashAwaitDone(t, own.done)
	}
	require.NoError(t, f.h.al.auditLogger.Close())
	entries, err := readAuditEntries(filepath.Join(f.h.home, "system", "audit.jsonl"))
	require.NoError(t, err)
	var observations []map[string]any
	for _, entry := range entries {
		details, ok := entry["details"].(map[string]any)
		if ok && details["tool_call_id"] == "uncertain" && details["disposition"] == "effect_present" {
			observations = append(observations, entry)
		}
	}
	require.Len(t, observations, 1, "one attributable readback event, separate from the missing historical return")
	_, hasDecision := observations[0]["decision"]
	assert.False(t, hasDecision, "successful proof is not a policy Allow or Deny")
	assert.NotEqual(t, "tool.policy.ask.denied", observations[0]["event"])
}

func TestCrashRecovery_FailedAuditWriteIsVisibleAsSkipped(t *testing.T) {
	f := crashFileCut(t, "append_file")
	p := &crashScript{}
	f.h.restore(t, f.snapshot, p)
	require.NotNil(t, f.h.al.auditLogger)
	require.NoError(t, f.h.al.auditLogger.Close())
	err := f.h.al.auditLogger.Log(&audit.Entry{Event: audit.EventToolPolicyAskDenied, Decision: audit.DecisionDeny, Tool: "append_file"})
	require.Error(t, err, "instrument: the real audit writer is definitely unavailable")
	before := audit.SnapshotSkipped().Total
	f.h.recover(t)
	got := f.h.record(t, f.record.SessionID)
	if own := f.h.al.executionDispositionFor(f.h.al.executionClaimFor(got)); own != nil {
		crashAwaitDone(t, own.done)
	}
	assert.Greater(t, audit.SnapshotSkipped().Total, before, "failed recovery audit writes must be externally observable, never claimed landed")
}
