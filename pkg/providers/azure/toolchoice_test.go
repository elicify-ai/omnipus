package azure

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func azureToolsFixture() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: protocoltypes.ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
}

// TestToolChoice_Azure_DefaultUnchanged proves a caller that never touches
// tool_choice still sees the exact same explicit "auto" this builder always
// hardcoded (S-13) — the union's typed field marshals inline as a bare
// string, so the wire assertion is a plain string compare.
func TestToolChoice_Azure_DefaultUnchanged(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		writeValidResponse(w)
	}))
	defer server.Close()

	p := mustNewProvider(t, "test-key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, azureToolsFixture(), "deployment", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if requestBody["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want %q", requestBody["tool_choice"], "auto")
	}
}

// TestToolChoice_Azure_ForcedRequired maps ADR-081's typed ToolChoice onto
// the openai-go SDK's ToolChoiceOptions union (S-13, [G-B1] "azure SDK typed
// union mapping").
func TestToolChoice_Azure_ForcedRequired(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		writeValidResponse(w)
	}))
	defer server.Close()

	p := mustNewProvider(t, "test-key", server.URL, "")
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, azureToolsFixture(), "deployment", options)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if requestBody["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want %q", requestBody["tool_choice"], "required")
	}
}

// TestToolChoice_Azure_RequiredWithNoTools_Guarded: the whole Tools/ToolChoice
// block is gated on len(tools) > 0, so requesting Required with no tools must
// never emit a tool_choice at all (matches this builder's pre-ADR-081
// behavior of omitting both fields together).
func TestToolChoice_Azure_RequiredWithNoTools_Guarded(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		writeValidResponse(w)
	}))
	defer server.Close()

	p := mustNewProvider(t, "test-key", server.URL, "")
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "deployment", options)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, present := requestBody["tools"]; present {
		t.Fatalf("tools key present with an empty tools list")
	}
	if _, present := requestBody["tool_choice"]; present {
		t.Errorf("tool_choice = %v, want the key absent (no tools offered)", requestBody["tool_choice"])
	}
}
