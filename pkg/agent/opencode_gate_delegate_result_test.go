package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ARCHITECT A1 / docs/agents.md, Workers and delegation: a startup instruction
// waits until the current run ends. Test the parent's real registered tool,
// not a delivery fake returning the wording we want to see.
func TestOpenCodeGateA1_DelegateResultDistinguishesQueuedStartupFromInterrupt(t *testing.T) {
	for _, action := range []string{"steer", "respond"} {
		for _, ready := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ready_%t", action, ready), func(t *testing.T) {
				al, driver, child := newOpencodeFixRun(t, ready, nil)
				al.SetSteerSessionLauncher(NewSteerLauncher(al)) // The gateway's real post-boot tool wiring.
				rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
				require.NoError(t, err)
				require.NotNil(t, rec.SteeredBy)
				parent := rec.SteeringSessionID()
				owner, ok := al.registry.GetAgent(testDefaultAgentID)
				require.True(t, ok)
				tool, ok := owner.Tools.Get("delegate")
				require.True(t, ok, "reachability: exercise the production-registered delegate tool")
				const correlation = "gate-a1-question"
				if action == "respond" {
					var question generated.SessionMessage
					require.NoError(t, question.FromSessionMessageQuestion(generated.SessionMessageQuestion{
						MessageId: "gate-a1-question-row", SessionId: child.SessionID, ParentSessionId: &parent,
						CreatedAt: time.Now().UTC(), Depth: 1, UntrustedOrigin: true,
						SenderIdentity: rec.AgentID, Text: "Which next step?", CorrelationId: correlation,
					}))
					_, err = al.GetMessageInboxStore().Append(parent, question)
					require.NoError(t, err, "respond must answer a genuinely open parent-inbox question")
				}
				const text = "apply the additional instruction to this same conversation"
				result := tool.Execute(tools.WithTranscriptSessionID(context.Background(), parent), map[string]any{
					"action": action, "session_id": child.SessionID, "text": text, "correlation_id": correlation,
				})
				require.NotNil(t, result)
				require.False(t, result.IsError, "tool admission and delivery must succeed: %s", result.ForLLM)
				driver.mu.Lock()
				interrupted := driver.firstCtx.Err()
				driver.mu.Unlock()
				if ready {
					assert.ErrorIs(t, interrupted, context.Canceled, "ready run must really be interrupted")
				} else {
					assert.NoError(t, interrupted, "starting process must stay alive while input waits")
					assert.Equal(t, 1, al.pendingSteeringCountForScope(child.SessionID), "the accepted input must actually be queued")
				}
				if action == "respond" {
					// DelegateRespondResponse.yaml is explicitly acknowledgement-only.
					// The internal note is NOT a parent-visible sentence. No new wire
					// field is authorized by A1; this is a same-conversation control.
					assert.JSONEq(t, `{"acknowledged":true}`, result.ForLLM)
				} else if ready {
					assert.Equal(t, fmt.Sprintf("Steering message delivered to external CLI session %s by interrupt + native-conversation resume (correlation_id=%s); it will reach the same conversation.", child.SessionID, correlation), result.ForLLM, "commission: preserve the interrupt case's wording")
				} else {
					notice := strings.ToLower(result.ForLLM)
					assert.Contains(t, notice, "queued", "A1: parent must be told this was queued, not already delivered")
					assert.Contains(t, notice, "after", "A1: parent must be told delivery follows the current run")
					assert.Contains(t, notice, "current run", "A1: explain which run must finish first")
					assert.NotContains(t, notice, "interrupt", "A1: a startup queue acknowledgement must not claim an interrupt")
					assert.NotContains(t, notice, "resume", "07:55Z scope note: queued wording must name neither interrupt nor resume")
				}
				driver.finishFirst()
				opencodeFixJoin(t, al)
				driver.mu.Lock()
				defer driver.mu.Unlock()
				assert.Len(t, driver.runOpts, 1, "never create a replacement conversation")
				assert.Equal(t, []string{opencodeFixNativeID}, driver.resumeIDs)
				require.Len(t, driver.resumeOpts, 1)
				assert.Equal(t, 1, strings.Count(driver.resumeOpts[0].Input, text), "same-conversation input delivered exactly once")
			})
		}
	}
}
