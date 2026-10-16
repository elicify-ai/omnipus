package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"gopkg.in/yaml.v3"
)

// Oracle: #1274 / founder L1 plan §10. These are required supported runtimes,
// not values copied from successful calls. Catalogue drift is a loud failure.
func TestL11274SupportedCLISetMatchesCreateSchema(t *testing.T) {
	want := []string{"claude-code", "codex", "opencode"}
	if got := runner.SupportedCLIs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("supported third-party CLI set = %v, want %v (#1274; WU-24 must retain workers)", got, want)
	}
	f := newL11274Fixture(t, "", false)
	omnipus, _ := f.al.GetRegistry().GetAgent("omnipus")
	tool, ok := omnipus.Tools.Get("create_agent")
	if !ok {
		t.Fatal("BLOCKED: create_agent not registered on Omnipus — required by #1274 / spec §6.5")
	}
	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatal("instrument: tool properties are not the expected schema object")
	}
	cli, ok := properties["cli"].(map[string]any)
	if !ok {
		t.Fatal("instrument: create schema lost the cli field")
	}
	if got, ok := cli["enum"].([]any); !ok || !reflect.DeepEqual(got, []any{"claude-code", "codex", "opencode"}) {
		t.Fatalf("create_agent cli enum = %v, want all three supported external workers", cli["enum"])
	}
}

func TestL11274OmnipusCreatesEachSupportedExternalWorker(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			args := l11274CreateArgs("Created "+cli, cli, path)
			result := f.execute(t, "create_agent", args)
			l11274AssertCreated(t, f, result, cli, path, "#6E8BA8")
		})
	}
}

// l11274AssertCreated checks a successful external-worker create and returns
// the created agent's id. Founder decision 2026-10-14 (WU-3): a new agent's id
// is a generated UUID, never derived from its name, so the id is read from the
// result and checked to be a UUID rather than predicted from the name.
func l11274AssertCreated(t *testing.T, f *l11274Fixture, result *tools.ToolResult, cli, path, color string) string {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("#1274: valid %s worker creation must succeed through Omnipus, got %+v", cli, result)
	}
	body := l11274ResultObject(t, result)
	id, _ := body["id"].(string)
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("created worker id %q must be a generated UUID: %v", id, err)
	}
	for key, want := range map[string]any{"id": id, "type": "subagent_3p", "cli": cli,
		"cli_path": path, "model": l11274Model, "status": "joined_workspace",
		// ConfigurationPersistenceStatus.yaml: all requested components saved.
		"persistence_status": "complete", "activation_status": "active"} {
		if got := body[key]; got != want {
			t.Errorf("created worker result[%s] = %v, want %v; ForLLM=%s", key, got, want, result.ForLLM)
		}
	}
	revision, ok := body["revision"].(string)
	if !ok || agentstore.ValidateRevision(revision) != nil {
		t.Errorf("created worker has no valid readback revision: %v", body["revision"])
	}
	durable, err := agentstore.New(f.home).ReadState(id)
	if err != nil {
		t.Fatalf("read created worker from real entity store: %v", err)
	}
	if durable.Revision != revision {
		t.Errorf("reported revision %q differs from the actual persisted revision %q", revision, durable.Revision)
	}
	stored := durable.Agent
	if stored.ID != id || stored.Name != "Created "+cli || stored.Type != config.AgentTypeWorker || stored.Color != color {
		t.Errorf("stored identity = (%q,%q,%q,%q), want (%q,%q,worker,%q)", stored.ID, stored.Name, stored.Type, stored.Color, id, "Created "+cli, color)
	}
	if stored.Model == nil || stored.Model.Primary != l11274Model {
		t.Errorf("stored model = %+v, want Primary=%q", stored.Model, l11274Model)
	}
	wantExecutor := &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli, CLIPath: path}
	if stored.Subagents == nil || !reflect.DeepEqual(stored.Subagents.Executor, wantExecutor) {
		t.Errorf("stored external executor = %+v, want %+v", stored.Subagents, wantExecutor)
	}
	state, err := workspace.ReadState(f.home, l11274WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Workspace.CoreTeam, []string{"omnipus", id}) {
		t.Errorf("create must join exactly the requesting workspace: team=%v, want [omnipus %s]", state.Workspace.CoreTeam, id)
	}
	live, ok := f.al.GetRegistry().GetAgent(id)
	if !ok || live == nil {
		t.Fatalf("created worker %q is durable but not available for immediate delegation", id)
	}
	if !reflect.DeepEqual(live.Subagents, stored.Subagents) || !f.al.GetRegistry().IsExternalCLI(id) {
		t.Errorf("live worker lost the stored external executor: live=%+v, stored=%+v", live.Subagents, stored.Subagents)
	}
	// config.json is a real save, while agents correctly live in the entity store.
	data, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved config.Config
	if err := json.Unmarshal(data, &saved); err != nil || saved.Sandbox.ToolPolicies["create_agent"] != "allow" {
		t.Errorf("real saved config is unreadable or lost the ceiling: err=%v, create_agent=%q", err, saved.Sandbox.ToolPolicies["create_agent"])
	}
	return id
}

