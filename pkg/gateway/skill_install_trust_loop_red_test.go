// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Skills & Tools redesign (#1963, epic #1519 page F), Part S: the install_skill
// the agent loop registers, and a tool with no trust source.
//
// TestInstall_LoopRegisteredInstallSkillRefusesUnverifiedUnderDeny runs the
// install_skill that the agent loop itself registers (pkg/agent/loop_wire.go,
// registerHandoffAndSkills), under trust Deny, on an unverified skill. S-28b
// (skill_install_trust_red_test.go) builds its own tool and hands it the trust
// setting, so it cannot notice a loop that stops doing the same; this test can.
// The loop's tool is read from its agent's tool registry, and the unverified
// skill comes through the registry manager the loop builds from
// tools.skills.marketplaces: a ClawHub-type entry whose base URL is a local
// test server that offers a skill with no registry hash (C-5: unverified).
// Nothing in production code is changed or reached around.
//
// TestInstall_LoopRegisteredInstallSkillInstallsUnverifiedUnderAllow is that
// test's control, on the same fixture with trust Allow (C-3, allow row: the
// install proceeds). It shows that the skill the fixture offers really installs,
// so the refusal under Deny comes from the trust rule and from nothing else. It
// also keeps the wiring guarded once the backend's round 19 lands: a loop whose
// install_skill has no trust source then fails closed and refuses under Allow
// too, and this test fails on it, whatever sentence that refusal carries.
//
// TestInstall_ToolWithoutTrustSourceFailsClosed is the oracle for the
// team-lead ruling on R2 (Part S order, item 3): a tool built with no trust
// source must not install an unverified skill under any setting, because
// nothing has told it what the setting is. The spec's Deny sentence says
// "set to Deny", which is not true of a missing setting, so the test asks for
// a refusal and for nothing installed, and does not pin a sentence the spec
// does not give for this case. Its control keeps the oracle honest: C-3 applies
// to unverified skills only, so a verified skill must still install.
//
// Oracles: skills-tools-redesign-spec.md C-3 (the deny row, "applies to
// unverified skills only"), FR-029 ("every install path (screen, POST
// /skills/install, install_skill) MUST refuse an unverified skill with the C-3
// text and leave nothing installed"), BDD S-28, and the R2 ruling. No expected
// value here was read off a run of the code.

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
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/stretchr/testify/require"
)

