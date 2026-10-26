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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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

func orphanACSnapshot(t *testing.T, h *cwR1Harness) session.WindowView {
	t.Helper()
	snap, err := h.store.WindowView(context.Background(), h.key)
	require.NoError(t, err, "read exact real-store snapshot")
	return snap
}

// orphanACSurvivingLive is the slots of before a trim at after.State.Skip must
// keep: those whose Ordinal is at or above the new Skip. Filter by ORDINAL, not
// by indexing the slice at Skip — a canceled, excluded slot can sit inside the
// evicted prefix, so Skip is not a count of dropped Live entries
// (ARCHITECT-ANSWER-FINAL-GATE.md item 4d).
func orphanACSurvivingLive(before, after session.WindowView) []session.ModelSlot {
	out := make([]session.ModelSlot, 0, len(before.Live))
	for _, slot := range before.Live {
		if slot.Ordinal >= after.State.Skip {
			out = append(out, slot)
		}
	}
	return out
}

// orphanACAssertUnsupportedSplit re-points an unsupported-recovery case whose
// offending record can no longer be STORED (ARCHITECT-ANSWER-FINAL-GATE.md item
// 4d, the same re-point as 4c). The archive holds only the admissible records, so
// the invalid sequence is built IN MEMORY for the send boundary, and the refusal
// of the offending record at ADMISSION is asserted separately — that boundary is
// what made the old fully-seeded fixture lose its record silently.
// orphanACAssertStandaloneOrphan re-points the leaf whose store-derived
// candidate cannot show the fault: a standalone tool result followed by a marker
// for the same id. assembleMessages reads the ARCHIVE (which deliberately cannot
// hold this record), so it hands back a sequence with no tool message at all and
// both prepareCallMessages and callProviderOnce see nothing wrong. The sequence
// is therefore built BY HAND — the ruling's "invalid sequence built in memory" —
// and callProviderOnce, which validates exactly what it is given, still refuses
// it with the original expected text.
//
// What is asserted:
//   - the orphan result is refused at ADMISSION, and the archive does not move;
//   - the send boundary refuses the marker-bound sequence with the same specific
//     refusal the original case expected.
func orphanACAssertStandaloneOrphan(t *testing.T, prefix []providers.Message, suffix []providers.Message, wantErr string) {
	t.Helper()
	h := cwR1New(t, 100000)
	h.append(t, prefix...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	ts := h.turn("")
	orphanACAssertView(t, h, ts, prefix)

	// (i) admission refuses the orphan result itself.
	_, _, aerr := h.store.AppendModelMessage(context.Background(), h.key, session.ModelAppend{
		Message:        suffix[0],
		ViewMembership: session.ViewMembershipBoth,
		Source:         session.EntrySource{Kind: "user"},
	})
	require.Error(t, aerr, "admission must refuse a tool result that names no issuing assistant")

	// (ii) the send boundary refuses the same sequence, built in memory.
	seq := make([]providers.Message, 0, 1+len(prefix)+len(suffix))
	seq = append(seq, providers.Message{Role: "system", Content: "PINNED instruction must remain"})
	seq = append(seq, prefix...)
	seq = append(seq, suffix...)
	r, provider := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, seq, provider)
	_, cerr := rr.rq.ri.rf.rt.callProviderOnce(seq, nil)
	require.EqualError(t, cerr, wantErr,
		"the send boundary must refuse the invalid structure built in memory")
	require.Len(t, r.requests(t), 0, "a rejected request reaches no provider")

	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACAssertUnsupportedSplit(t *testing.T, admissible []providers.Message, bad providers.Message, trailing []providers.Message, wantErr string, storeDerived bool) {
	t.Helper()
	h := cwR1New(t, 100000)
	h.append(t, admissible...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	ts := h.turn("")

	// The archive's own view holds exactly the admissible records.
	orphanACAssertView(t, h, ts, admissible)

	// The send boundary must still refuse the invalid sequence, with the
	// offending record exactly where a producer bug would place it. The
	// sequence passed on is the ASSEMBLED view (system envelope + records), so
	// the message index in the refusal matches the fully-seeded cases.
	seq := append([]providers.Message(nil), admissible...)
	seq = append(seq, bad)
	seq = append(seq, trailing...)
	assembled := orphanACAssemble(t, h, ts, seq)
	if storeDerived {
		orphanACAssertRejectedBeforeSend(t, h, ts, assembled, wantErr)
	} else {
		// The store-derived candidate cannot see this record: prepareCallMessages
		// re-derives from the archive, and the refused record is deliberately not
		// there. The send boundary is therefore asserted directly on the
		// assembled sequence, which callProviderOnce validates itself.
		// (ARCHITECT-ANSWER-FINAL-GATE.md item 4c anticipated exactly this.)
		r, provider := cwR1OpenAI(t, 0)
		rr := cwR1Flow(h, ts, assembled, provider)
		_, cerr := rr.rq.ri.rf.rt.callProviderOnce(assembled, nil)
		require.EqualError(t, cerr, wantErr, "the send boundary must refuse the invalid structure built in memory")
		require.Len(t, r.requests(t), 0, "a rejected request reaches no provider")
	}

	// And the same record is refused when SAVED.
	_, _, aerr := h.store.AppendModelMessage(context.Background(), h.key, session.ModelAppend{
		Message:        bad,
		ViewMembership: session.ViewMembershipBoth,
		Source:         session.EntrySource{Kind: "user"},
	})
	require.Error(t, aerr, "admission must refuse a record that cannot name its issuing assistant")
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACArchiveBytes(t *testing.T, h *cwR1Harness) []byte {
	t.Helper()
	// U2 Decision D / DEL-12: the ONE addressed archive is
	// <baseDir>/<owning session id>/transcript.jsonl (archive_day_store.go);
	// the retired model backend wrote <baseDir>/<key>.jsonl. h.key ("cw-r1")
	// carries no ":session:" segment, so owningSessionID leaves it unchanged.
	data, err := os.ReadFile(filepath.Join(h.dir, h.key, "transcript.jsonl"))
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

func orphanACAssertUnchanged(t *testing.T, h *cwR1Harness, before session.WindowView, bytes []byte) {
	t.Helper()
	require.Equal(t, before, orphanACSnapshot(t, h), "pure recovery view changes neither archive nor captured Skip/Count/AnchorLine/projection metadata")
	require.Equal(t, bytes, orphanACArchiveBytes(t, h), "recovery never rewrites, deletes or fabricates archive bytes")
}

func orphanACReopen(t *testing.T, h *cwR1Harness) {
	t.Helper()
	reloaded, err := session.NewUnifiedStore(h.dir)
	require.NoError(t, err, "reload uses a fresh real store reading persisted archive and metadata")
	t.Cleanup(func() { require.NoError(t, reloaded.Close(), "close reloaded store") })
	h.store = reloaded
	h.agent.Sessions = reloaded
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

// orphanACView5 is an expected DESIGN-5 model view with the archive address of
// every entry.
//
// DESIGN 5 and its 2026-10-26 addendum "Trim shares the projection" fix the
// projection shape:
//   - the assistant declaration is RETAINED;
//   - every durable sibling result is RETAINED, bound to the declaration that
//     issued it;
//   - a recognised cancellation record (the marker) is EXCLUDED — it is
//     bookkeeping, replaced by the observation;
//   - the occurrence a bound cancellation left without a result gains the
//     engine-authored recovery observation, at ordinal -1;
//   - every retained record keeps its ORIGINAL archive address.
type orphanACView5 struct {
	messages  []providers.Message
	addresses []int
}

// orphanACObservation returns the engine observation the DESIGN-5 projection
// inserts for callID. It is taken from the real recovered view — the
// observation is a fixture, like the archive records it completes; only its
// specified fields are checked (r2CheckedObservation), never its prose.
func orphanACObservation(t *testing.T, h *cwR1Harness, ts *turnState, callID string) providers.Message {
	t.Helper()
	out := orphanACAssemble(t, h, ts, h.agent.Sessions.GetHistory(h.key))
	return orphanACFindObservation(t, out[1:], callID)
}

// orphanACObservations returns every engine observation in a projected view,
// in view order. Two occurrences can share one provider call id, so the
// observations are addressed by position; each one is checked separately with
// r2CheckedObservation against its own issuing occurrence.
func orphanACObservations(t *testing.T, view []providers.Message) []providers.Message {
	t.Helper()
	out := make([]providers.Message, 0, 2)
	for _, m := range view {
		if m.Role == "tool" && strings.Contains(m.Content, "tool_recovery_observation") {
			out = append(out, m)
		}
	}
	return out
}

// orphanACDesign5ViewAt builds the expected DESIGN-5 model view for a fixture
// that carries one or more engine observations.
//
// DESIGN 5 (and its 2026-10-26 "Trim shares the projection" addendum): the
// retained raw archive lines keep their order and their ORIGINAL addresses;
// every observation the projection inserts is ephemeral (ordinal -1) and sits
// where the projection puts it — next to the declaration of the occurrence it
// completes. obsAt maps a RETAINED-line index to the observations inserted
// immediately before that line, in the order the projection inserts them.
func orphanACDesign5ViewAt(t *testing.T, raw []providers.Message, lines []int,
	obsAt map[int][]providers.Message,
) ([]providers.Message, []int) {
	t.Helper()
	view := make([]providers.Message, 0, len(lines)+len(obsAt))
	addresses := make([]int, 0, len(lines)+len(obsAt))
	for i, line := range lines {
		for _, obs := range obsAt[i] {
			view = append(view, obs)
			addresses = append(addresses, -1)
		}
		view = append(view, raw[line])
		addresses = append(addresses, line)
	}
	for _, obs := range obsAt[len(lines)] {
		view = append(view, obs)
		addresses = append(addresses, -1)
	}
	return view, addresses
}

// orphanACAssertAcceptedBeforeSend is the positive counterpart of
// orphanACAssertRejectedBeforeSend: a provider-valid projected view is not
// refused, and the request reaches the real HTTP endpoint.
func orphanACAssertAcceptedBeforeSend(t *testing.T, h *cwR1Harness, ts *turnState, messages []providers.Message) {
	t.Helper()
	r, provider := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, messages, provider)
	rr.rq.prepareCallMessages()
	require.NoError(t, ts.contextWindowError(), "a provider-valid projected group must not be refused")
	conversation := requireConversationBeforeVolatileTail(t, rr.rq.ri.rf.callMessages)
	require.Equal(t, messages, conversation, "the accepted candidate is the projected view, unchanged")
	response, err := rr.rq.ri.rf.rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
	require.NoError(t, err, "the send boundary accepts the projected view")
	require.NotNil(t, response)
	require.Len(t, r.requests(t), 1, "the real HTTP endpoint receives the accepted request")
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
