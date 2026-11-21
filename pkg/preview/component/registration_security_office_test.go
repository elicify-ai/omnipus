//go:build darwin || linux

package component_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W2-S01 / AD-4: rejection must precede ANY candidate execution. GOOS here
// selects real registration layout branches; executing their harmless scripts
// on this host is not Linux/Windows platform certification.
func TestOfficeRegistration_WritableCandidateRefusedBeforeVersionProbe(t *testing.T) {
	for _, layout := range []string{"linux", "windows"} {
		t.Run(layout, func(t *testing.T) {
			for _, metadata := range []string{"missing", "malformed"} {
				t.Run(metadata, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv("OMNIPUS_HOME", home)
					work := filepath.Join(home, "agents", "writer", "work")
					root := filepath.Join(work, "fake-office")
					program := filepath.Join(root, "program")
					require.NoError(t, os.MkdirAll(program, 0700))
					exeName, versionName := "soffice", "versionrc"
					if layout == "windows" {
						exeName, versionName = "soffice.exe", "version.ini"
					}
					exe := filepath.Join(program, exeName)
					marker := exe + ".marker"
					require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nprintf 'version probe executed' > \"$0.marker\"\nprintf 'LibreOffice 26.8.1.1\\n'\n"), 0700))
					require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("harmless fixture"), 0600))
					if metadata == "malformed" {
						require.NoError(t, os.WriteFile(filepath.Join(program, versionName), []byte("ProductVersion=not-a-version\n"), 0600))
					}
					ctx := tools.WithAgentID(context.Background(), "writer")
					dependency := filepath.Join(root, "resource.txt")
					written := tools.NewWriteFileTool(work, true).Execute(ctx, map[string]any{"path": dependency, "content": "agent wrote resource"})
					require.False(t, written.IsError, "real agent workspace authority must be writable: %s", written.ForLLM)
					data, err := os.ReadFile(dependency)
					require.NoError(t, err)
					require.Equal(t, "agent wrote resource", string(data))
					probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					output, err := exec.CommandContext(probeCtx, exe, "--headless", "--version").Output()
					require.NoError(t, err, "positive control: fixture must actually execute")
					require.Equal(t, "LibreOffice 26.8.1.1\n", string(output))
					data, err = os.ReadFile(marker)
					require.NoError(t, err)
					require.Equal(t, "version probe executed", string(data))
					require.NoError(t, os.Remove(marker))
					manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: root, Operator: "test-operator", GOOS: layout, WriteRoots: officepreview.WriterRoots(home, nil)})
					assert.Nil(t, manifest)
					assert.ErrorContains(t, err, "writable by an agent")
					assert.NoFileExists(t, marker, "W2-S01: an unsafe candidate must not execute even when version metadata is missing or malformed")
					assert.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"))
				})
			}
		})
	}
}
