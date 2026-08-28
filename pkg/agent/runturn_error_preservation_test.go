// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// runturn_error_preservation_test.go — FIX-1 (Phase 1, Wave 1A-B)
//
// BDD scenarios covered:
//
//	BDD-1 (US-1 / Acc 2): "Rate-limit error in chat is visible after navigating
//	                     away and back" — error must persist in JSONL transcript
//	BDD-2 (US-1 / Acc 3): "Provider error in chat is visible after navigating
//	                     away and back" — non-rate-limit provider error must
//	                     also persist in JSONL transcript
//
// Spec ref: docs/internal/specs/phase-1-chat-model-and-errors.md
//
//	FR-001: rate_limit denial MUST emit the authoritative EventKindRateLimit
//	        frame (without a duplicate EventKindError), AND write a system
//	        entry to the JSONL transcript so replay shows it.
//	FR-002: any provider error (auth failure, validation, transport) MUST emit
//	        EventKindError with ErrorPayload, AND write a system entry to the
//	        JSONL transcript.
//
// This file complements runturn_rate_limit_test.go. That file asserts the
// AUDIT row is written and that the WS `rate_limit` frame is emitted (via
// EventKindRateLimit on the bus); this file asserts the JSONL transcript
// carries an error entry the replay path can read.
//
// TDD rows: 17 (rate_limit emits the authoritative EventKindRateLimit),
// 18 (provider error emits EventKindError).

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// readTranscriptEntries is a thin helper around ReadTranscript that fails the
// test cleanly when the transcript is missing or unreadable.
func readTranscriptEntries(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "transcript for session %q must be readable", sessionID)
	return entries
}

// findSystemEntries returns the transcript entries whose Type is EntryTypeSystem
// (the slot reserved for non-message events such as errors and rate-limits).
func findSystemEntries(entries []session.TranscriptEntry) []session.TranscriptEntry {
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeSystem {
			out = append(out, e)
		}
	}
	return out
}

