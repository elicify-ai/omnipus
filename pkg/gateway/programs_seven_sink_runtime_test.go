// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// S48 all seven sinks belong to this single real runtime/rotation/export campaign.
// S106's foreground and background mandatory-kind rows share this actual child
// campaign: each executes success/error paths; neither is a source-only guard.
// Absence is checked only after every sink's nonsecret presence control is seen.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_ActualSevenSinks(t *testing.T) {
	t.Run("S48_every_sink_value_free", func(t *testing.T) {
		pgSinkRunCampaign(t, false)
	})
}

func TestProgramMasking_ActualGodModeSinks(t *testing.T) {
	t.Run("S106_God_Mode_every_sink_value_free", func(t *testing.T) {
		pgSinkRunCampaign(t, true)
	})
}

func pgSinkRunCampaign(t *testing.T, god bool) {
	t.Helper()
	// Test the value-absence instrument without injecting protected bytes into
	// any real product sink. Eight is the I-1 boundary; seven is permitted.
	for _, token := range []string{programsSinkSentinel, programsSinkRotated} {
		require.True(t, pgSinkHasProtectedWindow([]byte("before"+token+"after"), token))
		require.True(t, pgSinkHasProtectedWindow([]byte(token[4:12]), token))
		require.False(t, pgSinkHasProtectedWindow([]byte(token[:7]), token))
	}
	x := newProgramsSentinelFixture(t, god)
	require.False(t, x.al.GetConfig().Tools.FilterSensitiveData, "Given: general filter is genuinely off")
	require.Equal(t, []string{"github"}, x.al.GetConfig().Agents.List[0].Programs)
	labels := []string{x.svc.Connection("github").AccountLabel}
	require.NoError(t, x.api.auditor.Log(&audit.Entry{Event: "s1-seven-sinks-instrument", Decision: audit.DecisionAllow}))

	x.runOutput(t, "S48_FG_SUCCESS", programsSinkSentinel, false, 0, nil)
	x.runOutput(t, "S48_FG_ERROR", programsSinkSentinel, false, 3, nil)
	// The actual old-token child is already running when real rotation wins.
	x.runOutput(t, "S48_BG_SUCCESS", programsSinkSentinel, true, 0, func() {
		generation := int64(1)
		_, err := x.svc.Publish("github", programsSinkRotated, &generation, nil)
		require.NoError(t, err)
		x.svc.Wait()
		after := x.svc.Connection("github")
		require.Equal(t, "connected", after.State)
		require.Equal(t, int64(2), after.Generation)
		require.Equal(t, "hubot", after.AccountLabel)
		labels = append(labels, after.AccountLabel)
	})
	x.runOutput(t, "S48_BG_ERROR", programsSinkRotated, true, 3, nil)
	x.runOutput(t, "S48_FG_ROTATED", programsSinkRotated, false, 0, nil)

	exported := callPackageExport(t, x.api, `{"agent_ids":["dev"]}`)
	require.Equal(t, http.StatusOK, exported.Code, "instrument: actual registered export route succeeded (body deliberately not printed)")
	require.Equal(t, "application/octet-stream", exported.Hdr.Get("Content-Type"))
	pgSinkAssertValueFree(t, exported.Body, "actual encoded export bundle")
	manifest, payload, err := omnpkg.ReadPackage(bytes.NewReader(exported.Body))
	require.NoError(t, err, "instrument: actual exported bytes parse/decompress through the importer")
	require.Equal(t, programsSinkSoul, string(payload["omnipus/agents/dev/SOUL.md"]), "instrument: this is the real persisted Dev bundle, not an empty archive")
	_, hasRecord := payload["omnipus/agents/dev/agent.json"]
	require.True(t, hasRecord)
	packageView, err := json.Marshal(struct {
		Manifest any               `json:"Manifest"`
		Files    map[string]string `json:"Files"`
	}{Manifest: manifest, Files: pgSinkPayloadStrings(payload)})
	require.NoError(t, err)
	pgSinkAssertValueFree(t, packageView, "actual decompressed export manifest and payload")

	x.shutdown() // Join real producers before snapshotting their persisted/public outputs.
	entries, err := x.al.GetSessionStore().ReadTranscript(x.dev)
	require.NoError(t, err)
	transcript, err := json.Marshal(entries)
	require.NoError(t, err)
	// This bus-boundary fixture does not run the WS user-echo writer.
	// Instead prove the real reader sees all five scripted assistant replies.
	var savedReplies int
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == "S48 actual command response received" {
			savedReplies++
		}
	}
	require.Equal(t, 5, savedReplies, "instrument: every actual foreground/background-start assistant reply was persisted")
	pgSinkAssertValueFree(t, transcript, "actual persisted transcript")
	// S72 is also explicit: the real completion, not merely the bash start
	// arguments or scripted reply, must be saved masked. Keep its known RED.
	var savedCompletions string
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Background session ") {
			savedCompletions += entry.Content + "\n"
		}
	}
	for _, label := range []string{"S48_BG_SUCCESS", "S48_BG_ERROR"} {
		assert.True(t, strings.Contains(savedCompletions, label+"_OUTPUT\n[FILTERED]"), "S48/S72: actual background output persists, not a vacuous absence; content deliberately not printed")
	}

	x.provider.mu.Lock()
	requests, err := json.Marshal(x.provider.prompts[x.dev])
	x.provider.mu.Unlock()
	require.NoError(t, err)
	require.True(t, bytes.Contains(requests, []byte("S48 campaign: S48_FG_SUCCESS")), "instrument: actual model request observer saw this campaign")
	require.True(t, bytes.Contains(requests, []byte("[FILTERED]")), "instrument: actual masked output reached the model, not only a configured response")
	pgSinkAssertValueFree(t, requests, "actual recorded model requests")

	var messages []bus.OutboundMessage
	var contents []string
	for len(x.bus.OutboundChan()) > 0 {
		message := <-x.bus.OutboundChan()
		messages = append(messages, message)
		contents = append(contents, message.Content)
	}
	require.NotEmpty(t, messages, "instrument: actual published chat messages were observed")
	published := strings.Join(contents, "\n")
	for _, label := range []string{"S48_BG_SUCCESS", "S48_BG_ERROR"} {
		assert.True(t, strings.Contains(published, label+"_OUTPUT\n[FILTERED]"), "S48/S72: actual masked background feedback was published; content deliberately not printed")
	}
	pgSinkAssertValueFree(t, []byte(published), "actual published chat message contents")
	publishedRecords, err := json.Marshal(messages)
	require.NoError(t, err)
	pgSinkAssertValueFree(t, publishedRecords, "actual published chat message records")

	gatewayLog, err := os.ReadFile(filepath.Join(x.home, "logs", "gateway.log"))
	require.NoError(t, err, "missing gateway log is not empty successful evidence")
	for _, marker := range []string{"S48_NATIVE_LOG_READER_CONTROL", "S48_SLOG_READER_CONTROL", "Tool call: bash", "Async notification publishing result"} {
		require.True(t, bytes.Contains(gatewayLog, []byte(marker)), "instrument: real gateway log must contain %s", marker)
	}
	pgSinkAssertValueFree(t, gatewayLog, "actual gateway log")

	auditRaw, err := os.ReadFile(filepath.Join(x.home, "system", "audit.jsonl"))
	require.NoError(t, err, "missing audit file is not empty successful evidence")
	var controls, exports, tools int
	for _, line := range bytes.Split(bytes.TrimSpace(auditRaw), []byte{'\n'}) {
		var row audit.Entry
		require.NoError(t, json.Unmarshal(line, &row), "instrument: every actual audit line must decode")
		if row.Event == "s1-seven-sinks-instrument" {
			controls++
		}
		if row.Event == audit.EventPackageExport && row.AgentID == "dev" {
			exports++
		}
		if row.Event == audit.EventToolCall && row.Tool == "bash" && row.SessionID == x.dev {
			tools++
		}
	}
	require.Equal(t, 1, controls, "instrument: one separately labeled real audit control")
	require.Equal(t, 1, exports, "instrument: the actual Dev export wrote its own audit row")
	require.Equal(t, 5, tools, "instrument: all five actual registered child launches reached the real audit sink")
	pgSinkAssertValueFree(t, auditRaw, "actual shared audit file")
	require.Equal(t, []string{"octocat", "hubot"}, labels, "instrument: both actual checked labels are nonempty and correspond to their token generations")
	pgSinkAssertValueFree(t, []byte(strings.Join(labels, "\n")), "actual account labels")
	t.Logf("actual seven-sink campaign: children=5 foreground=3 background=2 rotation_generation=2 exported_payload_files=%d model_request_bytes=%d transcript_entries=%d published_messages=%d audit_tool_rows=%d", len(payload), len(requests), len(entries), len(messages), tools)
}

func pgSinkPayloadStrings(payload map[string][]byte) map[string]string {
	out := make(map[string]string, len(payload))
	for path, body := range payload {
		out[path] = string(body)
	}
	return out
}
