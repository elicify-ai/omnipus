//go:build goolm && stdjson

package agent

// RED plan and independent oracles: tests/plans/orphan-restart-recovery-acceptance.md.
// Architect Option A (2026-10-01) specifies local positional binding, exact
// exclusions, original archive indexes and unchanged pre-repair validation.
// Existing entry points keep this pack compilable before the new helper exists.
// No new implementation is read to derive an expected value. A fresh CHECK
// instance owns GREEN and scratch-clone mutation certification, not this author.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// One narrow symbol permits serial RED execution without running pkg/agent whole.
func TestOrphanRecoveryAcceptance(t *testing.T) {
	t.Run("rebuild_durability", orphanACTestRebuild)
	t.Run("archive_mapping_and_projection", orphanACTestMapping)
	t.Run("parallel_and_reused_ids", orphanACTestBindings)
	t.Run("unsupported_recovery_stays_rejected", orphanACTestNegatives)
	t.Run("completed_and_unbound_controls", orphanACTestControls)
}

func orphanACCall(id string) providers.ToolCall {
	return providers.ToolCall{ID: id, Type: "function",
		Function: &providers.FunctionCall{Name: "r1_tool", Arguments: `{"purpose":"recovery-acceptance"}`}}
}

func orphanACGroup(narration string, ids ...string) providers.Message {
	calls := make([]providers.ToolCall, len(ids))
	for i, id := range ids {
		calls[i] = orphanACCall(id)
	}
	return providers.Message{Role: "assistant", Content: narration, ToolCalls: calls}
}

func orphanACResult(id, text string) providers.Message {
	return providers.Message{Role: "tool", ToolCallID: id, Content: text}
}

func orphanACMarker(id string) providers.Message {
	return providers.Message{Role: "system", Content: fmt.Sprintf(
		`{"type":"turn_canceled_restart","tool_call_id":%q,"reason":"ungraceful_shutdown_recovery"}`, id)}
}

// indexes are explicit contract-derived addresses supplied by each case, not
// the result of a production selector, cancellation scanner or mapper.
func orphanACPick(raw []providers.Message, indexes ...int) []providers.Message {
	out := make([]providers.Message, len(indexes))
	for i, line := range indexes {
		out[i] = raw[line]
	}
	return out
}

func orphanACSnapshot(t *testing.T, h *cwR1Harness) memory.WindowSnapshot {
	t.Helper()
	snap, err := h.store.SnapshotWindow(context.Background(), h.key)
	require.NoError(t, err, "read exact real-store snapshot")
	return snap
}

func orphanACArchiveBytes(t *testing.T, h *cwR1Harness) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, h.key+".jsonl"))
	require.NoError(t, err, "read original archive bytes")
	return data
}

func orphanACAssemble(t *testing.T, h *cwR1Harness, ts *turnState, history []providers.Message) []providers.Message {
	t.Helper()
	out := h.al.assembleMessages(context.Background(), ts, history, "", nil, nil)
	require.NoError(t, ts.contextWindowError(), "recovery-visible assembly must not latch a storage/projection error")
	require.NotEmpty(t, out, "real assembly retains its pinned envelope")
	require.Equal(t, "system", out[0].Role, "pinned envelope precedes the exact archive-derived view")
	return out
}

func orphanACAssertView(t *testing.T, h *cwR1Harness, ts *turnState, want []providers.Message) []providers.Message {
	t.Helper()
	out := orphanACAssemble(t, h, ts, h.agent.Sessions.GetHistory(h.key))
	require.Equal(t, want, out[1:], "architect Option A: exclude only the locally bound canceled group/results/records; preserve the entire remaining sequence")
	return out
}

func orphanACAssertUnchanged(t *testing.T, h *cwR1Harness, before memory.WindowSnapshot, bytes []byte) {
	t.Helper()
	require.Equal(t, before, orphanACSnapshot(t, h), "pure recovery view changes neither archive nor captured Skip/Count/AnchorLine/projection metadata")
	require.Equal(t, bytes, orphanACArchiveBytes(t, h), "recovery never rewrites, deletes or fabricates archive bytes")
}

func orphanACReopen(t *testing.T, h *cwR1Harness) {
	t.Helper()
	reloaded, err := memory.NewJSONLStore(h.dir)
	require.NoError(t, err, "reload uses a fresh real store reading persisted archive and metadata")
	t.Cleanup(func() { require.NoError(t, reloaded.Close(), "close reloaded store") })
	h.store = reloaded
	h.agent.Sessions = session.NewJSONLBackend(reloaded)
}

func orphanACAssertRejectedBeforeSend(t *testing.T, h *cwR1Harness, ts *turnState, messages []providers.Message, wantErr string) {
	t.Helper()
	r, provider := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, messages, provider)
	rr.rq.prepareCallMessages()
	require.EqualError(t, ts.contextWindowError(), wantErr, "prepareCallMessages must reject the exact invalid group before repair")
	require.Equal(t, messages, rr.rq.ri.messages, "rejection is not permission to erase or alter the live candidate")
	require.Equal(t, messages, rr.rq.ri.rf.callMessages, "invalid declarations/results remain visible, not sanitized into a pass")
	response, err := rr.rq.ri.rf.rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
	require.EqualError(t, err, wantErr, "send boundary surfaces the same specific refusal")
	require.Nil(t, response, "rejected request cannot produce a successful response")
	require.Len(t, r.requests(t), 0, "real HTTP endpoint receives no unsupported-recovery request")
}

type orphanACMarkTestingT interface {
	require.TestingT
	Helper()
}

func orphanACAssertMark(t orphanACMarkTestingT, m providers.Message, id string, line, size, turn int) {
	t.Helper()
	// Decode the entire mark with the standard JSON decoder, not a production
	// projection/mark parser. The eight fields are from ToolResultRecallMark.
	var mark map[string]any
	require.NoError(t, json.Unmarshal([]byte(m.Content), &mark), "emptied result is exactly one mark, with no leaked source text")
	hint, ok := mark["hint"].(string)
	require.True(t, ok, "schema requires a usable recall hint")
	delete(mark, "hint")
	require.Equal(t, map[string]any{
		"error": "tool_result_recall_mark", "tool": "r1_tool", "tool_call_id": id,
		"archive_line": float64(line), "size_chars": float64(size),
		"turn": float64(turn), "content_state": "emptied",
	}, mark, "full schema payload addresses the original live result, never a compacted/excluded slot")
	require.Contains(t, hint, "recall_conversation", "schema hint names the retrieval tool")
	require.Regexp(t, fmt.Sprintf(`\btool_call_id["']?\s*[:=]\s*["']%s["']`, regexp.QuoteMeta(id)), hint, "hint identifies this exact retained result, not an ID substring")
	require.Regexp(t, fmt.Sprintf(`\barchive_line["']?\s*[:=]\s*%d(?:\s|[,{}()]|$)`, line), hint, "hint uses the exact original address, accepting whitespace but not another integer with the same prefix")
}
