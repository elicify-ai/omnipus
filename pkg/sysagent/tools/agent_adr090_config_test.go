package systools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Expectations in this file are derived from
// docs/internal/specs/adr-090-agent-configuration-and-skills-spec.md
// FR-002 / FR-003 / FR-005 / FR-007 — not from observed tool output.

func testInventory(skills []string, servers map[string][]string) func() systools.AgentConfigInventory {
	inv := systools.AgentConfigInventory{
		SkillsListed:  true,
		Skills:        map[string]struct{}{},
		ConfiguredMCP: map[string]struct{}{},
		LiveMCPTools:  map[string]map[string]struct{}{},
	}
	for _, id := range skills {
		inv.Skills[id] = struct{}{}
	}
	for server, tools := range servers {
		inv.ConfiguredMCP[server] = struct{}{}
		names := map[string]struct{}{}
		for _, name := range tools {
			names[name] = struct{}{}
		}
		inv.LiveMCPTools[server] = names
	}
	return func() systools.AgentConfigInventory { return inv }
}

func mailLiveInventory() (public string, names []string, hook func() systools.AgentConfigInventory) {
	mt := tools.NewMCPTool(nil, "mail", &mcp.Tool{Name: "inbox_read"})
	names = systools.CollectLiveMCPNames(mt)
	return mt.Name(), names, testInventory(nil, map[string][]string{"mail": names})
}

func createNativeArgs(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": "native custom agent",
		"soul":        "You help.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	}
}

func toolErrorCode(t *testing.T, body string) (code, message string) {
	t.Helper()
	parsed := parseError(t, body)
	errObj, _ := parsed["error"].(map[string]any)
	code, _ = errObj["code"].(string)
	message, _ = errObj["message"].(string)
	return code, message
}

func changedFields(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["changed_fields"].([]any)
	if !ok {
		t.Fatalf("changed_fields missing or wrong type: %#v", body["changed_fields"])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("changed_fields entry %#v is not a string", v)
		}
		out = append(out, s)
	}
	return out
}

func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// TestCreateAgent_AppliesInitialSkillsMCPAndPolicyPatchAtomically is FR-005:
// create_agent accepts skills, mcp_servers and tool_policy_changes and applies
// the patch on top of custom-agent defaults in the same persist, before
// publication.
func TestCreateAgent_AppliesInitialSkillsMCPAndPolicyPatchAtomically(t *testing.T) {
	deps, _ := newTestDeps()
	_, _, mailHook := mailLiveInventory()
	draftAndMail := testInventory([]string{"draft"}, nil)
	base := draftAndMail()
	mail := mailHook()
	base.ConfiguredMCP = mail.ConfiguredMCP
	base.LiveMCPTools = mail.LiveMCPTools
	deps.AgentConfigInventory = func() systools.AgentConfigInventory { return base }
	tool := systools.NewAgentCreateTool(deps)

	args := createNativeArgs("Capability Bot")
	args["skills"] = []any{"draft"}
	args["mcp_servers"] = []any{map[string]any{"id": "mail", "tools": []any{"inbox_read"}}}
	args["tool_policy_changes"] = map[string]any{"set": map[string]any{"bash": "ask"}}

	result := tool.Execute(context.Background(), args)
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if body["persistence_status"] != string(agentstore.PersistenceComplete) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistenceComplete)
	}
	if _, ok := body["revision"].(string); !ok || body["revision"] == "" {
		t.Errorf("revision missing: %#v", body["revision"])
	}
	if body["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("unwired publishers must report activation_status=%s, got %v", agentstore.ActivationNotAttempted, body["activation_status"])
	}

	ag, err := agentstore.New(deps.Home).Get("capability-bot")
	if err != nil {
		t.Fatalf("entity missing after create: %v", err)
	}
	if len(ag.Skills) != 1 || ag.Skills[0] != "draft" {
		t.Errorf("Skills=%v, want [draft]", ag.Skills)
	}
	if ag.Tools == nil || len(ag.Tools.MCP.Servers) != 1 || ag.Tools.MCP.Servers[0].ID != "mail" {
		t.Errorf("MCP servers=%v, want mail", ag.Tools)
	} else if !ag.Tools.MCP.Servers[0].ToolsSpecified || len(ag.Tools.MCP.Servers[0].Tools) != 1 || ag.Tools.MCP.Servers[0].Tools[0] != "inbox_read" {
		t.Errorf("MCP tools=%v specified=%v, want [inbox_read]", ag.Tools.MCP.Servers[0].Tools, ag.Tools.MCP.Servers[0].ToolsSpecified)
	}
	if got := ag.Tools.Builtin.Policies["bash"]; got != config.ToolPolicyAsk {
		t.Errorf("bash policy=%q, want ask (patch applied on custom defaults)", got)
	}
	if got := ag.Tools.Builtin.Policies["read_file"]; got != config.ToolPolicyAllow {
		t.Errorf("read_file policy=%q, want allow (custom default must survive the patch)", got)
	}
}