func TestL11274NativeCreateControlUsesSameStoreAndPublication(t *testing.T) {
	f := newL11274Fixture(t, "", false)
	args := map[string]any{"name": "Native control", "description": "Same create path, native runtime",
		"soul": l11274Soul, "model": l11274Model, "color": "#6E8BA8"}
	result := f.execute(t, "create_agent", args)
	if result.IsError {
		t.Fatalf("native control must succeed before attributing external failure: %s", result.ForLLM)
	}
	body := l11274ResultObject(t, result)
	id, _ := body["id"].(string)
	if _, err := uuid.Parse(id); err != nil || body["status"] != "joined_workspace" || body["activation_status"] != "active" {
		t.Errorf("native create did not really join and publish (generated id %q): %s", id, result.ForLLM)
	}
	stored, err := agentstore.New(f.home).Get(id)
	if err != nil || stored == nil || stored.IsExternalCLIWorker() || stored.Model == nil || stored.Model.Primary != l11274Model {
		t.Fatalf("native control entity readback: stored=%+v, err=%v", stored, err)
	}
	if _, ok := f.al.GetRegistry().GetAgent(id); !ok {
		t.Fatal("native control was not published")
	}
}

// Oracle: AgentColor.yaml, not coreagent.PaletteColors (the validator under test).
// Colour is a genuine positive control on this branch, not an invented red.
func TestL11274DescriptionColourExampleIsInContractAndAcceptedVerbatim(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "AgentColor.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Enum []string `yaml:"enum"`
	}
	if yamlErr := yaml.Unmarshal(data, &contract); yamlErr != nil || len(contract.Enum) != 10 {
		t.Fatalf("instrument: AgentColor contract must supply its ten values, got %v, err=%v", contract.Enum, yamlErr)
	}
	palette := make(map[string]bool, len(contract.Enum))
	for _, color := range contract.Enum {
		palette[color] = true
	}
	if !palette["#6E8BA8"] || palette["#22C55E"] {
		t.Fatalf("instrument: contract does not distinguish the valid palette from the reported invalid example: %v", contract.Enum)
	}
	f := newL11274Fixture(t, "", false)
	omnipus, _ := f.al.GetRegistry().GetAgent("omnipus")
	tool, ok := omnipus.Tools.Get("create_agent")
	if !ok {
		t.Fatal("BLOCKED: create_agent not registered — required by #1274")
	}
	description := tool.Description()
	literals := regexp.MustCompile(`#[0-9A-Fa-f]{6}`).FindAllString(description, -1)
	if len(literals) == 0 {
		t.Fatal("create_agent description offers no copyable colour example")
	}
	for _, color := range literals {
		if !palette[strings.ToUpper(color)] {
			t.Errorf("description offers invalid AgentColor %q, contract=%v", color, contract.Enum)
		}
	}
	// Copy the first offered hex verbatim. Do not freeze surrounding prompt
	// wording ("e.g.", "example", punctuation) owned by the prompt seat.
	example := literals[0]
	args := map[string]any{"name": "Example copy", "description": "Copied colour example verbatim",
		"soul": l11274Soul, "model": l11274Model, "color": example}
	result := f.execute(t, "create_agent", args)
	if result.IsError {
		t.Fatalf("copying colour %q from the description must be accepted: %s", example, result.ForLLM)
	}
	exampleID, _ := l11274ResultObject(t, result)["id"].(string)
	stored, err := agentstore.New(f.home).Get(exampleID)
	if err != nil || stored.Color != strings.ToUpper(example) {
		t.Fatalf("copied example colour was not persisted: stored=%+v, err=%v", stored, err)
	}
	args["name"], args["color"] = "Invalid colour control", "#22C55E"
	message := l11274FailureDetails(t, f.execute(t, "create_agent", args), "INVALID_COLOR")
	if !strings.Contains(message, "#22C55E") || !strings.Contains(message, "palette") {
		t.Errorf("invalid colour refusal must name the example and palette constraint, got %q", message)
	}
	l11274AssertAbsent(t, f, "invalid-colour-control")
}
