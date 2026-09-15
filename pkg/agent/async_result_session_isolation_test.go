// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A-I4 round 6, Priority 2 regression coverage: two delegate-completion
// notifications for the SAME agent, originating from two DIFFERENT chat
// sessions, must never share the in-memory conversation history (or the
// per-scope sessionWorker) that builds the notification turn's LLM prompt.
//
// Before this fix, processSystemMessage (pkg/agent/loop.go) hard-coded
// sessionKey := routing.BuildAgentMainSessionKey(agent.ID) — "agent:<id>:main"
// — for every reconstructed notify-turn, regardless of which real session's
// background work it was reporting on. Since agent.Sessions.GetHistory/
// SetHistory (the store backing the LLM's own prompt) is keyed ONLY by that
// SessionKey, two delegate completions for the same agent (routine — an
// orchestrator commonly runs several concurrent background delegates) shared
// ONE growing history bucket: the second notify-turn's LLM call saw the
// first, unrelated notify-turn's content in its own context. Live-verified:
// a session that only ever delegated to "Ava" received a persisted assistant
// message narrating a nonexistent "delegation to Ray" pulled from an
// entirely different session's exchange.
//
// The fix scopes the notify-turn's SessionKey to
// "agent:<id>:session:<originatingSessionID>" (msg.AsyncTranscriptSessionID)
// — the exact convention every regular routed chat turn already uses via
// agentSessionKey() — so each originating session gets its own isolated
// history bucket, matching regular chat's isolation guarantee.

package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingProviderSawInLastCall reports whether ANY message in the most
// recent Chat() call recorded by a *recordingProvider (loop_test.go) contains
// substr — used to prove what the LLM prompt actually contained, an
// assertion mockProvider (which ignores its input entirely) cannot make.
func recordingProviderSawInLastCall(t *testing.T, p *recordingProvider, substr string) bool {
	t.Helper()
	require.NotEmpty(t, p.lastMessages, "provider must have been called at least once")
	for _, m := range p.lastMessages {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}
