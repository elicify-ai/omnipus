package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const inspectionMarker = "[visual inspection snapshot not retained; re-read to view]"

type boundaryInspectionTool struct{ image tools.InspectionImage }

func (t *boundaryInspectionTool) Name() string                 { return "boundary_inspection" }
func (t *boundaryInspectionTool) Description() string          { return "Returns private visual evidence" }
func (t *boundaryInspectionTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *boundaryInspectionTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *boundaryInspectionTool) Parameters() map[string]any   { return map[string]any{"type": "object"} }
func (t *boundaryInspectionTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: inspectionMarker, InspectionImages: []tools.InspectionImage{t.image}}
}

type inspectionBoundaryProvider struct {
	calls    int
	requests [][]providers.Message
}

func (p *inspectionBoundaryProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "inspect-1", Type: "function", Name: "boundary_inspection", Arguments: map[string]any{}}}}, nil
	}
	return &providers.LLMResponse{Content: "inspection handled"}, nil
}

func (p *inspectionBoundaryProvider) GetDefaultModel() string { return "unknown-vision-model" }

func boundaryPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 0x51, G: 0x72, B: 0x93, A: 0xff})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestReadImage_HistoryAndDeliveryRegression(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unknown-vision-model"}
	cfg.Agents.Defaults.MaxToolIterations = 4
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Home: home}}
	cfg.Tools.Manifest.Compressed = false

	provider := &inspectionBoundaryProvider{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	imageBytes := boundaryPNG(t, 32, 24)
	authorizations := 0
	al.RegisterTool(&boundaryInspectionTool{image: tools.InspectionImage{
		Bytes: imageBytes, MIMEType: "image/png", Source: "private-page.png",
		Reauthorize: func(context.Context) error { authorizations++; return nil },
	}})
	agent := al.GetRegistry().GetDefaultAgent()
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"boundary_inspection": "allow"}})

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "privacy", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "inspect this"}
	if _, _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if authorizations != 1 {
		t.Fatalf("authorization checks = %d, want 1", authorizations)
	}
	if len(provider.requests) != 2 || !requestHasInspectionMedia(provider.requests[1]) {
		t.Fatalf("live provider request did not contain the inspection: %#v", provider.requests)
	}
	select {
	case outbound := <-msgBus.OutboundMediaChan():
		t.Fatalf("private inspection was published: %+v", outbound)
	default:
	}

	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatal(err)
	}
	key := resolveScopeKey(route, "")
	assertInspectionRecordsPrivate(t, agent.Sessions.GetHistory(key), imageBytes)
	archive, err := agent.Sessions.ReadArchive(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	assertInspectionRecordsPrivate(t, archive, imageBytes)

	if _, _, err := al.processMessage(context.Background(), bus.InboundMessage{Channel: "telegram", ChatID: "privacy", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "continue"}); err != nil {
		t.Fatal(err)
	}
	if authorizations != 1 {
		t.Fatalf("replay reauthorized or redelivered inspection; checks = %d", authorizations)
	}
	if requestHasInspectionMedia(provider.requests[len(provider.requests)-1]) {
		t.Fatal("replay request redelivered private inspection media")
	}
}

func requestHasInspectionMedia(messages []providers.Message) bool {
	for _, message := range messages {
		if len(message.Media) > 0 {
			return true
		}
	}
	return false
}

func assertInspectionRecordsPrivate(t *testing.T, records any, imageBytes []byte) {
	t.Helper()
	serialized, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	joined := string(serialized)
	if !strings.Contains(joined, inspectionMarker) {
		t.Fatalf("durable records lost inspection marker: %q", joined)
	}
	for _, secret := range []string{encoded, "data:image/", "private-page.png"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("durable records leaked %q", secret)
		}
	}
}

type candidateCaptureProvider struct {
	requests map[string][]providers.Message
	fail     map[string]error
	onChat   func(string)
}

func (p *candidateCaptureProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.requests[model] = append([]providers.Message(nil), messages...)
	if p.onChat != nil {
		p.onChat(model)
	}
	if err := p.fail[model]; err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *candidateCaptureProvider) GetDefaultModel() string { return "unused" }