// TestRunTurn_RateLimit_WritesErrorEntryToTranscript is the FIX-1 / FR-001
// transcript-side test. Drives runAgentLoop with a custom agent whose LLM
// budget is set to 1 call/hour; the first call succeeds, the second is
// rejected by the rate limiter. Asserts that the JSONL transcript carries a
// system entry carrying the rate-limit context so a session replay re-renders
// the error after a page reload.
//
// Traces to: spec §10 BDD-1, §13 FR-001, §12 test row 17, §18 Q1.
func TestRunTurn_RateLimit_WritesErrorEntryToTranscript(t *testing.T) {
	// ---------------------------------------------------------------------
	// Arrange: workspace, audit dir, AgentLoop with budget=1.
	// ---------------------------------------------------------------------
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// The provider supplies one real response (call 1). Call 2 is rejected
	// BEFORE the provider is invoked, so we don't need a second scripted step.
	provider := testutil.NewScenario().WithText("call 1 succeeded")

	const customAgentID = "rate-test-agent-rl-transcript"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: customAgentID, Name: "Rate Test Agent", Type: config.AgentTypeCustom},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "SETUP: custom agent must be registered")
	require.Equal(t, "custom", customAgent.AgentType,
		"SETUP: custom agent must have AgentType=custom so rate limiting applies")

	// Subscribe to the event bus so we can assert EventKindError is also
	// emitted on the bus (regression on the WS-frame side).
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	// Set up a real session with its own UnifiedStore + sessionID so we can
	// read the transcript back after the turn runs.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", customAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	// ---------------------------------------------------------------------
	// Act 1: drive the loop until the budget is consumed.
	// ---------------------------------------------------------------------
	_, err = al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:          "rl-session-warm",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "warm-up",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err, "call 1 must succeed — budget not yet exhausted")

	// Drain events from call 1 so the rate-limit EventKindError we assert on
	// below is unambiguously the one from call 2.
	drainEvents(sub.C)

	// ---------------------------------------------------------------------
	// Act 2: second call MUST be rate-limited. The transcript write MUST
	// include a system entry carrying the rate-limit context.
	// ---------------------------------------------------------------------
	_, err = al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:          "rl-session-blocked",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "trigger limit",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "call 2 must be rejected with an error")
	assert.Contains(t, strings.ToLower(err.Error()), "rate limit",
		"call 2 error must mention rate limit; got %q", err.Error())

	// ---------------------------------------------------------------------
	// Assert (A): the JSONL transcript carries a system entry referencing the
	// rate-limit denial. This is the FR-001 guarantee: on replay, the error
	// is visible after the user navigates away and back.
	// ---------------------------------------------------------------------
	entries := readTranscriptEntries(t, store, sessionID)
	systemEntries := findSystemEntries(entries)
	require.NotEmpty(t, systemEntries,
		"transcript must contain at least one EntryTypeSystem entry describing the rate-limit denial; "+
			"all entries: %+v", entries)

	// At least one system entry must reference the rate-limit policy. We
	// search by substring because the wire format of the payload is part of
	// the contract under design review — we want a stable assertion that
	// catches "payload not written" AND "wrong entry type".
	var rlEntry *session.TranscriptEntry
	for i := range systemEntries {
		c := systemEntries[i]
		if strings.Contains(strings.ToLower(c.Content), "rate limit") ||
			strings.Contains(strings.ToLower(c.Content), "rate_limit") {
			cp := c
			rlEntry = &cp
			break
		}
	}
	require.NotNil(t, rlEntry,
		"transcript must contain a system entry referencing the rate-limit; "+
			"system entries found: %+v", systemEntries)

	// The entry must carry the agent ID so the replay path can attribute it.
	assert.Equal(t, customAgentID, rlEntry.AgentID,
		"system entry must record the agent that was rate-limited")

	// ---------------------------------------------------------------------
	// Assert (B): the EventKindRateLimit frame is the AUTHORITATIVE rate-limit
	// signal on the bus (BLOCK 2 / FR-009 dedup). EventKindError must NOT
	// also be emitted for the same denial — a duplicate frame would render
	// the user a second bubble. The typed live WS frame for `rate_limited`
	// is suppressed (RateLimitFrame wins) per the BLOCK 2 invariant.
	// ---------------------------------------------------------------------
	events := drainEvents(sub.C)
	var errEvents []Event
	for _, e := range events {
		if e.Kind == EventKindError {
			errEvents = append(errEvents, e)
		}
	}
	assert.Empty(t, errEvents,
		"rate-limit denial must NOT also emit a duplicate EventKindError; "+
			"observed payload types: %v", payloadTypesOf(errEvents))

	// ---------------------------------------------------------------------
	// Assert (C): the EventKindRateLimit frame is STILL emitted on the bus
	// (no regression on the existing WS `rate_limit` frame).
	// ---------------------------------------------------------------------
	var rateLimitPayload *RateLimitPayload
	for _, e := range events {
		if e.Kind != EventKindRateLimit {
			continue
		}
		payload, ok := e.Payload.(RateLimitPayload)
		if ok {
			payloadCopy := payload
			rateLimitPayload = &payloadCopy
		}
		break
	}
	require.NotNil(t, rateLimitPayload,
		"EventKindRateLimit with RateLimitPayload must still be emitted (no regression on the WS frame side)")

	// ---------------------------------------------------------------------
	// Assert (D): the transcript entry carries the typed LLMError code
	// (BLOCK 2 / FR-008). The persisted ErrorCode must equal "rate_limited"
	// so the replay path can render the same banner.
	// ---------------------------------------------------------------------
	assert.Equal(t, "rate_limited", rlEntry.ErrorCode,
		"transcript entry must carry the typed rate_limited code")
	assert.True(t, rlEntry.ErrorRetryable,
		"rate-limit entries must mark ErrorRetryable=true")
	expectedRateLimitCopy := fmt.Sprintf("rate limit: %s (retry after %.0fs)",
		rateLimitPayload.PolicyRule, rateLimitPayload.RetryAfterSeconds)
	assert.Equal(t, expectedRateLimitCopy, rlEntry.Content,
		"transcript must preserve the friendly rate-limit copy")

	// ---------------------------------------------------------------------
	// Assert (D): audit row was written (regression on the existing test).
	// ---------------------------------------------------------------------
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
	if entries, err := readAuditEntries(auditPath); err == nil {
		var saw bool
		for _, e := range entries {
			if e["event"] == audit.EventRateLimit {
				saw = true
				break
			}
		}
		assert.True(t, saw, "audit.jsonl must still contain a rate_limit event (no regression)")
	}
}

