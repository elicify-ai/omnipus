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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
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
	toolName string
	toolArgs map[string]any
}

func (p *inspectionBoundaryProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "inspect-1", Type: "function", Name: p.toolName, Arguments: p.toolArgs}}}, nil
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

	imageBytes := boundaryPNG(t, 32, 24)
	imagePath := filepath.Join(home, "private-page.png")
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &inspectionBoundaryProvider{toolName: "read_file", toolArgs: map[string]any{"path": imagePath}}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	agent := al.GetRegistry().GetDefaultAgent()
	agent.Tools.Register(tools.NewReadFileTool(home, true, tools.MaxReadFileSize))
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"read_file": "allow"}})

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "privacy", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "inspect this"}
	if _, _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 || !requestHasInspectionMedia(provider.requests[1]) {
		t.Fatalf("live provider request did not contain the inspection: %#v", provider.requests)
	}
	assertInspectionFixture(t, provider.requests[1], 32, 24)
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
	assertInspectionRecordsPrivate(t, agent.Sessions.GetHistory(key), imageBytes, imagePath)
	archive, err := agent.Sessions.ReadArchive(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	assertInspectionRecordsPrivate(t, archive, imageBytes, imagePath)
	reloaded, err := session.NewUnifiedStoreWithHome(filepath.Join(agent.Home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	assertInspectionRecordsPrivate(t, reloaded.GetHistory(key), imageBytes, imagePath)
	reloadedArchive, err := reloaded.ReadArchive(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	assertInspectionRecordsPrivate(t, reloadedArchive, imageBytes, imagePath)

	if _, _, err := al.processMessage(context.Background(), bus.InboundMessage{Channel: "telegram", ChatID: "privacy", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "continue"}); err != nil {
		t.Fatal(err)
	}
	if requestHasInspectionMedia(provider.requests[len(provider.requests)-1]) {
		t.Fatal("replay request redelivered private inspection media")
	}
	// Use the actual reactive context-window path after a second full turn.
	// The image turn leaves the live window; its canonical archive survives.
	if _, trimmed := al.windowTrimForce(agent, "", key, true); !trimmed {
		t.Fatal("forced window trim did not evict the older inspection turn")
	}
	compacted, err := session.NewUnifiedStoreWithHome(filepath.Join(agent.Home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer compacted.Close()
	assertNoInspectionSecrets(t, compacted.GetHistory(key), imageBytes, imagePath)
	compactedArchive, err := compacted.ReadArchive(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	assertInspectionRecordsPrivate(t, compactedArchive, imageBytes, imagePath)
}

func assertInspectionFixture(t *testing.T, messages []providers.Message, width, height int) {
	t.Helper()
	count := 0
	for _, message := range messages {
		count += len(message.Media)
	}
	if count != 1 {
		t.Fatalf("inspection image count = %d, want 1", count)
	}
	for _, message := range messages {
		for _, dataURL := range message.Media {
			comma := strings.IndexByte(dataURL, ',')
			if comma < 0 {
				t.Fatal("inspection media is not a data URL")
			}
			data, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != width || img.Bounds().Dy() != height || color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA) != (color.RGBA{R: 0x51, G: 0x72, B: 0x93, A: 0xff}) {
				t.Fatalf("inspection fixture changed: bounds=%v first_pixel=%v", img.Bounds(), img.At(0, 0))
			}
			return
		}
	}
	t.Fatal("inspection request contained no image")
}

type interruptedInspectionProvider struct {
	calls   int
	started chan struct{}
}

type rejectingInspectionProvider struct {
	calls         int
	rejectedMedia bool
}

func (p *rejectingInspectionProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "inspect-rejected", Type: "function", Name: "boundary_inspection", Arguments: map[string]any{}}}}, nil
	}
	p.rejectedMedia = requestHasInspectionMedia(messages)
	return nil, errors.New("API error: model does not support image input")
}

func (p *rejectingInspectionProvider) GetDefaultModel() string { return "unknown-vision-model" }

func (p *interruptedInspectionProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "inspect-interrupted", Type: "function", Name: "boundary_inspection", Arguments: map[string]any{}}}}, nil
	}
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *interruptedInspectionProvider) GetDefaultModel() string { return "unknown-vision-model" }

func TestReadImage_InterruptedTurnDoesNotPersistInspection(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unknown-vision-model"}
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Home: home}}
	cfg.Tools.Manifest.Compressed = false
	provider := &interruptedInspectionProvider{started: make(chan struct{})}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	imageBytes := boundaryPNG(t, 32, 24)
	al.RegisterTool(&boundaryInspectionTool{image: tools.InspectionImage{Bytes: imageBytes, MIMEType: "image/png", Source: "interrupted-private.png", Reauthorize: func(context.Context) error { return nil }}})
	agent := al.GetRegistry().GetDefaultAgent()
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"boundary_inspection": "allow"}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	msg := bus.InboundMessage{Channel: "telegram", ChatID: "interrupted", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "inspect then stop"}
	go func() {
		_, _, err := al.processMessage(ctx, msg)
		done <- err
	}()
	select {
	case <-provider.started:
	case err := <-done:
		t.Fatalf("turn ended before image request: %v", err)
	case <-ctx.Done():
		t.Fatal("image request did not start before the test deadline")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted turn error = %v, want context.Canceled", err)
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatal(err)
	}
	key := resolveScopeKey(route, "")
	assertNoInspectionSecrets(t, agent.Sessions.GetHistory(key), imageBytes, "interrupted-private.png")
	archive, err := agent.Sessions.ReadArchive(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	assertNoInspectionSecrets(t, archive, imageBytes, "interrupted-private.png")
	reloaded, err := session.NewUnifiedStoreWithHome(filepath.Join(agent.Home, "sessions"), home)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	assertNoInspectionSecrets(t, reloaded.GetHistory(key), imageBytes, "interrupted-private.png")
	select {
	case outbound := <-msgBus.OutboundMediaChan():
		t.Fatalf("interrupted inspection was delivered: %+v", outbound)
	default:
	}
}