// TestCreateAgent_RejectsCapabilityFieldsOnExternalCLI is FR-005: capability
// fields on unsupported external runtimes must not write.
func TestCreateAgent_RejectsCapabilityFieldsOnExternalCLI(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory([]string{"draft"}, nil)
	tool := systools.NewAgentCreateTool(deps)

	args := createNativeArgs("External Bot")
	args["agent_type"] = "subagent_3p"
	args["cli"] = "opencode"
	args["skills"] = []any{"draft"}

	result := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Fatalf("expected INVALID_INPUT, got success: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if msg == "" {
		t.Error("error message must name the rejected field")
	}
	if _, err := agentstore.New(deps.Home).Get("external-bot"); err == nil {
		t.Error("external create with skills must not write an entity")
	}
}

func TestCreateAgent_RejectsUnknownSkillIDWithoutWrite(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory([]string{"draft"}, nil)
	tool := systools.NewAgentCreateTool(deps)

	args := createNativeArgs("Unknown Skill Create")
	args["skills"] = []any{"does-not-exist"}
	result := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "does-not-exist") {
		t.Errorf("message=%q, want it to name the unknown skill", msg)
	}
	if _, err := agentstore.New(deps.Home).Get("unknown-skill-create"); err == nil {
		t.Error("unknown skill must not create the agent")
	}
}

func TestUpdateAgent_RejectsUnknownSkillIDWithoutWrite(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory([]string{"draft"}, nil)
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer", Skills: []string{"draft"}}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"skills":   []any{"nope"},
	})
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.ForLLM)
	}
	code, _ := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if len(ag.Skills) != 1 || ag.Skills[0] != "draft" {
		t.Errorf("Skills after rejected write=%v, want [draft]", ag.Skills)
	}
}

func TestUpdateAgent_RejectsUnconfiguredMCPServerWithoutWrite(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory(nil, map[string][]string{"mail": {"inbox_read"}})
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":          "writer",
		"revision":    currentAgentRevision(t, deps, "writer"),
		"mcp_servers": []any{map[string]any{"id": "ghost"}},
	})
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "ghost") {
		t.Errorf("message=%q, want it to name the unconfigured server", msg)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Tools != nil && len(ag.Tools.MCP.Servers) != 0 {
		t.Errorf("MCP servers after rejected write=%v, want empty", ag.Tools.MCP.Servers)
	}
}

func TestUpdateAgent_RejectsNonLiveConnectorToolNameWithoutWrite(t *testing.T) {
	deps, _ := newTestDeps()
	_, _, mailHook := mailLiveInventory()
	deps.AgentConfigInventory = mailHook
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"mcp_servers": []any{map[string]any{
			"id":    "mail",
			"tools": []any{"not_a_live_tool"},
		}},
	})
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "not_a_live_tool") {
		t.Errorf("message=%q, want it to name the unavailable tool", msg)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Tools != nil && len(ag.Tools.MCP.Servers) != 0 {
		t.Errorf("MCP servers after rejected write=%v, want empty", ag.Tools.MCP.Servers)
	}
}

