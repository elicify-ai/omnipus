// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// media_downgrade_guard_test.go — FIX 5 regression: synthesizeImageRejection
// (loop.go, the friendly-message path for image-only capability/format
// rejections) consulted ts.mediaRetryDone — the PDF-class per-turn guard —
// instead of its own ts.imageRetryDone. turn.go's design comment on those two
// fields is explicit that the guard was deliberately split per media class so
// a PDF-class downgrade earlier in a turn can never consume the image-class
// budget (or vice versa). Reading the wrong guard meant a PDF downgrade
// earlier in the SAME turn silently blocked the friendly synthesis for a
// LATER, unrelated image rejection — it fell through to the generic
// classifier-driven strip-retry path instead, which (with no image media
// actually present to strip) had nothing to retry with and the turn
// surfaced as a hard failure instead of the friendly guidance message.
//
// The test drives al.runTurn directly (mirroring runAgentLoop's own
// ts-construction sequence exactly — newTurnState + al.newTurnEventScope) so
// it can pre-seed ts.mediaRetryDone=true BEFORE the turn runs, simulating "a
// PDF-class downgrade already happened earlier in this turn" without the
// fragility of trying to reproduce that via a real multi-iteration tool-call
// sequence (a PDF downgrade's message-level mutation is scoped to that one
// LLM-call's ephemeral view and does not persist across turn iterations —
// only the atomic per-turn guard does, which is exactly what this test
// isolates).

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestSynthesizeImageRejection_PriorPDFDowngrade_DoesNotBlockFriendlySynthesis
// is the FIX 5 regression test.
func TestSynthesizeImageRejection_PriorPDFDowngrade_DoesNotBlockFriendlySynthesis(t *testing.T) {
	const modelName = "cross-guard-model"
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: modelName},
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	// imageRejectionProvider (loop_media_test.go) always returns a
	// vision-capability-absent error, independent of message content —
	// exactly the shape TestAgentLoop_ImageRejection_FriendlyMessage already
	// proves synthesizeImageRejection turns into a friendly reply on a FRESH
	// turnState (imageRetryDone == mediaRetryDone == false).
	provider := &imageRejectionProvider{model: modelName}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")

	opts := processOptions{
		SessionKey:      "cross-guard-session",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "What is in this image?",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	}
	// Mirror runAgentLoop's own ts-construction sequence exactly (loop.go)
	// so this test drives the real production code path, just with direct
	// access to ts before the turn runs.
	ts := newTurnState(defaultAgent, opts, al.newTurnEventScope(defaultAgent.ID, opts.SessionKey))

	// Simulate a PDF-class downgrade having ALREADY consumed the PDF-class
	// guard earlier in this SAME turn (e.g. an earlier tool-call round
	// attached a PDF that TryMediaDowngrade downgraded). This is exactly
	// the state applyMediaDowngrade's real PDF branch leaves behind on
	// success — pre-seeding it directly is the precise, deterministic way
	// to exercise the guard-selection bug without needing a second
	// multi-iteration tool round (whose message-level effects do not
	// persist across iterations anyway, unlike this atomic guard).
	ts.mediaRetryDone.Store(true)

	result, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err,
		"the turn must complete via the friendly image-rejection synthesis, not error out. "+
			"Before FIX 5, synthesizeImageRejection read ts.mediaRetryDone (already true here) "+
			"instead of ts.imageRetryDone, so it never fired; the turn then fell through to "+
			"TryMediaDowngrade, which found no image media to strip (none was ever attached) "+
			"and the turn surfaced as a hard failure instead of the friendly guidance message")

	assert.Contains(t, result.finalContent, "can't view images",
		"final content must be the friendly image-rejection message, got: %q", result.finalContent)
	assert.True(t, ts.imageRetryDone.Load(),
		"the image-class guard must be the one consumed by the friendly synthesis")
	assert.True(t, ts.mediaRetryDone.Load(),
		"the pre-existing PDF-class guard state must be left untouched by the image-only synthesis")
}
