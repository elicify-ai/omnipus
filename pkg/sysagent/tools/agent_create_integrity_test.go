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
)

func createIntegrityPartialBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("result JSON: %v (%s)", err, raw)
	}
	return body
}

func TestCreateAgent_RejectsNonStringHeartbeatWithoutWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"null", nil},
		{"number", float64(1)},
		{"bool", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := newTestDeps()
			args := createNativeArgs("Hb Type")
			args["heartbeat"] = tc.value
			result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
			if !result.IsError {
				t.Fatalf("expected INVALID_INPUT, got success: %s", result.ForLLM)
			}
			code, msg := toolErrorCode(t, result.ForLLM)
			if code != "INVALID_INPUT" {
				t.Errorf("code=%s, want INVALID_INPUT", code)
			}
			if !strings.Contains(msg, "heartbeat") {
				t.Errorf("message=%q, want it to name heartbeat", msg)
			}
			if _, err := agentstore.New(deps.Home).Get("hb-type"); err == nil {
				t.Error("wrong-type heartbeat must not create the agent")
			}
		})
	}
}

func TestCreateAgent_InitAgentHomeFailureReportsPartial(t *testing.T) {
	deps, _ := newTestDeps()
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

func TestCreateAgent_HeartbeatWriteFailureReportsPartial(t *testing.T) {
	deps, _ := newTestDeps()
	id := "hb-fault"
	hbDir := filepath.Join(deps.Home, "agents", id, "HEARTBEAT.md")
	if err := os.MkdirAll(hbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	args := createNativeArgs("Hb Fault")
	args["heartbeat"] = "ping every morning"
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if !result.IsError {
		t.Fatalf("heartbeat write failure must not claim complete success: %s", result.ForLLM)
	}
	body := createIntegrityPartialBody(t, result.ForLLM)
	if body["persistence_status"] != string(agentstore.PersistencePartial) {
		t.Errorf("persistence_status=%v, want %s", body["persistence_status"], agentstore.PersistencePartial)
	}
	if body["error_stage"] != "heartbeat" {
		t.Errorf("error_stage=%v, want heartbeat", body["error_stage"])
	}
	if body["revision"] == nil || body["revision"] == "" {
		t.Errorf("revision missing on partial envelope: %#v", body["revision"])
	}
	if _, err := agentstore.New(deps.Home).Get(id); err != nil {
		t.Fatalf("entity must remain after heartbeat write failure: %v", err)
	}
}

func TestCreateAgent_HeartbeatFileModeIsPrivate(t *testing.T) {
	deps, _ := newTestDeps()
	args := createNativeArgs("Hb Mode")
	args["heartbeat"] = "stay brief"
	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.ForLLM)
	}
	path := filepath.Join(deps.Home, "agents", "hb-mode", "HEARTBEAT.md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("HEARTBEAT.md missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("HEARTBEAT.md mode=%o, want 0600", perm)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "stay brief" {
		t.Errorf("HEARTBEAT.md %q, want stay brief", got)
	}
}
