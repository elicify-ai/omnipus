package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestNewAgentInstanceWiresDocumentRuntimeForApprovedRoles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	// The macOS acceptance environment supplies Python from the project toolchain;
	// external document dependencies intentionally remain absent in this test.
	t.Setenv("PATH", "/Users/danielpiatkowski/Documents/Agent-Workspace/elicify-Skills/.venv/bin"+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, id := range []string{"mia", "worker", "admin"} {
		t.Run(id, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			agentCfg := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCore}
			instance := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
			if instance.DocumentRuntime == nil {
				t.Fatal("document runtime was not wired")
			}
			want, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, id)
			if err != nil {
				t.Fatal(err)
			}
			if *instance.DocumentRuntime != want {
				t.Fatalf("layout=%+v want=%+v", *instance.DocumentRuntime, want)
			}
			if _, err = os.Stat(want.Manifest); err != nil {
				t.Fatalf("manifest not provisioned: %v", err)
			}
			bash, ok := instance.Tools.Get("bash")
			if !ok {
				t.Fatal("bash not registered")
			}
			result := bash.Execute(tools.WithAgentID(context.Background(), id), map[string]any{"command": strings.Join(documentruntime.ProbeArgv(want), " ")})
			if !result.IsError {
				t.Fatalf("missing dependencies must remain incomplete: %+v", result)
			}
			if !strings.Contains(result.ForLLM, `"ok": false`) || !strings.Contains(result.ForLLM, `"component": "python"`) {
				t.Fatalf("probe must report the incomplete dependency as structured output: %+v", result)
			}

			finalize := bash.Execute(tools.WithAgentID(context.Background(), id), map[string]any{"command": documentruntime.FinalizeCommand})
			if !finalize.IsError {
				t.Fatalf("unready runtime unexpectedly finalized: %+v", finalize)
			}
			wantMessage := "restricted to Admin"
			if id == "admin" {
				wantMessage = "setup incomplete"
			}
			if !strings.Contains(finalize.ForLLM, wantMessage) {
				t.Fatalf("finalize result=%q want substring %q", finalize.ForLLM, wantMessage)
			}
			if id == "admin" {
				for _, path := range []string{filepath.Join(want.Bin, "python"), filepath.Join(want.Bin, "node"), filepath.Join(want.Bin, "soffice")} {
					if err = os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				finalize = bash.Execute(tools.WithAgentID(context.Background(), id), map[string]any{"command": documentruntime.FinalizeCommand})
				if finalize.IsError || !strings.Contains(finalize.ForLLM, `"status":"ready"`) {
					t.Fatalf("Admin finalization failed through registered bash: %+v", finalize)
				}
			}
		})
	}
}

func TestNewAgentLoopDocumentSkillPublishesProvisionedRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
		{ID: "worker", Name: "General Purpose", Type: config.AgentTypeWorker},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	for _, id := range []string{"mia", "worker"} {
		agent, ok := al.registry.GetAgent(id)
		if !ok {
			t.Fatalf("agent %q missing", id)
		}
		skill, ok := agent.Tools.Get("Skill")
		if !ok {
			t.Fatalf("agent %q Skill tool missing", id)
		}
		registered, ok := skill.(*tools.SkillTool)
		if !ok {
			t.Fatalf("agent %q Skill has type %T", id, skill)
		}
		registered.SetResolver(func(context.Context, string) tools.SkillLoadOutcome {
			return tools.SkillLoadOutcome{Status: tools.SkillLoadLoaded, Content: "document instructions"}
		}, func(context.Context, string) bool { return true }, nil)
		result := skill.Execute(tools.WithAgentID(context.Background(), id), map[string]any{"name": "elicify-docx"})
		if result.IsError {
			t.Fatalf("agent %q document skill load failed: %s", id, result.ForLLM)
		}
		layout := agent.DocumentRuntime
		if layout == nil || !strings.Contains(result.ForLLM, filepath.Join(layout.Skills, "elicify-docx")) || !strings.Contains(result.ForLLM, strings.Join(documentruntime.ProbeArgv(*layout), " ")) {
			t.Fatalf("agent %q skill omitted provisioned runtime: %s", id, result.ForLLM)
		}
	}
}

func TestNewAgentInstanceDoesNotWireDocumentRuntimeForOtherRoles(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	agentCfg := config.AgentConfig{ID: "jim", Name: "Jim", Type: config.AgentTypeCore}
	instance := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if instance.DocumentRuntime != nil {
		t.Fatalf("Jim unexpectedly received document runtime: %+v", instance.DocumentRuntime)
	}
}

func TestProvisionFirstPartyPreservesFinalizedManifest(t *testing.T) {
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := documentruntime.ProvisionFirstParty(layout)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Python, manifest.Node, manifest.Converter = "/ready/python", "/ready/node", "/ready/soffice"
	data, _ := json.Marshal(manifest)
	if err = os.WriteFile(layout.Manifest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := documentruntime.ProvisionFirstParty(layout)
	if err != nil {
		t.Fatal(err)
	}
	if got.Python != manifest.Python || got.Node != manifest.Node || got.Converter != manifest.Converter {
		t.Fatalf("ready paths erased: %+v", got)
	}
}

func TestProvisionFirstPartyIsSafeDuringConcurrentStartup(t *testing.T) {
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}

	const callers = 12
	results := make(chan documentruntime.Manifest, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manifest, provisionErr := documentruntime.ProvisionFirstParty(layout)
			if provisionErr != nil {
				errs <- provisionErr
				return
			}
			results <- manifest
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for provisionErr := range errs {
		t.Errorf("concurrent provision: %v", provisionErr)
	}
	for manifest := range results {
		if manifest.Revision != documentruntime.ManifestRevision {
			t.Errorf("revision=%q want %q", manifest.Revision, documentruntime.ManifestRevision)
		}
		if err := documentruntime.VerifyAssets(layout.Prefix, manifest.Assets); err != nil {
			t.Errorf("assets: %v", err)
		}
	}
}

func TestNewAgentInstanceProvisionFailureLeavesRuntimeUnwired(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	layout, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, "mia")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(layout.Manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(layout.Manifest, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	agentCfg := config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}
	instance := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if instance.DocumentRuntime != nil {
		t.Fatalf("failed provisioning was published as configured: %+v", instance.DocumentRuntime)
	}
	bash, ok := instance.Tools.Get("bash")
	if !ok {
		t.Fatal("bash not registered")
	}
	result := bash.Execute(tools.WithAgentID(context.Background(), "mia"), map[string]any{"command": documentruntime.FinalizeCommand})
	if !result.IsError || !strings.Contains(result.ForLLM, "not configured") {
		t.Fatalf("failed provisioning must stay unavailable: %+v", result)
	}
}
