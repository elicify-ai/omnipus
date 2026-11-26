package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEST-ANALYSIS gap 1: isolate each predicate of the REAL task-chat gate.
// These are classifier controls, not claims that every synthetic record is a
// supported new-chat target. The positive task root is driven by GatewayA/B.
func TestOpenCodeR5_TaskChatPredicateExcludesHelperNativeAndNonTask(t *testing.T) {
	for _, kind := range []string{"delegated_helper", "native_worker", "non_task_origin"} {
		t.Run(kind, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, "opencode")
			store := f.handler.agentLoop.ResolveSessionStore(f.sid)
			before := qa2ReopenHelperTranscript(t, store, f.sid)
			require.NoError(t, f.handler.agentLoop.GetSessionLifecycleStore().Mutate(f.sid, func(rec *session.LifecycleRecord) error {
				switch kind {
				case "delegated_helper":
					rec.SteeredBy = &session.SteeredBy{SteeringSessionID: "r5-parent", RootSessionID: "r5-parent"}
				case "native_worker":
					rec.Is3P = false
				case "non_task_origin":
					rec.Origin = &session.Origin{Kind: session.OriginKindChat}
				}
				return nil
			}))
			var err error
			f.record, err = f.handler.agentLoop.GetSessionLifecycleStore().Load(f.sid)
			require.NoError(t, err)
			hcm := &wsHandlerHandleChatMessage{h: f.handler, wc: f.conn, frameSessionID: f.sid, clientMessageID: opencodeR5Client}
			assert.Nil(t, hcm.classifyExternalTaskRunChat(opencodeR4GWWorker), "gap 1: Q13 B task-chat refusal must not capture %s", kind)
			visible, _ := opencodeR5Visible(t, f)
			assert.Empty(t, visible, "gap 1: leave non-task refusal/delivery to its existing path")
			assert.Equal(t, before, qa2ReopenHelperTranscript(t, store, f.sid), "gap 1: classifier non-applicability has no write")
			f.assertNoAdmission(t)
		})
	}
}

// TEXT.md section 3 / TEST-ANALYSIS gap 5 / CHECK F2. Not a catalogue sentence.
func TestOpenCodeR5_TaskChatMissingCLIDisplayUsesExactClauseFreeSentence(t *testing.T) {
	const want = "I couldn't deliver your message: this task doesn't accept chat messages here. To change the work, open Tasks and stop the task or its plan before editing the task."
	for _, rawCLI := range []string{"", "unmapped-cli-must-not-echo"} {
		for _, explicit := range []bool{false, true} {
			name := rawCLI + "/owner_resolved"
			if explicit {
				name = rawCLI + "/explicit_worker"
			}
			t.Run(name, func(t *testing.T) {
				f := opencodeR4GatewayFixture(t, "opencode")
				// An existing task remains addressable if its executor fact is
				// missing/unmapped now. Do not require launching an invalid CLI.
				for i := range f.handler.agentLoop.GetConfig().Agents.List {
					ac := &f.handler.agentLoop.GetConfig().Agents.List[i]
					if ac.ID == opencodeR4GWWorker {
						ac.Subagents.Executor.CLI = rawCLI
					}
				}
				registered, ok := f.handler.agentLoop.GetRegistry().GetAgent(opencodeR4GWWorker)
				require.True(t, ok)
				registered.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: rawCLI}}
				opencodeR5Send(t, f, f.sid, f.task.WorkspaceID, explicit)
				visible, _ := opencodeR5Visible(t, f)
				f.assertNoAdmission(t)
				if assert.Len(t, visible, 1) {
					assert.Equal(t, want, visible[0].text)
					assert.Equal(t, f.sid, visible[0].sessionID)
					assert.NotContains(t, visible[0].text, "{cli}")
					assert.NotContains(t, visible[0].text, "runs in  and")
					assert.NotContains(t, visible[0].text, "unmapped-cli-must-not-echo")
				}
				rows := opencodeR5ErrorRows(qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid))
				if assert.Len(t, rows, 1, "gap 5: clause-free refusal is durable, not just visible") {
					assert.Equal(t, want, rows[0].Content)
					assert.Empty(t, rows[0].ErrorCode, "architect ruling: task-chat refusal remains uncoded")
				}
			})
		}
	}
}
