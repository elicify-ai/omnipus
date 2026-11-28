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
			// §3.9 fixture move (sanctioned): on Linux the register command
			// runs the own-folder check first (the command sets its program
			// path from the running program), so the candidate lives inside
			// that own folder. The data home moves under it too, so the
			// command's real write-root enumeration (which follows the
			// data home) still covers the writable mode's base — that
			// refusal's subject is untouched, and its allowance pattern
			// still matches the candidate wherever it sits. macOS has no
			// own-folder check and keeps its staging.
			linuxOwn := ""
			if runtime.GOOS == "linux" {
				if self, selfErr := os.Executable(); selfErr == nil {
					if resolved, resolveErr := filepath.EvalSymlinks(self); resolveErr == nil {
						linuxOwn = filepath.Clean(filepath.Join(filepath.Dir(resolved), "..", "share", "omnipus"))
						if mkErr := os.MkdirAll(linuxOwn, 0o755); mkErr == nil {
							home = filepath.Join(linuxOwn, "data-home")
							require.NoError(t, os.MkdirAll(home, 0o700))
							t.Setenv("OMNIPUS_HOME", home)
							base = filepath.Join(home, "agents", "writer", "work")
						}
					}
				}
			}
			// The staged folders persist across modes (they live under the
			// package's own folder, not a per-mode temp dir): clear the
			// previous mode's candidate so no leftover metadata leaks into
			// this one (the writable mode writes no version files and must
			// not read the previous mode's).
			root := filepath.Join(base, "LibreOffice.app")
			if runtime.GOOS == "linux" {
				if mode == "writable" {
					root = filepath.Join(base, "office")
				} else if linuxOwn != "" {
					// §3.9 fixture move: the other modes refuse for their
					// own reason (metadata, executable escape) — their
					// candidate must not sit in an agent-writable folder, or
					// the writer exclusion refuses for the wrong reason.
					// Inside the own folder, outside the data home.
					root = filepath.Join(linuxOwn, mode+"-office")
				}
			}
			require.NoError(t, os.RemoveAll(root))
			configPath := filepath.Join(home, "config.json")
			t.Setenv("OMNIPUS_CONFIG", configPath)
			raw, err := json.Marshal(config.DefaultConfig())
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(configPath, raw, 0600))
			if mode == "writable" && runtime.GOOS != "linux" {
				base = filepath.Join(home, "agents", "writer", "work")
			}
			root = filepath.Join(base, "LibreOffice.app")
			if runtime.GOOS == "linux" && mode != "writable" && linuxOwn != "" {
				root = filepath.Join(linuxOwn, mode+"-office")
			}
			exe, metadata := filepath.Join(root, "Contents", "MacOS", "soffice"), filepath.Join(root, "Contents", "Info.plist")
			valid := "<plist><dict><key>CFBundleShortVersionString</key><string>26.8.1.1</string></dict></plist>"
			if runtime.GOOS == "linux" {
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