// TestRunTurn_ProviderError_WritesErrorEntryToTranscript is the FIX-1 /
// FR-002 transcript-side test. Drives runAgentLoop with a scripted provider
// that returns an error on every Chat call. The fixture scripts a 401, which
// classifies as provider_auth_failed since the three-way split of the old
// catch-all. Asserts that the JSONL transcript
// carries a system entry carrying the error context, AND that an
// EventKindError is emitted on the event bus.
//
// Traces to: spec §10 BDD-2, §13 FR-002, §12 test row 18.
func TestRunTurn_ProviderError_WritesErrorEntryToTranscript(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Scripted provider returns a structured auth failure on every Chat call.
	// The structured status/body is the production provider-error shape consumed
	// by errorToProviderError and the BLOCK 2 classifier.
	const rawProviderText = `UPSTREAM_SECRET_INVALID_API_KEY`
	providerErr := &common.ProviderError{
		Status:      401,
		Body:        rawProviderText,
		BodyPreview: rawProviderText,
		ContentType: "application/json",
	}
	const maxRetries = 3
	provider := testutil.NewScenario()
	for i := 0; i <= maxRetries; i++ {
		provider = provider.WithError(providerErr)
	}

	// Use the (explicitly registered) default agent so we don't fight
	// rate-limit semantics. No "main" sentinel to rely on anymore.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
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

	ctx := context.Background()
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "SETUP: default agent must be registered")

	sub := al.SubscribeEvents(64)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", defaultAgent.ID)
	require.NoError(t, err)
	sessionID := meta.ID

	// ---------------------------------------------------------------------
	// Act: drive the loop. The LLM call must fail repeatedly and the turn
	// must terminate with a provider error.
	// ---------------------------------------------------------------------
	_, err = al.runAgentLoop(ctx, defaultAgent, processOptions{
		SessionKey:          "err-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "trigger provider error",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "loop must surface an error when the provider fails every call")

	// ---------------------------------------------------------------------
	// Assert (A): the JSONL transcript carries a system entry referencing the
	// provider error. This is the FR-002 guarantee.
	// ---------------------------------------------------------------------
	entries := readTranscriptEntries(t, store, sessionID)
	systemEntries := findSystemEntries(entries)
	require.NotEmpty(t, systemEntries,
		"transcript must contain at least one EntryTypeSystem entry describing the provider error; "+
			"all entries: %+v", entries)

	// At least one system entry must carry the generic classifier copy. Raw
	// provider text must never be persisted.
	var errEntry *session.TranscriptEntry
	for i := range systemEntries {
		c := systemEntries[i]
		assert.NotContains(t, c.Content, rawProviderText,
			"transcript must never contain raw provider text")
		if c.ErrorCode == string(CodeProviderAuthFailed) {
			cp := c
			errEntry = &cp
			break
		}
	}
	require.NotNil(t, errEntry,
		"transcript must contain a system entry carrying provider_auth_failed; system entries found: %+v",
		systemEntries)

	assert.Equal(t, defaultAgent.ID, errEntry.AgentID,
		"system entry must record the agent that hit the provider error")
	assert.Equal(t, string(CodeProviderAuthFailed), errEntry.ErrorCode,
		"system entry must carry the provider-shaped BLOCK 2 code")
	assert.False(t, errEntry.ErrorRetryable,
		"provider rejection must not be marked retryable")
	assert.Equal(t, UserMessageForCode(CodeProviderAuthFailed), errEntry.Content,
		"transcript must use the generic classifier copy")
	assert.NotContains(t, errEntry.Content, rawProviderText,
		"raw provider text must never appear in the transcript")

	// ---------------------------------------------------------------------
	// Assert (B): the event bus carries an EventKindError with ErrorPayload.
	// ---------------------------------------------------------------------
	events := drainEvents(sub.C)
	var errEvents []Event
	for _, e := range events {
		if e.Kind == EventKindError {
			errEvents = append(errEvents, e)
		}
	}
	require.NotEmpty(t, errEvents,
		"event bus must carry at least one EventKindError after a provider error")

	// At least one EventKindError payload must be an ErrorPayload (not just
	// a rate-limit payload — those are tested in the rate-limit case above).
	foundErrPayload := false
	for _, e := range errEvents {
		if _, ok := e.Payload.(ErrorPayload); ok {
			foundErrPayload = true
			break
		}
	}
	assert.True(t, foundErrPayload,
		"EventKindError must carry an ErrorPayload for a non-rate-limit provider error; "+
			"observed payload types: %v", payloadTypesOf(errEvents))
}

// drainEvents reads every event currently buffered on ch and returns them.
// Used between phases of a test so we can assert only on events from the
// phase we care about.
func drainEvents(ch <-chan Event) []Event {
	var events []Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-time.After(50 * time.Millisecond):
			return events
		default:
			return events
		}
	}
}

// payloadTypesOf returns the Go type names of a slice of Event payloads.
// Used for diagnostic output when an assertion about payload type fails.
func payloadTypesOf(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, typeNameOf(e.Payload))
	}
	return out
}

// typeNameOf returns a stable name for a payload's Go type. We avoid
// fmt.Sprintf("%T") in the diagnostic path so failures print cleanly without
// pulling in fmt at the top of the file.
func typeNameOf(p any) string {
	switch p.(type) {
	case TurnStartPayload:
		return "TurnStartPayload"
	case TurnEndPayload:
		return "TurnEndPayload"
	case LLMRequestPayload:
		return "LLMRequestPayload"
	case LLMResponsePayload:
		return "LLMResponsePayload"
	case LLMDeltaPayload:
		return "LLMDeltaPayload"
	case LLMRetryPayload:
		return "LLMRetryPayload"
	case ErrorPayload:
		return "ErrorPayload"
	case RateLimitPayload:
		return "RateLimitPayload"
	case ContextCompressPayload:
		return "ContextCompressPayload"
	case ToolExecStartPayload:
		return "ToolExecStartPayload"
	case ToolExecEndPayload:
		return "ToolExecEndPayload"
	default:
		// Fall back to JSON shape (cheap) rather than fmt.Sprintf("%T") so
		// the diagnostic output is consistent across Go versions.
		if data, err := json.Marshal(p); err == nil {
			return "unknown(" + string(data) + ")"
		}
		return "unknown"
	}
}
