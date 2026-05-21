// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package datamodel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestDefaultGatewayPort_UnifiedAtFiveThousand is the regression guard for
// issue #160.
//
// Before the fix, three independent default sources disagreed:
//
//   - datamodel.defaultConfig (the JSON seed written by Init on first run)
//     used port 3000;
//   - config.DefaultConfig() (the in-memory Go struct used as the fallback
//     when boot can't read config.json) used port 18790;
//   - the CLAUDE.md operator docs document 5000 as the de facto gateway port.
//
// The mismatch broke fresh-install Docker containers: the embedded SPA was
// served on whichever port hit first, the help text pointed at a different
// one, and operators had to hunt across log lines to find the actual port.
// CLAUDE.md's "Quick start" instructions assumed 5000.
//
// This test pins the value in all three places. Any new default-port source
// added in the future must also be added here so it cannot drift silently.
func TestDefaultGatewayPort_UnifiedAtFiveThousand(t *testing.T) {
	const wantPort = 5000

	// Source 1: datamodel.defaultConfig seed. Materialize it by running
	// Init() into a tmp home and reading the resulting config.json — this
	// also covers the Marshal/Unmarshal round-trip that the operator would
	// actually see on disk.
	tmp := t.TempDir()
	if err := Init(tmp); err != nil {
		t.Fatalf("Init: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, "config.json"))
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse seeded config: %v", err)
	}
	gw, _ := m["gateway"].(map[string]any)
	if gw == nil {
		t.Fatalf("seeded config has no gateway section: %v", m)
	}
	// JSON numbers come back as float64 — compare via int conversion.
	gotPort, ok := gw["port"].(float64)
	if !ok {
		t.Fatalf("seeded gateway.port has unexpected type %T", gw["port"])
	}
	if int(gotPort) != wantPort {
		t.Errorf("datamodel.defaultConfig: gateway.port = %d, want %d", int(gotPort), wantPort)
	}

	// Source 2: config.DefaultConfig() — the in-memory Go struct fallback.
	cfg := config.DefaultConfig()
	if cfg.Gateway.Port != wantPort {
		t.Errorf("config.DefaultConfig(): Gateway.Port = %d, want %d", cfg.Gateway.Port, wantPort)
	}
}
