// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the hub delivery gate (spec C2/C8/D29/D31, FR-002,
// section 16 items 8/37(b) and 38, gated boundary matrix rows 1-3, Group B).
// The pinned production symbols are
// pkg/gateway/ws_session_hub.go::publishMeta (thinking flag on hubFrameMeta)
// and pkg/gateway/ws_session_hub.go::bind (journal-tail substitution).
//
// Oracles:
//   - a gate-off connection receives exactly {type, session_id, seq} in
//     place of each hidden thinking frame - zero thinking bytes - live AND
//     in journal-tail replay; sequence stays contiguous (D29).
//   - a gate-on connection receives the thinking frame BYTE-IDENTICAL.
//   - D31: a newer thinking frame for the same entry_id supersedes the prior
//     full payload in the journal; the prior slots become seq_skip at their
//     original sequences; the journal keeps ONE full payload per thinking row.
//   - supersession never touches another session's journal.
//
// RED status: compile-fail on the new hubFrameMeta.thinking/entryID fields.

// thinkingSentinelGateway is the recognised credential shape used across the
// WP-C gateway thinking tests as the leak probe.
const thinkingSentinelGateway = "sk-live-abcd1234EFGH"

// wpcGateHubConn is a hubConn fake with a per-connection gate flag - the
// shape *wsConn will have once showThinking() lands (dispatch-pinned).
type wpcGateHubConn struct {
	received [][]byte
	show     bool
}

func (c *wpcGateHubConn) enqueue(frame []byte) bool {
	c.received = append(c.received, frame)
	return true
}

func (c *wpcGateHubConn) showThinking() bool { return c.show }

func wpcThinkingFrame(t *testing.T, entryID, text string) []byte {
	t.Helper()
	b, err := json.Marshal(generated.ThinkingFrame{
		Type:           "thinking",
		SessionId:      "wpc-hub-sess",
		EntryId:        entryID,
		Text:           text,
		ElapsedMs:      120,
		ThinkingTokens: wpcIntPtr(9),
	})
	require.NoError(t, err)
	return b
}

func wpcDecodeFrame(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(frame, &m), "frame: %s", frame)
	return m
}

func wpcIntPtr(i int) *int { return &i }

func wpcInt64p(v int64) *int64 { return &v }

func wpcFrameType(m map[string]any) string {
	if v, ok := m["type"].(string); ok {
		return v
	}
	return ""
}

func wpcFrameSeq(m map[string]any) uint64 {
	if v, ok := m["seq"].(float64); ok {
		return uint64(v)
	}
	return 0
}

func TestHub_ThinkingGate_LiveSubstitutionAndByteIdentity(t *testing.T) {
	reg := newHubRegistry("wpc-boot")
	hub := reg.getOrCreate("wpc-hub-sess")

	gated := &wpcGateHubConn{show: false}
	ungated := &wpcGateHubConn{show: true}
	_, ok1 := hub.bindLive(gated)
	require.True(t, ok1)
	_, ok2 := hub.bindLive(ungated)
	require.True(t, ok2)

	frame := wpcThinkingFrame(t, "entry-1", "reasoning probe sk-live-abcd1234EFGH tail")
	_, out := hub.publishMeta(hubFrameMeta{thinking: true, entryID: "entry-1"}, frame)

	// Gate-on connection: byte-identical thinking frame.
	require.Len(t, ungated.received, 1)
	assert.Equal(t, string(out), string(ungated.received[0]),
		"the gate-on connection receives the frame byte-identical (ADR-095: narrowing is thinking-only)")

	// Gate-off connection: exactly one substitution, zero thinking bytes.
	require.Len(t, gated.received, 1, "the gated connection still receives ONE frame at the position (contiguity)")
	skip := wpcDecodeFrame(t, gated.received[0])
	assert.Equal(t, "seq_skip", wpcFrameType(skip), "the hidden frame becomes seq_skip (C8)")
	assert.Equal(t, "wpc-hub-sess", skip["session_id"], "seq_skip carries session_id")
	assert.Equal(t, float64(1), skip["seq"], "seq_skip occupies the original sequence position")
	assert.Len(t, skip, 3, "seq_skip is EXACTLY {type, session_id, seq} - zero thinking bytes (byte inspection)")
	_, hasText := skip["text"]
	assert.False(t, hasText, "seq_skip carries no text")
	_, hasEntry := skip["entry_id"]
	assert.False(t, hasEntry, "seq_skip carries no entry_id")

	// The published frame never carries the gate's mutation: the ungated
	// frame really was the thinking frame.
	assert.Equal(t, "thinking", wpcFrameType(wpcDecodeFrame(t, ungated.received[0])))
	_ = out
}