// unverifiedClawHub is a local stand-in for a ClawHub-type marketplace that
// offers one skill with no registry hash: the metadata lookup answers 404, so
// the hash cannot be checked, and the download answers a zip that holds the
// skill's SKILL.md. The skill is therefore unverified (C-5).
func unverifiedClawHub(t *testing.T, slug string) *httptest.Server {
	t.Helper()
	archive := uat2BuildZip(t, []uat2ZipEntry{
		{
			name: "SKILL.md",
			mode: 0o644,
			body: "---\nname: " + slug + "\ndescription: Use this installed test skill when requested.\n---\n\nBody.\n",
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/download" && r.URL.Query().Get("slug") == slug {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// loopRegisteredInstallSkill builds an agent loop the way the other gateway
// tests do, with the given trust value and one enabled ClawHub-type
// marketplace at marketplaceURL, and returns the install_skill the loop
// registered for its agent, with the skills directory that tool installs into.
// The test owns OMNIPUS_HOME, so the directory it checks is the one the loop's
// tool writes to (ADR-046 FR-009: $OMNIPUS_HOME/skills).
func loopRegisteredInstallSkill(
	t *testing.T, trust config.SkillTrustLevel, marketplaceURL string,
) (*tools.InstallSkillTool, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	cfg.Sandbox.SkillTrust = trust
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, BaseURL: marketplaceURL},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "the loop must have registered agent mia")
	raw, ok := inst.Tools.Get("install_skill")
	require.True(t, ok, "the loop must have registered install_skill for agent mia")
	tool, ok := raw.(*tools.InstallSkillTool)
	require.True(t, ok, "the loop's install_skill must be an *tools.InstallSkillTool; got %T", raw)
	return tool, filepath.Join(home, "skills")
}

// skillFolders lists the skill folders directly under dir. A dot-prefixed
// entry is never a skill (the skills loader skips it, and the install's
// staging area is one), so it is left out. A missing directory is an empty list.
func skillFolders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}
	}
	require.NoError(t, err)
	names := []string{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// TestInstall_LoopRegisteredInstallSkillRefusesUnverifiedUnderDeny (Part S, R1).
// Traces to: BDD S-28; spec C-3 (deny row), FR-029.
func TestInstall_LoopRegisteredInstallSkillRefusesUnverifiedUnderDeny(t *testing.T) {
	const slug = "notes-helper"
	srv := unverifiedClawHub(t, slug)
	// The spec's stored value for Deny (C-3), as a literal: the test pins the
	// spec's vocabulary, not the name of a Go constant.
	tool, skillsDir := loopRegisteredInstallSkill(t, config.SkillTrustLevel("deny"), srv.URL)
	before := skillFolders(t, skillsDir)

	result := tool.Execute(context.Background(), map[string]any{"slug": slug, "registry": "clawhub"})

	require.True(t, result.IsError,
		"the install_skill the loop registers must refuse an unverified skill under trust deny (C-3, FR-029); got %+v", result)
	require.Contains(t, installRefusalReason(result.ForLLM), c3DenyInstallRefusal, "the refusal's reason carries the C-3 text")
	require.Contains(t, installRefusalErrorMessage(result.ForLLM), c3DenyInstallRefusal,
		"the refusal's error.message carries the C-3 text")
	_, err := os.Stat(filepath.Join(skillsDir, slug))
	require.True(t, errors.Is(err, os.ErrNotExist), "no folder for the refused skill may exist")
	require.Equal(t, before, skillFolders(t, skillsDir), "no skill folder of any name may appear")
}

// TestInstall_LoopRegisteredInstallSkillInstallsUnverifiedUnderAllow (Part S,
// control of the test above). Traces to: spec C-3 (allow row: "proceeds").
func TestInstall_LoopRegisteredInstallSkillInstallsUnverifiedUnderAllow(t *testing.T) {
	const slug = "notes-helper"
	srv := unverifiedClawHub(t, slug)
	tool, skillsDir := loopRegisteredInstallSkill(t, config.SkillTrustLevel("allow"), srv.URL)

	result := tool.Execute(context.Background(), map[string]any{"slug": slug, "registry": "clawhub"})

	require.False(t, result.IsError,
		"the install_skill the loop registers must install an unverified skill under trust allow (C-3); got %s", result.ForLLM)
	_, err := os.Stat(filepath.Join(skillsDir, slug, "SKILL.md"))
	require.NoError(t, err, "the unverified skill is installed under trust allow")
}

// TestInstall_ToolWithoutTrustSourceFailsClosed (Part S, R2 oracle).
// Traces to: the team-lead ruling on R2; spec C-3 ("applies to unverified
// skills only"), FR-029.
func TestInstall_ToolWithoutTrustSourceFailsClosed(t *testing.T) {
	args := map[string]any{"slug": "notes-helper", "registry": "fake"}
	// newTool builds install_skill over one registry and never calls
	// SetSkillTrust, so the tool has no answer to what the trust setting is.
	newTool := func(t *testing.T, registry skills.SkillRegistry) (*tools.InstallSkillTool, string) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "skills")
		registryMgr := skills.NewRegistryManager()
		registryMgr.AddRegistry(registry)
		return tools.NewInstallSkillTool(registryMgr, dir), dir
	}

	t.Run("an unverified skill is refused and nothing is installed", func(t *testing.T) {
		tool, dir := newTool(t, &fakeSkillRegistry{}) // installs an unverified skill
		result := tool.Execute(context.Background(), args)
		require.True(t, result.IsError,
			"a tool with no trust source must refuse an unverified skill (R2 ruling: fail closed); got %+v", result)
		require.Equal(t, []string{}, skillFolders(t, dir), "no skill folder may exist after the refusal")
	})

	t.Run("control: a verified skill still installs", func(t *testing.T) {
		tool, dir := newTool(t, &fakeSkillRegistry{installRes: &skills.InstallResult{Verified: true}})
		result := tool.Execute(context.Background(), args)
		require.False(t, result.IsError, "trust applies to unverified skills only (C-3); got %s", result.ForLLM)
		_, err := os.Stat(filepath.Join(dir, "notes-helper", "SKILL.md"))
		require.NoError(t, err, "the verified skill is installed")
	})
}
