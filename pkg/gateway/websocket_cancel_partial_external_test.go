package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestCancelPartialNotice_ReachesOriginatingExternalChannel(t *testing.T) {
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	home := t.TempDir()
	al := mustAgentLoop(t, &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}},
		List:     []config.AgentConfig{{ID: "mia", Home: home}},
	}}, msgBus, &restMockProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	const sessionID = "external-stop-child"
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: sessionID, Generation: 1, State: session.LifecycleRunning,
		AgentID: "mia", OwnerScopeKind: session.OwnerScopeParentSession,
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: "parent", RootSessionID: "parent",
			ReportingTarget: session.ReportingTarget{Channel: "telegram", ChatID: "chat-42"},
		},
	}); err != nil {
		t.Fatalf("seed lifecycle: %v", err)
	}
	h := &WSHandler{agentLoop: al, msgBus: msgBus}
	h.sendExternalCancelPartialNotice(context.Background(), sessionID, steer.CancelReport{
		Reached: []string{sessionID}, Unreachable: []steer.UnreachableSession{{ID: "grandchild", Reason: "unreadable"}},
	})
	select {
	case got := <-msgBus.OutboundChan():
		if got.Channel != "telegram" || got.ChatID != "chat-42" || got.Content != "stopped 1 of 2; 1 unreachable" {
			t.Fatalf("external partial notice = %+v", got)
		}
	default:
		t.Fatal("partial Stop notice was not published to the originating channel")
	}
}
