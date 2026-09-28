// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C guard test for the inspect_session tool (spec section 8.1 row for
// pkg/tools/inspect_session.go: "Skip in tool output - tools present session
// state as answers/actions, not display copy; thinking is display-only per
// D2/D19, so tool output skips them").
//
// Guard: expected GREEN in RED. A thinking entry carries no role and no
// content (C3), so the tool's filters already keep it out of the output;
// the test pins the outcome, including the planted-role variant.

const wpcToolsThinkingSentinel = "sk-live-abcd1234EFGH"

func TestInspectSession_ThinkingEntryNeverInToolOutput(t *testing.T) {
	store := newFakeInspectSessionStore()
	now := time.Now().UTC()
	store.seed("wpc-sess", "wpc-worker", []session.TranscriptEntry{
		{ID: "e1", Role: "user", Content: "the instruction", Timestamp: now},
		{
			ID: "e2", Type: session.EntryTypeThinking,
			Role:         "", // real shape: no role
			Content:      "",
			ThinkingText: "inspect probe " + wpcToolsThinkingSentinel,
			Timestamp:    now,
		},
		{
			ID: "e3", Type: session.EntryTypeThinking,
			Role:         "assistant", // PLANTED - the explicit-filter case
			Content:      "",          // display copy stays in thinking_text (C3)
			ThinkingText: "inspect probe planted " + wpcToolsThinkingSentinel,
			Timestamp:    now,
		},
		{ID: "e4", Role: "assistant", Content: "the answer", Timestamp: now},
	})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"wpc-sess"})

	res := tool.Execute(ctx, map[string]any{"session_id": "wpc-sess"})
	require.False(t, res.IsError, "inspect_session: %s", res.ForLLM)

	var out struct {
		Entries []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out))
	require.Len(t, out.Entries, 2, "exactly the user and assistant entries surface; got %+v", out.Entries)
	assert.Equal(t, "user", out.Entries[0].Role)
	assert.Equal(t, "assistant", out.Entries[1].Role)

	assert.NotContains(t, res.ForLLM, wpcToolsThinkingSentinel,
		"thinking text never reaches inspect_session output (section 8.1)")
	assert.NotContains(t, res.ForLLM, "inspect probe", "no fragment of the thinking text either")
}
