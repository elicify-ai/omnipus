//go:build darwin

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
)

// R1-F01 / §2 A5, D7: a God setup CHILD, not a later bash, must consume an
// earlier published runtime. Both installs, discovery, output and Seatbelt are
// real. The declared helper bytes define the output oracle before execution.
func TestWU23R2_GodSetupConsumesEarlierSharedRuntime_Seatbelt(t *testing.T) {
	if wu23R1Isolated(t) {
		return
	}
	f := wu23R1Fixture(t)
	wu23R1UnlockPublished(t, f.Home)
	ctx := wu23Context(f)
	const helperName = "wu23-r2-prior-helper"
	const program = `#!/bin/sh
printf 'PRIOR_HELPER=ready\n'
printf 'PRIOR_PATH=%s\n' "$0"
if [ "${1-}" = --probe-write ]; then
  if { printf 'corrupted-runtime\n' > "$0"; } 2>/dev/null; then
    printf 'PRIOR_WRITE=allowed\n'
  else
    printf 'PRIOR_WRITE=denied\n'
  fi
fi
`
	install := "set -eu\nmkdir -p \"$OMNIPUS_ENV_PREFIX/bin\"\ncat > \"$OMNIPUS_ENV_PREFIX/bin/" + helperName + "\" <<'PROGRAM'\n" + program + "PROGRAM\nchmod 700 \"$OMNIPUS_ENV_PREFIX/bin/" + helperName + "\"\nprintf 'FIRST_INSTALL_CONTROL=ok\\n'\n"
	first, firstID, completed := wu23R1Install(t, f, true, install, "shared")
	if completed.IsError {
		t.Fatalf("first real God install failed: %s", completed.ForLLM)
	}
	firstPoll, firstResult := pollAction(t, first, ctx, firstID)
	if firstResult.IsError || firstPoll.Status != "done" || firstPoll.ExitCode != 0 {
		t.Fatalf("first generation was not published successfully: %+v %s", firstPoll, firstResult.ForLLM)
	}
	if output := wu23R2SetupOutput(t, first, firstID, ctx); output != "FIRST_INSTALL_CONTROL=ok\n" {
		t.Fatalf("first installer output=%q want exact declared control", output)
	}
	runtime, err := environmentsetup.RuntimeEnvPaths(f.Home, f.Work)
	if err != nil || len(runtime.Shared) != 1 {
		t.Fatalf("first published generation not discovered: shared=%v err=%v", runtime.Shared, err)
	}
	priorRoot := runtime.Shared[0].Dir
	priorProgram := filepath.Join(priorRoot, "bin", helperName)
	if data, readErr := os.ReadFile(priorProgram); readErr != nil || string(data) != program {
		t.Fatalf("published helper bytes do not equal declared program: bytes=%q err=%v", data, readErr)
	}
	// Neutralize ordinary read-only mode bits: only the second setup child's
	// kernel floor can prevent the real program from rewriting its own $0.
	wu23R1OwnerWritable(t, priorProgram)
	before := wu23R2RuntimeBytes(t, priorRoot)
	// Shared publication requires a real nonempty artifact. Persist the
	// earlier helper's actual output, then expose the same bytes on stdout.
	consumer := "set -eu\nprintf 'SETUP_CONTROL=ok\\n'\n" + helperName + " --probe-write > \"$OMNIPUS_ENV_PREFIX/consumed.txt\"\ncat \"$OMNIPUS_ENV_PREFIX/consumed.txt\"\nprintf 'CONSUMER_DONE=ok\\n'\n"
	second, secondID, consumed := wu23R1Install(t, f, true, consumer, "shared")
	output := wu23R2SetupOutput(t, second, secondID, ctx)
	t.Run("same_setup_child_positive", func(t *testing.T) {
		if !strings.HasPrefix(output, "SETUP_CONTROL=ok\n") {
			t.Fatalf("consumer did not execute its allowed same-child control: %q", output)
		}
	})
	t.Run("prior_runtime_bytes_immutable", func(t *testing.T) {
		if after := wu23R2RuntimeBytes(t, priorRoot); !reflect.DeepEqual(after, before) {
			t.Fatal("§2 A5: another setup changed prior-runtime file names or bytes")
		}
	})
	secondPoll, secondResult := pollAction(t, second, ctx, secondID)
	if consumed.IsError || secondResult.IsError || secondPoll.Status != "done" || secondPoll.ExitCode != 0 {
		t.Errorf("R1-F01: God setup must execute prior shared helper through its pre-denial roots: completion=%s poll=%+v", consumed.ForLLM, secondPoll)
	}
	want := "SETUP_CONTROL=ok\nPRIOR_HELPER=ready\nPRIOR_PATH=" + priorProgram + "\nPRIOR_WRITE=denied\nCONSUMER_DONE=ok\n"
	if output != want {
		t.Errorf("R1-F01 / A5: actual setup output=%q want=%q from declared earlier helper", output, want)
	}
	if secondID == firstID {
		t.Error("workflow must use two different real environment_setup sessions")
	}
	published, err := environmentsetup.RuntimeEnvPaths(f.Home, f.Work)
	if err != nil || len(published.Shared) != 2 {
		t.Errorf("both real installs must publish distinct generations: shared=%v err=%v", published.Shared, err)
	} else {
		for _, generation := range published.Shared {
			if generation.Dir == priorRoot {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(generation.Dir, "consumed.txt"))
			wantArtifact := "PRIOR_HELPER=ready\nPRIOR_PATH=" + priorProgram + "\nPRIOR_WRITE=denied\n"
			if readErr != nil || string(data) != wantArtifact {
				t.Errorf("new generation must contain prior helper's exact output: bytes=%q want=%q err=%v", data, wantArtifact, readErr)
			}
		}
	}
}

func wu23R2SetupOutput(t *testing.T, tool *EnvironmentSetupTool, id string, ctx context.Context) string {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{"action": "read", "session_id": id})
	if result == nil || result.IsError {
		t.Fatalf("real setup output cannot be read: %+v", result)
	}
	var response ExecResponse
	if err := json.NewDecoder(strings.NewReader(result.ForLLM)).Decode(&response); err != nil {
		t.Fatalf("real setup read does not contain its response: %v", err)
	}
	if response.SessionID != id {
		t.Fatalf("setup read is not from the requested session: %+v", response)
	}
	if response.Status != "done" {
		// Keep the failure loud, but inspect output and prior bytes as well so
		// a missing-root mutant cannot bypass the same-child controls.
		t.Errorf("real setup read has unexpected terminal status: %+v", response)
	}
	// ES-FR-04: the real session appends its publication notice to captured
	// output. Separate that documented frame, not arbitrary command bytes;
	// callers still compare the entire command-output portion exactly.
	output, _, hasNotice := strings.Cut(response.Output, "\n\n[environment_setup] ")
	if !hasNotice {
		t.Fatalf("completed shared setup output lacks its publication frame: %q", response.Output)
	}
	return output
}

func wu23R2RuntimeBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files[rel] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot real prior-runtime bytes: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("prior-runtime byte snapshot instrument found no files")
	}
	return files
}
