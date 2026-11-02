package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

type issue1435R2Request struct {
	model, method string
	messages      []providers.Message
}

// Only the paid provider/stream transport is replaced. Captures retain exact
// ordinary image media supplied by the actual production send boundaries.
type issue1435R2Provider struct {
	requests  []issue1435R2Request
	failLoose bool
}

func (*issue1435R2Provider) GetDefaultModel() string { return "scripted-model" }
func (p *issue1435R2Provider) capture(messages []providers.Message, model, method string) (*providers.LLMResponse, error) {
	copyMessages := append([]providers.Message(nil), messages...)
	for i := range copyMessages {
		copyMessages[i].Media = append([]string(nil), messages[i].Media...)
	}
	p.requests = append(p.requests, issue1435R2Request{model: model, method: method, messages: copyMessages})
	if p.failLoose && model == "loose-model" {
		return nil, errors.New("rate limit exceeded") // External-provider fallback stimulus.
	}
	return &providers.LLMResponse{Content: "request processed", FinishReason: "stop"}, nil
}
func (p *issue1435R2Provider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	return p.capture(messages, model, "Chat")
}
func (p *issue1435R2Provider) ChatStream(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.capture(messages, model, "ChatStream")
	if err == nil {
		chunk(response.Content)
	}
	return response, err
}

func issue1435R2Catalog(t *testing.T, patches int) *catalog.Catalog {
	t.Helper()
	// Policy fixtures independently chosen to expose default-only fitting:
	// tight edge=6000 (reviewed ZAI ceiling); tight patches=128 for the
	// patch-only case. Both are stricter than the default 7680 / 30000.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(visionCatalogJSON(t, "tight", "scripted-model"), &doc))
	defaults, ok := doc["default_resize_limits"].(map[string]any)
	require.True(t, ok)
	defaults["max_patches"] = 30_000
	rows, ok := doc["providers"].([]any)
	require.True(t, ok)
	tight, ok := rows[0].(map[string]any)
	require.True(t, ok)
	tight["resize_limits"] = map[string]any{"long_edge_px": 6000, "max_bytes": 5 << 20, "max_patches": patches}
	looseBytes, err := json.Marshal(tight)
	require.NoError(t, err)
	var loose map[string]any
	require.NoError(t, json.Unmarshal(looseBytes, &loose))
	loose["id"], loose["name"] = "loose", "loose"
	loose["resize_limits"] = map[string]any{"long_edge_px": 7680, "max_bytes": 10 << 20, "max_patches": 30_000}
	looseModels, ok := loose["models"].([]any)
	require.True(t, ok)
	looseModel, ok := looseModels[0].(map[string]any)
	require.True(t, ok)
	looseModel["id"] = "loose-model"
	doc["providers"] = []any{tight, loose}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	require.Equal(t, catalog.ResizeLimits{LongEdgePx: 6000, MaxBytes: 5 << 20, MaxPatches: patches}, cat.Resolve("tight", "scripted-model").Budget())
	return cat
}

func issue1435R2ImageRef(t *testing.T, store media.MediaStore, sessionID string, width, height int) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height))))
	path := filepath.Join(t.TempDir(), "browser-screenshot.png")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	ref, err := store.Store(path, media.MediaMeta{
		Filename: "browser-screenshot.png", ContentType: "image/png", Source: "browser_screenshot",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, media.SessionInlineScopePrefix+sessionID)
	require.NoError(t, err)
	return ref
}

func issue1435R2ToolMessage(t *testing.T, messages []providers.Message) providers.Message {
	t.Helper()
	var results []providers.Message
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == "round2-screenshot" {
			results = append(results, message)
		}
	}
	require.Len(t, results, 1, "the real request must contain exactly the issued screenshot's result")
	return results[0]
}

func issue1435R2AssertBudget(t *testing.T, msg providers.Message, budget catalog.ResizeLimits, boundary string) {
	t.Helper()
	require.Len(t, msg.Media, 1, "R1: %s must preserve a real image, not erase it", boundary)
	width, height, size := issue1435DecodeOutboundImage(t, msg.Media[0])
	require.LessOrEqual(t, max(width, height), budget.LongEdgePx,
		"#1435 R1: %s sent %dx%d against selected edge limit %d", boundary, width, height, budget.LongEdgePx)
	require.LessOrEqual(t, issue1435PatchCount(width, height), budget.MaxPatches,
		"#1435 R1: %s sent %d patches against selected limit %d", boundary, issue1435PatchCount(width, height), budget.MaxPatches)
	require.LessOrEqual(t, size, budget.MaxBytes)
}

// Exercise recordToolResult and its real admission/checkpoint/transcript/event
// callers. No direct attach helper or fabricated admitted message is used.
func issue1435R2AdmitScreenshot(t *testing.T, h *wu22Harness, cat *catalog.Catalog, width, height int) (*agentLoopRunTurnResponse, *agentLoopRunTurnToolsExecute) {
	t.Helper()
	h.al.SetCapabilityCatalog(cat)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	h.al.SetMediaStore(store)
	ref := issue1435R2ImageRef(t, store, h.sid, width, height)
	// Real request preparation assumes a leading conversation message. Seed
	// the person's request before the assistant/tool group so injected notes
	// cannot split a group that unrealistically occupied message zero.
	_, err := h.ts.appendWindowMessage(providers.Message{Role: "user", Content: "Capture the requested page."}, windowProducerUser)
	require.NoError(t, err)
	issuer := providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "round2-screenshot", Name: "browser_screenshot", Type: "function"}}}
	_, err = h.ts.appendWindowMessage(issuer, windowProducerAssistant)
	require.NoError(t, err)
	rr := h.responseStage()
	rr.normalizedToolCalls = issuer.ToolCalls
	rr.rq.ri.messages = h.store.GetHistory(h.sid)
	rr.rq.ri.turnMediaStore, rr.rq.ri.turnCatalog = store, cat
	rr.rq.ri.maxMediaSize = 10 << 20 // Fixture operator budget, above both small source images.
	ex := &agentLoopRunTurnToolsExecute{
		rx:       &agentLoopRunTurnTools{ctx: context.Background(), rr: rr},
		toolName: "browser_screenshot", toolCallID: "round2-screenshot",
		toolResult: &tools.ToolResult{ForLLM: "Screenshot captured for requested page.", Media: []string{ref}},
	}
	require.NoError(t, ex.recordToolResult(issuer.ToolCalls[0]), "real tool admission must succeed before image assertions")
	require.Equal(t, agentLoopRunTurnToolsExecuteNext, ex.finishCall(0), "real result must checkpoint and reach live/transcript sinks")
	return rr, ex
}

// Keep the source's data URL available for exact no-mutation controls without
// copying image bytes into assertion-failure output.
func issue1435R2EncodedImage(t *testing.T, url string) []byte {
	t.Helper()
	_, payload, ok := bytes.Cut([]byte(url), []byte(";base64,"))
	require.True(t, ok)
	data, err := base64.StdEncoding.DecodeString(string(payload))
	require.NoError(t, err)
	return data
}
