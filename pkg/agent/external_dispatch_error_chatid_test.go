// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_dispatch_error_chatid_test.go — FIX 4 regression:
// emitExternalCLIErrorEvent (external_dispatch.go) built its ErrorPayload
// with no ChatID, unlike every other ErrorPayload construction site. The WS
// forwarder's matchesChatID(p.ChatID) gate (pkg/gateway/websocket.go) never
// matches an empty ChatID, so the frame was silently dropped for every live
// subscriber — external-CLI (claude-code/codex/opencode sub-turn) errors
// were invisible live, appearing only after a page reload replayed the
// transcript.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestExternalDispatch_ErrorEvent_CarriesChatID is the FIX 4 regression test.
// It reuses the same newExternalTestLoop/withFakeDriver harness as the
// neighboring TestExternalDispatch_SanitizesRunnerError, adding a ChatID to
// the hand-built turnState (the harness leaves it at its zero value) and
// asserting the resulting EventKindError payload carries it through.
func TestExternalDispatch_ErrorEvent_CarriesChatID(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "codex", "")
	const wantChatID = "chat-ext-fix4"
	ts.opts.ChatID = wantChatID
	ts.chatID = wantChatID

	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	ts.transcriptStore = store
	ts.transcriptSessionID = "session_ext_fix4"

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err: &runner.ErrorEvent{
				Message: "fatal: connection reset",
				Fatal:   true,
			},
		})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.Error(t, err, "fatal error event must surface as a run failure")
	require.NotNil(t, res)

	var found bool
drain:
	for i := 0; i < 16; i++ {
		select {
		case ev := <-sub.C:
			if ev.Kind != EventKindError {
				continue
			}
			ep, ok := ev.Payload.(ErrorPayload)
			if !ok || ep.Stage != "external_cli" {
				continue
			}
			found = true
			assert.Equal(t, wantChatID, ep.ChatID,
				"ErrorPayload.ChatID must be set from ts.opts.ChatID — the WS forwarder's "+
					"matchesChatID gate never matches an empty ChatID, so an unset value "+
					"silently drops the frame for every live subscriber")
			assert.NotEmpty(t, ep.Code,
				"ErrorPayload.Code should also be populated (FIX 3) from the sanitizer's own classification")
		default:
			break drain
		}
	}
	require.True(t, found, "EventKindError for external_cli stage must be emitted on the bus")
}
