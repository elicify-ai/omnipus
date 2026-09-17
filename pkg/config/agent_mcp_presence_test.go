package config

import (
	"encoding/json"
	"testing"
)

func TestAgentMCPBindingToolsPresenceRoundTripsOmittedAndEmptyDifferently(t *testing.T) {
	var omitted, empty AgentMCPServerBinding
	if err := json.Unmarshal([]byte(`{"id":"mail"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"mail","tools":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if omitted.ToolsSpecified {
		t.Fatal("omitted tools unexpectedly marked specified")
	}
	if !empty.ToolsSpecified || len(empty.Tools) != 0 {
		t.Fatalf("empty tools=%v specified=%v want specified empty", empty.Tools, empty.ToolsSpecified)
	}
	omittedJSON, err := json.Marshal(omitted)
	if err != nil {
		t.Fatal(err)
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if string(omittedJSON) != `{"id":"mail"}` {
		t.Fatalf("omitted JSON=%s", omittedJSON)
	}
	if string(emptyJSON) != `{"id":"mail","tools":[]}` {
		t.Fatalf("empty JSON=%s", emptyJSON)
	}
}

func TestAgentMCPBindingAllowsWildcardOnlyAndRejectsWildcardMixedWithExact(t *testing.T) {
	wildcard := []string{"*"}
	if err := ValidateAgentMCPServerBinding(AgentMCPServerBinding{ID: "mail", Tools: wildcard, ToolsSpecified: true}); err != nil {
		t.Fatalf("wildcard only: %v", err)
	}
	mixed := []string{"*", "send"}
	if err := ValidateAgentMCPServerBinding(AgentMCPServerBinding{ID: "mail", Tools: mixed, ToolsSpecified: true}); err == nil {
		t.Fatal("wildcard mixed with exact tool must reject")
	}
	empty := []string{}
	if err := ValidateAgentMCPServerBinding(AgentMCPServerBinding{ID: "mail", Tools: empty, ToolsSpecified: true}); err != nil {
		t.Fatalf("explicit none: %v", err)
	}
	if err := ValidateAgentMCPServerBinding(AgentMCPServerBinding{ID: "mail", Tools: nil}); err != nil {
		t.Fatalf("omitted means all: %v", err)
	}
}
