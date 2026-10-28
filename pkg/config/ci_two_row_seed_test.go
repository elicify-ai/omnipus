package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCITwoRowSeed_ScriptMatchesGoldenAndLoads pins design.md §7 R5.
// scripts/e2e-model.sh seed-config must print the golden two-row document,
// and that document must load with default_model equal to the primary pair
// (minimax-coding-plan / MiniMax-M3.1-Flash) and both provider rows valid.
// The golden's field order and the port 19999 are the contract in
// /workspace/ci-minimax/api-contract.md §2.
func TestCITwoRowSeed_ScriptMatchesGoldenAndLoads(t *testing.T) {
	root := repoRootForSeed(t)
	script := filepath.Join(root, "scripts", "e2e-model.sh")
	goldenPath := filepath.Join(root, "pkg", "config", "testdata", "ci_two_row_seed.golden.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	modelFile := filepath.Join(t.TempDir(), "e2e-model.json")
	const spec = `{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}
`
	if err := os.WriteFile(modelFile, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec json: %v", err)
	}

	cmd := exec.Command("bash", script, "seed-config", "19999", "--file", modelFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("seed-config failed: %v\n%s", err, out)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("seed-config stdout does not match %s\n--- got ---\n%s\n--- want ---\n%s", goldenPath, out, want)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, out, 0o600); err != nil {
		t.Fatalf("write seeded config: %v", err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig(seed) = %v, want nil", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d entries, want 2", len(cfg.Providers))
	}

	primary := cfg.Providers[0]
	if primary.Provider != "minimax-coding-plan" || primary.Model != "MiniMax-M3.1-Flash" || primary.APIKeyRef != "MINIMAX_API_KEY" {
		t.Errorf("providers[0] = %+v, want minimax-coding-plan / MiniMax-M3.1-Flash / MINIMAX_API_KEY", primary)
	}
	if primary.APIBase != "https://api.minimax.io/anthropic/v1" {
		t.Errorf("providers[0].APIBase = %q, want the MiniMax Anthropic base", primary.APIBase)
	}
	if err := primary.Validate(); err != nil {
		t.Errorf("providers[0].Validate() = %v, want nil", err)
	}

	fallback := cfg.Providers[1]
	if fallback.Provider != "openrouter" || fallback.Model != "deepseek/deepseek-v4.1-flash" || fallback.APIKeyRef != "OPENROUTER_API_KEY" {
		t.Errorf("providers[1] = %+v, want openrouter / deepseek/deepseek-v4.1-flash / OPENROUTER_API_KEY", fallback)
	}
	if fallback.APIBase != "https://openrouter.ai/api/v1" {
		t.Errorf("providers[1].APIBase = %q, want the OpenRouter base", fallback.APIBase)
	}
	if err := fallback.Validate(); err != nil {
		t.Errorf("providers[1].Validate() = %v, want nil", err)
	}

	wantDefault := DefaultModel{Provider: "minimax-coding-plan", Model: "MiniMax-M3.1-Flash"}
	if cfg.Agents.Defaults.DefaultModel != wantDefault {
		t.Errorf("agents.defaults.default_model = %+v, want %+v", cfg.Agents.Defaults.DefaultModel, wantDefault)
	}
}

func repoRootForSeed(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the test working directory")
		}
		dir = parent
	}
}
