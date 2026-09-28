// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the tool-result sentinel ban (spec §16 item 28, §13
// Group C scenario "the sentinel appears only on the gated thinking path",
// §6 ToolCallProgress text ban's sibling; ADR-095's proving-test list).
//
// Oracle: after a real turn whose reasoning carries a unique sentinel, the
// sentinel appears NOWHERE in the stored transcript — not in any
// tool_call entry's content or arguments, not in any assistant or user
// entry — because the capture pipeline redacts reasoning before it is
// stored at all. This is the invariant that makes the read-side gate
// (concern 3's REST/hub/replay tests) sound: a surface that fails the gate
// can only leak what storage ever held.
//
// Toggle-on/toggle-off clause: the toggle governs READ surfaces only (pinned
// at the gate tests, pkg/gateway/thinking_gate_test.go and the REST/hub/replay
// gate tests); the turn loop has no login, so the storage invariant is
// toggle-independent by construction — stated here, asserted there.
//
// RED status: ban/regression guard — expected GREEN in RED once the capture
// pipeline (concern 1's RED pins) is implemented; before that the turn
// cannot produce thinking entries and the instrument check fails loudly.

func TestThinkingSentinel_NeverInAnyToolResultOrNonThinkingEntry(t *testing.T) {
	prov := &thinkingStreamProvider{rounds: []thinkingRound{
		{
			reasoning: "The tool will fail; the deploy key is " + thinkingSentinel + " — do not leak it.",
			toolName:  "no_such_tool_wpc",
			toolArgs:  `{"path":"x"}`,
		},
		{
			reasoning: "Confirmed unavailable.",
			answer:    "The deploy check failed.",
		},
	}}
	al := newThinkingTestLoop(t, prov)
	w := newSessionWorker("wpc-toolresult-ban", al, func() {})
	w.processTurn(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-a"},
		ChatID:  "chat-a",
		Content: "check the deploy",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
	})

	entries := transcriptEntries(t, al)
	require.NotEmpty(t, entries, "instrument check: the turn stored no entries")

	// Instrument check (rule 6): reasoning really flowed — at least one
	// thinking entry exists whose text carries the capture pipeline's
	// redaction marker. Without this, a turn that produced NO reasoning would
	// pass the ban vacuously.
	var sawRedactedThinking bool
	for _, e := range entries {
		if e.Type == session.EntryTypeThinking && strings.Contains(e.ThinkingText, "[REDACTED]") {
			sawRedactedThinking = true
		}
	}
	require.True(t, sawRedactedThinking,
		"instrument check: no redacted thinking entry found — the reasoning never flowed, so this ban would prove nothing")

	// The ban: every string field of every non-thinking entry is
	// sentinel-free. Tool results live on tool_call entries' content;
	// arguments ride the ToolCalls slice.
	for i, e := range entries {
		if e.Type == session.EntryTypeThinking {
			continue
		}
		assert.NotContains(t, e.Content, thinkingSentinel, "entry %d (%s) content", i, e.Type)
		assert.NotContains(t, e.ThinkingText, thinkingSentinel, "entry %d (%s) thinking text", i, e.Type)
		for j, tc := range e.ToolCalls {
			args, _ := json.Marshal(tc.Parameters)
			res, _ := json.Marshal(tc.Result)
			assert.NotContains(t, tc.Tool, thinkingSentinel, "entry %d tool call %d name", i, j)
			assert.NotContains(t, string(args), thinkingSentinel, "entry %d tool call %d arguments", i, j)
			assert.NotContains(t, string(res), thinkingSentinel, "entry %d tool call %d result", i, j)
		}
	}
}
