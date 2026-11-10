// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-4 reachability and turn-origin oracles. Each test drives a real agent
// turn (AgentLoop.Run plus a production inbound), not a stub of request_secret.
//
// Oracles:
//   - US-6 AS-2 / FR-068 / FR-087: a web turn raises one secret card; after
//     submit the turn resumes exactly once, with reference names only.
//   - BDD-6.1 / FR-061: the card shows Omnipus, a purpose, and the endpoint.
//   - BDD-6.6 / FR-240: a channel-person turn is refused with
//     "open the web app and ask <Agent> there" and raises no card.
//   - US-16 AS-15 / FR-240 / K7: an approval raised while answering a channel
//     person is labelled "while answering <person> on <connection>".
//     Peer label "asked by <Agent>" is the same rule. Room intake is not a
//     public input on this base (rooms are a later unit); that row is not
//     invented here.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// wu4EligiblePeers is the gateway address seam the peer-origin test needs
// so AdmitRequest can run. Both agents in the fixture are eligible mains.
type wu4EligiblePeers struct{}

func (wu4EligiblePeers) PairEligible(string, string) (bool, error) { return true, nil }

func (wu4EligiblePeers) PublishGuestReply(string, session.TranscriptEntry) {}

const (
	// secretCardSentinel is the value the user types. It must never appear in
	// a frame, a resume prompt, or an error (BDD-6.2, FR-062).
	secretCardSentinel = "SENTINEL-WU4-VALUE"
	// secretCardEndpoint is the recipient BDD-6.1 names.
	secretCardEndpoint = "https://api.openai.com/v1"
	// secretCardPurpose is the purpose the card must show (BDD-6.1).
	secretCardPurpose = "OpenAI key"
	// channelRefusalMia is FR-061 / BDD-6.6 for agent Mia.
	channelRefusalMia = "open the web app and ask Mia there"
	// channelOriginLabel is §6.18 / K7 for person Anna on connection tg-1.
	channelOriginLabel = "while answering Anna on tg-1"
	// peerOriginLabel is §6.18 for a turn asked by Jim.
	peerOriginLabel = "asked by Jim"
)

// secretScriptProvider returns one tool call, then text. It keeps every
// prompt so a test can see the tool result and the resume.
type secretScriptProvider struct {
	mu       sync.Mutex
	calls    int
	prompts  [][]providers.Message
	toolName string
	toolArgs map[string]any
}

func (p *secretScriptProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.prompts = append(p.prompts, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if n == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "call-wu4", Type: "function", Name: p.toolName, Arguments: p.toolArgs,
		}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *secretScriptProvider) GetDefaultModel() string { return "test-model" }

func (p *secretScriptProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *secretScriptProvider) promptText(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.prompts) {
		return ""
	}
	var b strings.Builder
	for _, m := range p.prompts[n-1] {
		b.WriteString(m.Role)
		b.WriteString(":")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func secretCardAgentConfig(agentID, name string, auto bool) *config.Config {
	home := "" // mustAgentLoop pins the home to the isolated test directory.
	return &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Sandbox: config.OmnipusSandboxConfig{
			AutoApprove:  auto,
			ToolPolicies: map[string]string{"bash": "ask", "request_secret": "allow"},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 4,
			},
			List: []config.AgentConfig{{
				ID:   agentID,
				Name: name,
				Type: config.AgentTypeCore,
			}},
		},
	}
}

func startSecretCardLoop(t *testing.T, cfg *config.Config, provider providers.LLMProvider) (*agent.AgentLoop, *bus.MessageBus, *WSHandler) {
	t.Helper()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	al.WireSysagentDeps(&systools.Deps{Home: cfg.Agents.Defaults.Home, GetCfg: al.GetConfig})
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0
	handler := newWSHandler(msgBus, al, "")
	handler.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go al.Run(runCtx)
	t.Cleanup(handler.Wait)
	return al, msgBus, handler
}

