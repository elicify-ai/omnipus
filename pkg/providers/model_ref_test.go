package providers

import "testing"

func TestParseModelRef_WithSlash(t *testing.T) {
	ref := ParseModelRef("anthropic/claude-opus", "openai")
	if ref == nil {
		t.Fatal("expected non-nil ref")
	}
	if ref.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", ref.Provider)
	}
	if ref.Model != "claude-opus" {
		t.Errorf("model = %q, want claude-opus", ref.Model)
	}
}

func TestParseModelRef_WithoutSlash(t *testing.T) {
	ref := ParseModelRef("gpt-4", "openai")
	if ref == nil {
		t.Fatal("expected non-nil ref")
	}
	if ref.Provider != "openai" {
		t.Errorf("provider = %q, want openai", ref.Provider)
	}
	if ref.Model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", ref.Model)
	}
}

func TestParseModelRef_Empty(t *testing.T) {
	ref := ParseModelRef("", "openai")
	if ref != nil {
		t.Errorf("expected nil for empty string, got %+v", ref)
	}
}

func TestParseModelRef_EmptyModelAfterSlash(t *testing.T) {
	ref := ParseModelRef("openai/", "default")
	if ref != nil {
		t.Errorf("expected nil for empty model, got %+v", ref)
	}
}

func TestParseModelRef_WhitespaceHandling(t *testing.T) {
	ref := ParseModelRef("  anthropic / claude-opus  ", "openai")
	if ref == nil {
		t.Fatal("expected non-nil ref")
	}
	if ref.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", ref.Provider)
	}
	if ref.Model != "claude-opus" {
		t.Errorf("model = %q, want claude-opus", ref.Model)
	}
}

// TestNormalizeProvider — ADR-067 FR-011 / SC-009: NormalizeProvider is a
// TRIM, and nothing more. It used to be the binary's last alias table
// (`z.ai`/`z-ai` → `zai`, `google` → `gemini`, three `qwen-*` spellings
// collapsed, …); every one of those renames is gone, because a silent rename
// is how a config that names one provider ends up billing another.
func TestNormalizeProvider(t *testing.T) {
	tests := []struct{ in, want string }{
		{"openai", "openai"},
		{"  openai  ", "openai"},
		{"", ""},
		// Case is SIGNIFICANT (A-19): an id that differs only in case is a
		// DIFFERENT id, and resolves to nothing.
		{"OpenAI", "OpenAI"},
		{"ZAI", "ZAI"},
		// Retired spellings stay exactly as typed — no rename.
		{"z-ai", "z-ai"},
		{"z.ai", "z.ai"},
		{"gemini", "gemini"},
		{"qwen-international", "qwen-international"},
		{"alibaba-coding-anthropic", "alibaba-coding-anthropic"},
	}
	for _, tt := range tests {
		if got := NormalizeProvider(tt.in); got != tt.want {
			t.Errorf("NormalizeProvider(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestModelKey(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     string
	}{
		{"openai", "gpt-4", "openai/gpt-4"},
		// The key is a DEDUP key, so the model half is lower-cased; the
		// provider half is the id verbatim — there is no rename (FR-011).
		{"Anthropic", "Claude-Opus", "Anthropic/claude-opus"},
		{"claude", "sonnet", "claude/sonnet"},
		{"z.ai", "Model-X", "z.ai/model-x"},
	}

	for _, tt := range tests {
		got := ModelKey(tt.provider, tt.model)
		if got != tt.want {
			t.Errorf("ModelKey(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
		}
	}
}

// TestParseModelRef_NoProviderRenaming — the same rule seen through
// ParseModelRef: whatever provider segment the caller wrote is what comes
// back.
func TestParseModelRef_NoProviderRenaming(t *testing.T) {
	if ref := ParseModelRef("z.ai/glm-5.2", "openai"); ref == nil || ref.Provider != "z.ai" {
		t.Errorf("provider = %v, want z.ai verbatim", ref)
	}
	if ref := ParseModelRef("model-x", "GPT"); ref == nil || ref.Provider != "GPT" {
		t.Errorf("default provider = %v, want GPT verbatim", ref)
	}
}
