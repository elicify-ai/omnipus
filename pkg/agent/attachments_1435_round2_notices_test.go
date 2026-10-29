package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief R2 + independent CHECK: freeze the complete promised marker, not just
// a loose 'unavailable' substring. This is the existing visible marker whose
// preservation through admission was explicitly commissioned.
const issue1435R2ExactMarker = "[attachment unavailable: browser-screenshot.png (image too large to fit the model's size limits, even after resizing)]"

func TestIssue1435R2_UnfixableImageMarkerReachesModelLiveAndReplay(t *testing.T) {
	provider := &issue1435R2Provider{}
	h := wu22NewHarness(t, provider)
	cat := issue1435R2Catalog(t, 30_000)
	// 8193^2 = 67,125,249 pixels, above the budgeted 64-Mpixel band.
	// The fixture is a valid stdlib-encoded PNG, not a forged header.
	rr, ex := issue1435R2AdmitScreenshot(t, h, cat, 8193, 8193)
	require.Empty(t, ex.toolResultMsg.Media, "unfixable image is omitted, but its explanation must remain")
	rr.rq.prepareCallMessages()
	rr.rq.ri.rf.rt.activeProvider = provider
	rr.rq.ri.rf.rt.activeCandidates = nil
	h.al.fallback = nil
	_, err := rr.rq.ri.rf.rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "instrument: a real provider input was captured")
	modelText := issue1435R2ToolMessage(t, provider.requests[0].messages).Content
	assert.Contains(t, modelText, "Screenshot captured for requested page.", "original tool text must survive")
	assert.Contains(t, modelText, issue1435R2ExactMarker,
		"#1435 R2: exact honest marker was discarded before the actual model input")

	h.mu.Lock()
	var visible []string
	for _, event := range h.events {
		switch payload := event.Payload.(type) {
		case ToolExecEndPayload:
			if payload.ToolCallID == "round2-screenshot" {
				visible = append(visible, payload.Result)
			}
		case ErrorPayload:
			visible = append(visible, payload.Message)
		}
	}
	h.mu.Unlock()
	assert.Contains(t, strings.Join(visible, "\n"), issue1435R2ExactMarker,
		"#1435 R2: the exact notice must reach an actual outgoing user-visible event")
	reopened, err := session.NewUnifiedStore(h.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	entries, err := reopened.ReadTranscript(h.sid)
	require.NoError(t, err)
	var replay []string
	for _, entry := range entries {
		if entry.Status == "error" {
			replay = append(replay, entry.Content)
		}
		for _, call := range entry.ToolCalls {
			if call.ID == "round2-screenshot" {
				text, ok := call.Result["text"].(string)
				require.True(t, ok, "actual replayed tool result must preserve its text")
				replay = append(replay, text)
			}
		}
	}
	assert.Contains(t, strings.Join(replay, "\n"), issue1435R2ExactMarker,
		"#1435 R2: exact notice must survive persisted/reopened user replay, not just a helper scratch message")
}
