package gateway

// Rev8 S106/I-1: actual environment_setup and FR014 git-probe children.
// Model and external executable are the only process-edge substitutes. In
// particular no completion, transcript, mask, probe or export is manufactured.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_ActualOtherChildSinks(t *testing.T) {
	t.Run("S106_environment_setup_every_sink_value_free", func(t *testing.T) {
		x := newProgramsSentinelFixture(t, false, func(cfg *config.Config) {
			cfg.Sandbox.ToolPolicies["environment_setup"] = "allow"
			cfg.Agents.List[0].Tools.Builtin.Policies["environment_setup"] = config.ToolPolicyAllow
		})
		// Default manifests defer this tool. Load its actual schema through the
		// registered discovery tool rather than bypassing the offered-tool gate.
		loaded := pgOtherToolTurn(t, x, "setup-load", "ToolSearch", map[string]any{"names": []string{"environment_setup"}})
		require.True(t, strings.Contains(loaded, "environment_setup"), "real discovery exposes the permitted setup tool")
		count, release := filepath.Join(x.work, "setup-count"), filepath.Join(x.work, "setup-release")
		t.Cleanup(func() { require.NoError(t, os.WriteFile(release, []byte("release"), 0600)) })
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; mv %q %q; while [ ! -f %q ]; do sleep 0.01; done; printf 'S106_SETUP_STDOUT\\n'; echo $GH_TOKEN; printf 'S106_SETUP_STDERR\\n' >&2; echo $GH_TOKEN >&2", count+".tmp", count+".tmp", count, release)
		started := pgOtherToolTurn(t, x, "setup-start", "environment_setup", map[string]any{
			"action": "run", "purpose": "exercise actual setup child output protection", "command": command,
		})
		var session tools.ExecResponse
		require.NoError(t, json.NewDecoder(strings.NewReader(started)).Decode(&session), "actual setup start envelope precedes its human guidance")
		require.Equal(t, "running", session.Status)
		require.NotEmpty(t, session.SessionID)
		require.Eventually(t, func() bool { _, err := os.Stat(count); return err == nil }, 5*time.Second, 10*time.Millisecond, "actual setup child wrote its count before release")
		raw, err := os.ReadFile(count)
		require.NoError(t, err)
		// Value-free diagnostics for the observed count0 failure. Do not print
		// tool results or tokens, change the expected22, or infer a cause from
		// a later PASS. Preserve the real provider's observed trusted origins.
		observedOrigins := x.provider.observedOrigins(x.dev)
		origins := make([][]bus.Origin, 0, len(observedOrigins))
		for _, provenance := range observedOrigins {
			origins = append(origins, provenance.Origins())
		}
		t.Logf("S106 setup resolution: child_count_matches22=%t model_origins=%v unresolved_notice=%t web_off_notice=%t store_unavailable_notice=%t",
			strings.TrimSpace(string(raw)) == "22", origins,
			strings.Contains(started, programs.MsgWithheldUnresolved), strings.Contains(started, programs.MsgWithheldWebChat), strings.Contains(started, programs.MsgStoreUnavailable))
		assert.Equal(t, "22", strings.TrimSpace(string(raw)), "21-byte S130 value plus printenv newline, not a configured response")
		require.NoError(t, os.WriteFile(release, []byte("release"), 0600))
		x.waitEnds(t, x.dev)
		x.provider.waitFor(x.dev, 3, "actual setup callback continuation", t)
		x.provider.mu.Lock()
		var completion string
		for _, request := range x.provider.prompts[x.dev] {
			for _, msg := range request {
				if msg.Role == "user" && strings.Contains(msg.Content, "Background session "+session.SessionID+" finished") {
					completion += msg.Content
				}
			}
		}
		x.provider.mu.Unlock()
		require.NotEmpty(t, completion, "actual setup completion reached the real notifier/consumer/model boundary")
		for _, marker := range []string{"S106_SETUP_STDOUT\n[FILTERED]", "S106_SETUP_STDERR\n[FILTERED]", "exit code 0"} {
			assert.True(t, strings.Contains(completion, marker), "actual setup completion contains %s (content deliberately not printed)", marker)
		}
		pgSinkAssertValueFree(t, []byte(completion), "actual setup completion")
		read := pgOtherToolTurn(t, x, "setup-read", "environment_setup", map[string]any{"action": "read", "session_id": session.SessionID})
		var output tools.ExecResponse
		require.NoError(t, json.NewDecoder(strings.NewReader(read)).Decode(&output))
		require.Equal(t, "done", output.Status)
		require.Zero(t, output.ExitCode)
		for _, marker := range []string{"S106_SETUP_STDOUT\n[FILTERED]", "S106_SETUP_STDERR\n[FILTERED]"} {
			assert.True(t, strings.Contains(output.Output, marker), "actual registered setup read contains masked output; content deliberately not printed")
		}
		pgOtherAssertSinks(t, x, "environment_setup", 2, "S106 actual setup-read response", "S106_SETUP_STDOUT\n[FILTERED]", "S106_SETUP_STDOUT")
		t.Logf("S106 setup assertions reached: child_count_matches22=%t; use the actual test verdict for callback/read/seven-sink acceptance", strings.TrimSpace(string(raw)) == "22")
	})
	t.Run("S106_git_version_probe_every_sink_value_free", func(t *testing.T) {
		x := newProgramsSentinelFixture(t, false)
		bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
		observation := filepath.Join(x.work, "probe-observation")
		// The real FR014 probe intentionally receives MaskOnly, not GH_TOKEN.
		// Its external executable emits the known synthetic value itself. The
		// model args never contain that value; both probe streams are exercised.
		script := fmt.Sprintf(`#!/bin/sh
if [ "$#" -eq 1 ] && [ "$1" = --version ]; then
 printf 'probe-stdout-stderr\n' >> %q
 printf 'git version 2.43.0\n%s\n'
 printf '%s\n' >&2
 exit 0
fi
[ "$#" -eq 2 ] && [ "$1" = ls-remote ] && [ "$2" = https://github.com/o/r ] || exit 81
[ "$GH_TOKEN" = %s ] || exit 82
printf 'command-after-probe\n' >> %q
printf 'S106_GIT_COMMAND_RAN\n'
`, observation, programsSinkSentinel, programsSinkSentinel, programsSinkSentinel, observation)
		require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700))
		result := pgOtherToolTurn(t, x, "git-probe", "bash", map[string]any{"command": "git ls-remote https://github.com/o/r"})
		require.Equal(t, "S106_GIT_COMMAND_RAN", strings.TrimSpace(result), "actual command succeeded after its real probe, not an approval error")
		raw, err := os.ReadFile(observation)
		require.NoError(t, err)
		require.Equal(t, "probe-stdout-stderr\ncommand-after-probe\n", string(raw), "exact actual probe-before-command inventory, no cached/no-probe false pass")
		pgOtherAssertSinks(t, x, "bash", 1, "S106 actual git-probe response", "S106 actual git-probe response", "S106_GIT_COMMAND_RAN")
		t.Log("actual S106 git probe: real_probe_before_command=true probe_stdout_and_stderr=true seven_sinks_observed=true")
	})
}

