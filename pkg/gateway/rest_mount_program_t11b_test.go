package gateway

// Founder Q12 A / S2; T11 §5: a real mount's host target is an agent write
// grant even though it is outside Omnipus home and sandbox.allowed_paths.
// Real signed-in Settings route, workspace mount store, filesystem and disk.

import (
	"net/http"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsThirdPartyAgents_MountedHostProgramRefused_S2(t *testing.T) {
	api, f, home := t11bSettingsAPI(t)
	allowed := t11bWriteExe(t, "codex-before-mount")
	put := func(path string) int {
		body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: path}, gen.CliPreset{})
		w := t11bPut(t, f, body)
		return w.Code
	}
	require.Equal(t, http.StatusOK, put(allowed), "instrument: this real person can save a suitable program")
	require.Empty(t, api.agentLoop.GetConfig().Sandbox.AllowedPaths, "mount roots must not be smuggled in through allowed_paths")
	program := t11bWriteExe(t, "codex-in-host-mount")
	target, err := filepath.EvalSymlinks(filepath.Dir(program))
	require.NoError(t, err)
	ws := seedLibraryWorkspace(t, api, "T11 S2 mount write grant")
	mount, _, err := workspace.CreateMount(home, ws, "cli-host", target)
	require.NoError(t, err, "instrument: create the actual persisted mount, not a fake config path")
	require.Equal(t, target, mount.HostPath)
	require.Contains(t, workspace.AllowedMountRoots(home, ws), target, "instrument: agents really receive this host write grant")
	at, err := filepath.EvalSymlinks(program)
	require.NoError(t, err)
	body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program}, gen.CliPreset{})
	f.expectRejected(t, body, "codex", "path", "cli_path_invalid", "agent_writable_location", at,
		"The program path for Codex was not saved: it is inside a folder agents can write to.")
	assert.Equal(t, allowed, api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path,
		"S2: a refused mount-host program must not replace the prior live setting")
}
