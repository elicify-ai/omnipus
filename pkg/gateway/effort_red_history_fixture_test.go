package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Seed canonical bytes, not the projected output. The offline derived-index
// rebuild is fixture setup only; no production reader/writer is mocked.
func egwSeedHistoryMetadata(t *testing.T, env *egwT7HistoryEnv) string {
	t.Helper()
	store := env.api.agentLoop.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	now := time.Now().UTC().Add(-time.Minute)
	appendEntry := func(id, role, content string) {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{ID: id, Role: role, AgentID: "mia", Content: content, Model: "B", Timestamp: now}))
		now = now.Add(time.Millisecond)
	}
	appendEntry("earlier-contributor-120", "assistant", "earlier provider attempt")
	for i := 0; i < 45; i++ {
		appendEntry(fmt.Sprintf("gap-%02d", i), "user", fmt.Sprintf("intervening source group %d", i))
	}
	appendEntry("entry-egw-a", "assistant", "answer high")
	appendEntry("entry-egw-b", "assistant", "answer mapped zero")
	appendEntry("entry-egw-c", "assistant", "answer absent")
	appendEntry("entry-egw-d", "assistant", "answer provider default")
	cfg := env.api.agentLoop.GetConfig()
	env.api.agentLoop.Stop()
	env.api.agentLoop.Close()
	file := filepath.Join(store.BaseDir(), meta.ID, "transcript.jsonl")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	var patched bytes.Buffer
	count := 0
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var row map[string]any
		require.NoError(t, json.Unmarshal(line, &row))
		switch row["id"] {
		case "earlier-contributor-120":
			row["reasoning_tokens"] = 120
			count++
		case "entry-egw-a":
			row["effort"] = map[string]any{"chosen": "high", "sent": "high", "provider": "PB", "model": "B"}
			row["turn_reasoning_tokens"] = 120
			count++
		case "entry-egw-b":
			row["effort"] = map[string]any{"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "PB", "model": "B"}
			row["turn_reasoning_tokens"] = 0
			count++
		case "entry-egw-d":
			row["effort"] = map[string]any{"chosen": "provider_default"}
			count++
		}
		encoded, encodeErr := json.Marshal(row)
		require.NoError(t, encodeErr)
		patched.Write(encoded)
		patched.WriteByte('\n')
	}
	require.Equal(t, 4, count, "instrument control: the canonical source contains every seeded record")
	require.NoError(t, os.WriteFile(file, patched.Bytes(), 0600))
	build, err := session.PrepareChatHistoryIndex(context.Background(), filepath.Join(store.BaseDir(), meta.ID), session.ChatHistoryIndexBuildOptions{})
	require.NoError(t, err)
	require.NoError(t, build.Publish(context.Background()))
	require.NoError(t, build.Close())
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	env.api.agentLoop = al
	env.handler = newWSHandler(msgBus, al, "")
	return meta.ID
}

func egwAssertHistoryMetadata(t *testing.T, got map[string]map[string]any) {
	t.Helper()
	assert.NotContains(t, got, "earlier-contributor-120", "page excludes contributor: snapshot must not be recomputed from page")
	wants := map[string]map[string]any{
		"entry-egw-a": {"chosen": "high", "sent": "high", "provider": "PB", "model": "B"},
		"entry-egw-b": {"chosen": "medium", "sent": "low", "reason": "mapped", "provider": "PB", "model": "B"},
		"entry-egw-d": {"chosen": "provider_default"},
	}
	for _, id := range []string{"entry-egw-a", "entry-egw-b", "entry-egw-c", "entry-egw-d"} {
		row, ok := got[id]
		require.True(t, ok, "positive control: actual answer %s must be served", id)
		assert.NotContains(t, row, "reasoning_tokens", "no per-call member on either history wire form")
		assert.NotEqual(t, "This saved message could not be displayed.", row["content"])
		if want, exists := wants[id]; exists {
			assert.Equal(t, want, row["effort"], "persisted effort on %s", id)
		} else {
			assert.NotContains(t, row, "effort")
		}
		switch id {
		case "entry-egw-a":
			assert.Equal(t, float64(120), row["turn_reasoning_tokens"], "stored turn sum, not selected-page sum")
		case "entry-egw-b":
			assert.Equal(t, float64(0), row["turn_reasoning_tokens"], "reported zero is not absent")
		default:
			assert.NotContains(t, row, "turn_reasoning_tokens", "unreported count is absent, not manufactured zero")
		}
	}
}

func egwEffortCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	model := func(id string, levels []string) map[string]any {
		return map[string]any{"id": id, "name": id, "context_window": 128000, "max_output_tokens": 16384, "input_modalities": []string{"text"}, "tool_call": true, "status": "active", "reasoning": map[string]any{"levels": levels, "switch": false}}
	}
	raw, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.1", "updated_at": "2026-10-30T06:00:00Z", "source": "red-fixture", "default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760}, "providers": []any{map[string]any{"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter", "api": "https://openrouter.ai/api/v1", "protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://openrouter.ai/api/v1"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": []any{model("m-old", []string{"none", "low", "high"}), model("m-none", []string{"none", "low", "high"}), model("m-levels", []string{"low", "medium", "high"})}}}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	return cat
}
