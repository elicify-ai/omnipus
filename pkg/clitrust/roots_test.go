// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package clitrust_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// TestAgentWritableRoots_IncompleteMountRefuses is S6. A workspace whose mount
// record cannot be trusted is not a shorter allow-list: an unreadable record,
// malformed JSON, or a record that dropped an entry must all be errors.
// A valid record still lists that mount. Spec §3.2.
func TestAgentWritableRoots_IncompleteMountRefuses(t *testing.T) {
	_, roots := loadT11CLITrust(t)

	t.Run("valid mount is listed", func(t *testing.T) {
		home, id := t11MountWorkspace(t)
		host := t.TempDir()
		mount, _, err := workspace.CreateMount(home, id, "extra", host)
		require.NoError(t, err)
		got, err := roots(home, &config.Config{})
		require.NoError(t, err)
		require.Truef(t, t11ListedRoot(got, mount.HostPath),
			"spec §3.2: a valid mount root must be forbidden, got %v", got)
	})

	t.Run("unreadable mount record", func(t *testing.T) {
		home, id := t11MountWorkspace(t)
		path := t11MountRecord(t, home, id, []byte(`{"workspace_id":"`+id+`","mounts":[]}`))
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0o700))
		t11RequireMountListUnusable(t, roots, home)
	})

	t.Run("malformed mount record", func(t *testing.T) {
		home, id := t11MountWorkspace(t)
		t11MountRecord(t, home, id, []byte(`{"mounts":[`))
		t11RequireMountListUnusable(t, roots, home)
	})

	t.Run("dropped mount entry", func(t *testing.T) {
		home, id := t11MountWorkspace(t)
		host := t.TempDir()
		body, err := json.Marshal(map[string]any{
			"workspace_id": id,
			"mounts": []map[string]string{
				{"name": "ok", "host_path": host},
				{"name": "ok", "host_path": "/etc"},
			},
		})
		require.NoError(t, err)
		t11MountRecord(t, home, id, body)
		t11RequireMountListUnusable(t, roots, home)
	})
}

func t11MountWorkspace(t *testing.T) (home, id string) {
	t.Helper()
	home = t.TempDir()
	id = "t11mrec"
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: "T11"}))
	return home, id
}

func t11MountRecord(t *testing.T, home, id string, body []byte) string {
	t.Helper()
	path, err := workspace.MountStorePath(home, id)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

func t11RequireMountListUnusable(t *testing.T, roots func(string, *config.Config) ([]string, error), home string) {
	t.Helper()
	got, err := roots(home, &config.Config{})
	require.Error(t, err, "spec §3.2: an incomplete mount record fails closed")
	require.Empty(t, got, "a failed mount list must not be usable as an allow-list")
	require.Contains(t, strings.ToLower(err.Error()), "mount")
}

func t11ListedRoot(list []string, want string) bool {
	want = filepath.Clean(want)
	for _, got := range list {
		if filepath.Clean(got) == want {
			return true
		}
	}
	return false
}
