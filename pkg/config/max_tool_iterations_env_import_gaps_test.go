// max_tool_iterations_env_import_gaps_test.go — #904 env-import gaps from the
// 8-reviewer gate: the fresh-install path (no config.json yet), and that the
// import never rewrites unrelated keys.
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md — "API and Data"
// (marker agents.defaults.max_tool_iterations_env_imported; with the marker
// present the env var is never read again), User Story 7; founder decisions
// D5 (not a setting source), D6 (copy once, then ignore). The gateway leg
// "import at boot only, never on a refresh" lives in
// pkg/gateway/rest_performance_max_tool_iterations_gaps_test.go.

package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mtiDecodeNumbers(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("config.json is not JSON: %v\n%s", err, raw)
	}
	return m
}

// setDiskGlobal rewrites agents.defaults.max_tool_iterations the way an admin
// save through Settings does (raw read-modify-write of config.json).
func mtiSetDiskGlobal(t *testing.T, p string, v int) {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m := mtiDecodeNumbers(t, raw)
	mtiDefaultsOf(t, m)["max_tool_iterations"] = v
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Fresh install, env var set, no config.json: the value applies for this
// boot, and when config.json is first persisted the import MARKER goes with
// it — otherwise the env var stays "not yet imported" and a later load can
// copy it over the admin's own save (D6: copy once, then ignore).
func TestEnvImport_FreshInstall_MarkerWrittenOnFirstPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(mtiEnvVar, "80")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("fresh-install LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 80 {
		t.Fatalf("fresh-install in-memory global = %d, want 80 (the env value applies on the first boot)", got)
	}
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatalf("first persist: %v", err)
	}
	d := mtiDiskDefaults(t, p)
	if d["max_tool_iterations"] != float64(80) {
		t.Errorf("first persisted global = %v, want 80", d["max_tool_iterations"])
	}
	if d["max_tool_iterations_env_imported"] != true {
		t.Errorf("first persisted config.json must carry max_tool_iterations_env_imported: true; got %v", d["max_tool_iterations_env_imported"])
	}
}

// Same fresh install; the admin later saves 150 in Settings. With the env
// var still 80, the next load keeps 150 in memory and on disk.
func TestEnvImport_FreshInstall_LaterAdminSaveWins(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(mtiEnvVar, "80")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("fresh-install LoadConfig: %v", err)
	}
	if saveErr := SaveConfig(p, cfg); saveErr != nil {
		t.Fatalf("first persist: %v", saveErr)
	}
	mtiSetDiskGlobal(t, p, 150)

	cfg2, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig after the admin save: %v", err)
	}
	if got := cfg2.Agents.Defaults.MaxToolIterations; got != 150 {
		t.Errorf("global after the admin save = %d, want 150 (the env value never overwrites a later admin save)", got)
	}
	if got := mtiDiskDefaults(t, p)["max_tool_iterations"]; got != float64(150) {
		t.Errorf("config.json global = %v, want 150", got)
	}
}

// The import rewrite changes exactly two members of agents.defaults and
// nothing else in the file (numbers compared verbatim).
func TestEnvImport_PreservesEveryOtherKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	before := []byte(`{"version":` + mtiItoa(CurrentVersion) + `,` +
		`"agents":{"defaults":{"workspace":"./workspace","max_tokens":4096,"max_tool_iterations":200},"list":[]},` +
		`"gateway":{"host":"127.0.0.1","port":18790},` +
		`"planning":{"goal_max_rounds":7},` +
		`"performance":{"max_parallel_agents":3},` +
		`"providers":[]}`)
	if err := os.WriteFile(p, before, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(mtiEnvVar, "80")
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := mtiDecodeNumbers(t, before)
	d := mtiDefaultsOf(t, want)
	d["max_tool_iterations"] = json.Number("80")
	d["max_tool_iterations_env_imported"] = true
	got := mtiDecodeNumbers(t, after)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("the import must change only the two agents.defaults members\nwant %v\n got %v", want, got)
	}
}

// mtiDefaultsOf returns m["agents"]["defaults"], failing the test when either
// level is missing or not a JSON object.
func mtiDefaultsOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	agents, ok := m["agents"].(map[string]any)
	if !ok {
		t.Fatalf("config.json: agents is not an object: %#v", m["agents"])
	}
	defaults, ok := agents["defaults"].(map[string]any)
	if !ok {
		t.Fatalf("config.json: agents.defaults is not an object: %#v", agents["defaults"])
	}
	return defaults
}
