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
// configured trust here is the spec's stored value "deny" (C-3), written as
// the literal so that the test pins the spec's vocabulary and not the name of
// a Go constant.
//
// The spec's S-27 also disables the screen's Install button in the review;
// the review endpoint itself (POST /skills/install/review) is C-13 and is
// RED-tested by orders 35/53 — the subtest below pins only its trust-Deny
// refusal so the rule is not lost between the two.
//
// S-28b builds install_skill by hand over a fake registry and gives it the
// API's live trust setting with a direct SetSkillTrust call, the way the agent
// loop does for the tool it registers (pkg/agent/loop_wire.go). The call is
// direct on purpose: a tree whose tool has no such setter does not build. The
// tool the loop itself registers is run by
// skill_install_trust_loop_red_test.go. The refusal reaches the model as a
// JSON envelope, and encoding/json writes "&" in it as the six characters
// \u0026, so the C-3 sentence ("Skills & Tools") is read from the decoded
// reason and from the decoded error.message (installRefusalReason,
// installRefusalErrorMessage), not from the raw bytes.

import (
	"context"
	"encoding/json"
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
	// The spec's stored value for Deny (C-3), as a literal: the test pins the
	// spec's vocabulary, not the name of a Go constant.
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

// c3DenyInstallRefusal is the sentence C-3's deny row gives for the refusal of
// an install, word for word from the spec. It is the oracle of every install
// path under Deny (S-27, S-28, FR-029).
const c3DenyInstallRefusal = "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."

// installRefusalReason is the sentence an install_skill refusal carries. The
// result is a JSON envelope whose "reason" holds the refusal, and encoding/json
// writes "&" inside it as the six characters \u0026, so a sentence the spec
// words with an ampersand ("Skills & Tools", C-3) is read from the decoded
// reason, not from the raw bytes. A result that is not such an envelope is its
// own reason.
func installRefusalReason(text string) string {
	var envelope struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err == nil && envelope.Reason != "" {
		return envelope.Reason
	}
	return text
}

// installRefusalErrorMessage is the "error.message" of the same refusal
// envelope: the sentence reaches the model twice, as the reason and as the
// error's message. Like the reason it is read decoded, because the raw bytes
// carry "&" as the six characters \u0026. It is empty when the result is not
// such an envelope or carries no message.
func installRefusalErrorMessage(text string) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return ""
	}
	return envelope.Error.Message
}

// TestInstall_TrustDenyRefusesUnverified (order 6, S-27/S-28).
// Traces to: BDD S-27, S-28; spec C-3 (trust table, deny row).
func TestInstall_TrustDenyRefusesUnverified(t *testing.T) {
	const wantText = c3DenyInstallRefusal

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
		// The trust source the agent loop gives the tool it registers
		// (pkg/agent/loop_wire.go): the live setting, read at call time.
		tool.SetSkillTrust(func() config.SkillTrustLevel {
			return api.agentLoop.GetConfig().Sandbox.EffectiveSkillTrust()
		})
		result := tool.Execute(context.Background(), map[string]any{"slug": "notes-helper", "registry": "fake"})
		require.True(t, result.IsError, "trust deny must refuse the install_skill call (C-3); got %+v", result)
		require.Contains(t, installRefusalReason(result.ForLLM), wantText, "the refusal carries the C-3 text")
		require.Contains(t, installRefusalErrorMessage(result.ForLLM), wantText, "the refusal's error.message carries the C-3 text")
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
