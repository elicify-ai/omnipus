package agent

// Spec rev8 S47/S73/S74/S103 and S130/D9 row4. Actual loop-wired child
// boundaries; process-edge account fake and scripted provider only.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func programsReadMaskedLines(t *testing.T, f *wu5Fixture, sid, child, call string) {
	t.Helper()
	read, raw := programsBashAction(t, f, sid, call, map[string]any{"action": "read", "session_id": child})
	require.NotEmpty(t, strings.TrimSpace(read.Output), "the ongoing old-token child actually produced another line")
	for _, line := range strings.Split(strings.TrimSpace(read.Output), "\n") {
		assert.Equal(t, "[FILTERED]", strings.TrimSpace(line), "S47: every retained old-token output line stays masked")
	}
	require.NotContains(t, raw, "tok_valid_12345")
}

func TestProgramMasking_ActualBackgroundEdges(t *testing.T) {
	t.Run("S47_replaced_token_stays_masked", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, "tok_valid_12345")
		stop := filepath.Join(f.home, "workspaces", "sales", "work", "s47-stop")
		require.NoError(t, os.MkdirAll(filepath.Dir(stop), 0700))
		child := programsStartBackground(t, f, sid, "s47-start", fmt.Sprintf("while [ ! -f %q ]; do printf '%%s\\n' \"$GH_TOKEN\"; sleep 1; done; printf '%%s\\n' \"$GH_TOKEN\"", stop))
		time.Sleep(time.Second)
		programsReadMaskedLines(t, f, sid, child, "s47-instrument")
		bin := t.TempDir()
		if runtime.GOOS == "windows" {
			t.Fatal("BLOCKED: S47's current account-check executable fixture is POSIX; a native Windows process fixture is required separately")
		}
		check := "#!/bin/sh\n[ \"$#\" -eq 4 ] && [ \"$1\" = api ] && [ \"$2\" = user ] && [ \"$3\" = --jq ] && [ \"$4\" = .login ] || exit 8\n[ \"$GH_TOKEN\" = tok_new_654321 ] || exit 9\nprintf 'hubot\\n'\n"
		require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"), []byte(check), 0700))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		svc := programsTurnService(t, f)
		_, err := svc.Publish("github", "tok_new_654321", nil, nil)
		require.NoError(t, err)
		svc.Wait()
		require.Equal(t, "connected", svc.Connection("github").State)
		require.Equal(t, "hubot", svc.Connection("github").AccountLabel)
		time.Sleep(time.Second)
		programsReadMaskedLines(t, f, sid, child, "s47-after-replace")
		after, err := svc.Disconnect("github")
		require.NoError(t, err)
		require.Equal(t, "not_connected", after.State)
		time.Sleep(time.Second)
		programsReadMaskedLines(t, f, sid, child, "s47-after-disconnect")
		require.NoError(t, os.WriteFile(stop, []byte("finish"), 0600))
		completion := programsCompletion(t, events, sid)
		require.Contains(t, completion.Content, "[FILTERED]", "S47: the old child emits once more before completion, even after disconnection")
		require.NotContains(t, completion.Content, "tok_valid_12345")
		programsReadMaskedLines(t, f, sid, child, "s47-completed")
	})
	t.Run("D9_row4_timeout_stdout10_partial_result", func(t *testing.T) {
		f := newProgramsTurnFixtureToken(t, programsBackgroundSentinel, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
		sid := f.mainSession("sales", "dev")
		const call = "d9-timeout"
		args := map[string]any{"command": "printf 'S130_CHILD_RAN\\n'; printf '%.10s' \"$GH_TOKEN\"; sleep 30", "timeout_seconds": 1}
		f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: args}}}, wu5Stop("timeout received")})
		programsWebTurn(t, f, sid, "run the timed output probe")
		f.waitForCalls(sid, 2, "actual foreground timeout")
		result := f.provider.resultForCall(sid, call)
		require.Contains(t, result, "Partial output before timeout", "D9 row4: the actual timeout branch delivered partial output")
		require.Contains(t, result, "S130_CHILD_RAN")
		require.Contains(t, result, "[FILTERED]")
		programsNoTokenWindow(t, programsBackgroundSentinel, result, "actual foreground timeout result")
	})
	t.Run("S74_truncation_at_the_notifier_edge", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		// S74: 1 MiB minus10, then the21-byte sentinel; do not replace the
		// actual child's retention/notifier path with truncateAsyncNotifyContent.
		programsStartBackground(t, f, sid, "s74-start", "sleep 1; dd if=/dev/zero bs=1048566 count=1 2>/dev/null | tr '\\000' a; printf '%s' \"$GH_TOKEN\"")
		programsCompletion(t, events, sid)
		var wake bus.InboundMessage
		select {
		case wake = <-f.bus.InboundChan():
		case <-time.After(12 * time.Second):
			t.Fatal("S74: the real notifier did not publish its continuation input")
		}
		require.Equal(t, "async:bash", wake.Sender.CanonicalID)
		require.Equal(t, sid, wake.AsyncTranscriptSessionID)
		require.GreaterOrEqual(t, strings.Count(wake.Content, "a"), 1000, "instrument: this is the actual large child result, not empty/denied output")
		programsNoTokenWindow(t, programsBackgroundSentinel, wake.Content, "S74 actual notifier continuation input")
	})
	t.Run("S103_normal_cap_delivered_companion", func(t *testing.T) {
		f := newProgramsTurnFixtureToken(t, programsBackgroundSentinel, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
		result := programsForeground(t, f, "s103-normal", "dd if=/dev/zero bs=1000 count=1 2>/dev/null | tr '\\000' a; printf '%sEND' \"$GH_TOKEN\"")
		require.Equal(t, strings.Repeat("a", 1000)+"[FILTERED]END", result, "S103: exact normal-cap delivered companion; the instrumented80-byte cap remains a separate loud row")
		programsNoTokenWindow(t, programsBackgroundSentinel, result, "normal-cap delivered companion")
	})
}

