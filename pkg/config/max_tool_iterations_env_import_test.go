// max_tool_iterations_env_import_test.go — #904 RED: the retired env var
// OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS is imported ONCE into the saved
// config (clamped to 1–1000), marked as imported, and never read again
// (D5, D6, D7, D17; FR-011, FR-020). Test plan row 3 (TestEnvImport_OnceAndClamp).
//
// Spec: docs/internal/specs/tool-iteration-limit-spec.md (Approved) — Dataset
// "Env import" (no marker; file global 200), User Story 7 scenarios, "API and
// Data" (marker key agents.defaults.max_tool_iterations_env_imported, written
// through the load-time self-heal path — so LoadConfig is where it happens).
// Every expected value is from the dataset, not from running LoadConfig.

package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

const mtiEnvVar = "OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"

// mtiLogBuf captures both log paths an implementation may use: the default
// slog logger and pkg/logger's zerolog file sink.
type mtiLogBuf struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	file string
}

func (b *mtiLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *mtiLogBuf) lines() []string {
	b.mu.Lock()
	all := b.buf.String()
	b.mu.Unlock()
	if raw, err := os.ReadFile(b.file); err == nil {
		all += "\n" + string(raw)
	}
	var out []string
	for _, l := range strings.Split(all, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func mtiIsWarn(l string) bool {
	return strings.Contains(l, "level=WARN") || strings.Contains(l, `"level":"warn"`)
}

func captureMTIConfigLogs(t *testing.T) *mtiLogBuf {
	t.Helper()
	b := &mtiLogBuf{file: filepath.Join(t.TempDir(), "mti-config.log")}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	if err := logger.EnableFileLogging(b.file); err != nil {
		t.Fatalf("enable file logging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
		slog.SetDefault(prev)
	})
	return b
}

// writeMTIConfig writes a v-current config.json whose agents.defaults carries
// the given extra JSON members (e.g. `"max_tool_iterations":200`).
func writeMTIConfig(t *testing.T, defaultsExtra string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	defaults := `"workspace":"./workspace"`
	if defaultsExtra != "" {
		defaults += "," + defaultsExtra
	}
	raw := `{"version":` + mtiItoa(CurrentVersion) + `,"agents":{"defaults":{` + defaults + `},"list":[]},"providers":[]}`
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mtiItoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// diskDefaults returns agents.defaults from config.json on disk.
func mtiDiskDefaults(t *testing.T, p string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("config.json not JSON: %v", err)
	}
	agents, _ := m["agents"].(map[string]any)
	d, _ := agents["defaults"].(map[string]any)
	return d
}

// TestEnvImport_OnceAndClamp — Dataset "Env import" rows 1–6: the value is
// copied into config.json (clamped to the nearest bound, D7), the marker is
// written, out-of-range originals are named in a WARN, and startup succeeds.
func TestEnvImport_OnceAndClamp(t *testing.T) {
	cases := []struct {
		row       int
		env       string
		wantSaved float64
		wantWarn  bool
	}{
		{1, "80", 80, false},
		{2, "1", 1, false},
		{3, "1000", 1000, false},
		{4, "0", 1, true},
		{5, "5000", 1000, true},
		{6, "-7", 1, true},
	}
	for _, tc := range cases {
		t.Run("row"+mtiItoa(tc.row)+"_"+tc.env, func(t *testing.T) {
			logs := captureMTIConfigLogs(t)
			p := writeMTIConfig(t, `"max_tool_iterations":200`)
			t.Setenv(mtiEnvVar, tc.env)

			cfg, err := LoadConfig(p)
			if err != nil {
				t.Fatalf("LoadConfig must never refuse over the env value (D7): %v", err)
			}
			d := mtiDiskDefaults(t, p)
			if d["max_tool_iterations"] != tc.wantSaved {
				t.Errorf("config.json agents.defaults.max_tool_iterations = %v, want %v (imported, clamped to 1–1000)", d["max_tool_iterations"], tc.wantSaved)
			}
			if d["max_tool_iterations_env_imported"] != true {
				t.Errorf("config.json must carry max_tool_iterations_env_imported: true after the import; got %v", d["max_tool_iterations_env_imported"])
			}
			if cfg.Agents.Defaults.MaxToolIterations != int(tc.wantSaved) {
				t.Errorf("in-memory global = %d, want %v", cfg.Agents.Defaults.MaxToolIterations, tc.wantSaved)
			}
			var mention, warnWithValue bool
			for _, l := range logs.lines() {
				if strings.Contains(l, mtiEnvVar) {
					mention = true
					if mtiIsWarn(l) && strings.Contains(l, tc.env) {
						warnWithValue = true
					}
				}
			}
			if !mention {
				t.Errorf("a log line naming %s must say it was copied and is no longer used; lines: %v", mtiEnvVar, logs.lines())
			}
			if tc.wantWarn && !warnWithValue {
				t.Errorf("a WARN naming the original value %q must be logged (D7); lines: %v", tc.env, logs.lines())
			}
		})
	}
}

// TestEnvImport_IgnoredAfterImport — Scenario "Env var ignored after import":
// marker set, global 150 on disk, env 80 → the global stays 150, the file is
// untouched, and a log line says the env var is ignored (D5).
func TestEnvImport_IgnoredAfterImport(t *testing.T) {
	logs := captureMTIConfigLogs(t)
	p := writeMTIConfig(t, `"max_tool_iterations":150,"max_tool_iterations_env_imported":true`)
	before, _ := os.ReadFile(p)
	t.Setenv(mtiEnvVar, "80")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 150 {
		t.Errorf("global = %d, want 150 (the env var must never be read after the marker, D5)", cfg.Agents.Defaults.MaxToolIterations)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Errorf("config.json must be untouched when the marker is already set")
	}
	found := false
	for _, l := range logs.lines() {
		if strings.Contains(l, mtiEnvVar) {
			found = true
		}
	}
	if !found {
		t.Errorf("a log line naming %s must say it is ignored; lines: %v", mtiEnvVar, logs.lines())
	}
}

// TestEnvImport_SecondBootDoesNotReimport — "imported once": after the first
// import (80), a later boot with a different env value keeps 80.
func TestEnvImport_SecondBootDoesNotReimport(t *testing.T) {
	p := writeMTIConfig(t, `"max_tool_iterations":200`)
	t.Setenv(mtiEnvVar, "80")
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("first LoadConfig: %v", err)
	}
	t.Setenv(mtiEnvVar, "90")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("second LoadConfig: %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 80 {
		t.Errorf("second boot global = %d, want 80 (imported once, then ignored)", cfg.Agents.Defaults.MaxToolIterations)
	}
	if got := mtiDiskDefaults(t, p)["max_tool_iterations"]; got != float64(80) {
		t.Errorf("config.json global = %v, want 80", got)
	}
}

// TestEnvImport_NonNumericNotImported — Dataset "Env import" row 7 /
// Scenario "Non-numeric env var is not imported" (D17): no import, no
// marker, file bytes unchanged, WARN names the value, startup succeeds.
func TestEnvImport_NonNumericNotImported(t *testing.T) {
	logs := captureMTIConfigLogs(t)
	p := writeMTIConfig(t, `"max_tool_iterations":200`)
	before, _ := os.ReadFile(p)
	t.Setenv(mtiEnvVar, "abc")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig must not refuse to start over a non-numeric env value (D17): %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 200 {
		t.Errorf("global = %d, want 200 unchanged", cfg.Agents.Defaults.MaxToolIterations)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Errorf("config.json must be byte-identical (no import, no marker); got %s", after)
	}
	warned := false
	for _, l := range logs.lines() {
		if mtiIsWarn(l) && strings.Contains(l, "abc") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("a WARN naming the value \"abc\" must be logged; lines: %v", logs.lines())
	}
}

// TestEnvImport_UnsetNoWrite — Dataset "Env import" row 8: env unset → no
// marker written, file untouched, global 200 (regression guard; expected to
// hold on the pre-change code too).
func TestEnvImport_UnsetNoWrite(t *testing.T) {
	p := writeMTIConfig(t, `"max_tool_iterations":200`)
	before, _ := os.ReadFile(p)
	t.Setenv(mtiEnvVar, "")
	if err := os.Unsetenv(mtiEnvVar); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 200 {
		t.Errorf("global = %d, want 200", cfg.Agents.Defaults.MaxToolIterations)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Errorf("config.json must be untouched when the env var is unset")
	}
}

// TestMTILogCapture_Instrument proves captureMTIConfigLogs can see a WARN
// from BOTH log paths — so an empty capture in the tests above means
// "nothing was logged", not "the capture is blind".
func TestMTILogCapture_Instrument(t *testing.T) {
	logs := captureMTIConfigLogs(t)
	slog.Warn("mti-probe-slog")
	logger.WarnF("mti-probe-zerolog", map[string]any{"k": "v"})
	var sawSlog, sawZerolog bool
	for _, l := range logs.lines() {
		if mtiIsWarn(l) && strings.Contains(l, "mti-probe-slog") {
			sawSlog = true
		}
		if mtiIsWarn(l) && strings.Contains(l, "mti-probe-zerolog") {
			sawZerolog = true
		}
	}
	if !sawSlog || !sawZerolog {
		t.Fatalf("log capture is blind: slog=%v zerolog=%v; lines: %v", sawSlog, sawZerolog, logs.lines())
	}
}
