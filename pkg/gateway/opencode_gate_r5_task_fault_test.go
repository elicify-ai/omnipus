package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ARCHITECT round5 F2: effect-free ordinary validation comes before Q13 B.
func TestOpenCodeR5_TaskChatWrongWorkspaceHasNoRefusalWriteOrCLIDisclosure(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "owner_resolved"
		if explicit {
			name = "explicit_worker"
		}
		t.Run(name, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, "opencode")
			const otherWorkspace = "01JXR5DIFFERENTWORKSPACE001"
			home := f.handler.agentLoop.GetConfig().Agents.Defaults.Home
			require.NotEmpty(t, home, "instrument: alternate workspace must use the configured temporary home")
			writeWorkspaceRecord(t, home, otherWorkspace, false)
			before := qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid)
			opencodeR5Send(t, f, f.sid, otherWorkspace, explicit)
			visible, types := opencodeR5Visible(t, f)
			f.assertNoAdmission(t)
			require.Len(t, visible, 1, "F2: wrong workspace receives only the ordinary validation refusal")
			// F2's validation-before-effects rule is independent of whether
			// the worker was explicit; prefer this specific workspace refusal
			// over the former generic explicit-worker-target rejection.
			assert.Equal(t, session.ErrWorkspaceImmutable.Error(), visible[0].text, "F2: workspace validation wins over task classification in both frame forms")
			assert.NotContains(t, visible[0].text, "OpenCode", "F2: invalid workspace must not disclose the task's CLI")
			assert.NotContains(t, types, string(generated.WsFrameTypeSessionStarted))
			assert.Equal(t, before, qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid), "F2: rejected frame must not append a task refusal")
		})
	}
}

// F2 negative control: an unknown id must never borrow the attached task's identity.
func TestOpenCodeR5_TaskChatUnknownSessionHasNoRefusalWriteOrCLIDisclosure(t *testing.T) {
	f := opencodeR4GatewayFixture(t, "opencode")
	before := qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid)
	metas, err := f.handler.agentLoop.GetSessionStore().ListSessions()
	require.NoError(t, err)
	const unknown = "r5-unknown-session"
	require.Nil(t, f.handler.agentLoop.ResolveSessionStore(unknown), "instrument: unknown id really has no owning store")
	opencodeR5Send(t, f, unknown, f.task.WorkspaceID, false)
	visible, _ := opencodeR5Visible(t, f)
	f.assertNoAdmission(t)
	require.Len(t, visible, 1)
	assert.Equal(t, "session not found", visible[0].text, "F2: retain the existing lookup rejection")
	assert.NotContains(t, visible[0].text, "OpenCode")
	assert.Equal(t, before, qa2ReopenHelperTranscript(t, f.handler.agentLoop.ResolveSessionStore(f.sid), f.sid))
	after, err := f.handler.agentLoop.GetSessionStore().ListSessions()
	require.NoError(t, err)
	assert.Equal(t, metas, after, "F2: an unknown session must not be fabricated or persisted")
}

// SFH-R5-1: fault the real refusal append after otherwise healthy classification.
func TestOpenCodeR5_TaskChatRefusalSaveFailureIsSeparatelyVisibleAndNonTerminal(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "owner_resolved"
		if explicit {
			name = "explicit_worker"
		}
		t.Run(name, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, "opencode")
			store := f.handler.agentLoop.ResolveSessionStore(f.sid)
			before := qa2ReopenHelperTranscript(t, store, f.sid)
			output := opencodeR5CaptureLog(t)
			restore, path := opencodeGateBlockTranscript(t, store, f.sid)
			_, cause := f.handler.agentLoop.AppendSessionRefusal(f.sid, opencodeR4GWWorker, "", "storage instrument")
			require.Error(t, cause, "instrument: the actual refusal writer sees the archive fault")
			opencodeR5Send(t, f, f.sid, f.task.WorkspaceID, explicit)
			visible, types := opencodeR5Visible(t, f)
			restore()
			f.assertNoAdmission(t)
			assert.Equal(t, before, qa2ReopenHelperTranscript(t, store, f.sid), "SFH-R5-1: do not falsely claim the refusal was saved")
			opencodeR5AssertCauseLogged(t, output, cause)
			var original, warnings int
			for _, notice := range visible {
				assert.Equal(t, f.sid, notice.sessionID)
				assert.True(t, notice.nonTerminal, "SFH-R5-1: no notice may end the task's live answer")
				assert.NotContains(t, notice.text, path, "storage paths belong in the log, not product copy")
				if notice.text == opencodeR4TaskChatSentence("opencode") {
					original++
				} else {
					warnings++
					opencodeR5AssertUnsavedWarning(t, notice.text)
				}
			}
			assert.Equal(t, 1, original, "SFH-R5-1: preserve the original refusal exactly once")
			assert.Equal(t, 1, warnings, "SFH-R5-1: separate approved unsaved-history warning is missing")
			assert.NotContains(t, types, string(generated.WsFrameTypeDone))
		})
	}
}

// SFH-R5-3: no generic-worker fallback and no owner-resolved admission on uncertainty.
func TestOpenCodeR5_TaskChatLifecycleReadFailureIsVisibleSavedAndNeverAdmitted(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "owner_resolved"
		if explicit {
			name = "explicit_worker"
		}
		t.Run(name, func(t *testing.T) {
			f := opencodeR4GatewayFixture(t, "opencode")
			store := f.handler.agentLoop.ResolveSessionStore(f.sid)
			before := qa2ReopenHelperTranscript(t, store, f.sid)
			output := opencodeR5CaptureLog(t)
			restore, cause := opencodeR5FaultLifecycle(t, f)
			require.Equal(t, before, qa2ReopenHelperTranscript(t, store, f.sid), "instrument: transcript remains readable during the lifecycle-only fault")
			opencodeR5Send(t, f, f.sid, f.task.WorkspaceID, explicit)
			visible, types := opencodeR5Visible(t, f)
			restore()
			f.assertNoAdmission(t)
			opencodeR5AssertCauseLogged(t, output, cause)
			entries := qa2ReopenHelperTranscript(t, store, f.sid)
			for _, entry := range entries {
				assert.NotEqual(t, opencodeR5Input, entry.Content, "SFH-R5-3: a detected read failure must not persist accepted input")
			}
			saved := opencodeR5ErrorRows(entries)
			if assert.Len(t, visible, 1, "SFH-R5-3: surface one session-state read refusal") {
				notice := visible[0]
				assert.Equal(t, f.sid, notice.sessionID)
				assert.Equal(t, opencodeR5Client, notice.clientID, "SFH-R5-3: correlate the failed intake, not a different turn")
				assert.True(t, notice.nonTerminal)
				// Literal oracle: prompt-r4/TEXT.md section 5, not catalogue copy
				// and not a prefix for serverFailureText composition.
				const want = "I couldn't deliver your message because I couldn't read this task's state. Check disk space and permissions, then retry."
				assert.Equal(t, want, notice.text, "SFH-R5-3: the exact approved read-failure refusal must surface")
				assert.NotEqual(t, opencodeR4TaskChatSentence("opencode"), notice.text, "classification did not succeed")
				assert.NotContains(t, notice.text, cause.Error())
				if assert.Len(t, saved, 1, "SFH-R5-3: healthy transcript must retain the read-failure refusal") {
					assert.Equal(t, notice.text, saved[0].Content)
				}
			}
			assert.NotContains(t, types, string(generated.WsFrameTypeDone))
		})
	}
}
