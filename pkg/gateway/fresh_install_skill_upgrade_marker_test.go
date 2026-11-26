//go:build goolm && stdjson

package gateway

// Architect ruling on handoff-8, follow-up (Skills & Tools #1963, C-19): a
// fresh install has no old values to translate, so its config must carry the
// "upgrade done" marker (sandbox.migrations.skill_permissions = done) from
// the start. Without it, HealIfUpgraded skips the heal until the next boot
// (a project skill's key is never created at turn start, so the skill stays
// refused), and the second boot runs the whole one-time upgrade over a
// config already in the new shape.
//
// Oracles (skills-tools-redesign-spec.md): C-19 shelf table "created when
// Omnipus first reads W's project shelf (turn start or reconcile)", starting
// permission Ask (ST-Q3 B); line 154 "runs after the one-time migrations of
// C-19"; line 405, a person's value is kept.
//
// The first boot writes no config.json; the test saves the booted live
// config as the first settings write does. The turn step calls
// skillperm.HealIfUpgraded, the call the turn start
// makes (pkg/agent/project_skill_keys.go::storeNewProjectSkillKeys).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillperm"
	"github.com/elicify-ai/omnipus/pkg/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshInstallBoot runs the real boot step that holds the C-19 upgrade,
// the config load, the roster persist and the skill-map heal.
func freshInstallBoot(t *testing.T, home string) *config.Config {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	t.Cleanup(func() {
		if rc.liveLimits != nil {
			rc.liveLimits.Close()
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
		cancel()
	})
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err)
	require.False(t, stop)
	return rc.cfg
}

func freshInstallSandbox(t *testing.T, configPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err, "the first boot writes config.json")
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	sandbox, ok := document["sandbox"].(map[string]any)
	require.True(t, ok, "config.json has a sandbox section: %s", raw)
	return sandbox
}

func freshInstallMarker(sandbox map[string]any) any {
	migrations, _ := sandbox["migrations"].(map[string]any)
	return migrations[skillperm.MigrationName]
}

func TestFreshInstall_SkillUpgradeMarkerAcrossBoots(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, configPath)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	old := http.DefaultTransport
	http.DefaultTransport = bootDownloadTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("fresh-install instrument forbids network: " + r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	require.NoFileExists(t, configPath, "setup: a fresh install has no config.json")

	// First boot. It does not write config.json itself; the first settings
	// write saves the live config the boot loaded, so that is the config
	// written on first creation.
	first := freshInstallBoot(t, home)
	require.NoError(t, config.SaveConfig(configPath, first))
	assert.Equal(t, "done", freshInstallMarker(freshInstallSandbox(t, configPath)),
		"C-19: a fresh install has nothing to translate, so the config written on first creation carries the upgrade-done marker")

	// A turn on a project skill: workspace w1 mounts a folder holding notes.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: notes\ndescription: Project notes\n---\n\nNOTES\n"), 0o600))
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "w1", Name: "W1", Status: "active"}))
	_, err = workspace.EnsureWorkDir(home, "w1")
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, "w1", "project", root)
	require.NoError(t, err)
	const key = "project:w1/notes"
	policies, _, _, err := skillperm.HealIfUpgraded(configPath)
	require.NoError(t, err)
	require.Equal(t, "ask", policies[key],
		"C-19: the turn start stores the project key at Ask on a fresh install, not only after a second boot")

	// A person then denies the project skill.
	sandbox := freshInstallSandbox(t, configPath)
	stored, _ := sandbox["skill_policies"].(map[string]any)
	require.Equal(t, "ask", stored[key], "the key is durable in config.json")
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	onDisk, ok := document["sandbox"].(map[string]any)
	require.True(t, ok)
	onDiskPolicies, ok := onDisk["skill_policies"].(map[string]any)
	require.True(t, ok)
	onDiskPolicies[key] = "deny"
	raw, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, raw, 0o600))

	// Second boot: the one-time upgrade does not run again and the value stays.
	cfg := freshInstallBoot(t, home)
	entities := filepath.Join(home, "entities")
	for _, name := range []string{".skill-permissions-upgrade-snapshot.json", ".skill-permissions-upgrade-snapshot.done.json", ".skill-permissions-upgrade-ledger.json"} {
		assert.NoFileExists(t, filepath.Join(entities, name), "the second boot of a fresh install runs no C-19 upgrade")
	}
	sandbox = freshInstallSandbox(t, configPath)
	assert.Equal(t, "done", freshInstallMarker(sandbox))
	stored, _ = sandbox["skill_policies"].(map[string]any)
	assert.Equal(t, "deny", stored[key], "line 405: the person's value is kept")
	assert.Equal(t, "deny", cfg.Sandbox.SkillPolicies[key], "the loaded config holds the person's value")
}
