package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
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
