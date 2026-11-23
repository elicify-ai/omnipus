package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func opencodeR4PriorStart(t *testing.T, al *AgentLoop, sid string) *session.LifecycleRecord {
	t.Helper()
	require.NoError(t, al.GetSessionLifecycleStore().Mutate(sid, func(rec *session.LifecycleRecord) error {
		rec.ExternalRunStarted = true
		return nil
	}))
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	return rec
}

func opencodeR4Classified(t *testing.T, err error, wantChat string) LLMError {
	t.Helper()
	assert.ErrorIs(t, err, errExternalResumeUnavailable, "D3: real producer identity must survive its contextual wrapping")
	llm := TranslateTurnError(err)
	assert.Equal(t, opencodeR4Code, string(llm.Code), "D3/B16a: select by errors.Is, never unknown or external_cli_failed")
	assert.Equal(t, "ambiguous", string(AttributionForCode(llm.Code)))
	assert.Equal(t, wantChat, llm.Message, "D3: exact W14 chat form")
	assert.NotContains(t, llm.Message, "workspace", "B16b workspace refusal is NOT form 1")
	return llm
}

func opencodeR4ParentCompletion(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord, err error, neutral string) {
	t.Helper()
	outcome, state, reason := completionDisposition(turnResult{status: TurnEndStatusError}, err, "")
	assert.Equal(t, steer.OutcomeFailed, outcome)
	assert.Equal(t, session.LifecycleFailed, state)
	assert.Equal(t, "failed: "+neutral, reason, "D3: parent disposition uses the neutral twin, not first person")
	message, buildErr := al.completionMessage(rec, outcome, "", reason)
	require.NoError(t, buildErr)
	class, classErr := session.ClassifySessionMessage(message)
	require.NoError(t, classErr)
	require.Equal(t, "error", class.Kind)
	parentError, decodeErr := message.AsSessionMessageError()
	require.NoError(t, decodeErr)
	assert.Equal(t, "failed: "+neutral, parentError.Text, "D3: actual parent completion payload retains neutral W14")
}

