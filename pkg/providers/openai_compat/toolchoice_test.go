package openai_compat

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func toolsFixture() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
}

// TestToolChoice_OpenAICompat_DefaultUnchanged proves a caller that never
// touches tool_choice still gets the pre-ADR-081 "auto" default — no
// observable change for the vast majority of calls (S-13).
func TestToolChoice_OpenAICompat_DefaultUnchanged(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "")
	body := p.buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", map[string]any{},
	)
	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want %q", body["tool_choice"], "auto")
	}
}

// TestToolChoice_OpenAICompat_ForcedRequired proves options[OptionKeyToolChoice]
// set to Required forces "required" onto the wire body (S-13).
func TestToolChoice_OpenAICompat_ForcedRequired(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "")
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", options)
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want %q", body["tool_choice"], "required")
	}
}

// TestToolChoice_OpenAICompat_ForcedAutoExplicit proves an explicit Auto
// request also round-trips onto the wire (not just the "no option" default).
func TestToolChoice_OpenAICompat_ForcedAutoExplicit(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "")
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceAuto},
	}
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", options)
	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want %q", body["tool_choice"], "auto")
	}
}

// TestToolChoice_OpenAICompat_ExtraBodyClean_ForcedWins covers DS-2's
// "extraBody clean" row: no operator override present, forcing applies
// normally.
func TestToolChoice_OpenAICompat_ExtraBodyClean_ForcedWins(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "", WithExtraBody(map[string]any{
		"some_other_field": "value",
	}))
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", options)
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want %q (forced value must survive an extra_body with no override)", body["tool_choice"], "required")
	}
}

// TestToolChoice_OpenAICompat_ExtraBodyOverride_ForcedStillWins is FR-008 /
// S-33: an operator's own extra_body.tool_choice must not silently defeat a
// forced choice. This asserts the "win" half of "win or WARN" — the forced
// value is what actually reaches the wire regardless of what extraBody
// configured (the WARN is a best-effort observability addition, not
// separately asserted here — this codebase has no log-capture harness).
func TestToolChoice_OpenAICompat_ExtraBodyOverride_ForcedStillWins(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "", WithExtraBody(map[string]any{
		"tool_choice": "auto", // operator-configured override that would defeat forcing if applied last unconditionally
	}))
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", options)
	if body["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want %q (forced value must defeat extra_body's own tool_choice, never be silently overridden)",
			body["tool_choice"], "required")
	}
}

// TestToolChoice_OpenAICompat_RequiredWithNoTools_Guarded is the "required
// with an empty tools list must not be emitted" guard: no tools and no
// native search means the builder never sends "tools" at all, and forcing
// "required" in that state would be a guaranteed 400 from the backend. The
// tool_choice key must be entirely absent, not "required".
func TestToolChoice_OpenAICompat_RequiredWithNoTools_Guarded(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "")
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", options)
	if _, present := body["tools"]; present {
		t.Fatalf("tools key present with an empty tools list and no native search")
	}
	if _, present := body["tool_choice"]; present {
		t.Errorf("tool_choice = %v, want the key absent (no tools offered)", body["tool_choice"])
	}
}

// TestToolChoice_OpenAICompat_WrongType_DegradesToAuto proves a caller that
// mistakenly puts a bare string under the well-known key does not silently
// force anything — it degrades to the ordinary "auto" default, same as if
// the option were never set.
func TestToolChoice_OpenAICompat_WrongType_DegradesToAuto(t *testing.T) {
	p := mustNewProvider(t, "key", "https://example.test", "")
	options := map[string]any{protocoltypes.OptionKeyToolChoice: "required"} // wrong Go type, deliberate
	body := p.buildRequestBody([]Message{{Role: "user", Content: "hi"}}, toolsFixture(), "gpt-4o", options)
	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want %q (wrong-typed option must degrade, not silently pass through)", body["tool_choice"], "auto")
	}
}
