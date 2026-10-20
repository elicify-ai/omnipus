package promptcache

import (
	"os"
	"strings"
	"testing"
)

// TestCacheKeyAllowList_SendsAndSuppresses is TDD row 9. The provider table's
// send column is the oracle. The value of a key or header is the session id;
// this function decides whether one is sent. The loop assignment is checked
// below so a value of the agent id fails the same row.
func TestCacheKeyAllowList_SendsAndSuppresses(t *testing.T) {
	wantKey := []string{"openai", "mistral"}
	for _, id := range wantKey {
		got := CacheTransportFor(id, "")
		if !got.SendPromptCacheKey {
			t.Errorf("%s: SendPromptCacheKey = false, want true", id)
		}
		if got.SendEphemeralCacheControl {
			t.Errorf("%s: got a cache marker; the table sends a key only", id)
		}
	}

	for _, id := range []string{"scaleway", "ovhcloud"} {
		got := CacheTransportFor(id, "")
		if got.SendPromptCacheKey || got.SessionHeader != "" || got.SendBodySessionID || got.SendEphemeralCacheControl {
			t.Errorf("%s: transport = %+v, want no prompt_cache_key and no marker", id, got)
		}
	}

	groq := CacheTransportFor("groq", "")
	if groq != (CacheTransport{}) {
		t.Errorf("groq transport = %+v, want neither a key nor a marker", groq)
	}

	// xAI's chat path documents the header, not a body key. Value is the session id.
	xai := CacheTransportFor("xai", "")
	if xai.SessionHeader != "x-grok-conv-id" {
		t.Errorf("xai header = %q, want x-grok-conv-id", xai.SessionHeader)
	}
	if xai.SendPromptCacheKey {
		t.Error("xai chat path must not also send prompt_cache_key")
	}

	src, err := os.ReadFile(filepathJoinAgentLoopTurns())
	if err != nil {
		t.Fatalf("read loop assignment: %v", err)
	}
	text := string(src)
	const agentValue = `"prompt_cache_key": rq.ri.rf.rt.ts.agent.ID`
	if strings.Contains(text, agentValue) {
		t.Errorf("prompt_cache_key is assigned the agent id (%s); FR-012 requires the session id", agentValue)
	}
}

func filepathJoinAgentLoopTurns() string {
	return "../../agent/loop_run_turn.go"
}

// TestVercelClaude_ManualCacheControl is TDD row 32.
func TestVercelClaude_ManualCacheControl(t *testing.T) {
	claude := CacheTransportFor("vercel", "anthropic/claude-opus-4.5")
	if !claude.SendEphemeralCacheControl {
		t.Error("Vercel Claude: SendEphemeralCacheControl = false, want a manual cache_control mark of type ephemeral")
	}
	if !claude.SendPromptCacheKey {
		t.Error("Vercel Claude: SendPromptCacheKey = false, want the session pin prompt_cache_key")
	}
	if claude.SendBodySessionID {
		t.Error("Vercel must not send caching:auto or a body session_id rewrite")
	}

	implicit := CacheTransportFor("vercel", "openai/gpt-4o")
	if implicit.SendEphemeralCacheControl {
		t.Error("an implicit Vercel upstream gained cache_control; only a Claude model id does")
	}
	if !implicit.SendPromptCacheKey {
		t.Error("implicit Vercel upstream: SendPromptCacheKey = false, want the session pin and no marker")
	}
}
