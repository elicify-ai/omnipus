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
// a palette value in any letter-case is stored uppercase, everything else (a well-formed hex
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

func TestToolsRefuseAColourOfTheRemovedPreviousPalette(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	isErr, body := createWithColor(t, deps, "#3B82F6")
	if !isErr {
		t.Fatalf("a previous-palette colour is invalid now (greenfield, founder 2026-10-12): %s", body)
	}

	deps2, _ := newTestDeps(t)
	store := agentstore.New(deps2.Home)
	if err := store.Create("painter", &config.AgentConfig{ID: "painter", Name: "Painter"}); err != nil {
		t.Fatal(err)
	}
	res := systools.NewAgentUpdateTool(deps2).Execute(context.Background(), map[string]any{
		"id": "painter", "revision": currentAgentRevision(t, deps2, "painter"), "color": "#9ca3af",
	})
	if !res.IsError {
		t.Fatalf("a previous-palette colour must be refused on update: %s", res.ForLLM)
	}
}

// Founder/coordinator ruling 2026-10-13: create_agent behaves like REST - an
// omitted or empty colour defaults to Pewter (#8E9196), and the tool schema says
// color is optional.
func TestCreateAgent_EmptyOrOmittedColorDefaultsToPewter(t *testing.T) {
	for _, args := range []map[string]any{
		{"name": "No Colour A", "description": "d", "soul": "You help.", "model": "test/model"},
		{"name": "No Colour B", "description": "d", "soul": "You help.", "model": "test/model", "color": ""},
	} {
		deps, _ := newTestDepsWithHome(t)
		res := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		if res.IsError {
			t.Fatalf("a missing colour defaults, it is not an error: %s", res.ForLLM)
		}
		id, _ := parseSuccess(t, res.ForLLM)["id"].(string)
		ag, err := agentstore.New(deps.Home).Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if ag.Color != "#8E9196" {
			t.Errorf("stored colour %q, want the default Pewter #8E9196", ag.Color)
		}
	}
}

func TestCreateAgent_SchemaSaysColorIsOptional(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	tool := systools.NewAgentCreateTool(deps)
	// Instrument check: the loop below proves "color" is absent only if the list
	// was actually read. A type change (for example to []any) would make the
	// comma-ok assertion fail quietly and the loop see nothing, passing for any
	// content. name is required today, so a list that was read must contain it.
	required, ok := tool.Parameters()["required"].([]string)
	if !ok {
		t.Fatalf("the required list is not a []string: %T", tool.Parameters()["required"])
	}
	hasName := false
	for _, r := range required {
		hasName = hasName || r == "name"
	}
	if !hasName {
		t.Fatalf("instrument check: name must be in the required list that was read, got %v", required)
	}
	for _, r := range required {
		if r == "color" {
			t.Fatalf("color must not be in the required list: %v", required)
		}
	}
	props, _ := tool.Parameters()["properties"].(map[string]any)
	color, _ := props["color"].(map[string]any)
	if d, _ := color["description"].(string); !strings.Contains(d, "optional") || !strings.Contains(d, "Pewter") {
		t.Errorf("color description should say optional with default Pewter, got %q", d)
	}
	if d := tool.Description(); strings.Contains(d, "color (a palette hex, e.g. #6E8BA8) are all required") {
		t.Errorf("tool description still says color is required: %q", d)
	}
}
