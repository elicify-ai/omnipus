//go:build goolm && stdjson

package gateway

// Team-lead round after handoff-9, item 1 (Skills & Tools #1963): on a fresh
// install the first boot must leave a config.json in place, so that the
// first turn on a project skill creates its key and the boot cleanup does
// not fail on a missing file at every start.
//
// Oracles (skills-tools-redesign-spec.md): FR-080 "agent turns MUST create
// missing project-skill keys at first sight"; C-19 shelf table, the key is
// "created when Omnipus first reads W's project shelf (turn start or
// reconcile)" and starts at Ask (ST-Q3 B). An Ask key is a usable skill (it
// asks); a missing key fails closed to deny.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const freshBootHealError = "skill permission self-heal did not finish"

// freshFirstBootWithLoop runs the real boot steps up to the agent loop on
// home (the logging and data-model preamble, which writes the first-run
// config.json, then the config load and the agent loop), with a mock model,
// and returns the run context. The boot's slog output is captured in logs.
func freshFirstBootWithLoop(t *testing.T, home string, logs *bytes.Buffer) *runContextWithOptions {
	t.Helper()
	require.NoError(t, bootLoggingAndDataModel(home))
	t.Cleanup(logger.DisableFileLogging)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	t.Cleanup(func() {
		if rc.agentLoop != nil {
			rc.agentLoop.Close()
		}
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
	rc.cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)
	return rc
}

func TestFreshInstall_FirstBootTurnCreatesProjectSkillKey(t *testing.T) {
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
	var logs bytes.Buffer
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	require.NoFileExists(t, configPath, "setup: a fresh install has no config.json")

	// First boot, nothing saved by anyone else.
	rc := freshFirstBootWithLoop(t, home, &logs)
	require.FileExists(t, configPath, "the first boot leaves a config.json in place")
	assert.NotContains(t, logs.String(), freshBootHealError, "the boot cleanup does not fail on a fresh install")
	require.NotEmpty(t, rc.cfg.Agents.List, "setup: the first boot seeds agents")
	agentID := rc.cfg.Agents.List[0].ID

	// A workspace whose mount holds the project skill notes.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude", "skills", "notes")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: notes\ndescription: Project notes\n---\n\nNOTES\n"), 0o600))
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "w1", Name: "W1", Status: "active", CoreTeam: []string{agentID}}))
	_, err = workspace.EnsureWorkDir(home, "w1")
	require.NoError(t, err)
	_, _, err = workspace.CreateMount(home, "w1", "project", root)
	require.NoError(t, err)

	// A turn in w1.
	const key = "project:w1/notes"
	meta, err := rc.agentLoop.GetSessionStore().NewScheduledSession(agentID)
	require.NoError(t, err)
	w1 := "w1"
	require.NoError(t, rc.agentLoop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &w1}))
	turnCtx, turnCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer turnCancel()
	_, err = rc.agentLoop.ProcessScheduled(turnCtx, agentID, meta.ID, "hello", "scheduled", meta.ID)
	require.NoError(t, err)

	v, ok := storedGlobalSkillPolicy(t, configPath, key)
	assert.True(t, ok, "FR-080: the first turn on the project skill stores its key")
	assert.Equal(t, "ask", v, "C-19: the new key starts at Ask, so the skill is usable (it asks)")
	assert.Equal(t, "ask", rc.agentLoop.GetConfig().Sandbox.SkillPolicies[key], "the running config holds the key")

	// Second boot: still no cleanup error.
	logs.Reset()
	freshFirstBootWithLoop(t, home, &logs)
	assert.NotContains(t, logs.String(), freshBootHealError, "no repeating cleanup error at the next start")
}
