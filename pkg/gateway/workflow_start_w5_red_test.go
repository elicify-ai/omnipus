package gateway

// Order 20 / CHECK F17: C-8 and WorkflowStart's per-workflow requirement.
// Replace workflowStartTargetRequired's whole-decision boolean with the
// real authenticated WebSocket read loop, validation, chat admission,
// inbound bus and persisted transcript. A ping/pong is the intake barrier;
// absence is inspected only after that barrier, never after an arbitrary sleep.
// This boundary test does NOT certify S-53's model-driven write-skill load
// or category/publication chain (order 55/other F17 work still required).

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowStart_W5(t *testing.T) {
	// Literal C-8 cases: only W1/W18 require a target; W5 may propose
	// global installation, and unknown workflows remain refused. Both
	// validation settings exercise the server check, not just the schema.
	rows := []struct {
		name, workflow string
		target         bool
		accepted       bool
	}{
		{"W5 without target is accepted", "W5", false, true},
		{"W5 with target is accepted", "W5", true, true},
		{"W1 without target is refused", "W1", false, false},
		{"W18 without target is refused", "W18", false, false},
		{"W1 with target is accepted", "W1", true, true},
		{"W18 with target is accepted", "W18", true, true},
		{"unknown workflow is refused", "W9", true, false},
	}
	for _, validate := range []bool{false, true} {
		mode := "validation off/"
		if validate {
			mode = "validation on/"
		}
		for _, row := range rows {
			t.Run(mode+row.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv(config.EnvHome, home)
				t.Setenv("OMNIPUS_BEARER_TOKEN", "")
				cfg := config.DefaultConfig()
				cfg.Gateway.DevModeBypass, cfg.Gateway.ValidateInbound = true, validate
				cfg.Agents.List = []config.AgentConfig{{ID: "omnipus"}}
				msgBus := bus.NewMessageBus()
				t.Cleanup(msgBus.Close)
				al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
				main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
				require.NoError(t, err, "fixture: Omnipus uses its one real main conversation")
				handler := newWSHandler(msgBus, al, "")
				t.Cleanup(handler.Wait)
				srv := httptest.NewServer(handler)
				t.Cleanup(srv.Close)
				conn := wu0aWSConn(t, srv)
				agentID := "omnipus"
				frame := generated.MessageFrame{
					Type: string(generated.WsFrameTypeMessage), AgentId: &agentID,
					Content:       "Write a new skill",
					SessionId:     &main.ID,
					WorkflowStart: &generated.WorkflowStart{Workflow: row.workflow},
				}
				if row.target {
					target := testHarnessWorkspaceMembershipID
					frame.WorkflowStart.TargetWorkspaceId = &target
				}
				body, err := json.Marshal(frame)
				require.NoError(t, err)
				before, err := al.GetSessionStore().ReadTranscript(main.ID)
				require.NoError(t, err)
				errs := wu0aSendAndCollect(t, conn, string(body))
				// No loop consumes inbound messages in this intake-boundary test.
				// Pong proves the read loop finished this frame before inspection.
				if !row.accepted {
					assert.Len(t, errs, 1, "C-8: one real refusal, not a fixture predicate")
					if len(errs) == 1 {
						assert.NotEmpty(t, errs[0], "the wire refusal explains the rejected start; C-8 specifies no literal copy")
					}
					assert.Empty(t, msgBus.InboundChan(), "refusal must admit no turn")
					after, readErr := al.GetSessionStore().ReadTranscript(main.ID)
					require.NoError(t, readErr)
					assert.Equal(t, before, after, "a rejected start writes no chat message")
					return
				}
				require.Empty(t, errs, "the actual W5/target control is admitted")
				require.Len(t, msgBus.InboundChan(), 1, "exactly one real chat message was admitted")
				message := <-msgBus.InboundChan()
				require.Equal(t, "Write a new skill", message.Content, "the person's original message is retained")
				require.NotEmpty(t, message.SessionID)
				meta, err := al.GetSessionStore().GetMeta(message.SessionID)
				require.NoError(t, err)
				require.Equal(t, "omnipus", meta.AgentID, "the real chat belongs to Omnipus")
				entries, err := al.GetSessionStore().ReadTranscript(message.SessionID)
				require.NoError(t, err)
				var userContent []string
				for _, entry := range entries {
					if entry.Role == "user" {
						userContent = append(userContent, entry.Content)
					}
				}
				require.Equal(t, []string{"Write a new skill"}, userContent, "the actual message was persisted once")
			})
		}
	}
}
