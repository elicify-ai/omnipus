package agent

// Spec rev8 S47/S72/S102/S104/S130. Real loop, encrypted store, returned bash
// session, masking, notifier, provider boundary and transcript. No paid model.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const programsBackgroundSentinel = "sentinel_9f3c2a7e5b1d"

func programsBackgroundFixture(t *testing.T, token string) (*wu5Fixture, string, <-chan AsyncNotifyEvent) {
	t.Helper()
	f := newProgramsTurnFixtureToken(t, token, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
	programsWireMessaging(t, f)
	lifecycle := f.al.GetSessionLifecycleStore()
	f.al.SetSteerAudienceDeps(NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, f.al.GetSessionStore())), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	events := make(chan AsyncNotifyEvent, 8)
	f.al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		if event.SourceKind == "bash" {
			events <- event
		}
	})
	return f, f.mainSession("sales", "dev"), events
}

func programsBashAction(t *testing.T, f *wu5Fixture, sid, call string, args map[string]any) (tools.ExecResponse, string) {
	t.Helper()
	f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: args}}}, wu5Stop("background action received")})
	programsWebTurn(t, f, sid, "run the background-output probe")
	f.waitForCalls(sid, 2, call+" real bash action")
	raw := f.provider.resultForCall(sid, call)
	var response tools.ExecResponse
	require.NoError(t, json.NewDecoder(strings.NewReader(raw)).Decode(&response), "the real bash action returned its session envelope")
	require.Empty(t, response.Error, "the actual bash action was admitted")
	return response, raw
}

func programsStartBackground(t *testing.T, f *wu5Fixture, sid, call, command string) string {
	t.Helper()
	response, _ := programsBashAction(t, f, sid, call, map[string]any{"command": command, "run_in_background": true})
	require.NotEmpty(t, response.SessionID, "the real child returned a session id, not a simulated buffer")
	return response.SessionID
}

func programsCompletion(t *testing.T, events <-chan AsyncNotifyEvent, sid string) AsyncNotifyEvent {
	t.Helper()
	select {
	case event := <-events:
		require.Equal(t, sid, event.TranscriptSessionID, "the callback belongs to the originating actual transcript")
		return event
	case <-time.After(12 * time.Second):
		t.Fatal("the actual background child did not produce its completion callback")
		return AsyncNotifyEvent{}
	}
}

func programsNoTokenWindow(t *testing.T, token, output, boundary string) {
	t.Helper()
	for i := 0; i+8 <= len(token); i++ {
		assert.NotContains(t, output, token[i:i+8], "%s: no protected eight-byte token window", boundary)
	}
}

func programsDriveActualCompletion(t *testing.T, f *wu5Fixture, sid string) string {
	t.Helper()
	var wake bus.InboundMessage
	select {
	case wake = <-f.bus.InboundChan():
	case <-time.After(12 * time.Second):
		t.Fatal("no real background continuation was published")
	}
	require.Equal(t, "async:bash", wake.Sender.CanonicalID)
	require.Equal(t, sid, wake.AsyncTranscriptSessionID)
	f.provider.setScript(sid, []providers.LLMResponse{wu5Stop("actual continuation completed")})
	_, _, err := f.al.processMessage(context.Background(), wake)
	require.NoError(t, err)
	f.waitForCalls(sid, 1, "the real completion reached the provider")
	return wake.Content
}

func programsAssertModelAndTranscript(t *testing.T, f *wu5Fixture, sid, token string) {
	t.Helper()
	f.provider.mu.Lock()
	model, err := json.Marshal(f.provider.prompts[sid])
	f.provider.mu.Unlock()
	require.NoError(t, err)
	assert.Contains(t, string(model), "[FILTERED]", "the real model-request history must contain the background result")
	programsNoTokenWindow(t, token, string(model), "recorded model requests")
	entries, err := f.al.GetSessionStore().ReadTranscript(sid)
	require.NoError(t, err)
	transcript, err := json.Marshal(entries)
	require.NoError(t, err)
	assert.Contains(t, string(transcript), "[FILTERED]", "S72/D9: the actual transcript must persist the completed result")
	programsNoTokenWindow(t, token, string(transcript), "actual persisted transcript")
}

