package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func createIntegrityPartialBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("result JSON: %v (%s)", err, raw)
	}
	return body
}

// r17 AG-06 / FR-134 / 15.9: heartbeat is an unknown creation argument,
// not an optional string. Keep the zero-write guarantee for every JSON kind.
func TestCreateAgent_RejectsRetiredHeartbeatWithoutWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"string", "stay brief"},
		{"empty_string", ""},
		{"null", nil},
		{"number", float64(1)},
		{"bool", true},
		{"array", []any{"stay brief"}},
		{"object", map[string]any{"body": "stay brief"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := newTestDeps(t)
			args := createNativeArgs("Retired Heartbeat")
			args["heartbeat"] = tc.value
			registry := tools.NewToolRegistry()
			registry.Register(systools.NewAgentCreateTool(deps))
			before := wu3DurableInventory(t, deps.Home)
			result := registry.Execute(context.Background(), "create_agent", args)
			if !result.IsError {
				t.Fatalf("retired heartbeat must be refused, got success: %s", result.ForLLM)
			}
			body := createIntegrityPartialBody(t, result.ForLLM)
			errBody, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("schema refusal must identify the unknown argument: %s", result.ForLLM)
			}
			const want = `invalid arguments for tool "create_agent": unexpected property "heartbeat"`
			if errBody["message"] != want {
				t.Errorf("message=%q, want %q", errBody["message"], want)
			}
			if body["outcome"] != tools.OutcomeFailedNothingSaved {
				t.Errorf("outcome=%v, want %s", body["outcome"], tools.OutcomeFailedNothingSaved)
			}
			changed, ok := body["changed_fields"].([]any)
			if !ok || len(changed) != 0 {
				t.Errorf("changed_fields=%#v, want an empty array", body["changed_fields"])
			}
			requireNoDurableChange(t, deps.Home, before, "unknown heartbeat must not create any durable state")
		})
	}
}

func TestCreateAgent_InitAgentHomeFailureReportsPartial(t *testing.T) {
	// Fixture keyed by the id the name produced before ids became generated UUIDs.
	systools.UseNameKeyedAgentIDsForTest(t)
	deps, _ := newTestDeps(t)
	id := "home-fault"
	sessions := filepath.Join(deps.Home, "agents", id, "sessions")
	if err := os.MkdirAll(filepath.Dir(sessions), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessions, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs("Home Fault"))
	if !result.IsError {
		t.Fatalf("InitAgentHome failure must not claim complete success: %s", result.ForLLM)
	}
	body := createIntegrityPartialBody(t, result.ForLLM)
	if body["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistencePartial)
	}
	if body["error_stage"] != "init_home" {
		t.Errorf("error_stage=%v, want init_home", body["error_stage"])
	}
	if body["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want not_attempted", body["activation_status"])
	}
	rev, _ := body["revision"].(string)
	if rev == "" {
		t.Errorf("revision missing on partial envelope: %#v", body["revision"])
	}
	if _, err := agentstore.New(deps.Home).Get(id); err != nil {
		t.Fatalf("entity must remain readable after partial home init: %v", err)
	}
	retry := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs("Home Fault"))
	if !retry.IsError {
		t.Fatalf("retry must see the durable entity, got success: %s", retry.ForLLM)
	}
	code, _ := toolErrorCode(t, retry.ForLLM)
	if code != "AGENT_ALREADY_EXISTS" {
		t.Errorf("retry code=%s, want AGENT_ALREADY_EXISTS (recover via get_agent/delete_agent, not a silent recreate)", code)
	}
}

// AG-06 retires the heartbeat write stage. Preserve the partial-persistence
// guarantee at a supported home-init stage; the sessions collision remains
// covered by TestCreateAgent_InitAgentHomeFailureReportsPartial above.
func TestCreateAgent_MemoryHomeFailureReportsPartial(t *testing.T) {
	systools.UseNameKeyedAgentIDsForTest(t)
	deps, _ := newTestDeps(t)
	const id = "home-memory-fault"
	memory := filepath.Join(deps.Home, "agents", id, "memory")
	if err := os.MkdirAll(filepath.Dir(memory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs("Home Memory Fault"))
	if !result.IsError {
		t.Fatalf("home initialization failure must not claim complete success: %s", result.ForLLM)
	}
	body := createIntegrityPartialBody(t, result.ForLLM)
	if body["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistencePartial)
	}
	if body["error_stage"] != "init_home" {
		t.Errorf("error_stage=%v, want init_home", body["error_stage"])
	}
	if body["activation_status"] != string(agentstore.ActivationNotAttempted) {
		t.Errorf("activation_status=%v, want %s", body["activation_status"], agentstore.ActivationNotAttempted)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "agent home could not be initialized") {
		t.Errorf("message=%q, want the home-initialization failure", msg)
	}
	state, err := agentstore.New(deps.Home).ReadState(id)
	if err != nil {
		t.Fatalf("entity must remain readable after partial home initialization: %v", err)
	}
	if state.Agent == nil || state.Agent.Name != "Home Memory Fault" {
		t.Fatalf("the protected entity must really have been persisted: %#v", state.Agent)
	}
	if state.Revision == "" || body["revision"] != state.Revision {
		t.Errorf("partial revision=%v, want current readable revision %q", body["revision"], state.Revision)
	}
}

// AG-06 removes heartbeat creation. Retain exact private-file content/mode
// coverage for the supported native instruction file, SOUL.md.
func TestCreateAgent_SoulFileModeIsPrivate(t *testing.T) {
	systools.UseNameKeyedAgentIDsForTest(t)
	deps, _ := newTestDeps(t)
	args := createNativeArgs("Soul Mode")
	args["soul"] = "stay brief"
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	path := filepath.Join(deps.Home, "agents", "soul-mode", "SOUL.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("SOUL.md missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("SOUL.md mode=%o, want 0600", perm)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "stay brief" {
		t.Errorf("SOUL.md %q, want stay brief", got)
	}
}