func TestUpdateAgent_EmptySkillsAndMCPServersClear(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory([]string{"draft"}, map[string][]string{"mail": {"inbox_read"}})
	store := agentstore.New(deps.Home)
	empty := []string{}
	if err := store.Create("writer", &config.AgentConfig{
		ID: "writer", Name: "Writer", Skills: []string{"draft"},
		Tools: &config.AgentToolsCfg{MCP: config.AgentMCPToolsCfg{Servers: []config.AgentMCPServerBinding{{ID: "mail", Tools: empty, ToolsSpecified: true}}}},
	}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":          "writer",
		"revision":    currentAgentRevision(t, deps, "writer"),
		"skills":      []any{},
		"mcp_servers": []any{},
	})
	if result.IsError {
		t.Fatalf("empty clears must succeed, got: %s", result.ForLLM)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if len(ag.Skills) != 0 {
		t.Errorf("Skills after [] clear=%v, want empty", ag.Skills)
	}
	if ag.Tools != nil && len(ag.Tools.MCP.Servers) != 0 {
		t.Errorf("MCP servers after [] clear=%v, want no assignments", ag.Tools)
	}
}

func TestUpdateAgent_RejectsNullAndWrongTypeScalars(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	tool := systools.NewAgentUpdateTool(deps)

	for _, tc := range []struct {
		field string
		value any
	}{
		{"name", nil},
		{"name", float64(42)},
		{"description", nil},
		{"memory_enabled", "yes"},
		{"skills", nil},
		{"voice", float64(1)},
	} {
		before, err := store.Get("writer")
		if err != nil {
			t.Fatal(err)
		}
		result := tool.Execute(context.Background(), map[string]any{
			"id":       "writer",
			"revision": currentAgentRevision(t, deps, "writer"),
			tc.field:   tc.value,
		})
		if !result.IsError {
			t.Fatalf("%s=%v: expected INVALID_INPUT, got success: %s", tc.field, tc.value, result.ForLLM)
		}
		code, msg := toolErrorCode(t, result.ForLLM)
		if code != "INVALID_INPUT" {
			t.Errorf("%s=%v: code=%s, want INVALID_INPUT", tc.field, tc.value, code)
		}
		if !strings.Contains(msg, tc.field) {
			t.Errorf("%s=%v: message=%q, want it to name the field", tc.field, tc.value, msg)
		}
		after, err := store.Get("writer")
		if err != nil {
			t.Fatal(err)
		}
		if after.Name != before.Name {
			t.Errorf("%s=%v: Name changed to %q despite rejection", tc.field, tc.value, after.Name)
		}
	}
}

func TestUpdateAgent_NullContextWindowOverrideClears(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	window := 4096
	if err := store.Create("writer", &config.AgentConfig{
		ID: "writer", Name: "Writer", ContextWindowOverride: &window,
	}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":                      "writer",
		"revision":                currentAgentRevision(t, deps, "writer"),
		"context_window_override": nil,
	})
	if result.IsError {
		t.Fatalf("null context_window_override must clear, got: %s", result.ForLLM)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.ContextWindowOverride != nil {
		t.Errorf("ContextWindowOverride=%v, want nil after null clear", *ag.ContextWindowOverride)
	}
}

func TestUpdateAgent_AppliesSharedEditableConfiguration(t *testing.T) {
	deps, cfg := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	temp := 0.2

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":                      "writer",
		"revision":                currentAgentRevision(t, deps, "writer"),
		"memory_enabled":          enabled,
		"voice":                   "alloy",
		"context_window_override": float64(2048),
		"model_params":            map[string]any{"temperature": temp, "max_tokens": float64(128)},
		"shell_policy": map[string]any{
			"enable_deny_patterns": true,
			"custom_deny_patterns": []any{"rm -rf"},
		},
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	fields := changedFields(t, body)
	for _, want := range []string{"memory_enabled", "voice", "context_window_override", "model_params", "shell_policy"} {
		if !containsField(fields, want) {
			t.Errorf("changed_fields=%v missing %s", fields, want)
		}
	}

	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.MemoryEnabled == nil || *ag.MemoryEnabled != true {
		t.Errorf("MemoryEnabled=%v, want true", ag.MemoryEnabled)
	}
	if ag.Voice != "alloy" {
		t.Errorf("Voice=%q, want alloy", ag.Voice)
	}
	if ag.ContextWindowOverride == nil || *ag.ContextWindowOverride != 2048 {
		t.Errorf("ContextWindowOverride=%v, want 2048", ag.ContextWindowOverride)
	}
	if ag.ModelParams == nil || ag.ModelParams.Temperature == nil || *ag.ModelParams.Temperature != 0.2 {
		t.Errorf("ModelParams.Temperature=%v, want 0.2", ag.ModelParams)
	}
	if ag.ModelParams == nil || ag.ModelParams.MaxTokens == nil || *ag.ModelParams.MaxTokens != 128 {
		t.Errorf("ModelParams.MaxTokens=%v, want 128", ag.ModelParams)
	}
	if ag.ShellPolicy == nil || !ag.ShellPolicy.EnableDenyPatterns || len(ag.ShellPolicy.CustomDenyPatterns) != 1 || ag.ShellPolicy.CustomDenyPatterns[0] != "rm -rf" {
		t.Errorf("ShellPolicy=%v, want enable + [rm -rf]", ag.ShellPolicy)
	}
	_ = cfg
}