func TestProgramMasking_ActualBackground(t *testing.T) {
	t.Run("S102_background_JSON_read", func(t *testing.T) {
		const token = `q"uo\te_tok_7731`
		f, sid, events := programsBackgroundFixture(t, token)
		child := programsStartBackground(t, f, sid, "s102-start", "echo \"$GH_TOKEN\"; sleep 5")
		time.Sleep(time.Second)
		read, raw := programsBashAction(t, f, sid, "s102-read", map[string]any{"action": "read", "session_id": child})
		require.Contains(t, read.Output, "[FILTERED]", "S102: a real JSON read carries the replacement")
		require.NotContains(t, raw, token)
		encoded, err := json.Marshal(token)
		require.NoError(t, err)
		require.NotContains(t, raw, string(encoded[1:len(encoded)-1]), "the independently derived JSON spelling is hidden too")
		programsCompletion(t, events, sid)
	})
	t.Run("S104_background_retention_and_timed_reads", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		child := programsStartBackground(t, f, sid, "s104-start", "printf 'S104_CHILD_RAN\\n'; printf '%.10s' \"$GH_TOKEN\"; sleep 3; printf '%s\\n' \"${GH_TOKEN#??????????}\"")
		time.Sleep(time.Second)
		first, rawFirst := programsBashAction(t, f, sid, "s104-first", map[string]any{"action": "read", "session_id": child})
		require.Contains(t, first.Output, "S104_CHILD_RAN", "the running child actually wrote before the first read")
		programsNoTokenWindow(t, programsBackgroundSentinel, rawFirst, "S104 first real JSON read")
		completion := programsCompletion(t, events, sid)
		assert.Contains(t, completion.Content, "[FILTERED]", "S104: completion has actual masked output, not an empty absence")
		programsNoTokenWindow(t, programsBackgroundSentinel, completion.Content, "S104 actual completion")
		last, rawLast := programsBashAction(t, f, sid, "s104-last", map[string]any{"action": "read", "session_id": child})
		require.Equal(t, "done", last.Status)
		programsNoTokenWindow(t, programsBackgroundSentinel, rawLast, "S104 completed JSON read")
	})
	for _, row := range []struct{ name, command string }{
		{"S72_background_success_filter_off", "sleep 1; echo $GH_TOKEN"},
		{"D9_row5_exit1_stdout20_background_completion", "sleep 1; printf '%.20s' \"$GH_TOKEN\"; exit 1"},
	} {
		t.Run(row.name, func(t *testing.T) {
			f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
			programsStartBackground(t, f, sid, row.name+"-start", row.command)
			completion := programsCompletion(t, events, sid)
			require.Contains(t, completion.Content, "[FILTERED]")
			programsNoTokenWindow(t, programsBackgroundSentinel, completion.Content, "actual callback")
			wake := programsDriveActualCompletion(t, f, sid)
			require.Contains(t, wake, "[FILTERED]", "the actual continuation input contains the result")
			programsNoTokenWindow(t, programsBackgroundSentinel, wake, "actual continuation input")
			programsAssertModelAndTranscript(t, f, sid, programsBackgroundSentinel)
			outbound := drainOutbound(f.bus)
			published, err := json.Marshal(outbound)
			require.NoError(t, err)
			require.Contains(t, string(published), "[FILTERED]", "S72/D9: actual published feedback, not only a notifier observer")
			programsNoTokenWindow(t, programsBackgroundSentinel, string(published), "actual published messages")
		})
	}
	t.Run("D9_row6_sessionkill_stderr20_background_read", func(t *testing.T) {
		f, sid, events := programsBackgroundFixture(t, programsBackgroundSentinel)
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		ready := filepath.Join(work, "d9-ready")
		child := programsStartBackground(t, f, sid, "d9-kill-start", fmt.Sprintf("printf '%%.20s' \"$GH_TOKEN\" >&2; printf 'ready' > %q; sleep 30", ready))
		require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 5*time.Second, 10*time.Millisecond, "the actual child wrote the fragment before the kill")
		programsBashAction(t, f, sid, "d9-kill", map[string]any{"action": "kill", "session_id": child})
		completion := programsCompletion(t, events, sid)
		programsNoTokenWindow(t, programsBackgroundSentinel, completion.Content, "actual kill callback")
		read, raw := programsBashAction(t, f, sid, "d9-kill-read", map[string]any{"action": "read", "session_id": child})
		require.Contains(t, read.Output, "[FILTERED]", "D9 row6: actual post-kill read contains the replacement")
		programsNoTokenWindow(t, programsBackgroundSentinel, raw, "actual post-kill JSON read")
	})
}
