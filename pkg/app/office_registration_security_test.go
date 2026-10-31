//go:build goolm && stdjson

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// This is the real on-disk unreadable-configuration case, distinct from the
// service callback returning nil. A candidate outside home is writable by an
// operator allowance; corrupt settings must not make that authority disappear.
func TestOfficeRegistration_UnreadableConfigurationRefuses(t *testing.T) {
	home, work, candidate := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	configPath := filepath.Join(home, "config.json")
	t.Setenv("OMNIPUS_CONFIG", configPath)
	pattern := "^" + regexp.QuoteMeta(candidate) + "(?:/|$)"
	cfg := config.DefaultConfig()
	cfg.Tools.AllowWritePaths = []string{pattern}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, raw, 0600))
	loaded, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, []string{pattern}, loaded.Tools.AllowWritePaths)
	target := filepath.Join(candidate, "resource.txt")
	writer := tools.NewWriteFileTool(work, true, []*regexp.Regexp{regexp.MustCompile(pattern)})
	result := writer.Execute(tools.WithAgentID(context.Background(), "writer"), map[string]any{"path": target, "content": "configured writer control"})
	require.False(t, result.IsError, "%s", result.ForLLM)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "configured writer control", string(data))
	require.NoError(t, os.WriteFile(configPath, []byte("{unreadable configuration"), 0600))
	_, err = config.LoadConfig(configPath)
	require.Error(t, err, "positive control: real config loader must reject the damaged source")
	root := NewRootCommand()
	cmd, _, findErr := root.Find([]string{"pdf-renderer", "register"})
	if findErr != nil || cmd.CommandPath() != "omnipus pdf-renderer register" {
		t.Fatal("BLOCKED: operator component registration not implemented — required by office-preview spec AD-4 / FR-042")
	}
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"pdf-renderer", "register", candidate})
	err = root.Execute()
	require.ErrorContains(t, err, "configuration could not be read", "must fail closed at the actual operator command, not merely fail later for a bad component")
	require.NotContains(t, output.String(), "Registered")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindPDFRenderer), "manifest.json"))
}
