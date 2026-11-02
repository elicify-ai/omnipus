package agent

import (
	"crypto/sha256"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

var issue1435R2BudgetCases = []struct {
	name                   string
	width, height, patches int
}{
	{"selected_edge", 6144, 512, 30_000}, // Below default 7680, above selected 6000.
	{"selected_patches", 512, 512, 128},  // 256 source patches, below default 30000 but above selected 128.
}

func TestIssue1435R2_FreshToolAdmissionUsesSelectedBudget(t *testing.T) {
	for _, tc := range issue1435R2BudgetCases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &issue1435R2Provider{}
			h := wu22NewHarness(t, provider)
			cat := issue1435R2Catalog(t, tc.patches)
			h.agent.Candidates = []providers.FallbackCandidate{{Provider: "tight", Model: "scripted-model"}}
			h.agent.StoreProviderPool(map[string]providers.LLMProvider{"tight": provider})
			_, admitted := issue1435R2AdmitScreenshot(t, h, cat, tc.width, tc.height)
			issue1435R2AssertBudget(t, admitted.toolResultMsg, cat.Resolve("tight", "scripted-model").Budget(), "real tool-result admission")
		})
	}
}

func TestIssue1435R2_EveryRealSendUsesSelectedCandidateBudget(t *testing.T) {
	for _, tc := range issue1435R2BudgetCases {
		for _, path := range []string{"plain_chat", "plain_stream", "single_candidate_chain", "multi_candidate_primary", "multi_candidate_fallback"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				provider := &issue1435R2Provider{failLoose: path == "multi_candidate_fallback"}
				h := wu22NewHarness(t, provider)
				cat := issue1435R2Catalog(t, tc.patches)
				// A FRESH screenshot is admitted while the looser provider owns
				// the tool round. The next selected/candidate request is tighter.
				// Admission-only fixes must not hide a missing send-time fit.
				h.agent.Model = "loose-model"
				h.agent.Candidates = []providers.FallbackCandidate{{Provider: "loose", Model: "loose-model"}}
				h.agent.StoreProviderPool(map[string]providers.LLMProvider{"loose": provider, "tight": provider})
				rr, admitted := issue1435R2AdmitScreenshot(t, h, cat, tc.width, tc.height)
				require.Len(t, admitted.toolResultMsg.Media, 1)
				before := sha256.Sum256(issue1435R2EncodedImage(t, admitted.toolResultMsg.Media[0]))
				tight := providers.FallbackCandidate{Provider: "tight", Model: "scripted-model"}
				loose := providers.FallbackCandidate{Provider: "loose", Model: "loose-model"}
				candidates := []providers.FallbackCandidate{tight}
				switch path {
				case "multi_candidate_primary":
					candidates = []providers.FallbackCandidate{tight, loose}
				case "multi_candidate_fallback":
					candidates = []providers.FallbackCandidate{loose, tight}
				}
				h.agent.Model, h.agent.Candidates = candidates[0].Model, candidates
				rt := rr.rq.ri.rf.rt
				rt.activeCandidates, rt.activeProvider, rt.llmModel = candidates, provider, candidates[0].Model
				if path == "plain_chat" || path == "plain_stream" {
					h.al.fallback = nil
				} else {
					h.al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker())
				}
				if path == "plain_stream" {
					h.al.bus.SetStreamDelegate(&identityStampStreamDelegate{streamer: h.stream})
				}
				rr.rq.prepareCallMessages()
				response, err := rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
				require.NoError(t, err, "instrument: real send/candidate selection must reach the capture")
				require.Equal(t, "request processed", response.Content)
				var tightRequests []issue1435R2Request
				for _, request := range provider.requests {
					if request.model == "scripted-model" {
						tightRequests = append(tightRequests, request)
					}
				}
				require.Len(t, tightRequests, 1, "instrument: selected tighter candidate must actually be called")
				wantMethod := "Chat"
				if path == "plain_stream" {
					wantMethod = "ChatStream"
				}
				require.Equal(t, wantMethod, tightRequests[0].method, "exercise the real named transport path")
				canonical := issue1435R2ToolMessage(t, h.store.GetHistory(h.sid))
				require.Len(t, canonical.Media, 1, "do not erase the original durable image to fit a request")
				require.Equal(t, before, sha256.Sum256(issue1435R2EncodedImage(t, canonical.Media[0])), "send preparation must preserve canonical history")
				issue1435R2AssertBudget(t, issue1435R2ToolMessage(t, tightRequests[0].messages), cat.Resolve("tight", "scripted-model").Budget(), path)
			})
		}
	}
}