func TestHub_ThinkingGate_JournalTailSubstitution(t *testing.T) {
	reg := newHubRegistry("wpc-boot")
	hub := reg.getOrCreate("wpc-hub-sess")

	// Journal: thinking(seq1), answer(seq2).
	_, outThink := hub.publishMeta(hubFrameMeta{thinking: true, entryID: "e1"},
		wpcThinkingFrame(t, "e1", "catch-up probe"))
	_, outAnswer := hub.publishMeta(hubFrameMeta{}, []byte(`{"type":"token","content":"answer"}`))

	// Gate-on attach: the journal tail carries the full thinking payload.
	tailOn := hub.bind(&wpcGateHubConn{show: true}, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, tailOn.Servable)
	require.Len(t, tailOn.Tail, 2, "gate-on attach gets both frames")
	assert.Equal(t, string(outThink), string(tailOn.Tail[0]), "gate-on tail keeps the thinking payload byte-identical")
	assert.Equal(t, string(outAnswer), string(tailOn.Tail[1]))

	// Gate-off attach: the hidden frame is a content-free skip in the tail.
	tailOff := hub.bind(&wpcGateHubConn{show: false}, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, tailOff.Servable)
	require.Len(t, tailOff.Tail, 2, "the tail keeps every sequence position (contiguity, D29)")
	skip := wpcDecodeFrame(t, tailOff.Tail[0])
	assert.Equal(t, "seq_skip", wpcFrameType(skip))
	assert.Len(t, skip, 3, "the tail skip frame carries only {type, session_id, seq}")
	assert.Equal(t, "token", wpcFrameType(wpcDecodeFrame(t, tailOff.Tail[1])),
		"the answer frame is never substituted - only hidden thinking frames become skips (D29)")
	assert.Equal(t, string(outAnswer), string(tailOff.Tail[1]),
		"the answer frame arrives byte-identical to a gate-off attach too")
}

func TestHub_ThinkingGate_JournalTailContiguity(t *testing.T) {
	reg := newHubRegistry("wpc-boot")
	hub := reg.getOrCreate("wpc-hub-sess")

	for i := 0; i < 3; i++ {
		hub.publishMeta(hubFrameMeta{thinking: true, entryID: "e1"},
			wpcThinkingFrame(t, "e1", "chunk"))
		hub.publishMeta(hubFrameMeta{}, []byte(`{"type":"token","content":"a"}`))
	}

	conn := &wpcGateHubConn{show: false}
	res := hub.bind(conn, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, res.Servable)
	require.NotEmpty(t, res.Tail)

	// Every seq 1..head arrives exactly once, in order, with no gaps.
	var seqs []int
	for _, b := range res.Tail {
		m := wpcDecodeFrame(t, b)
		tt := wpcFrameType(m)
		require.Contains(t, []string{"seq_skip", "thinking", "token"}, tt, "unexpected frame type %q in tail: %s", tt, b)
		seqs = append(seqs, int(wpcFrameSeq(m)))
	}
	require.Len(t, seqs, 6)
	for i, s := range seqs {
		assert.Equal(t, i+1, s, "sequence must be gap-free: position %d carries seq %d", i, s)
	}
	assert.Equal(t, "seq_skip", wpcFrameType(wpcDecodeFrame(t, res.Tail[0])),
		"the hidden thinking frame occupies its sequence position as a skip")
	assert.Equal(t, "thinking", wpcFrameType(wpcDecodeFrame(t, res.Tail[4])),
		"a later thinking frame still arrives as thinking (supersession keeps the newest full payload, D31)")
}

