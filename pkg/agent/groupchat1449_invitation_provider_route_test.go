package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracles: #1449 FR-039 (the origin agent's model), FR-041/FR-045 and
// US-8 scenario 5 (unavailable summary, usable group); security finding F2's
// approved fix (never send to the installation default on a named route miss).
// Only the HTTP services and workspace directory/effects are doubles. The
// provider factory, loop, registry, instance, session store and rooms are real.
const (
	invitationRouteOwner         = "summary-owner"
	invitationRouteProvider      = "qa-private-6c"
	invitationRouteModel         = "private-summary-model"
	invitationRouteWorkspace     = "sales"
	invitationRoutePrivateText   = "Our private launch decision is Friday."
	invitationRoutePrivateReply  = "The team decided to launch on Friday."
	invitationRouteDefaultReply  = "This summary came from the wrong provider."
	invitationRouteMissingKeyRef = "QA_6C_INVITATION_MISSING_PRIVATE_KEY"
)

func TestGroupChat1449_InvitationUsesOriginAgentProviderAtBoot(t *testing.T) {
	f := newInvitationRouteFixture(t, false)
	room := f.summarizeInGroup(t)

	if calls := f.installation.snapshot(); len(calls) != 0 {
		t.Errorf("F2: installation-default endpoint received %d summary request(s), want 0", len(calls))
	}
	calls := f.private.snapshot()
	if len(calls) != 1 {
		t.Errorf("F2: origin-agent endpoint received %d summary request(s), want 1", len(calls))
	} else {
		call := calls[0]
		if call.method != http.MethodPost || call.path != "/v1/chat/completions" {
			t.Errorf("origin-agent HTTP request = %s %s, want POST /v1/chat/completions", call.method, call.path)
		}
		if call.model != invitationRouteModel {
			t.Errorf("origin-agent request model = %q, want %q", call.model, invitationRouteModel)
		}
		if !strings.Contains(call.messages, invitationRoutePrivateText) {
			t.Errorf("origin-agent summary request omitted the origin chat: %s", call.messages)
		}
	}
	if f.summaryErr != nil {
		t.Errorf("origin-agent summary failed: %v", f.summaryErr)
	}
	if !f.result.Called || f.result.Text != invitationRoutePrivateReply {
		t.Errorf("F2: summary result = %+v, want Called=true Text=%q from the origin agent's endpoint", f.result, invitationRoutePrivateReply)
	}
	if room.InvitationContextStatus == nil || string(*room.InvitationContextStatus) != "ready" {
		t.Errorf("FR-039: group invitation status = %v, want ready", room.InvitationContextStatus)
	}
	if room.InvitationContext == nil || room.InvitationContext.Text == nil || *room.InvitationContext.Text != invitationRoutePrivateReply {
		t.Errorf("FR-044: group invitation detail = %+v, want private-provider text %q", room.InvitationContext, invitationRoutePrivateReply)
	}
	f.assertOriginUnchanged(t)
}

func TestGroupChat1449_InvitationUnavailableNamedRouteNeverUsesDefault(t *testing.T) {
	f := newInvitationRouteFixture(t, true)
	room := f.summarizeInGroup(t)

	if calls := f.installation.snapshot(); len(calls) != 0 {
		t.Errorf("F2: unavailable named route sent %d request(s) to installation-default endpoint, want 0", len(calls))
	}
	if calls := f.private.snapshot(); len(calls) != 0 {
		t.Errorf("F2: unavailable named route sent %d request(s) to origin-agent endpoint, want 0", len(calls))
	}
	// A missing configured credential is a named-route refusal, not a
	// fabricated provider HTTP error or permission to borrow another client.
	var routeErr *providers.ModelRouteUnavailableError
	if !errors.As(f.summaryErr, &routeErr) {
		t.Errorf("F2: unavailable named route error = %v, want *providers.ModelRouteUnavailableError", f.summaryErr)
	} else if routeErr.Provider != invitationRouteProvider || routeErr.Model != invitationRouteModel {
		t.Errorf("named-route refusal = (%q, %q), want (%q, %q)", routeErr.Provider, routeErr.Model, invitationRouteProvider, invitationRouteModel)
	}
	if f.result != (RoomInvitationResult{}) {
		t.Errorf("F2: unavailable named route result = %+v, want no model call, text or cost", f.result)
	}
	// This is the real group detail consumed by the Participants panel, not a
	// test-computed status. FR-045/US-8.5 require the panel to render
	// "Invitation context: unavailable" for this state.
	if room.InvitationContextStatus == nil || string(*room.InvitationContextStatus) != "unavailable" {
		t.Errorf("FR-041/FR-045: group invitation status = %v, want unavailable", room.InvitationContextStatus)
	}
	if room.InvitationContext == nil || string(room.InvitationContext.Status) != "unavailable" || room.InvitationContext.Text != nil {
		t.Errorf("FR-045: unavailable group detail = %+v, want status unavailable and no summary text", room.InvitationContext)
	}
	if room.ReadOnlyReason != nil || room.Id == "" || len(room.Members) != 2 {
		t.Errorf("US-8.5: summary refusal must leave the group usable: %+v", room)
	}
	if room.AllocatedCostMicroUsd != 0 || room.AllocatedCostUnpriced {
		t.Errorf("uncalled summary cost = %d unpriced=%v, want zero and false", room.AllocatedCostMicroUsd, room.AllocatedCostUnpriced)
	}
	f.assertOriginUnchanged(t)
}

