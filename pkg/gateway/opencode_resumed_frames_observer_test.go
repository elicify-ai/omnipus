package gateway

import (
	"encoding/json"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// not-wire-format: observations of real hub output; wire payloads use the
// generated types. Keep every sequenced child frame, not just token/done, so
// a status or tool frame cannot create a false sequence-gap assertion.
type opencode1895LiveObservation struct {
	raw    [][]byte
	seqs   []int64
	tokens []gen.TokenFrame
	done   []gen.DoneFrame
	errors []gen.ErrorFrame
	ready  bool
	text   string
}

func (o *opencode1895LiveObservation) observe(t *testing.T, raw []byte, sessionID string) {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	var kind, sid string
	require.NoError(t, json.Unmarshal(fields["type"], &kind))
	if value, ok := fields["session_id"]; ok {
		require.NoError(t, json.Unmarshal(value, &sid))
	}
	switch kind {
	case "token", "done", "error", "tool_call_start", "tool_call_result":
		require.Equal(t, sessionID, sid, "conversation frames must stay in the subscribed helper chat")
	}
	if sid != sessionID {
		return // Non-conversation broadcasts are outside this session's sequence.
	}
	o.raw = append(o.raw, append([]byte(nil), raw...))
	if value, ok := fields["seq"]; ok {
		var seq int64
		require.NoError(t, json.Unmarshal(value, &seq))
		require.Positive(t, seq, "contract: live sequence numbers start above zero")
		if len(o.seqs) > 0 {
			assert.Equal(t, o.seqs[len(o.seqs)-1]+1, seq, "#1895: no dropped or reordered session frame across interrupt/resume")
		}
		o.seqs = append(o.seqs, seq)
	}
	switch kind {
	case "tool_call_result":
		var result gen.ToolCallResultFrame
		require.NoError(t, json.Unmarshal(raw, &result))
		o.ready = o.ready || result.CallId == "fixture-ready-1895"
	case "token":
		var token gen.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &token))
		require.NotNil(t, token.Seq, "a live token is not an unsequenced snapshot copy")
		o.tokens = append(o.tokens, token)
		o.text += token.Content
	case "done":
		var done gen.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &done))
		require.NotNil(t, done.Seq, "a live done frame must share the session sequence")
		o.done = append(o.done, done)
	case "error":
		var failure gen.ErrorFrame
		require.NoError(t, json.Unmarshal(raw, &failure))
		o.errors = append(o.errors, failure)
	}
}

func (o *opencode1895LiveObservation) readUntil(t *testing.T, viewer *wsConn, sessionID, label string, reached func() bool) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second) // Deadlock failsafe, never a latency oracle.
	defer timer.Stop()
	for !reached() {
		select {
		case raw := <-viewer.sendCh:
			o.observe(t, raw, sessionID)
		case <-timer.C:
			t.Fatalf("#1895: %s; text=%q tokens=%d done=%d errors=%v sequences=%v", label, o.text, len(o.tokens), len(o.done), o.errors, o.seqs)
		}
	}
}

func (o *opencode1895LiveObservation) drain(t *testing.T, viewer *wsConn, sessionID string) {
	t.Helper()
	for {
		select {
		case raw := <-viewer.sendCh:
			o.observe(t, raw, sessionID)
		default:
			return
		}
	}
}

func (o *opencode1895LiveObservation) doneForTurn(turnID string) []gen.DoneFrame {
	var result []gen.DoneFrame
	for _, done := range o.done {
		if done.TurnId != nil && *done.TurnId == turnID {
			result = append(result, done)
		}
	}
	return result
}