func TestHub_D31_SupersessionKeepsOneFullPayloadPerEntry(t *testing.T) {
	reg := newHubRegistry("wpc-boot")
	hub := reg.getOrCreate("wpc-hub-sess")

	// Three thinking updates for the same entry, then an answer.
	hub.publishMeta(hubFrameMeta{thinking: true, entryID: "e1"}, wpcThinkingFrame(t, "e1", "first probe"))
	hub.publishMeta(hubFrameMeta{thinking: true, entryID: "e1"}, wpcThinkingFrame(t, "e1", "second probe"))
	_, latest := hub.publishMeta(hubFrameMeta{thinking: true, entryID: "e1"}, wpcThinkingFrame(t, "e1", "third probe"))
	_, answer := hub.publishMeta(hubFrameMeta{}, []byte(`{"type":"token","content":"the answer"}`))

	// Gate-on attach: skips at the two superseded seqs, ONE full payload (the
	// newest), then the answer - in original sequence order.
	tailOn := hub.bind(&wpcGateHubConn{show: true}, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, tailOn.Servable)
	require.Len(t, tailOn.Tail, 4, "all four sequence positions survive supersession")
	assert.Equal(t, "seq_skip", wpcFrameType(wpcDecodeFrame(t, tailOn.Tail[0])), "superseded seq 1 becomes a skip")
	assert.Equal(t, "seq_skip", wpcFrameType(wpcDecodeFrame(t, tailOn.Tail[1])), "superseded seq 2 becomes a skip")
	assert.Equal(t, "thinking", wpcFrameType(wpcDecodeFrame(t, tailOn.Tail[2])), "the newest payload stays full")
	assert.Equal(t, string(latest), string(tailOn.Tail[2]), "the surviving full payload is the NEWEST frame's bytes")
	assert.Equal(t, string(answer), string(tailOn.Tail[3]), "answer catch-up stays intact after supersession")

	// Gate-off attach: every thinking position is a skip; the answer survives.
	tailOff := hub.bind(&wpcGateHubConn{show: false}, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, tailOff.Servable)
	require.Len(t, tailOff.Tail, 4)
	for _, idx := range []int{0, 1, 2} {
		m := wpcDecodeFrame(t, tailOff.Tail[idx])
		assert.Equal(t, "seq_skip", wpcFrameType(m), "gate-off: thinking position %d is a skip", idx)
		assert.Len(t, m, 3, "gate-off skip carries only {type, session_id, seq}")
	}
	assert.Equal(t, string(answer), string(tailOff.Tail[3]), "gate-off: answer catch-up intact")

	// D31 keeps the newest payload's TEXT (not an older chunk).
	m := wpcDecodeFrame(t, tailOn.Tail[2])
	assert.Contains(t, m["text"], "third probe", "the surviving full payload is the newest thinking text")

	// A second session's journal is untrimmed by this supersession.
	hub2 := reg.getOrCreate("wpc-hub-sess-2")
	_, otherFrame := hub2.publishMeta(hubFrameMeta{}, []byte(`{"type":"token","content":"other"}`))
	tailOther := hub2.bind(&wpcGateHubConn{show: true}, wpcInt64p(0), hubStrp("wpc-boot"), "wpc-boot")
	require.True(t, tailOther.Servable)
	require.Len(t, tailOther.Tail, 1)
	assert.Equal(t, string(otherFrame), string(tailOther.Tail[0]),
		"D31 supersession on one session never trims another session's journal")

	_ = sort.Ints
}