// invitationRouteCall records the external model-service boundary. It is not
// a gateway/SPA wire-format carrier.
type invitationRouteCall struct {
	method, path, model, messages string
}

type invitationRouteRecorder struct {
	mu    sync.Mutex
	calls []invitationRouteCall
}

func newInvitationRouteEndpoint(t *testing.T, reply string) (*httptest.Server, *invitationRouteRecorder) {
	t.Helper()
	recorder := &invitationRouteRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		decodeErr := json.NewDecoder(r.Body).Decode(&body)
		var model string
		modelErr := json.Unmarshal(body["model"], &model)
		recorder.mu.Lock()
		recorder.calls = append(recorder.calls, invitationRouteCall{
			method: r.Method, path: r.URL.Path, model: model, messages: string(body["messages"]),
		})
		recorder.mu.Unlock()
		if decodeErr != nil || modelErr != nil {
			t.Errorf("invalid summary HTTP request: body=%v model=%v", decodeErr, modelErr)
			http.Error(w, "invalid model request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id": "summary-response", "object": "chat.completion", "model": model,
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop",
			}},
		}); err != nil {
			t.Errorf("write recording endpoint response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

func (r *invitationRouteRecorder) snapshot() []invitationRouteCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]invitationRouteCall(nil), r.calls...)
}

type invitationRouteFixture struct {
	loop                  *AgentLoop
	store                 *session.UnifiedStore
	origin, before        string
	installation, private *invitationRouteRecorder
	result                RoomInvitationResult
	summaryErr            error
	summaryCalls          int
}

func newInvitationRouteFixture(t *testing.T, unavailable bool) *invitationRouteFixture {
	t.Helper()
	defaultServer, installation := newInvitationRouteEndpoint(t, invitationRouteDefaultReply)
	privateServer, private := newInvitationRouteEndpoint(t, invitationRoutePrivateReply)
	home := filepath.Join(t.TempDir(), "install")
	t.Setenv("OMNIPUS_HOME", home)
	privateRow := &config.ModelConfig{
		Provider: invitationRouteProvider, Model: invitationRouteModel,
		Custom: true, Protocol: "openai-compatible", APIBase: privateServer.URL + "/v1",
	}
	if unavailable {
		// Both HTTP servers remain live. It is the named route, not a stopped
		// server, that is unavailable; construction must retain the failure.
		t.Setenv(invitationRouteMissingKeyRef, "")
		privateRow.APIKeyRef = invitationRouteMissingKeyRef
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "workspace"),
				DefaultModel: config.DefaultModel{Provider: "qa-default-6c", Model: "installation-model"},
				MaxTokens:    4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{
				ID:    invitationRouteOwner,
				Model: &config.AgentModelConfig{Provider: invitationRouteProvider, Primary: invitationRouteModel},
			}},
		},
		Providers: []*config.ModelConfig{{
			Provider: "qa-default-6c", Model: "installation-model",
			Custom: true, Protocol: "openai-compatible", APIBase: defaultServer.URL + "/v1",
		}, privateRow},
	}
	// The successful gateway startup branch calls this same factory. Feed
	// its default client through the full normal loop → registry → instance
	// construction. Never replace inst.Provider, Candidates or its route pool.
	startup, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("construct installation-default provider: %v", err)
	}
	loop, err := NewAgentLoop(cfg, bus.NewMessageBus(), startup)
	if err != nil {
		t.Fatalf("construct real agent loop and registry: %v", err)
	}
	t.Cleanup(loop.Close)
	if inst, ok := loop.GetRegistry().GetAgent(invitationRouteOwner); !ok || inst == nil {
		t.Fatal("real registry did not construct the configured origin agent")
	}
	store := loop.GetSessionStore()
	origin, err := store.GetOrCreateMainSession(invitationRouteWorkspace, invitationRouteOwner)
	if err != nil {
		t.Fatalf("create origin main chat: %v", err)
	}
	if err := store.AppendTranscript(origin.ID, session.TranscriptEntry{
		Type: session.EntryTypeMessage, Role: "user", Content: invitationRoutePrivateText,
	}); err != nil {
		t.Fatalf("write origin chat: %v", err)
	}
	return &invitationRouteFixture{
		loop: loop, store: store, origin: origin.ID,
		before: groupChatChatSnapshot(t, store, origin.ID), installation: installation, private: private,
	}
}