func TestUpdateAgent_DefaultWritesSingleton(t *testing.T) {
	deps, cfg := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"default":  true,
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	if cfg.Agents.Defaults.DefaultAgentID != "writer" {
		t.Errorf("DefaultAgentID=%q, want writer", cfg.Agents.Defaults.DefaultAgentID)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if !ag.Default {
		t.Error("entity Default bool must be true")
	}
}

func TestUpdateAgent_RejectsDefaultAndVoiceOnWorker(t *testing.T) {
	deps, cfg := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("helper", &config.AgentConfig{
		ID: "helper", Name: "Helper", Type: config.AgentTypeWorker,
	}); err != nil {
		t.Fatal(err)
	}
	tool := systools.NewAgentUpdateTool(deps)

	defaultResult := tool.Execute(context.Background(), map[string]any{
		"id":       "helper",
		"revision": currentAgentRevision(t, deps, "helper"),
		"default":  true,
	})
	if !defaultResult.IsError {
		t.Fatalf("worker default=true must reject, got: %s", defaultResult.ForLLM)
	}
	code, _ := toolErrorCode(t, defaultResult.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("default code=%s, want INVALID_INPUT", code)
	}
	if cfg.Agents.Defaults.DefaultAgentID == "helper" {
		t.Error("singleton must not point at the worker")
	}

	voiceResult := tool.Execute(context.Background(), map[string]any{
		"id":       "helper",
		"revision": currentAgentRevision(t, deps, "helper"),
		"voice":    "alloy",
	})
	if !voiceResult.IsError {
		t.Fatalf("worker voice must reject, got: %s", voiceResult.ForLLM)
	}
	ag, err := store.Get("helper")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Voice != "" {
		t.Errorf("Voice=%q, want empty after rejection", ag.Voice)
	}
}

func TestUpdateAgent_RejectsUnknownArgumentAndTopP(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	tool := systools.NewAgentUpdateTool(deps)

	unknown := tool.Execute(context.Background(), map[string]any{
		"id":              "writer",
		"revision":        currentAgentRevision(t, deps, "writer"),
		"timeout_seconds": float64(30),
	})
	if !unknown.IsError {
		t.Fatalf("unknown field must reject, got: %s", unknown.ForLLM)
	}
	code, msg := toolErrorCode(t, unknown.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("unknown code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "timeout_seconds") {
		t.Errorf("message=%q, want timeout_seconds", msg)
	}

	topP := tool.Execute(context.Background(), map[string]any{
		"id":           "writer",
		"revision":     currentAgentRevision(t, deps, "writer"),
		"model_params": map[string]any{"top_p": 0.9},
	})
	if !topP.IsError {
		t.Fatalf("top_p must reject, got: %s", topP.ForLLM)
	}
	code, msg = toolErrorCode(t, topP.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("top_p code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "top_p") {
		t.Errorf("message=%q, want top_p", msg)
	}
}

func TestUpdateAgent_UnwiredPublishersReportNotAttempted(t *testing.T) {
	deps, _ := newTestDeps()
	if err := agentstore.New(deps.Home).Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":          "writer",
		"revision":    currentAgentRevision(t, deps, "writer"),
		"description": "no publisher wired",
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if body["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want %s", body["activation_status"], agentstore.ActivationNotAttempted)
	}
	if body["persistence_status"] != string(agentstore.PersistenceComplete) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistenceComplete)
	}
}

