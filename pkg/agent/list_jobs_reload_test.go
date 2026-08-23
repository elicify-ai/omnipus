// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// list_jobs_reload_test.go covers the one property of wireJobRosterForAgent
// that a boot-time test cannot see: whether the config `list_jobs` redacts
// with is the LIVE one or the one that existed when the tool was wired.
//
// Boot is not the interesting case — at boot al.cfg and the cfg being wired
// are the same object, so a captured value and a live read are
// indistinguishable. They diverge on RELOAD, because ReloadProviderAndConfig
// calls registerSharedTools (which re-runs this wiring) BEFORE it swaps
// al.cfg. A tool that captured al.GetConfig() at wiring time therefore gets
// the PRE-reload config on every reload — so the reload that turns
// tools.filter_sensitive_data on is precisely the one it misses.
//
// The assertion is the OUTCOME an operator would check: is the secret in the
// bytes the caller receives. Not "SetConfig was called", not "the field is a
// closure" — both of those pass against the broken version.
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// listJobsReloadConfig builds a config for the roster-reload harness. filter
// selects whether tools.filter_sensitive_data is on, which is the single
// setting under test; everything else is identical between generations so the
// only thing a reload changes is redaction.
func listJobsReloadConfig(home string, filter bool, secrets []string) *config.Config {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{
					ID: "planner-agent", Name: "Planner", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "planner-agent"),
				},
			},
		},
	}
	cfg.Tools.FilterSensitiveData = filter
	if len(secrets) > 0 {
		cfg.RegisterSensitiveValues(secrets)
	}
	return cfg
}

// listJobsRosterFor runs list_jobs for the agent as it exists on the loop's
// CURRENT registry, returning the raw result bytes. It re-resolves the agent
// every time on purpose: ReloadProviderAndConfig builds a brand-new registry
// with brand-new AgentInstances, so a handle captured before the reload would
// be testing a discarded object rather than the live one.
func listJobsRosterFor(t *testing.T, al *AgentLoop, agentID string) string {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		t.Fatalf("agent %q is not registered", agentID)
	}
	if _, ok := inst.Tools.Get("list_jobs"); !ok {
		t.Fatal("list_jobs was not registered by wireJobRosterForAgent")
	}
	res := inst.Tools.Execute(
		tools.WithAgentID(context.Background(), agentID),
		"list_jobs", map[string]any{"kind": "plan"},
	)
	if res == nil || res.IsError {
		t.Fatalf("list_jobs must succeed, got %+v", res)
	}
	// Parsing is not decoration: it proves the string searched below is the
	// roster the caller actually receives, not an error envelope that happens
	// to contain no secret because it contains no rows.
	var payload struct {
		Rows []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("could not parse list_jobs result %q: %v", res.ForLLM, err)
	}
	if len(payload.Rows) != 1 || payload.Rows[0].ID != "p-redaction" {
		t.Fatalf("want exactly the caller's own plan row, got %+v", payload.Rows)
	}
	return res.ForLLM
}

// TestWireJobRoster_RedactionFollowsAHotReload proves list_jobs redacts with
// the config that is live AT CALL TIME, so enabling
// tools.filter_sensitive_data in Settings takes effect on the reload that
// enabled it rather than on some unrelated later one.
func TestWireJobRoster_RedactionFollowsAHotReload(t *testing.T) {
	const secret = "sk-live-list-jobs-hot-reload-canary"

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// Generation 1: redaction OFF. This is the state an operator is about to
	// change, and it also establishes that the secret is genuinely reachable —
	// without it, a "secret absent" assertion after the reload could pass
	// simply because the row never carried it.
	cfg1 := listJobsReloadConfig(home, false, nil)
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg1, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })

	planStore := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(planStore)
	mustCreatePlan(t, planStore, &plan.Plan{
		ID: "p-redaction", Title: "Deploy with " + secret,
		WorkspaceID: "ws", OwnerAgentID: "planner-agent", State: plan.StateRunning,
		SourceChannel: "telegram", SourceChatID: "chat-p-redaction",
	})

	before := listJobsRosterFor(t, al, "planner-agent")
	if !strings.Contains(before, secret) {
		t.Fatalf("test setup: the secret must be reachable before redaction is enabled, got %q", before)
	}

	// Generation 2: the operator enables tools.filter_sensitive_data and the
	// gateway hot-reloads. registerSharedTools re-runs BEFORE al.cfg is
	// swapped, which is exactly what an eagerly-captured config would read.
	cfg2 := listJobsReloadConfig(home, true, []string{secret})
	ensureTestWorkspaceMembership(t, cfg2)
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfg2); err != nil {
		t.Fatalf("ReloadProviderAndConfig: %v", err)
	}
	if !al.GetConfig().Tools.IsFilterSensitiveDataEnabled() {
		t.Fatal("test setup: the reload must have installed the redaction-enabled config")
	}

	after := listJobsRosterFor(t, al, "planner-agent")
	if strings.Contains(after, secret) {
		t.Errorf("the secret survived a reload that enabled tools.filter_sensitive_data — "+
			"list_jobs is redacting with a stale config: %q", after)
	}
	// Windowed, like the pkg/tools redaction test: a partially-replaced secret
	// is still a disclosure, and a whole-string check would miss it.
	for i := 0; i+8 <= len(secret); i++ {
		if window := secret[i : i+8]; strings.Contains(after, window) {
			t.Errorf("8-byte window %q of the secret reached the caller after the reload", window)
		}
	}
}
