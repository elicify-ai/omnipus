// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 6.
// TestInstall_TrustDenyRefusesUnverified: under trust Deny, an unverified
// skill cannot be installed through the screen REST path or by an agent's
// install_skill, and nothing lands on disk.
//
// Oracles: skills-tools-redesign-spec.md C-3 trust table ("deny | refused:
// 'This skill is unverified, and unverified skills are set to Deny in
// Skills & Tools. Nothing was installed.'"), BDD S-27 and S-28. The
// configured trust here is the POST-migration vocabulary value "deny"
// (config.SkillTrustLevel is a string type, so the assignment compiles
// before the migration of C-3 lands — today it matches nothing, which is
// exactly the missing behaviour this test pins).
//
// The spec's S-27 also disables the screen's Install button in the review;
// the review endpoint itself (POST /skills/install/review) is C-13 and is
// RED-tested by orders 35/53 — the subtest below pins only its trust-Deny
// refusal so the rule is not lost between the two.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/stretchr/testify/require"
)

// trustDenyAPI is newTestRestAPIWithHome with sandbox.skill_trust set to
// the post-migration Deny value.
func trustDenyAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	// C-3 vocabulary value after the migration; today's enum does not know
	// it (string-typed, so this compiles), which is the gap under test.
	cfg.Sandbox.SkillTrust = config.SkillTrustLevel("deny")
	// A slug install or review needs an enabled skill marketplace: without one
	// the handler answers 409 "no skill marketplace is enabled" before it ever
	// reaches the trust rule, so S-27 below would fail for the wrong reason.
	// Same entry as newTestRestAPIWithClawHub.
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, BaseURL: "https://clawhub.ai"},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
		// The registry a slug install downloads from: an unverified skill (no
		// registry hash), like S-28b's. Never reaches the network.
		skillRegistry: &fakeSkillRegistry{},
	}
}

// TestInstall_TrustDenyRefusesUnverified (order 6, S-27/S-28).
// Traces to: BDD S-27, S-28; spec C-3 (trust table, deny row).
func TestInstall_TrustDenyRefusesUnverified(t *testing.T) {
	const wantText = "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."

	t.Run("S-28a REST upload install of an unverified skill is refused", func(t *testing.T) {
		api := trustDenyAPI(t)
		// An uploaded zip has no registry hash, so it is unverified (C-5).
		data := uat2BuildZip(t, []uat2ZipEntry{
			{name: "SKILL.md", mode: 0o644, body: "---\nname: notes-helper\ndescription: unverified helper\n---\nbody"},
		})
		w := uat2InstallZip(t, api, "notes-helper", data)
		require.Equal(t, http.StatusForbidden, w.Code,
			"trust deny must refuse the install (C-3); got %d body: %s", w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), wantText, "the refusal carries the C-3 text")
		// Nothing on disk afterwards (S-28).
		_, err := os.Stat(filepath.Join(api.homePath, "skills", "notes-helper"))
		require.True(t, errors.Is(err, os.ErrNotExist), "no folder for the refused skill may exist")
	})

	t.Run("S-28b install_skill of an unverified skill is refused", func(t *testing.T) {
		api := trustDenyAPI(t)
		globalSkills := filepath.Join(api.homePath, "skills")
		require.NoError(t, os.MkdirAll(globalSkills, 0o755))
		registryMgr := skills.NewRegistryManager()
		registryMgr.AddRegistry(&fakeSkillRegistry{}) // installs an unverified skill
		tool := tools.NewInstallSkillTool(registryMgr, globalSkills)
		result := tool.Execute(context.Background(), map[string]any{"slug": "notes-helper", "registry": "fake"})
		require.True(t, result.IsError, "trust deny must refuse the install_skill call (C-3); got %+v", result)
		require.Contains(t, result.ForLLM, wantText, "the refusal carries the C-3 text")
		_, err := os.Stat(filepath.Join(globalSkills, "notes-helper"))
		require.True(t, errors.Is(err, os.ErrNotExist), "no folder for the refused skill may exist")
	})

	t.Run("S-27 the install review refuses an unverified skill under trust deny", func(t *testing.T) {
		api := trustDenyAPI(t)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/skills/install/review",
			strings.NewReader(`{"slug":"notes-helper"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.installSkill(w, r)
		// The screen disables Install with the Deny reason (S-27); its
		// server half refuses the staging of an unverified skill under
		// Deny. The review route is C-13 (orders 35/53) — until it exists
		// this subtest cannot reach the handler, which is part of the same
		// red.
		require.Contains(t, w.Body.String(), "Deny", "the refusal names the Deny setting (S-27)")
	})
}