func TestReadImage_ProviderImageRejectionStaysIncomplete(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "unknown-vision-model"}
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Home: home}}
	cfg.Tools.Manifest.Compressed = false
	provider := &rejectingInspectionProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	imageBytes := boundaryPNG(t, 32, 24)
	al.RegisterTool(&boundaryInspectionTool{image: tools.InspectionImage{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { return nil }}})
	agent := al.GetRegistry().GetDefaultAgent()
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"boundary_inspection": "allow"}})

	response, _, err := al.processMessage(context.Background(), bus.InboundMessage{Channel: "telegram", ChatID: "rejected", Sender: bus.SenderInfo{CanonicalID: "owner"}, Content: "inspect this"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || !provider.rejectedMedia {
		t.Fatalf("provider calls=%d rejected_media=%v, want one tool call and one real image rejection", provider.calls, provider.rejectedMedia)
	}
	if !strings.Contains(response, "can't view images") || strings.Contains(response, "inspection handled") {
		t.Fatalf("terminal rejection claimed inspection success or lacked guidance: %q", response)
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

func assertInspectionRecordsPrivate(t *testing.T, records any, imageBytes []byte, source string) {
	t.Helper()
	serialized, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	joined := string(serialized)
	if !strings.Contains(joined, "not retained; re-read to view") || !strings.Contains(joined, source) {
		t.Fatalf("durable records lost inspection marker: %q", joined)
	}
	for _, secret := range []string{encoded, "data:image/"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("durable records leaked %q", secret)
		}
	}
}

func assertNoInspectionSecrets(t *testing.T, records any, imageBytes []byte, source string) {
	t.Helper()
	serialized, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{base64.StdEncoding.EncodeToString(imageBytes), "data:image/", source} {
		if strings.Contains(string(serialized), secret) {
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

	unknownProvider := &candidateCaptureProvider{requests: map[string][]providers.Message{}, fail: map[string]error{}}
	unknownAgent := &AgentInstance{ID: "mia", Provider: unknownProvider}
	unknownChecks := 0
	unknownRT := &agentLoopRunTurn{
		al: &AgentLoop{}, ts: newTurnState(unknownAgent, processOptions{}, turnEventScope{}), turnCtx: context.Background(), activeProvider: unknownProvider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "future-provider", Model: "future-model"}}, llmModel: "future-model",
		inspectionImages: map[string][]tools.InspectionImage{"inspect-1": {{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { unknownChecks++; return nil }}}},
	}
	unknownRT.al.SetCapabilityCatalog(cat)
	if _, err := unknownRT.callProvider(canonical, nil); err != nil {
		t.Fatal(err)
	}
	if unknownChecks != 1 || !requestHasInspectionMedia(unknownProvider.requests["future-model"]) {
		t.Fatalf("unknown candidate did not receive one optimistic authorized attempt: checks=%d request=%#v", unknownChecks, unknownProvider.requests["future-model"])
	}

	timeoutProvider := &candidateCaptureProvider{requests: map[string][]providers.Message{}, fail: map[string]error{"chatgpt-4o-latest": context.DeadlineExceeded}}
	timeoutAgent := &AgentInstance{ID: "mia", Provider: timeoutProvider}
	timeoutAgent.StoreProviderPool(map[string]providers.LLMProvider{"302ai": timeoutProvider, "zai": timeoutProvider})
	timeoutLoop := &AgentLoop{fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	timeoutLoop.SetCapabilityCatalog(cat)
	timeoutChecks := 0
	timeoutRT := &agentLoopRunTurn{
		al: timeoutLoop, ts: newTurnState(timeoutAgent, processOptions{}, turnEventScope{}), turnCtx: context.Background(), activeProvider: timeoutProvider,
		activeCandidates: []providers.FallbackCandidate{{Provider: "302ai", Model: "chatgpt-4o-latest"}, {Provider: "zai", Model: "glm-4.5v"}}, llmModel: "chatgpt-4o-latest",
		inspectionImages: map[string][]tools.InspectionImage{"inspect-1": {{Bytes: imageBytes, MIMEType: "image/png", Reauthorize: func(context.Context) error { timeoutChecks++; return nil }}}},
	}
	if _, err := timeoutRT.callProvider(canonical, nil); err != nil {
		t.Fatal(err)
	}
	if timeoutChecks != 2 || !requestHasInspectionMedia(timeoutProvider.requests["glm-4.5v"]) {
		t.Fatalf("timeout fallback did not reauthorize and retry once: checks=%d request=%#v", timeoutChecks, timeoutProvider.requests["glm-4.5v"])
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
