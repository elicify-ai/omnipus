package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// S2-03 (combined review; architect ruling): create_agent and update_agent use
// the same colour rule as REST - coreagent.CanonicalColor (AgentColor.yaml):
// a palette value in any letter-case is stored uppercase, a previous-palette
// value is mapped to its new equivalent, everything else (a well-formed hex
// outside both sets such as #22C55E included) is refused.

func createWithColor(t *testing.T, deps *systools.Deps, color string) (isErr bool, body string) {
	t.Helper()
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Color Bot", "description": "d", "soul": "You help.", "model": "test/model", "color": color,
	})
	return res.IsError, res.ForLLM
}

func TestCreateAgent_ColorOutsideThePaletteIsRefused(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	isErr, body := createWithColor(t, deps, "#22C55E")
	if !isErr {
		t.Fatalf("a well-formed hex outside the palette must be refused, got success: %s", body)
	}
	code, _ := toolErrorCode(t, body)
	if code != "INVALID_COLOR" {
		t.Errorf("code=%s, want INVALID_COLOR", code)
	}
	if !strings.Contains(body, "#6E8BA8") {
		t.Errorf("the refusal should list palette values; body=%s", body)
	}
}

func TestCreateAgent_ColorIsStoredCanonical(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"#6e8ba8", "#6E8BA8"}, // lower-case palette value
		{"#3B82F6", "#6E8BA8"}, // previous palette (Blue -> Steel blue)
	} {
		deps, _ := newTestDepsWithHome(t)
		isErr, body := createWithColor(t, deps, tc.in)
		if isErr {
			t.Fatalf("%s must be accepted: %s", tc.in, body)
		}
		id, _ := parseSuccess(t, body)["id"].(string)
		ag, err := agentstore.New(deps.Home).Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if ag.Color != tc.want {
			t.Errorf("input %s stored as %q, want %q", tc.in, ag.Color, tc.want)
		}
	}
}

func TestUpdateAgent_ColorOutsideThePaletteIsRefusedAndNothingChanges(t *testing.T) {
	deps, _ := newTestDeps(t)
	store := agentstore.New(deps.Home)
	if err := store.Create("painter", &config.AgentConfig{ID: "painter", Name: "Painter", Color: "#6E8BA8"}); err != nil {
		t.Fatal(err)
	}
	res := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": "painter", "revision": currentAgentRevision(t, deps, "painter"), "color": "#22C55E",
	})
	if !res.IsError {
		t.Fatalf("off-palette colour must be refused: %s", res.ForLLM)
	}
	ag, err := store.Get("painter")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Color != "#6E8BA8" {
		t.Errorf("colour after a refused update = %q, want unchanged", ag.Color)
	}
}

func TestUpdateAgent_LegacyColorIsStoredCanonical(t *testing.T) {
	deps, _ := newTestDeps(t)
	store := agentstore.New(deps.Home)
	if err := store.Create("painter", &config.AgentConfig{ID: "painter", Name: "Painter"}); err != nil {
		t.Fatal(err)
	}
	res := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": "painter", "revision": currentAgentRevision(t, deps, "painter"), "color": "#9ca3af",
	})
	if res.IsError {
		t.Fatalf("a previous-palette colour must be accepted: %s", res.ForLLM)
	}
	ag, err := store.Get("painter")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Color != "#8E9196" {
		t.Errorf("stored %q, want #8E9196 (Grey -> Pewter)", ag.Color)
	}
}
