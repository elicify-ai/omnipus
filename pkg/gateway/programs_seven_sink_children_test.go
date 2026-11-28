// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pgSinkHasProtectedWindow(raw []byte, token string) bool {
	for n := 0; n+8 <= len(token); n++ {
		if bytes.Contains(raw, []byte(token[n:n+8])) {
			return true
		}
	}
	return false
}

func pgSinkAssertValueFree(t *testing.T, raw []byte, sink string) {
	t.Helper()
	for _, token := range []string{programsSinkSentinel, programsSinkRotated} {
		assert.False(t, pgSinkHasProtectedWindow(raw, token), "S48: %s contains a protected token window (content deliberately not printed)", sink)
	}
}

func (x *programsSentinelFixture) startTurn(t *testing.T, call string, args map[string]any) string {
	t.Helper()
	before := len(x.provider.observedOrigins(x.dev))
	x.provider.setScript(x.dev, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "bash", Arguments: args}}},
		wu5StopGW("S48 actual command response received"),
		wu5StopGW("S48 actual background continuation received"),
	})
	require.NoError(t, x.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: x.dev, ChatID: x.dev, Content: "S48 campaign: " + call, Provenance: bus.NewProvenance(bus.OriginWeb)}))
	x.waitEnds(t, x.dev)
	x.provider.waitFor(x.dev, 2, "S48 actual registered bash result", t)
	result := x.provider.result(x.dev, call)
	require.NotEmpty(t, result, "instrument: actual bash result was consumed by the provider")
	pgSinkAssertValueFree(t, []byte(result), "actual bash result")
	origins := x.provider.observedOrigins(x.dev)[before:]
	require.Len(t, origins, 2, "instrument: this command's two actual model requests were observed")
	connection := x.svc.Connection("github")
	t.Logf("actual phase=%s connected=%t generation=%d command_unresolved=%t result_unresolved=%t notice_unresolved=%t notice_web_off=%t notice_store_unavailable=%t", call, connection.State == "connected", connection.Generation, origins[0].IsUnresolved(), origins[1].IsUnresolved(), strings.Contains(result, programs.MsgWithheldUnresolved), strings.Contains(result, programs.MsgWithheldWebChat), strings.Contains(result, programs.MsgStoreUnavailable))
	for _, origin := range origins {
		assert.Equal(t, []bus.Origin{bus.OriginWeb}, origin.Origins(), "S48/I-2: actual command and result requests inherit this web input, with no fabricated carrier")
	}
	return result
}

// Each private count file is written by the real child, not by this fixture.
// The provider is never given a literal token or a pre-masked result.
func (x *programsSentinelFixture) runOutput(t *testing.T, label, token string, background bool, exitCode int, whenHeld func()) {
	t.Helper()
	countPath := filepath.Join(x.work, label+"-count")
	marker := label + "_OUTPUT"
	command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; mv %q %q; ", countPath+".tmp", countPath+".tmp", countPath)
	release := filepath.Join(x.work, label+"-release")
	if background {
		command += fmt.Sprintf("while [ ! -f %q ]; do sleep 0.01; done; ", release)
		t.Cleanup(func() { require.NoError(t, os.WriteFile(release, []byte("release"), 0600)) })
	}
	command += fmt.Sprintf("printf '%s\\n'; echo $GH_TOKEN; exit %d", marker, exitCode)
	args := map[string]any{"command": command}
	if background {
		args["run_in_background"] = true
	}
	events := x.al.SubscribeEvents(64)
	defer x.al.UnsubscribeEvents(events.ID)
	result := x.startTurn(t, label, args)
	var outcomes []agent.ToolExecEndPayload
	for len(events.C) > 0 {
		event := <-events.C
		if event.Kind != agent.EventKindToolExecEnd {
			continue
		}
		outcome, ok := event.Payload.(agent.ToolExecEndPayload)
		require.True(t, ok)
		if string(outcome.ToolCallID) == label && outcome.SessionID == x.dev {
			outcomes = append(outcomes, outcome)
		}
	}
	require.Len(t, outcomes, 1, "instrument: one actual matching tool outcome, not model text")
	assert.Equal(t, !background && exitCode != 0, outcomes[0].IsError, "S48: real foreground success/error or admitted background start")
	require.Eventually(t, func() bool { _, err := os.Stat(countPath); return err == nil }, 5*time.Second, 10*time.Millisecond, "instrument: actual child wrote its own count file")
	raw, err := os.ReadFile(countPath)
	require.NoError(t, err)
	count, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	// Keep the exact byte-count oracle, but collect the other real sinks even
	// when credential delivery is wrong. assert.Equal still fails this test.
	assert.Equal(t, len(token)+1, count, "S48: actual child receives the selected token (printenv adds one newline)")
	if !background {
		assert.True(t, strings.Contains(result, marker+"\n[FILTERED]"), "S48: actual foreground output is present and masked, not a denied/empty result; content deliberately not printed")
		t.Logf("actual foreground child=%s count=%s expected_exit=%d", label, strings.TrimSpace(string(raw)), exitCode)
		return
	}
	var started tools.ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result), &started), "instrument: actual background child returned a session envelope")
	require.Empty(t, started.Error)
	require.NotEmpty(t, started.SessionID)
	if whenHeld != nil {
		whenHeld()
	}
	require.NoError(t, os.WriteFile(release, []byte("release"), 0600))
	x.waitEnds(t, x.dev) // The actual notifier's input starts the real continuation.
	x.provider.waitFor(x.dev, 3, "S48 actual notifier continuation", t)
	x.provider.mu.Lock()
	var completions []string
	for _, request := range x.provider.prompts[x.dev] {
		for _, msg := range request {
			if msg.Role == "user" && strings.Contains(msg.Content, marker) && strings.Contains(msg.Content, "Background session "+started.SessionID+" finished") {
				completions = append(completions, msg.Content)
			}
		}
	}
	x.provider.mu.Unlock()
	require.NotEmpty(t, completions, "instrument: this actual child's completion, not a scripted wake, reached the real model boundary")
	for _, completion := range completions {
		assert.True(t, strings.Contains(completion, fmt.Sprintf("exit code %d", exitCode)), "the real background child reached the requested success/error exit; content deliberately not printed")
		assert.True(t, strings.Contains(completion, marker+"\n[FILTERED]"), "S48/S72: actual background continuation contains the masked child output; content deliberately not printed")
		pgSinkAssertValueFree(t, []byte(completion), "actual background continuation")
	}
	t.Logf("actual background child=%s count=%s exit=%d continuation_observed=true", label, strings.TrimSpace(string(raw)), exitCode)
}