func TestReadImage_ProviderFailureAndCandidate(t *testing.T) {
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes := boundaryPNG(t, 7000, 2)
	rechecks := 0
	inspection := tools.InspectionImage{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { rechecks++; return nil }}
	provider := &candidateCaptureProvider{requests: map[string][]providers.Message{}, fail: map[string]error{"chatgpt-4o-latest": errors.New("rate limit exceeded")}}
	agent := &AgentInstance{ID: "mia", Provider: provider}
	agent.StoreProviderPool(map[string]providers.LLMProvider{"302ai": provider, "zai": provider})
	al := &AgentLoop{fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	al.SetCapabilityCatalog(cat)
	ts := newTurnState(agent, processOptions{}, turnEventScope{})
	rt := &agentLoopRunTurn{
		al: al, ts: ts, turnCtx: context.Background(), activeProvider: provider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "chatgpt-4o-latest"}, {Provider: "zai", Model: "glm-4.5v"}},
		llmModel:         "chatgpt-4o-latest", inspectionImages: map[string][]tools.InspectionImage{"inspect-1": {inspection}},
	}
	canonical := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "inspect-1", Type: "function", Name: "boundary_inspection"}}},
		{Role: "tool", ToolCallID: "inspect-1", Content: inspectionMarker},
	}
	if _, err := rt.callProvider(canonical, nil); err != nil {
		t.Fatal(err)
	}
	if rechecks != 2 {
		t.Fatalf("candidate attempts rechecked authorization %d times, want 2", rechecks)
	}
	primary := provider.requests["chatgpt-4o-latest"]
	fallback := provider.requests["glm-4.5v"]
	if !requestHasInspectionMedia(primary) || !requestHasInspectionMedia(fallback) {
		t.Fatalf("vision candidates lost inspection media: primary=%#v fallback=%#v", primary, fallback)
	}
	if !strings.Contains(fallback[1].Content, "resized from 7000x2") {
		t.Fatalf("fallback did not apply its own resize budget: %q", fallback[1].Content)
	}
	primaryWidth := inspectionMediaWidth(t, primary[1].Media[0])
	fallbackWidth := inspectionMediaWidth(t, fallback[1].Media[0])
	if primaryWidth != 7000 || fallbackWidth > 6000 || fallbackWidth >= primaryWidth {
		t.Fatalf("candidate widths primary=%d fallback=%d, want original then <=6000", primaryWidth, fallbackWidth)
	}

	nonVisionProvider := &candidateCaptureProvider{requests: map[string][]providers.Message{}, fail: map[string]error{"chatgpt-4o-latest": errors.New("rate limit exceeded")}}
	nonVisionAgent := &AgentInstance{ID: "mia", Provider: nonVisionProvider}
	nonVisionAgent.StoreProviderPool(map[string]providers.LLMProvider{"302ai": nonVisionProvider})
	nonVisionLoop := &AgentLoop{fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	nonVisionLoop.SetCapabilityCatalog(cat)
	nonVisionChecks := 0
	nonVisionRT := &agentLoopRunTurn{
		al: nonVisionLoop, ts: newTurnState(nonVisionAgent, processOptions{}, turnEventScope{}), turnCtx: context.Background(), activeProvider: nonVisionProvider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "chatgpt-4o-latest"}, {Provider: "302ai", Model: "deepseek-chat"}},
		llmModel:         "chatgpt-4o-latest", inspectionImages: map[string][]tools.InspectionImage{"inspect-1": {{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { nonVisionChecks++; return nil }}}},
	}
	if _, err := nonVisionRT.callProvider(canonical, nil); err != nil {
		t.Fatal(err)
	}
	if nonVisionChecks != 1 {
		t.Fatalf("non-vision fallback reauthorization checks = %d, want only primary attempt", nonVisionChecks)
	}
	nonVisionRequest := nonVisionProvider.requests["deepseek-chat"]
	if requestHasInspectionMedia(nonVisionRequest) || !strings.Contains(nonVisionRequest[1].Content, "does not support image input") {
		t.Fatalf("non-vision fallback received bytes or lacked guidance: %#v", nonVisionRequest)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelProvider := &candidateCaptureProvider{requests: map[string][]providers.Message{}, fail: map[string]error{"chatgpt-4o-latest": context.Canceled}}
	cancelProvider.onChat = func(model string) {
		if model == "chatgpt-4o-latest" {
			cancel()
		}
	}
	cancelAgent := &AgentInstance{ID: "mia", Provider: cancelProvider}
	cancelAgent.StoreProviderPool(map[string]providers.LLMProvider{"302ai": cancelProvider, "zai": cancelProvider})
	cancelLoop := &AgentLoop{fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	cancelLoop.SetCapabilityCatalog(cat)
	cancelChecks := 0
	cancelRT := &agentLoopRunTurn{
		al: cancelLoop, ts: newTurnState(cancelAgent, processOptions{}, turnEventScope{}), turnCtx: cancelCtx, activeProvider: cancelProvider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "chatgpt-4o-latest"}, {Provider: "zai", Model: "glm-4.5v"}},
		llmModel:         "chatgpt-4o-latest", inspectionImages: map[string][]tools.InspectionImage{"inspect-1": {{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { cancelChecks++; return nil }}}},
	}
	if _, err := cancelRT.callProvider(canonical, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled provider error = %v, want context.Canceled", err)
	}
	if cancelChecks != 1 || len(cancelProvider.requests["glm-4.5v"]) != 0 {
		t.Fatalf("cancellation was not terminal: checks=%d fallback requests=%d", cancelChecks, len(cancelProvider.requests["glm-4.5v"]))
	}
}

func inspectionMediaWidth(t *testing.T, dataURL string) int {
	t.Helper()
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		t.Fatalf("invalid inspection data URL")
	}
	data, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width
}
