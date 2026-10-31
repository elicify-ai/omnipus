//go:build (darwin || linux) && goolm && stdjson

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// S01 operator-route closure control, distinct from layout overrides in the
// component tests. This executes the host's real Cobra registration command.
func TestRegisterOfficeNeverExecutesCandidateCLI(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "unsupported", "writable", "escaping_executable"} {
		t.Run(mode, func(t *testing.T) {
			home, base := t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			configPath := filepath.Join(home, "config.json")
			t.Setenv("OMNIPUS_CONFIG", configPath)
			raw, err := json.Marshal(config.DefaultConfig())
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(configPath, raw, 0600))
			if mode == "writable" {
				base = filepath.Join(home, "agents", "writer", "work")
			}
			root := filepath.Join(base, "LibreOffice.app")
			exe, metadata := filepath.Join(root, "Contents", "MacOS", "soffice"), filepath.Join(root, "Contents", "Info.plist")
			valid := "<plist><dict><key>CFBundleShortVersionString</key><string>26.8.1.1</string></dict></plist>"
			if runtime.GOOS == "linux" {
				root = filepath.Join(base, "office")
				exe, metadata = filepath.Join(root, "program", "soffice.bin"), filepath.Join(root, "program", "versionrc")
				valid = "ProductVersion=26.8.1.1\n"
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0700))
			marker := filepath.Join(home, "candidate.marker")
			program := []byte("#!/bin/sh\nprintf 'CLI candidate executed' > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\n")
			require.NoError(t, os.WriteFile(exe, program, 0700))
			if runtime.GOOS == "linux" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "program", "soffice"), program, 0700))
			}
			want := "version unreadable"
			switch mode {
			case "malformed":
				require.NoError(t, os.WriteFile(metadata, []byte("invalid metadata"), 0600))
			case "unsupported":
				require.NoError(t, os.WriteFile(metadata, []byte(strings.ReplaceAll(valid, "26.8.1.1", "26.2.1.1")), 0600))
				want = "not supported"
			case "writable":
				dependency := filepath.Join(root, "dependency.txt")
				written := tools.NewWriteFileTool(base, true).Execute(tools.WithAgentID(context.Background(), "writer"), map[string]any{"path": dependency, "content": "CLI writer control"})
				require.False(t, written.IsError, "%s", written.ForLLM)
				data, readErr := os.ReadFile(dependency)
				require.NoError(t, readErr)
				require.Equal(t, "CLI writer control", string(data))
				want = "writable by an agent"
			case "escaping_executable":
				foreign := filepath.Join(t.TempDir(), "foreign")
				require.NoError(t, os.WriteFile(foreign, program, 0700))
				require.NoError(t, os.Remove(exe))
				require.NoError(t, os.Symlink(foreign, exe))
				want = "points outside"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, exec.CommandContext(ctx, exe, "--version").Run())
			data, err := os.ReadFile(marker)
			require.NoError(t, err)
			require.Equal(t, "CLI candidate executed", string(data))
			require.NoError(t, os.Remove(marker))
			rootCommand := NewRootCommand()
			var output bytes.Buffer
			rootCommand.SetOut(&output)
			rootCommand.SetErr(&output)
			rootCommand.SetArgs([]string{"office", "register", root})
			err = rootCommand.Execute()
			require.NoFileExists(t, marker, "the real operator command must never run the candidate")
			require.ErrorContains(t, err, want)
			require.NotContains(t, output.String(), "Registered")
			require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"))
		})
	}
}
