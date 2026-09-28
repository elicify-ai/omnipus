package agent

// steer_route_pinned_agent_test.go — ADR-091 identity, inbound-route pinning:
// a message addressed to a steered child's session runs under the CHILD's
// record-pinned agent (LifecycleRecord.AgentID, written by SteerLauncher.Launch
// and honoured by every other identity-bearing path — reconstructSteeredTurn,
// SteerAudienceResolver, boot sweep). The SPA's sticky agent-picker dropdown
// (inbound agent_id metadata) must never re-target a steered child's session
// to a different agent: a live repro (2026-09-27, e2e
// steered-session-reachability, gateway debug log + Playwright trace) showed
// the steer message typed into the child's own live view routed as
// "Routed to explicit agent (dropdown) agent_id=jim" into a worker-pinned
// child — a jim turn ran inside the child's session, re-executed the child's
// original task ("Delegated once as instructed."), minting a duplicate
// delegate subtree that kept the delegation alive past the test's pill
// assertion. There was no steered-session intercept at the top of
// resolveMessageRoute — the dropdown check fired first.

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// steerRouteFixture builds an AgentLoop with two registered agents — "jim"
// (a chat target, the registry default) and "worker" (AgentTypeWorker, the
// tier ADR-091 delegates to) — plus a lifecycle store wired through the
// production field so resolver behavior matches the gateway's boot order.
func steerRouteFixture(t *testing.T) (*AgentLoop, *session.LifecycleStore) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "jim", Default: true},
		{ID: "worker", Type: config.AgentTypeWorker},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })
	registerInstance(t, al, cfg, home, "jim", config.AgentTypeCustom)
	registerInstance(t, al, cfg, home, "worker", config.AgentTypeWorker)

	ls := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	al.mu.Lock()
	al.sessionLifecycleStoreForTools = ls
	al.mu.Unlock()

	return al, ls
}

func TestResolveMessageRoute_SteeredChild_PinnedAgentBeatsDropdown(t *testing.T) {
	al, ls := steerRouteFixture(t)

	const parent = "session_parent_steer"
	const child = "session_child_a"
	persistLifecycle(t, ls, &session.LifecycleRecord{
		SessionID: child, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "worker",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parent,
			RootSessionID:     parent,
		},
	})

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: child,
		ChatID:    "webchat:tester",
		Content:   "Steering update: acknowledge with \"steer received\" in this child session only.",
		Metadata:  map[string]string{"agent_id": "jim"},
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "worker" {
		t.Errorf("route.AgentID = %q, want the record-pinned %q (dropdown agent_id=jim must not re-target a steered child)", route.AgentID, "worker")
	}
	wantKey := "agent:worker:session:" + child
	if route.SessionKey != wantKey {
		t.Errorf("route.SessionKey = %q, want %q", route.SessionKey, wantKey)
	}
	if agent == nil || agent.ID != "worker" {
		got := ""
		if agent != nil {
			got = agent.ID
		}
		t.Errorf("resolved agent = %q, want %q", got, "worker")
	}
}

func TestResolveMessageRoute_SteeredChild_PinnedAgentWithoutDropdown(t *testing.T) {
	al, ls := steerRouteFixture(t)

	const parent = "session_parent_steer"
	const child = "session_child_b"
	persistLifecycle(t, ls, &session.LifecycleRecord{
		SessionID: child, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "worker",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parent,
			RootSessionID:     parent,
		},
	})

	// No explicit agent_id metadata at all — the cascade would resolve the
	// registry default (jim) into the child's session. The pin must hold here
	// too: the child's view is the child's agent's surface.
	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: child,
		ChatID:    "webchat:tester",
		Content:   "hello child",
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "worker" {
		t.Errorf("route.AgentID = %q, want the record-pinned %q even with no dropdown selection", route.AgentID, "worker")
	}
	wantKey := "agent:worker:session:" + child
	if route.SessionKey != wantKey {
		t.Errorf("route.SessionKey = %q, want %q", route.SessionKey, wantKey)
	}
	if agent == nil || agent.ID != "worker" {
		got := ""
		if agent != nil {
			got = agent.ID
		}
		t.Errorf("resolved agent = %q, want %q", got, "worker")
	}
}

func TestResolveMessageRoute_OrdinarySession_KeepsDropdownRouting(t *testing.T) {
	al, _ := steerRouteFixture(t)

	// Control: an ordinary (non-steered) session keeps today's behavior — the
	// sticky dropdown re-targets the chat, which is the designed handoff
	// feature on roots. No lifecycle record exists for this session id.
	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "session_ordinary_root",
		ChatID:    "webchat:tester",
		Content:   "hello root",
		Metadata:  map[string]string{"agent_id": "jim"},
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" {
		t.Errorf("route.AgentID = %q, want %q (dropdown behavior on a non-steered session must not change)", route.AgentID, "jim")
	}
	if agent == nil || agent.ID != "jim" {
		got := ""
		if agent != nil {
			got = agent.ID
		}
		t.Errorf("resolved agent = %q, want %q", got, "jim")
	}
}

func TestResolveMessageRoute_OrdinaryRecordedSession_KeepsDropdownRouting(t *testing.T) {
	al, ls := steerRouteFixture(t)

	// Control for the not-steered branch WITH a record: an ordinary session
	// that HAS a durable lifecycle record but no SteeredBy edge. The record's
	// own AgentID must never act as a route pin — the pin is for steered
	// children only (pinnedSteeredAgentID's contract: SteeredBy == nil keeps
	// today's dropdown routing). The shape mirrors mintTaskLifecycleRecord
	// (task_executor.go), the production writer of non-steered records: Origin
	// task + TaskID, AgentID set, SteeredBy nil. This case is what kills the
	// M2 mutant (dropping the SteeredBy == nil condition): the no-record
	// control above exits at rec == nil and never reaches the SteeredBy
	// branch, so M2 survived CHECK with the original three tests.
	persistLifecycle(t, ls, &session.LifecycleRecord{
		SessionID: "session_ordinary_recorded_root", Generation: 1,
		State:          session.LifecycleQueued,
		WorkspaceID:    "ws",
		AgentID:        "worker",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task_ordinary_recorded"},
	})

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "session_ordinary_recorded_root",
		ChatID:    "webchat:tester",
		Content:   "hello root",
		Metadata:  map[string]string{"agent_id": "jim"},
	}

	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" {
		t.Errorf("route.AgentID = %q, want %q (a record without SteeredBy is not a pin; dropdown routing on a non-steered recorded session must not change)", route.AgentID, "jim")
	}
	wantKey := "agent:jim:session:session_ordinary_recorded_root"
	if route.SessionKey != wantKey {
		t.Errorf("route.SessionKey = %q, want %q", route.SessionKey, wantKey)
	}
	if agent == nil || agent.ID != "jim" {
		got := ""
		if agent != nil {
			got = agent.ID
		}
		t.Errorf("resolved agent = %q, want %q", got, "jim")
	}
}
