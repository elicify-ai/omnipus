// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// runturn_typed_exits_test.go — ADR-066 D7 (T066-11): the four silent
// runTurn exits become typed.
//
// Spec: docs/internal/specs/adr-066-context-overflow-spec.md — FR-034,
// B-40, SC-006, test row 36 (TestRunTurn_SilentExitsNowTyped).
//
// Before D7, runTurn had four return sites that surfaced a bare
// fmt.Errorf("turn canceled") / fmt.Errorf("turn timed out") with no log
// line, no EventKindError, and no transcript entry: two in the outer LLM
// error block and two in the empty-response retry block. The turn ended and
// the user saw silence (or, via the session worker, the "we can't tell why"
// copy). SC-006 requires each of the four sites to produce three artefacts
// with its typed code — ≥ 1 log line carrying the raw cause, one turn-end
// event, one transcript entry — and never `unknown`.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// captureAgentLog redirects the package logger to a temp file at WARN level
// (the exits log at WARN/ERROR) and returns a reader over what was logged.
// Mirrors boot_sweep_unified_meta_test.go's captureLogFile.
func captureAgentLog(t *testing.T) func() string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "typed-exits.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})
	return func() string {
		data, err := os.ReadFile(logFile)
		require.NoError(t, err)
		return string(data)
	}
}

// TestRunTurn_SilentExitsNowTyped drives each of the four formerly silent
// sites and asserts the SC-006 artefacts.
//
//	site "outer":  callLLM returns the context error on the first call.
//	site "empty":  callLLM returns "" first (no tool calls, no reasoning), the
//	               FR-006 empty-response retry then returns the context error.
//
// crossed with cancel (context.Canceled → turn_canceled, attribution user)
// and deadline (context.DeadlineExceeded → turn_timed_out, provider).
func TestRunTurn_SilentExitsNowTyped(t *testing.T) {
	cases := []struct {
		name       string
		site       string // "outer" | "empty"
		cause      error
		wantCode   LLMErrorCode
		wantAttr   LLMErrorAttribution
		wantStatus TurnEndStatus
	}{
		{"outer/cancel", "outer", context.Canceled, CodeTurnCanceled, "user", TurnEndStatusAborted},
		{"outer/deadline", "outer", context.DeadlineExceeded, CodeTurnTimedOut, "provider", TurnEndStatusError},
		{"empty/cancel", "empty", context.Canceled, CodeTurnCanceled, "user", TurnEndStatusAborted},
		{"empty/deadline", "empty", context.DeadlineExceeded, CodeTurnTimedOut, "provider", TurnEndStatusError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readLog := captureAgentLog(t)

			tmpHome := t.TempDir()
			workspaceDir := filepath.Join(tmpHome, "workspace")
			require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

			provider := testutil.NewScenario()
			if tc.site == "empty" {
				provider = provider.WithText("")
			}
			// The deadline site inline-retries a FailoverTimeout up to
			// maxRetries (2) times before surfacing; script enough errors to
			// exhaust every attempt. The cancel site breaks on the first.
			for i := 0; i < 4; i++ {
				provider = provider.WithError(tc.cause)
			}

			cfg := &config.Config{
				Agents: config.AgentsConfig{
					Defaults: config.AgentDefaults{
						Home:              workspaceDir,
						ModelName:         "scripted-model",
						MaxTokens:         4096,
						MaxToolIterations: 10,
					},
					List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
				},
			}

			msgBus := bus.NewMessageBus()
			t.Cleanup(func() { msgBus.Close() })
			al := mustNewAgentLoop(t, cfg, msgBus, provider)
			t.Cleanup(al.Close)

			agent := al.GetRegistry().GetDefaultAgent()
			require.NotNil(t, agent)

			sub := al.SubscribeEvents(64)
			t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

			store := al.GetSessionStore()
			require.NotNil(t, store)
			meta, err := store.NewSession(session.SessionTypeChat, "web", agent.ID)
			require.NoError(t, err)
			sessionID := meta.ID

			_, err = al.runAgentLoop(context.Background(), agent, processOptions{
				SessionKey:          "typed-exit-" + tc.name,
				Channel:             "web",
				ChatID:              sessionID,
				UserMessage:         "hello",
				DefaultResponse:     defaultResponse,
				SendResponse:        false,
				TranscriptSessionID: sessionID,
				TranscriptStore:     store,
			})
			require.Error(t, err, "the turn must still return an error to its caller")
			assert.True(t, errors.Is(err, tc.cause),
				"the returned error must keep the raw cause in its chain so callers can errors.Is it; got %v", err)
			assert.Equal(t, tc.wantCode, TranslateTurnError(err).Code,
				"the caller-side classifier must see the typed code, not unknown")

			// Artefact 1: the transcript carries exactly one system entry with
			// the typed code.
			entries := readTranscriptEntries(t, store, sessionID)
			var errEntries []session.TranscriptEntry
			for _, e := range findSystemEntries(entries) {
				if e.ErrorCode != "" {
					errEntries = append(errEntries, e)
				}
			}
			require.Len(t, errEntries, 1,
				"exactly one error transcript entry expected; got %+v", errEntries)
			assert.Equal(t, string(tc.wantCode), errEntries[0].ErrorCode)
			assert.NotEqual(t, string(CodeUnknown), errEntries[0].ErrorCode, "never unknown")
			assert.Equal(t, UserMessageForCode(tc.wantCode), errEntries[0].Content,
				"transcript content must be the catalogue copy for the code")
			assert.NotContains(t, errEntries[0].Content, tc.cause.Error(),
				"raw cause must not leak into the user-facing transcript copy")

			// Artefact 2: one EventKindError with the typed code, and exactly
			// one turn-end event with the expected status.
			events := drainEvents(sub.C)
			var errPayloads []ErrorPayload
			var turnEnds []TurnEndPayload
			for _, e := range events {
				switch p := e.Payload.(type) {
				case ErrorPayload:
					if e.Kind == EventKindError {
						errPayloads = append(errPayloads, p)
					}
				case TurnEndPayload:
					if e.Kind == EventKindTurnEnd {
						turnEnds = append(turnEnds, p)
					}
				}
			}
			require.Len(t, errPayloads, 1,
				"exactly one EventKindError expected; got %d (%v)", len(errPayloads), payloadTypesOf(events))
			assert.Equal(t, string(tc.wantCode), errPayloads[0].Code)
			assert.Equal(t, UserMessageForCode(tc.wantCode), errPayloads[0].Message)
			assert.Equal(t, "llm", errPayloads[0].Stage)
			require.Len(t, turnEnds, 1, "exactly one turn-end event expected")
			assert.Equal(t, tc.wantStatus, turnEnds[0].Status)

			// Attribution is contract data; the code must carry the one the
			// spec assigns (B-40).
			assert.Equal(t, tc.wantAttr, AttributionForCode(tc.wantCode))

			// Artefact 3: ≥ 1 log line carrying the typed code AND the raw cause.
			logText := readLog()
			var sawLine bool
			for _, line := range strings.Split(logText, "\n") {
				if strings.Contains(line, string(tc.wantCode)) && strings.Contains(line, tc.cause.Error()) {
					sawLine = true
					break
				}
			}
			assert.True(t, sawLine,
				"expected a log line carrying both %q and the raw cause %q; log was:\n%s",
				tc.wantCode, tc.cause.Error(), logText)
		})
	}
}