func (f *invitationRouteFixture) SummarizeInvitation(ctx context.Context, req rooms.InvitationRequest) (rooms.InvitationSummary, error) {
	f.summaryCalls++
	f.result, f.summaryErr = f.loop.SummarizeRoomInvitation(ctx, RoomInvitationRequest{
		WorkspaceID: req.WorkspaceID, OriginSessionID: req.OriginSessionID, AgentID: req.AgentID,
		MaxEntries: req.MaxEntries, MaxBytes: req.MaxBytes,
	})
	return rooms.InvitationSummary{
		Text: f.result.Text, Called: f.result.Called, CostUSD: f.result.CostUSD, Priced: f.result.Priced,
	}, f.summaryErr
}

func (f *invitationRouteFixture) summarizeInGroup(t *testing.T) generated.Room {
	t.Helper()
	svc := rooms.New(rooms.NewFileJournal(t.TempDir()), rooms.Deps{Directory: f, Effects: f, Summarizer: f})
	t.Cleanup(svc.Close)
	created, err := svc.Create(invitationRouteWorkspace, generated.RoomCreateRequest{
		Kind: "adhoc", AgentIds: []string{invitationRouteOwner, "invited-agent"}, OriginSessionId: &f.origin,
	}, "test-user")
	if err != nil {
		t.Fatalf("start group from main chat: %v", err)
	}
	// Synchronize on the job's completion, not a sleep or a status poll. The
	// same synchronization makes the summary result fields safe to inspect.
	svc.WaitIdleForTest()
	if f.summaryCalls != 1 {
		t.Fatalf("real room service ran %d summary jobs, want 1", f.summaryCalls)
	}
	got, err := svc.Get(created.Id)
	if err != nil {
		t.Fatalf("read group after summarizing: %v", err)
	}
	return got
}

func (f *invitationRouteFixture) assertOriginUnchanged(t *testing.T) {
	t.Helper()
	if after := groupChatChatSnapshot(t, f.store, f.origin); after != f.before {
		t.Errorf("FR-038/FR-046: invitation changed the origin chat:\nbefore: %s\nafter: %s", f.before, after)
	}
}

// Workspace directory and notification sinks are process-edge fixtures. No
// receiver is supplied because these tests create/read a group, not a turn.
func (f *invitationRouteFixture) LookupWorkspace(id string) (rooms.WorkspaceInfo, bool, error) {
	return rooms.WorkspaceInfo{ID: invitationRouteWorkspace, Name: "Sales", Agents: []rooms.Agent{
		{ID: invitationRouteOwner, Name: "Origin"}, {ID: "invited-agent", Name: "Invited"},
	}}, id == invitationRouteWorkspace, nil
}

func (f *invitationRouteFixture) AgentName(id string) (string, bool) {
	return id, id == invitationRouteOwner || id == "invited-agent"
}

func (f *invitationRouteFixture) SessionInWorkspace(id, workspace string) bool {
	return id == f.origin && workspace == invitationRouteWorkspace
}

func (f *invitationRouteFixture) Limits() (int, int)       { return 0, 65536 }
func (f *invitationRouteFixture) Publish(_ string, _ any)  {}
func (f *invitationRouteFixture) Audit(_ rooms.AuditEvent) {}