func TestUpdateAgent_PublisherErrorReportsActivationFailed(t *testing.T) {
	deps, _ := newTestDeps()
	deps.UpsertAgentFastFunc = func(string) error { return errors.New("registry busy") }
	if err := agentstore.New(deps.Home).Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":          "writer",
		"revision":    currentAgentRevision(t, deps, "writer"),
		"description": "publish will fail",
	})
	if result.IsError {
		t.Fatalf("persisted update must still succeed, got: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if body["activation_status"] != string(agentstore.ActivationFailed) {
		t.Errorf("activation_status=%v, want %s", body["activation_status"], agentstore.ActivationFailed)
	}
}

func TestGetAgent_RegistryMembershipAloneDoesNotProveActivation(t *testing.T) {
	homeDeps, _ := newTestDeps()
	if err := agentstore.New(homeDeps.Home).Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}

	unwired := systools.NewAgentGetTool(homeDeps).Execute(context.Background(), map[string]any{"id": "writer"})
	if unwired.IsError {
		t.Fatalf("get_agent failed: %s", unwired.ForLLM)
	}
	body := parseSuccess(t, unwired.ForLLM)
	if body["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("nil AgentIsLive must report %s, got %v", agentstore.ActivationNotAttempted, body["activation_status"])
	}

	homeDeps.AgentIsLive = func(id string) bool { return id == "writer" }
	live := systools.NewAgentGetTool(homeDeps).Execute(context.Background(), map[string]any{"id": "writer"})
	if live.IsError {
		t.Fatalf("get_agent failed: %s", live.ForLLM)
	}
	liveBody := parseSuccess(t, live.ForLLM)
	if liveBody["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("live registry membership without a matching revision must report %s, got %v", agentstore.ActivationNotAttempted, liveBody["activation_status"])
	}

	homeDeps.AgentIsLive = func(string) bool { return false }
	stored := systools.NewAgentGetTool(homeDeps).Execute(context.Background(), map[string]any{"id": "writer"})
	storedBody := parseSuccess(t, stored.ForLLM)
	if storedBody["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("stored-but-unpublished must report %s, got %v", agentstore.ActivationNotAttempted, storedBody["activation_status"])
	}
}

func TestCreateAgent_KnownEmptySkillInventoryRejectsExplicitIDs(t *testing.T) {
	deps, _ := newTestDeps()
	deps.AgentConfigInventory = testInventory(nil, nil)
	args := createNativeArgs("Fresh Skills")
	args["skills"] = []any{"not-yet-indexed"}
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if !result.IsError {
		t.Fatalf("known-empty inventory must reject unknown skill IDs, got: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "not-yet-indexed") {
		t.Errorf("message=%q, want the unknown skill id", msg)
	}
	if _, err := agentstore.New(deps.Home).Get("fresh-skills"); err == nil {
		t.Error("unknown skill must not create the agent")
	}
}

func TestCreateAgent_UnavailableSkillInventoryRejectsExplicitIDs(t *testing.T) {
	deps, _ := newTestDeps()
	args := createNativeArgs("No Inventory")
	args["skills"] = []any{"draft"}
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if !result.IsError {
		t.Fatalf("unavailable inventory must reject explicit skill IDs, got: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "unavailable") {
		t.Errorf("message=%q, want unavailable inventory", msg)
	}
	if _, err := agentstore.New(deps.Home).Get("no-inventory"); err == nil {
		t.Error("unavailable inventory must not create the agent")
	}
}

func TestCollectLiveMCPNames_IncludesPublicAndRemote(t *testing.T) {
	mt := tools.NewMCPTool(nil, "mail", &mcp.Tool{Name: "inbox_read"})
	names := systools.CollectLiveMCPNames(mt)
	public := mt.Name()
	if public == "inbox_read" {
		t.Fatalf("public registry name %q must not equal the remote name", public)
	}
	havePublic, haveRemote := false, false
	for _, n := range names {
		if n == public {
			havePublic = true
		}
		if n == "inbox_read" {
			haveRemote = true
		}
	}
	if !havePublic || !haveRemote {
		t.Fatalf("CollectLiveMCPNames=%v, want public %q and remote inbox_read", names, public)
	}
}

func TestUpdateAgent_AcceptsRemoteAndPublicMCPToolNames(t *testing.T) {
	public, _, hook := mailLiveInventory()
	for _, toolName := range []string{"inbox_read", public} {
		t.Run(toolName, func(t *testing.T) {
			deps, _ := newTestDeps()
			deps.AgentConfigInventory = hook
			store := agentstore.New(deps.Home)
			if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
				t.Fatal(err)
			}
			result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
				"id":       "writer",
				"revision": currentAgentRevision(t, deps, "writer"),
				"mcp_servers": []any{map[string]any{
					"id":    "mail",
					"tools": []any{toolName},
				}},
			})
			if result.IsError {
				t.Fatalf("live %s must be accepted, got: %s", toolName, result.ForLLM)
			}
			ag, err := store.Get("writer")
			if err != nil {
				t.Fatal(err)
			}
			if ag.Tools == nil || len(ag.Tools.MCP.Servers) != 1 || len(ag.Tools.MCP.Servers[0].Tools) != 1 || ag.Tools.MCP.Servers[0].Tools[0] != toolName {
				t.Errorf("stored tools=%v, want [%s]", ag.Tools, toolName)
			}
		})
	}
}

