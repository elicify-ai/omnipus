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

// TestNewAgentInstanceWiresDocumentRuntimeForAllNativeAgents pins the ES-FR-04
// context-based availability rule: EVERY native agent — including the Deny-matrix
// roles (Jim, Researcher) and a custom agent — gets the managed document runtime
// view. Availability follows the turn context; POLICY, not registration, decides
// who can invoke what. The old Mia/worker/Admin gate is retired.
func TestNewAgentInstanceWiresDocumentRuntimeForAllNativeAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	// The macOS acceptance environment supplies Python from the project toolchain;
	// external document dependencies intentionally remain absent in this test.
	t.Setenv("PATH", "/Users/danielpiatkowski/Documents/Agent-Workspace/elicify-Skills/.venv/bin"+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, id := range []string{"mia", "worker", "admin", "jim", "researcher"} {
		t.Run(id, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
			agentCfg := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCore}
			instance := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
			if instance.DocumentRuntime == nil {
				t.Fatal("document runtime was not wired")
			}
			want, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, "shared")
			if err != nil {
				t.Fatal(err)
			}
			if *instance.DocumentRuntime != want {
				t.Fatalf("layout=%+v want=%+v", *instance.DocumentRuntime, want)
			}
			if _, err = os.Stat(want.Manifest); err != nil {
				t.Fatalf("manifest not provisioned: %v", err)
			}
			// The probe must run through the registered bash (the exempt argv)
			// and report the missing external dependency as structured output —
			// readiness is the agent's own check, never an app assertion.
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

			// The retired Admin finalize route must stay retired: the magic
			// command is an ordinary command now, whatever the caller's role.
			finalize := bash.Execute(tools.WithAgentID(context.Background(), id), map[string]any{"command": documentruntime.FinalizeCommand})
			if !finalize.IsError {
				t.Fatalf("finalize must not be intercepted for %s: %+v", id, finalize)
			}
			if strings.Contains(finalize.ForLLM, `"status":"ready"`) || strings.Contains(finalize.ForLLM, "restricted to Admin") {
				t.Fatalf("finalize interception survived for %s: %+v", id, finalize)
			}

			// environment_setup is registered live for every native agent
			// (ES-FR-01); its Ask matrix is seeded policy, not registration.
			if _, ok := instance.Tools.Get("environment_setup"); !ok {
				t.Fatalf("environment_setup not registered for %s", id)
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

// TestNewAgentInstanceWiresDocumentRuntimeForCustomAgent covers ES-BDD-02's
// category: a custom native agent with no special role gets the same runtime
// view — document skills included — with no delegation edges required.
func TestNewAgentInstanceWiresDocumentRuntimeForCustomAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	agentCfg := config.AgentConfig{ID: "custom-doc-agent", Name: "Custom", Type: config.AgentTypeCore}
	instance := NewAgentInstance(&agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if instance.DocumentRuntime == nil {
		t.Fatal("custom agent unexpectedly lacks the document runtime view")
	}
	if _, ok := instance.Tools.Get("environment_setup"); !ok {
		t.Fatal("custom agent lacks environment_setup registration")
	}
	// Skill-tool REGISTRATION is a loop-layer concern (the instance ships the
	// runtime view the skill load publishes into) — proven end to end by
	// TestNewAgentLoopDocumentSkillPublishesProvisionedRuntime on native
	// agents; a bare instance must expose the runtime, not the registry
	// surface of a higher layer.
}

func TestProvisionFirstPartyPreservesFinalizedManifest(t *testing.T) {
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, "shared")
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
	layout, err := documentruntime.ResolveLayout(t.TempDir(), documentruntime.ManifestRevision, "shared")
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

// TestNewAgentInstanceProvisionFailureLeavesRuntimeUnwired keeps the failure
// posture: when first-party provisioning fails, the runtime view stays
// unwired, bash runs without the managed layer (and without any interception
// of the retired finalize command).
func TestNewAgentInstanceProvisionFailureLeavesRuntimeUnwired(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	layout, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, "shared")
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
	if !result.IsError {
		t.Fatalf("finalize must be an ordinary failing command: %+v", result)
	}
	if strings.Contains(result.ForLLM, `"status":"ready"`) || strings.Contains(result.ForLLM, "restricted to Admin") {
		t.Fatalf("finalize interception survived a failed provisioning: %+v", result)
	}
}