func readFrameType(t *testing.T, wc *wsConn, frameType string, timeout time.Duration) (map[string]any, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case raw := <-wc.sendCh:
			var f map[string]any
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatalf("frame is not JSON: %v", err)
			}
			if f["type"] == frameType {
				return f, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

// TestSecretCard_RealTurn_RaisesCardAndResumesExactlyOnce is the WU-4
// reachability oracle plus BDD-6.1, BDD-6.2 and FR-087: one real Omnipus web
// turn raises one secret card, and after the person submits, that turn
// resumes exactly once with reference names and without the secret value.
func TestSecretCard_RealTurn_RaisesCardAndResumesExactlyOnce(t *testing.T) {
	provider := &secretScriptProvider{toolName: "request_secret", toolArgs: map[string]any{
		// The spec does not name request_secret's parameter object. These
		// keys are the card fields K1 already requires the server to show
		// (purpose, recipient endpoint). They are the request, not a guessed
		// store schema.
		"purpose":  secretCardPurpose,
		"endpoint": secretCardEndpoint,
	}}
	cfg := secretCardAgentConfig(string(coreagent.IDOmnipus), "Omnipus", false)
	al, _, handler := startSecretCardLoop(t, cfg, provider)
	tabs := apprResAttachConns(t, handler, 1)
	// FR-005: Omnipus has one conversation. A session-less message is refused
	// before any turn, so the card is requested inside the existing main.
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, string(coreagent.IDOmnipus))
	if err != nil {
		t.Fatalf("GetOrCreateMainSession: %v", err)
	}

	handler.handleChatMessageWithClientID(
		context.Background(), "wu4-secret-chat", main.ID,
		"I need an OpenAI key for "+secretCardEndpoint,
		string(coreagent.IDOmnipus), nil, "", testHarnessWorkspaceMembershipID,
		"client-wu4-secret", nil, makeTestConn(),
	)

	var raw map[string]any
	var otherFrame string
	deadline := time.Now().Add(8 * time.Second)
	for raw == nil && time.Now().Before(deadline) {
		if provider.callCount() >= 2 {
			t.Fatalf("request_secret finished the Omnipus turn without raising a secret card (US-6 AS-2). tool transcript:\n%s",
				provider.promptText(2))
		}
		select {
		case buf := <-tabs[0].sendCh:
			var f map[string]any
			if unmarshalErr := json.Unmarshal(buf, &f); unmarshalErr != nil {
				t.Fatalf("frame is not JSON: %v", unmarshalErr)
			}
			if f["type"] == "secret_request" {
				raw = f
			} else {
				otherFrame = string(buf)
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if raw == nil {
		t.Fatalf("BLOCKED: no secret_request frame on a real Omnipus web turn (model calls=%d) — required by US-6 AS-2, BDD-6.1, FR-061. last other frame: %s",
			provider.callCount(), otherFrame)
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	var frame gen.SecretRequestFrame
	if unmarshalErr := json.Unmarshal(encoded, &frame); unmarshalErr != nil {
		t.Fatalf("secret_request frame does not match K1 SecretRequestFrame: %v\n%s", unmarshalErr, encoded)
	}
	if frame.ActingAgentName != "Omnipus" {
		t.Fatalf("acting agent = %q, want Omnipus (BDD-6.1)", frame.ActingAgentName)
	}
	if frame.Purpose == "" {
		t.Fatal("card purpose is empty — FR-061 requires the purpose on the card")
	}
	shown := frame.Purpose + "\n" + strings.Join(frame.Recipient.Lines, "\n")
	if !strings.Contains(shown, secretCardEndpoint) {
		t.Fatalf("card shows %q, want the endpoint %s (BDD-6.1)", shown, secretCardEndpoint)
	}
	if n := len(frame.Fields); n < 1 || n > 8 {
		t.Fatalf("card has %d fields, want 1–8 (FR-071)", n)
	}
	if strings.Contains(string(encoded), secretCardSentinel) {
		t.Fatal("secret_request frame contained the secret value (FR-062)")
	}

	values := map[string]string{frame.Fields[0].Key: secretCardSentinel}
	submit := gen.SecretSubmitRequest{Values: &values}
	body, err := json.Marshal(submit)
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}
	api := &restAPI{agentLoop: al, homePath: cfg.Agents.Defaults.Home, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/secret-requests/"+frame.RequestId+"/submit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = uniqueTestSourceIP()
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, al.GetConfig()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
		t.Fatalf("BLOCKED: POST /api/v1/secret-requests/{id}/submit is not registered — required by K5, FR-087 (card %s was raised)", frame.RequestId)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d %q, want 200 and a SecretRequestState (K5)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretCardSentinel) {
		t.Fatalf("submit response contained the secret value: %q", rec.Body.String())
	}
	var state gen.SecretRequestState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("submit response is not SecretRequestState: %v", err)
	}
	if state.State != gen.SecretRequestStateStateSubmitted {
		t.Fatalf("state = %q, want submitted (FR-087)", state.State)
	}
	if state.ReferenceNames == nil || len(*state.ReferenceNames) == 0 {
		t.Fatal("submitted state omitted reference_names (K5, FR-068)")
	}

	resumeDeadline := time.Now().Add(8 * time.Second)
	for provider.callCount() < 2 && time.Now().Before(resumeDeadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if provider.callCount() < 2 {
		t.Fatal("submit stored the secret but the Omnipus turn did not resume (FR-068, FR-087)")
	}
	time.Sleep(300 * time.Millisecond)
	if got := provider.callCount(); got != 2 {
		t.Fatalf("resume ran %d model calls after the card, want exactly 2 (one request, one resume) — FR-087 exactly once", got)
	}
	resume := provider.promptText(2)
	if strings.Contains(resume, secretCardSentinel) {
		t.Fatal("resumed prompt contained the secret value (FR-062, FR-068)")
	}
	for _, ref := range *state.ReferenceNames {
		if ref == "" || strings.Contains(ref, secretCardSentinel) {
			t.Fatalf("reference name %q is empty or is the secret itself", ref)
		}
		if !strings.Contains(resume, ref) {
			t.Fatalf("resumed prompt does not list reference %q (FR-068)\n%s", ref, resume)
		}
	}
}

// TestSecretRequest_ChannelTriggeredTurnRefused_MainMode is BDD-6.6's main-mode
// row and the web control is the test above. A turn Mia's main chat consumes
// from Anna on tg-1 must refuse request_secret and raise no card.
func TestSecretRequest_ChannelTriggeredTurnRefused_MainMode(t *testing.T) {
	provider := &secretScriptProvider{toolName: "request_secret", toolArgs: map[string]any{
		"purpose":  secretCardPurpose,
		"endpoint": secretCardEndpoint,
	}}
	cfg := secretCardAgentConfig("mia", "Mia", false)
	al, msgBus, handler := startSecretCardLoop(t, cfg, provider)
	tabs := apprResAttachConns(t, handler, 1)

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("fixture: session store missing")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "tg-1",
		ChatID:     "anna-dm",
		SessionID:  meta.ID,
		Content:    "please store an OpenAI key",
		Sender: bus.SenderInfo{
			DisplayName: "Anna",
			Username:    "anna",
			Platform:    "telegram",
			InstanceID:  "tg-1",
		},
		Peer:          bus.Peer{Kind: bus.PeerDirect, ID: "anna", InstanceID: "tg-1"},
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": "mia"},
	}); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for provider.callCount() < 2 && time.Now().Before(deadline) {
		if _, ok := readFrameType(t, tabs[0], "secret_request", 30*time.Millisecond); ok {
			t.Fatal("a secret card was raised for a turn triggered by Anna on tg-1 — BDD-6.6 / FR-240 forbid that")
		}
	}
	if provider.callCount() < 2 {
		t.Fatalf("channel turn did not finish request_secret (no refusal, no card) — want %q (BDD-6.6)", channelRefusalMia)
	}
	got := provider.promptText(2)
	if !strings.Contains(got, channelRefusalMia) {
		t.Fatalf("channel-triggered request_secret result does not contain %q (BDD-6.6, FR-061)\n%s", channelRefusalMia, got)
	}
	if _, ok := readFrameType(t, tabs[0], "secret_request", 200*time.Millisecond); ok {
		t.Fatal("secret_request frame arrived after a channel-triggered refusal")
	}
}

// TestApprovalOriginLabel_ChannelPerson is spec test 191's channel row
// (US-16 AS-15, FR-240, K7). The approval is a real bash ask on a turn that
// consumed Anna's message on connection tg-1.
func TestApprovalOriginLabel_ChannelPerson(t *testing.T) {
	provider := &secretScriptProvider{toolName: "bash", toolArgs: map[string]any{"command": "echo wu4-origin"}}
	cfg := secretCardAgentConfig("mia", "Mia", false)
	al, msgBus, handler := startSecretCardLoop(t, cfg, provider)
	tabs := apprResAttachConns(t, handler, 1)

	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:    "telegram",
		InstanceID: "tg-1",
		ChatID:     "anna-dm",
		SessionID:  meta.ID,
		Content:    "run the check",
		Sender: bus.SenderInfo{
			DisplayName: "Anna",
			Platform:    "telegram",
			InstanceID:  "tg-1",
		},
		Peer:          bus.Peer{Kind: bus.PeerDirect, ID: "anna", InstanceID: "tg-1"},
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": "mia"},
	}); err != nil {
		t.Fatalf("PublishInbound: %v", err)
	}

	frame, ok := readFrameType(t, tabs[0], "tool_approval_required", 8*time.Second)
	if !ok {
		t.Fatal("no tool_approval_required frame for the channel-triggered bash ask — cannot check origin_label (FR-240, K7)")
	}
	label, _ := frame["origin_label"].(string)
	if label != channelOriginLabel {
		t.Fatalf("origin_label = %q, want %q (FR-240, §6.18)", label, channelOriginLabel)
	}
}