func TestUpdateAgent_DefaultSingletonSaveFailureReportsPartial(t *testing.T) {
	deps, cfg := newTestDeps()
	deps.SaveConfigLocked = func(*config.Config) error { return errors.New("disk full") }
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"default":  true,
	})
	if !result.IsError {
		t.Fatalf("singleton save failure must be an error, got: %s", result.ForLLM)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &body); err != nil {
		t.Fatal(err)
	}
	if body["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistencePartial)
	}
	if body["error_stage"] != "defaults_singleton" {
		t.Errorf("error_stage=%v, want defaults_singleton", body["error_stage"])
	}
	if body["revision"] == nil || body["revision"] == "" {
		t.Errorf("revision missing on partial envelope: %#v", body["revision"])
	}
	fields, _ := body["changed_fields"].([]any)
	foundDefault := false
	for _, f := range fields {
		if f == "default" {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Errorf("changed_fields=%v must include default", fields)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if !ag.Default {
		t.Error("entity Default must already be true after the first write")
	}
	if cfg.Agents.Defaults.DefaultAgentID == "writer" {
		t.Error("rolled-back singleton must not name writer")
	}
}

func TestUpdateAgent_DefaultWithoutWriterDoesNotWrite(t *testing.T) {
	deps, cfg := newTestDeps()
	deps.MutateConfig = nil
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"default":  true,
	})
	if !result.IsError {
		t.Fatalf("nil writer must not claim default changed, got: %s", result.ForLLM)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Default {
		t.Error("entity must not be written when the singleton writer is missing")
	}
	if cfg.Agents.Defaults.DefaultAgentID == "writer" {
		t.Error("singleton must not name writer")
	}
}

func TestUpdateAgent_RejectsUnknownShellPolicyField(t *testing.T) {
	deps, _ := newTestDeps()
	store := agentstore.New(deps.Home)
	if err := store.Create("writer", &config.AgentConfig{ID: "writer", Name: "Writer"}); err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":       "writer",
		"revision": currentAgentRevision(t, deps, "writer"),
		"shell_policy": map[string]any{
			"enable_deny_patterns": true,
			"bogus":                true,
		},
	})
	if !result.IsError {
		t.Fatalf("unknown nested field must reject, got: %s", result.ForLLM)
	}
	code, msg := toolErrorCode(t, result.ForLLM)
	if code != "INVALID_INPUT" {
		t.Errorf("code=%s, want INVALID_INPUT", code)
	}
	if !strings.Contains(msg, "bogus") {
		t.Errorf("message=%q, want bogus", msg)
	}
	ag, err := store.Get("writer")
	if err != nil {
		t.Fatal(err)
	}
	if ag.ShellPolicy != nil {
		t.Errorf("shell_policy after rejection=%v, want nil", ag.ShellPolicy)
	}
}
