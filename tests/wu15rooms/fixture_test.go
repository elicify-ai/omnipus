package wu15rooms_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Chief Q-F B: this is deliberately a separate typed test package. On the
// pre-room base pkg/rooms is absent, so ONLY this new package is compile-RED;
// existing gateway tests keep building. Runtime sensitivity belongs to CHECK.
const workspaceID = "sales"

var errCrashCut = errors.New("WU15 injected crash boundary")

type fixture struct {
	t       *testing.T
	home    string
	loop    *agent.AgentLoop
	bus     *bus.MessageBus
	service *rooms.Service
	journal *cutJournal
	crashed atomic.Bool
	writer  *countingWriter
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "work"), DefaultModel: config.DefaultModel{Model: "wu15-unpriced"}, MaxTokens: 4096},
		List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom}, {ID: "jim", Name: "Jim", Type: config.AgentTypeCustom}},
	}}
	// Seed the three policies the fixture needs to exercise; reconcile fills the rest
	// from the shipped ceiling the same way production boot does (ADR-076/ADR-077).
	cfg.Sandbox.ToolPolicies = map[string]string{"send_message": "allow", "answer_request": "allow", tools.AskUserQuestionToolName: "allow"}
	// Reconcile the global tool-policy ceiling exactly as gateway does at boot.
	// This ensures set_todos (and every other shipped builtin) has its default entry,
	// so the fixture runs against the same ceiling a real install would.
	known := map[string]struct{}{}
	for k := range config.DefaultConfig().Sandbox.ToolPolicies {
		known[k] = struct{}{}
	}
	config.ReconcileToolPolicyCeiling(cfg, known)
	require.NoError(t, os.MkdirAll(cfg.Agents.Defaults.Home, 0o700))
	ids, owner := []string{"mia", "jim"}, "qa"
	ws := gen.Workspace{Id: workspaceID, Name: "Sales", Status: gen.WorkspaceStatusActive, CoreTeam: &ids, Owner: &owner, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	data, err := json.Marshal(ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", workspaceID+".json"), data, 0o600))
	b := bus.NewMessageBus()
	al, err := agent.NewAgentLoop(cfg, b, &idleProvider{})
	require.NoError(t, err)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	boot := session.NewBootEpochStore(home)
	_, err = boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	f := &fixture{t: t, home: home, loop: al, bus: b}
	f.journal = &cutJournal{Journal: rooms.NewFileJournal(rooms.DefaultDir(home)), crashed: &f.crashed}
	f.writer = &countingWriter{UnifiedStore: al.GetSessionStore()}
	al.SetRoomReceiverWriter(f.writer)
	al.SetAddressDeps(f)
	for _, id := range ids {
		_, err = al.GetSessionStore().GetOrCreateMainSession(workspaceID, id)
		require.NoError(t, err)
	}
	f.reload()
	t.Cleanup(func() { f.service.Close(); f.service.WaitIdleForTest(); al.Close() })
	return f
}

func (f *fixture) reload() {
	if f.service != nil {
		f.service.Close()
		f.service.WaitIdleForTest()
	}
	f.crashed.Store(false)
	f.service = rooms.New(f.journal, rooms.DepsFrom(f))
	f.loop.SetRoomRuntime(f)
}

func (f *fixture) room(name string) gen.Room {
	f.t.Helper()
	room, err := f.service.Create(workspaceID, gen.RoomCreateRequest{Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: []string{"mia", "jim"}}, "qa")
	require.NoError(f.t, err)
	return room
}

func (f *fixture) main(id string) string {
	f.t.Helper()
	sid, err := session.MainSessionID(workspaceID, id)
	require.NoError(f.t, err)
	return sid
}

func (f *fixture) snapshot(id string) gen.RoomSnapshotFrame {
	f.t.Helper()
	var out gen.RoomSnapshotFrame
	require.NoError(f.t, f.service.Attach(id, 0, func(snapshot gen.RoomSnapshotFrame) { out = snapshot }))
	return out
}

func (f *fixture) post(roomID, clientID, text string, ids ...string) error {
	if ids == nil {
		ids = []string{}
	}
	return f.service.PostMessage(roomID, gen.RoomMessageRequest{ClientMessageId: clientID, Text: text, Mentions: ids}, "qa")
}

// Directory reads actual workspace bytes and actual registered agent names.
// It is only the external directory dependency, not a fake fan-out/receiver.
func (f *fixture) LookupWorkspace(id string) (rooms.WorkspaceInfo, bool, error) {
	data, err := os.ReadFile(filepath.Join(f.home, "workspaces", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return rooms.WorkspaceInfo{}, false, nil
	}
	if err != nil {
		return rooms.WorkspaceInfo{}, false, err
	}
	var stored gen.Workspace
	if err = json.Unmarshal(data, &stored); err != nil {
		return rooms.WorkspaceInfo{}, false, err
	}
	ws := rooms.WorkspaceInfo{ID: stored.Id, Name: stored.Name}
	if stored.CoreTeam != nil {
		for _, agentID := range *stored.CoreTeam {
			if instance, ok := f.loop.GetRegistry().GetAgent(agentID); ok {
				ws.Agents = append(ws.Agents, rooms.Agent{ID: agentID, Name: instance.Name})
			}
		}
	}
	return ws, true, nil
}
func (f *fixture) AgentName(id string) (string, bool) {
	instance, ok := f.loop.GetRegistry().GetAgent(id)
	if !ok {
		return "", false
	}
	return instance.Name, true
}
func (f *fixture) SessionInWorkspace(sid, ws string) bool {
	meta, err := f.loop.GetSessionStore().GetMeta(sid)
	return err == nil && meta.WorkspaceID == ws && string(meta.Type) == "main"
}
func (*fixture) Limits() (int, int) { return 60, 65536 } // FR-176.
func (f *fixture) PairEligible(ws, id string) (bool, error) {
	workspace, found, err := f.LookupWorkspace(ws)
	if err != nil || !found {
		return false, err
	}
	for _, member := range workspace.Agents {
		if member.ID == id {
			return true, nil
		}
	}
	return false, nil
}
func (*fixture) PublishGuestReply(string, session.TranscriptEntry) {}
func (*fixture) Publish(string, any)                               {}
func (*fixture) Audit(rooms.AuditEvent)                            {}
func (*fixture) CancelApproval(string)                             {}

func (f *fixture) Admit(ctx context.Context, a rooms.Admission) (rooms.AdmitResult, error) {
	if f.crashed.Load() {
		return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitUnavailable, errCrashCut)
	}
	result, err := f.loop.AdmitRoomRequest(ctx, agent.RoomRequestAdmission{
		Receiver: a.Receiver, DeliveryID: a.DeliveryID, RoomID: a.RoomID, RoomName: a.RoomName,
		Sender: a.Sender, SenderLabel: a.SenderLabel, DisplayText: a.DisplayText, ModelText: a.ModelText,
		OnAwaitingApproval: a.OnAwaitingApproval, BeforeCommit: a.BeforeCommit,
	})
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrRoomRequestRefused):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitRefused, err)
		case errors.Is(err, agent.ErrRoomRequestCancelled):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitCancelled, err)
		case errors.Is(err, agent.ErrRoomReceiverUnavailable):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitUnavailable, err)
		default:
			return rooms.AdmitResult{}, err
		}
	}
	return rooms.AdmitResult{ReceiverSessionID: result.ReceiverSessionID, Held: result.Held, AlreadyAdmitted: result.AlreadyAdmitted}, nil
}
func (f *fixture) Facts(receiver addressing.Pair, id string) (rooms.ReceiverFacts, error) {
	sid, err := session.MainSessionID(receiver.WorkspaceID, receiver.AgentID)
	if err != nil {
		return rooms.ReceiverFacts{}, err
	}
	capture, found, err := f.loop.RequestLedger().Get(sid, id)
	return rooms.ReceiverFacts{Found: found, SessionID: sid, Delivered: capture.Delivered, Discarded: capture.Discarded, Held: capture.Held}, err
}
func (f *fixture) Discard(sid, id string) error { return f.loop.DiscardRequest(sid, id) }
func (f *fixture) Mirror(m rooms.Mirror) error {
	return f.loop.MirrorRoomAnswer(m.MainSessionID, m.MirrorID, m.Author, m.Ref, m.Content)
}
func (f *fixture) BeginRoomConsume(room, request, sid string) bool {
	return f.service.BeginConsume(room, request, sid)
}
func (f *fixture) RoomConsumeHeld(room, request string) { f.service.ConsumeHeld(room, request) }
func (f *fixture) RoomConsumeEnded(room, request string, err error) (bool, error) {
	return f.service.ConsumeEnded(room, request, err)
}
func (f *fixture) RoomReminderTurnEnded(sid string) error { return f.service.ReminderTurnEnded(sid) }
func (f *fixture) RoomTurnCost(c agent.RoomTurnCost) {
	f.service.AddTurnCost(rooms.TurnCost{RoomID: c.RoomID, RequestID: c.RequestID, TurnID: c.TurnID, CostUSD: c.CostUSD, Priced: c.Priced, Inputs: c.Inputs, RoomInputs: c.RoomInputs})
}
func (f *fixture) DeliverRoomAnswer(ctx context.Context, a agent.RoomAnswer) (tools.ReplyReceipt, error) {
	result, err := f.service.DeliverAnswer(ctx, rooms.Answer{RoomID: a.RoomID, RequestID: a.RequestID, Responder: a.Responder, ResponderSessionID: a.ResponderSessionID, Content: a.Content})
	return tools.ReplyReceipt{ReplyID: result.AnswerID, Destination: result.RoomName}, err
}

type idleProvider struct{ starts atomic.Int64 }

func (p *idleProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.starts.Add(1)
	return &providers.LLMResponse{Content: "fixture idle", FinishReason: "stop"}, nil
}
func (*idleProvider) GetDefaultModel() string { return "wu15-unpriced" }

var _ rooms.Directory = (*fixture)(nil)
var _ rooms.Receivers = (*fixture)(nil)
var _ rooms.Effects = (*fixture)(nil)
var _ agent.RoomRuntime = (*fixture)(nil)
var _ agent.AddressDeps = (*fixture)(nil)