// S73 starts at the real turn-options boundary: the spec's suppression bit is
// an input, while the loop, child, audience resolver, error persistence and event
// publication remain production code. It does not claim a gateway-toggle UAT.
func TestProgramMasking_ActualSuppressedError(t *testing.T) {
	t.Run("S73_background_error_notice_persisted", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		payloads := make(chan ToolExecEndPayload, 8)
		f.al.SetEventSyncTap(func(event Event) {
			if payload, ok := event.Payload.(ToolExecEndPayload); ok {
				payloads <- payload
			}
		})
		const call = "s73-background-error"
		f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: map[string]any{"command": "sleep 1; echo $GH_TOKEN; exit 3", "run_in_background": true}}}}, wu5Stop("error child started")})
		msg := bus.InboundMessage{Channel: "webchat", ChatID: sid, SessionID: sid, Content: "start the error-output probe", Provenance: bus.NewProvenance(bus.OriginWeb)}
		_, agent, err := f.al.resolveMessageRoute(msg)
		require.NoError(t, err)
		pm := &agentLoopProcessMessage{al: f.al, ctx: context.Background(), msg: msg, sessionKey: "agent:dev:session:" + sid, transcriptSessionID: sid, transcriptStore: f.al.GetSessionStore(), workspaceID: "sales"}
		pm.prepareTurn()
		pm.opts.SuppressToolFeedback, pm.opts.SendResponse = true, true
		_, _, err = f.al.runInboundTurnWithRevival(context.Background(), agent, msg, pm.opts)
		require.NoError(t, err)
		f.waitForCalls(sid, 2, "actual suppressed-feedback background start")
		require.Contains(t, f.provider.resultForCall(sid, call), "sessionId", "the real child was admitted and owns an actual session")
		programsCompletion(t, events, sid)
		// handleAsyncResult publishes the error notice/event BEFORE Notify.
		// The observed actual completion is therefore a deterministic barrier,
		// not a deadline widened until a missing event happens to arrive.
		var all, errors []ToolExecEndPayload
		for len(payloads) > 0 {
			payload := <-payloads
			all = append(all, payload)
			if payload.Async && payload.IsError {
				errors = append(errors, payload)
			}
		}
		require.NotEmpty(t, all, "instrument: the real event tap saw the child's tool result")
		assert.Len(t, errors, 1, "S73: one real async error event must exist after the completed callback")
		for _, payload := range errors {
			assert.Equal(t, sid, payload.SessionID)
			assert.Equal(t, "bash", payload.Tool)
			assert.Contains(t, payload.Result, "[FILTERED]", "S73: actual emitted error payload")
			programsNoTokenWindow(t, programsBackgroundSentinel, payload.Result, "actual async error event")
		}
		entries, err := f.al.GetSessionStore().ReadTranscript(sid)
		require.NoError(t, err)
		var notices []string
		for _, entry := range entries {
			if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
				notices = append(notices, entry.Content)
			}
		}
		assert.Len(t, notices, 1, "S73: the actual callback must persist exactly one error notice")
		for _, notice := range notices {
			assert.Contains(t, notice, "[FILTERED]")
			programsNoTokenWindow(t, programsBackgroundSentinel, notice, "actual persisted async error notice")
		}
	})
}