func pgOtherToolTurn(t *testing.T, x *programsSentinelFixture, call, tool string, args map[string]any) string {
	t.Helper()
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: tool, Arguments: args}}},
		wu5StopGW("S106 actual " + call + " response"),
		wu5StopGW("S106 actual asynchronous continuation"),
	})
	require.NoError(t, x.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: x.dev, ChatID: x.dev, Content: "S106 real child campaign " + call, Provenance: bus.NewProvenance(bus.OriginWeb)}))
	x.waitEnds(t, x.dev)
	x.provider.waitFor(x.dev, 2, "actual "+tool+" model-consumed result", t)
	result := x.provider.result(x.dev, call)
	require.NotEmpty(t, result, "actual tool result reached provider")
	pgSinkAssertValueFree(t, []byte(result), "actual "+tool+" result")
	return result
}

func pgOtherAssertSinks(t *testing.T, x *programsSentinelFixture, tool string, wantCalls int, reply, publishedControl, requestControl string) {
	t.Helper()
	for _, token := range []string{programsSinkSentinel, programsSinkRotated} {
		require.True(t, pgSinkHasProtectedWindow([]byte(token), token))
		require.True(t, pgSinkHasProtectedWindow([]byte(token[4:12]), token))
		require.False(t, pgSinkHasProtectedWindow([]byte(token[:7]), token))
	}
	exported := callPackageExport(t, x.api, `{"agent_ids":["dev"]}`)
	require.Equal(t, http.StatusOK, exported.Code, "actual registered export succeeds; body deliberately not printed")
	require.Equal(t, "application/octet-stream", exported.Hdr.Get("Content-Type"))
	pgSinkAssertValueFree(t, exported.Body, "actual encoded export")
	manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(exported.Body))
	require.NoError(t, err)
	require.Equal(t, programsSinkSoul, string(payload["omnipus/agents/dev/SOUL.md"]))
	_, hasRecord := payload["omnipus/agents/dev/agent.json"]
	require.True(t, hasRecord, "actual exported Dev record, not an empty archive")
	decoded, err := json.Marshal([]any{manifest, pgSinkPayloadStrings(payload)})
	require.NoError(t, err)
	pgSinkAssertValueFree(t, decoded, "actual decoded export")
	x.shutdown() // Join real producers before reading real persisted/public sinks.
	entries, err := x.al.GetSessionStore().ReadTranscript(x.dev)
	require.NoError(t, err)
	var replyCount int
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == reply {
			replyCount++
		}
	}
	require.Equal(t, 1, replyCount, "real transcript reader sees this campaign's actual reply")
	transcript, err := json.Marshal(entries)
	require.NoError(t, err)
	pgSinkAssertValueFree(t, transcript, "actual persisted transcript")
	x.provider.mu.Lock()
	requests, err := json.Marshal(x.provider.prompts[x.dev])
	x.provider.mu.Unlock()
	require.NoError(t, err)
	require.True(t, bytes.Contains(requests, []byte("S106 real child campaign")), "actual model input observed")
	require.True(t, bytes.Contains(requests, []byte(requestControl)), "actual model records contain real child output")
	pgSinkAssertValueFree(t, requests, "actual model request records")
	var messages []bus.OutboundMessage
	var contents []string
	for len(x.bus.OutboundChan()) > 0 {
		message := <-x.bus.OutboundChan()
		messages = append(messages, message)
		contents = append(contents, message.Content)
	}
	require.NotEmpty(t, messages)
	assert.True(t, strings.Contains(strings.Join(contents, "\n"), publishedControl), "real chat publication contains the campaign control; content deliberately not printed")
	published, err := json.Marshal(messages)
	require.NoError(t, err)
	pgSinkAssertValueFree(t, published, "actual published chat records")
	gatewayLog, err := os.ReadFile(filepath.Join(x.home, "logs", "gateway.log"))
	require.NoError(t, err)
	for _, marker := range []string{"S48_NATIVE_LOG_READER_CONTROL", "S48_SLOG_READER_CONTROL", "Tool call: " + tool} {
		require.True(t, bytes.Contains(gatewayLog, []byte(marker)), "real gateway log contains %s", marker)
	}
	pgSinkAssertValueFree(t, gatewayLog, "actual gateway file log")
	auditRaw, err := os.ReadFile(filepath.Join(x.home, "system", "audit.jsonl"))
	require.NoError(t, err)
	var calls, exports int
	for _, line := range bytes.Split(bytes.TrimSpace(auditRaw), []byte{'\n'}) {
		var row audit.Entry
		require.NoError(t, json.Unmarshal(line, &row))
		if row.Event == audit.EventToolCall && row.Tool == tool && row.SessionID == x.dev {
			calls++
		}
		if row.Event == audit.EventPackageExport && row.AgentID == "dev" {
			exports++
		}
	}
	require.Equal(t, wantCalls, calls, "real registered tool audit rows")
	require.Equal(t, 1, exports, "real registered export audit row")
	pgSinkAssertValueFree(t, auditRaw, "actual shared audit file")
	label := x.svc.Connection("github").AccountLabel
	require.Equal(t, "octocat", label, "actual checked account label")
	pgSinkAssertValueFree(t, []byte(label), "actual checked account label")
	t.Logf("actual seven sinks: tool=%s calls=%d transcript_entries=%d published_messages=%d exported_files=%d", tool, calls, len(entries), len(messages), len(payload))
}
