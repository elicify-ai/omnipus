package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func issue1435R3PatchlessCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	var input map[string]any
	require.NoError(t, json.Unmarshal(visionCatalogJSON(t, "tight", "scripted-model"), &input))
	// Absence means unknown patch accounting, not "images unsupported".
	limits := map[string]any{"long_edge_px": 6000, "max_bytes": 5 << 20}
	input["default_resize_limits"] = limits
	rows, ok := input["providers"].([]any)
	require.True(t, ok)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	row["resize_limits"] = limits
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	require.Equal(t, catalog.ResizeLimits{LongEdgePx: 6000, MaxBytes: 5 << 20}, cat.Resolve("tight", "scripted-model").Budget())
	return cat
}

func TestIssue1435R3_PatchlessBudgetKeepsFullPageToolImage(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"small_image_control", 32, 32}, {"full_page", 2560, 15424}} {
		t.Run(size.name, func(t *testing.T) {
			provider := &issue1435R2Provider{}
			h := wu22NewHarness(t, provider)
			cat := issue1435R3PatchlessCatalog(t)
			h.agent.Candidates = []providers.FallbackCandidate{{Provider: "tight", Model: "scripted-model"}}
			h.agent.StoreProviderPool(map[string]providers.LLMProvider{"tight": provider})
			rr, admitted := issue1435R2AdmitScreenshot(t, h, cat, size.width, size.height)
			require.Len(t, admitted.toolResultMsg.Media, 1,
				"#1435 R3 item 4: an unknown patch cap must not discard a valid image that can be downscaled to the known byte/edge limits")
			rr.rq.ri.rf.rt.activeCandidates = h.agent.Candidates
			rr.rq.ri.rf.rt.activeProvider = provider
			rr.rq.prepareCallMessages()
			_, err := rr.rq.ri.rf.rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
			require.NoError(t, err)
			require.Len(t, provider.requests, 1)
			msg := issue1435R2ToolMessage(t, provider.requests[0].messages)
			require.Len(t, msg.Media, 1, "selected patch-less budget must preserve the actual provider input")
			width, height, bytes := issue1435DecodeOutboundImage(t, msg.Media[0])
			budget := cat.Resolve("tight", "scripted-model").Budget()
			require.LessOrEqual(t, max(width, height), budget.LongEdgePx)
			require.LessOrEqual(t, bytes, budget.MaxBytes)
			require.LessOrEqual(t, width, size.width)
			require.LessOrEqual(t, height, size.height)
		})
	}
}

func TestIssue1435R3_NoticeDedupesAcrossInitialAndSteeringResolution(t *testing.T) {
	for _, tc := range []struct {
		name             string
		steeringDistinct bool
		wantNotices      int
	}{
		{"same_ref_at_two_sites", false, 1},
		{"distinct_ref_still_gets_its_notice", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The provider is never called: both resolution sites run through
			// the real conductor stages before any send.
			provider := &issue1435R2Provider{}
			h := wu22NewHarness(t, provider)
			store := media.NewFileMediaStore()
			t.Cleanup(store.Stop)
			h.al.SetMediaStore(store)
			acceptedRef := issue1435R2ImageRef(t, store, h.sid, 32, 32)
			steeringRefs := []string{acceptedRef}
			if tc.steeringDistinct {
				steeringRefs = append(steeringRefs, issue1435R2ImageRef(t, store, h.sid, 32, 32))
			}
			require.NoError(t, store.ReleaseAll(media.SessionInlineScopePrefix+h.sid))
			for _, ref := range append([]string{acceptedRef}, steeringRefs...) {
				_, err := store.Resolve(ref)
				require.ErrorIs(t, err, media.ErrNotFound, "instrument: every fixture ref must really be missing")
			}
			h.ts.media, h.ts.opts.Media = []string{acceptedRef}, []string{acceptedRef}
			h.ts.userMessage = "Inspect this attachment and continue with the text."
			countLive := func() int {
				h.mu.Lock()
				defer h.mu.Unlock()
				var notices []ErrorPayload
				for _, event := range h.events {
					if payload, ok := event.Payload.(ErrorPayload); ok && payload.Code == "media_unavailable" {
						notices = append(notices, payload)
					}
				}
				return len(notices)
			}

			// Site 1: the REAL initial-assembly stage — assembleInitialContext
			// -> resolveMediaRefsWithOffload -> notifyMediaUnavailable. Never
			// the notifier called directly. turnMediaStore MUST be wired: the
			// production resolver's nil-store guard would skip resolution.
			rr := h.responseStage()
			rr.rq.ri.turnMediaStore = store
			rr.rq.ri.wsDir = t.TempDir()
			rp := &agentLoopRunTurnPrepare{rc: &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: rr}}}
			_, stopped := rp.assembleInitialContext()
			require.False(t, stopped, "instrument: initial assembly must complete")
			require.Equal(t, 1, countLive(), "site 1 must emit exactly one live notice for the accepted-but-missing ref")

			// Site 3: the REAL per-iteration injection stage — beginIteration
			// -> resolveMediaRefsWithOffload over pending steering input (the
			// same production input the cwR1 continuation tests drive).
			const steeringText = "Round3 second resolution site: inspect the attachment again."
			ri := rr.rq.ri
			ri.pendingMessages = []providers.Message{{Role: "user", Content: steeringText, Media: steeringRefs}}
			ri.pendingSteeringReceipts = []string{"issue-1435-r3-steer"}
			require.Equal(t, agentLoopRunTurnIterationNext, ri.beginIteration(),
				"instrument: the real injection stage must complete after resolving the steering attachment")
			var steeringResolved string
			for _, message := range ri.messages {
				if strings.Contains(message.Content, steeringText) {
					steeringResolved = message.Content
				}
			}
			require.Contains(t, steeringResolved, "[attachment unavailable:",
				"instrument: site 3 really consumed the missing ref through the production resolver")
			require.Empty(t, provider.requests, "sanity: no provider involvement exists in this design")

			require.Equal(t, tc.wantNotices, countLive(),
				"#1435 R3 item 5: exactly one live notice per missing ref per turn, even across initial AND steering resolution")
			entries, err := h.store.ReadTranscript(h.sid)
			require.NoError(t, err)
			var persisted []session.TranscriptEntry
			for _, entry := range entries {
				if entry.ErrorCode == "media_unavailable" {
					persisted = append(persisted, entry)
				}
			}
			require.Len(t, persisted, tc.wantNotices, "#1435 R3 item 5: dedupe must also hold in actual persisted user replay")
			t.Logf("two real resolution sites: live=%d persisted=%d want=%d", countLive(), len(persisted), tc.wantNotices)
		})
	}
}