// TestApprovalOriginLabel_PeerAskedByAgent is spec test 191's peer row.
// Jim asks Mia; Mia's approval card says "asked by Jim" (§6.18).
func TestApprovalOriginLabel_PeerAskedByAgent(t *testing.T) {
	provider := &secretScriptProvider{toolName: "bash", toolArgs: map[string]any{"command": "echo wu4-peer"}}
	cfg := secretCardAgentConfig("mia", "Mia", false)
	cfg.Sandbox.ToolPolicies["send_message"] = "allow"
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID: "jim", Name: "Jim", Type: config.AgentTypeCore,
	})
	al, _, handler := startSecretCardLoop(t, cfg, provider)
	al.SetAddressDeps(wu4EligiblePeers{})
	tabs := apprResAttachConns(t, handler, 1)

	store := al.GetSessionStore()
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
	if err != nil {
		t.Fatalf("NewSession jim: %v", err)
	}
	ws := testHarnessWorkspaceMembershipID
	if err := store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	pair := addressing.Pair{WorkspaceID: ws, AgentID: "jim"}
	if _, _, err := al.AdmitRequest(context.Background(), agent.RequestAdmission{
		Receiver:    addressing.Pair{WorkspaceID: ws, AgentID: "mia"},
		Content:     "run the check",
		Sender:      addressing.Sender{Agent: pair},
		SenderLabel: "Jim",
		Source: addressing.Source{
			Kind:      addressing.SourceConversation,
			SessionID: asker.ID,
			Owner:     pair,
		},
	}); err != nil {
		t.Fatalf("AdmitRequest: %v", err)
	}

	frame, ok := readFrameType(t, tabs[0], "tool_approval_required", 8*time.Second)
	if !ok {
		t.Fatal("no tool_approval_required frame for the peer-asked bash — cannot check origin_label (FR-240, §6.18)")
	}
	label, _ := frame["origin_label"].(string)
	if label != peerOriginLabel {
		t.Fatalf("origin_label = %q, want %q (FR-240, §6.18 peer row)", label, peerOriginLabel)
	}
}