// R4-RED-5 / D3: call all THREE actual producers. The raw sentinel carries no
// CLI fact, so raw/ordinary %w chains must use W14's no-fact fallback. Named
// facts are then tested through actual dispatch/revive, never by forcing a
// code into a test-authored externalCLIRefusalError wrapper.
func TestOpenCodeR4_05_ResumeUnavailableClassifiesAtEveryProducerAndSurface(t *testing.T) {
	for _, cli := range opencodeR4CLIs {
		t.Run(cli.key+"/producers", func(t *testing.T) {
			al, _ := opencodeR4Loop(t, cli.key)
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "producer classification")
			rec := opencodeR4PriorStart(t, al, child.SessionID)
			reservation, reserveErr := al.reserveExternalConversation(child.SessionID, rec)
			require.Nil(t, reservation)
			ts := &turnState{transcriptSessionID: child.SessionID, generation: child.Generation}
			holder, resume, beginErr := al.beginExternalRun(child.SessionID, ts, func() {}, true)
			require.False(t, resume)
			require.NotNil(t, holder)
			driver, driverErr := holder.driverForRun(cli.key, nil, true)
			require.Nil(t, driver)
			for _, producer := range []struct {
				name string
				err  error
			}{
				{"reserveExternalConversation", reserveErr},
				{"beginExternalRun", beginErr},
				{"driverForRun", driverErr},
			} {
				t.Run(producer.name, func(t *testing.T) {
					require.ErrorIs(t, producer.err, errExternalResumeUnavailable, "instrument: reached the real producer's missing-conversation branch")
					for _, wrapping := range []struct {
						name string
						err  error
					}{
						{"direct", producer.err},
						{"ordinary_dispatch_wrapper", fmt.Errorf("external-cli dispatch: %w", producer.err)},
						{"ordinary_revive_wrapper", fmt.Errorf("steer: revive %q: %w", child.SessionID, producer.err)},
					} {
						t.Run(wrapping.name, func(t *testing.T) {
							opencodeR4Classified(t, wrapping.err, opencodeR4NoFact)
						})
					}
				})
			}
			t.Run("workspace_identity_is_not_resume_unavailable", func(t *testing.T) {
				err := fmt.Errorf("external-cli dispatch: %w", errExternalWorkspaceChanged)
				assert.NotErrorIs(t, err, errExternalResumeUnavailable)
				llm := TranslateTurnError(err)
				assert.NotEqual(t, opencodeR4Code, string(llm.Code), "D3: no unwired B16b/form-2 variant in this landing")
				assert.NotEqual(t, opencodeR4NoFact, llm.Message)
			})
			t.Run("same_text_without_identity_is_not_the_sentinel", func(t *testing.T) {
				imitation := errors.New(errExternalResumeUnavailable.Error())
				assert.NotErrorIs(t, imitation, errExternalResumeUnavailable)
				assert.NotEqual(t, opencodeR4Code, string(TranslateTurnError(imitation).Code), "D3: parsing the sentinel's text is not identity classification")
			})
		})
	}

	for _, cli := range append(append([]struct{ key, display string }{}, opencodeR4CLIs...), struct{ key, display string }{"unmapped-cli-do-not-echo", ""}) {
		t.Run(cli.key+"/real_dispatch_and_parent_surfaces", func(t *testing.T) {
			launchCLI := cli.key
			if cli.display == "" {
				launchCLI = "opencode" // Launch requires a configured, supported program.
			}
			al, _ := opencodeR4Loop(t, launchCLI)
			_, processes := opencodeR4ProcessEdge(t, al)
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "resume-only classified dispatch")
			rec := opencodeR4PriorStart(t, al, child.SessionID)
			worker, ok := al.registry.GetAgent(delegateExtCLIAgentID)
			require.True(t, ok)
			// The classifier must handle a now-unmapped executor fact on a
			// previously launched helper, without ever echoing raw config text.
			worker.Subagents.Executor.CLI = cli.key
			ts := newTurnState(worker, processOptions{
				SessionKey: child.SessionID, TranscriptSessionID: child.SessionID,
				TranscriptStore: al.ResolveSessionStore(child.SessionID), WorkspaceID: rec.WorkspaceID,
				Channel: "webchat", ChatID: child.SessionID,
			}, al.newTurnEventScope(worker.ID, child.SessionID))
			ts.generation = child.Generation
			result, runErr := runExternalCLISubTurn(context.Background(), al, ts, "new instruction after lost conversation")
			wantChat := opencodeR4NoFact
			if cli.display != "" {
				wantChat = opencodeR4Chat(cli.display)
			}
			opencodeR4Classified(t, runErr, wantChat)
			assert.Zero(t, processes.Load(), "D3: classification must never start a replacement process")
			if cli.display == "" {
				assert.NotContains(t, TranslateTurnError(runErr).Message, cli.key, "closed display-name map must not echo unknown config")
				return // This branch's no-fact message is fully asserted above.
			}
			llm := TranslateTurnError(runErr)
			assert.Equal(t, opencodeR4Neutral(cli.display), llm.NeutralMessage)
			if assert.NotNil(t, result, "D3: real parent tool result must carry the classified refusal, not nil") {
				assert.ErrorIs(t, result.Err, errExternalResumeUnavailable)
				assert.Contains(t, result.ContentForLLM(), opencodeR4Code)
				assert.Equal(t, 1, strings.Count(result.ContentForLLM(), opencodeR4Neutral(cli.display)), "D3: tool result contains the neutral W14 sentence once")
				assert.NotContains(t, result.ContentForLLM(), wantChat, "parent tool result must not use chat voice")
			}
			opencodeR4ParentCompletion(t, al, rec, runErr, opencodeR4Neutral(cli.display))
		})

		if cli.display == "" {
			continue // Real revive needs one of the three supported executor kinds.
		}
		t.Run(cli.key+"/real_revive_preserves_identity_and_facts", func(t *testing.T) {
			al, _ := opencodeR4Loop(t, cli.key)
			child := launchDelegateTo(t, al, delegateExtCLIAgentID, "lost conversation before revive")
			rec := opencodeR4Stopped(t, al, child.SessionID)
			revived, reviveErr := al.ReviveStoppedSession(context.Background(), child.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "r4-person"}, "carry on")
			assert.False(t, revived)
			opencodeR4Classified(t, reviveErr, opencodeR4Chat(cli.display))
			opencodeR4ParentCompletion(t, al, rec, reviveErr, opencodeR4Neutral(cli.display))
		})
	}
}
