//go:build darwin || linux

package component_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func registrationMarkerProgram(t *testing.T, path, marker string) {
	t.Helper()
	quoted := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
	program := "#!/bin/sh\nprintf 'candidate executed' > " + quoted + "\nprintf 'LibreOffice 26.8.1.1\\n'\n"
	require.NoError(t, os.WriteFile(path, []byte(program), 0700))
}

func registrationMarkerControl(t *testing.T, executable, marker string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").Output()
	require.NoError(t, err, "positive control must really execute the candidate")
	require.Equal(t, "LibreOffice 26.8.1.1\n", string(output))
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "candidate executed", string(data))
	require.NoError(t, os.Remove(marker))
}

// S01 closure control: the original execution route is already removed on
// 264bd7bc7. These new leaves may pass on arrival; no false RED is claimed.
// GOOS chooses layout branches on this Unix host, not native Windows proof.
func TestRegisterOfficeNeverExecutesCandidate(t *testing.T) {
	for _, layout := range []string{"linux", "windows"} {
		t.Run(layout, func(t *testing.T) {
			modes := []string{"missing", "malformed", "unsupported", "registry_fallback", "writable", "escaping_executable"}
			if layout == "windows" {
				modes = append(modes, "escaping_front")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					home, root := t.TempDir(), t.TempDir()
					t.Setenv("OMNIPUS_HOME", home)
					work := filepath.Join(home, "agents", "writer", "work")
					if mode == "writable" {
						root = filepath.Join(work, "office")
					}
					program := filepath.Join(root, "program")
					require.NoError(t, os.MkdirAll(program, 0700))
					frontName, versionName := "soffice", "versionrc"
					if layout == "windows" {
						frontName, versionName = "soffice.exe", "version.ini"
					}
					marker := filepath.Join(home, "candidate.marker")
					front, bin := filepath.Join(program, frontName), filepath.Join(program, "soffice.bin")
					registrationMarkerProgram(t, front, marker)
					registrationMarkerProgram(t, bin, marker)
					wantError := "version unreadable"
					switch mode {
					case "malformed":
						require.NoError(t, os.WriteFile(filepath.Join(program, versionName), []byte("ProductVersion=invalid\n"), 0600))
					case "unsupported":
						require.NoError(t, os.WriteFile(filepath.Join(program, versionName), []byte("ProductVersion=26.2.1.1\n"), 0600))
						wantError = "not supported"
					case "registry_fallback":
						require.NoError(t, os.WriteFile(filepath.Join(program, versionName), []byte("[Bootstrap]\nbuildid=fixture\n"), 0600))
						dir := filepath.Join(root, "share", "registry")
						require.NoError(t, os.MkdirAll(dir, 0700))
						xml := `<oor:data xmlns:oor="http://openoffice.org/2001/registry"><oor:component-data oor:name="Setup" oor:package="org.openoffice"><node oor:name="Product"><prop oor:name="ooSetupVersionAboutBox"><value>26.8.1.1</value></prop></node></oor:component-data></oor:data>`
						require.NoError(t, os.WriteFile(filepath.Join(dir, "main.xcd"), []byte(xml), 0600))
						wantError = ""
					case "writable":
						written := tools.NewWriteFileTool(work, true).Execute(tools.WithAgentID(context.Background(), "writer"), map[string]any{"path": filepath.Join(root, "dependency"), "content": "authority control"})
						require.False(t, written.IsError, "%s", written.ForLLM)
						data, err := os.ReadFile(filepath.Join(root, "dependency"))
						require.NoError(t, err)
						require.Equal(t, "authority control", string(data))
						wantError = "writable by an agent"
					case "escaping_executable", "escaping_front":
						foreign := filepath.Join(t.TempDir(), "foreign-executable")
						registrationMarkerProgram(t, foreign, marker)
						link := bin
						if mode == "escaping_front" {
							link = front
						}
						require.NoError(t, os.Remove(link))
						require.NoError(t, os.Symlink(foreign, link))
						wantError = "points outside"
					}
					registrationMarkerControl(t, front, marker)
					registrationMarkerControl(t, bin, marker)
					manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: root, Operator: "test-operator", GOOS: layout, WriteRoots: officepreview.WriterRoots(home, &config.Config{})})
					require.NoFileExists(t, marker, "S01: registration must NEVER execute the candidate")
					if wantError == "" {
						require.NoError(t, err)
						require.Equal(t, "26.8.1.1", manifest.Version)
						require.Equal(t, component.TrustOperatorRegistered, manifest.Trust)
					} else {
						require.ErrorContains(t, err, wantError)
						require.Nil(t, manifest)
						require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"))
					}
				})
			}
		})
	}
}
