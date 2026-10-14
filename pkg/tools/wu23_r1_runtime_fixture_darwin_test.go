//go:build darwin

package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// Global backend/base state is installed only in the private re-exec. No
// filesystem/kernel function is mocked and no unsupported kernel is skipped.
func wu23R1Isolated(t *testing.T) bool {
	t.Helper()
	if slices.Contains(os.Args, "--wu23-r1-inner") {
		return false
	}
	name := strings.Split(t.Name(), "/")[0]
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v", "--", "--wu23-r1-inner")
	cmd.Env = sandbox.ScrubGatewayEnv()
	out, err := cmd.CombinedOutput()
	t.Logf("isolated real-child receipt:\n%s", out)
	if err != nil {
		t.Fatalf("WU23 R1 real production workflow failed: %v", err)
	}
	return true
}

func wu23R1Fixture(t *testing.T) fixture.Layout {
	t.Helper()
	f := fixture.New(t)
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Fatal("BLOCKED: real Seatbelt unavailable — WU23 round 1 requires a non-skip receipt")
	}
	if err := backend.Apply(sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, f.Home, nil, nil, nil, nil)); err != nil {
		t.Fatal(err)
	}
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: f.Home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	return f
}

func wu23R1UnlockPublished(t *testing.T, home string) {
	t.Helper()
	t.Cleanup(func() {
		root := filepath.Join(home, "toolchains")
		if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
			return
		}
		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, entryErr error) error {
			if entryErr != nil {
				return entryErr
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return nil
		})
		if walkErr != nil {
			t.Errorf("restore published fixture directory permissions: %v", walkErr)
		}
	})
}

func wu23R1OwnerWritable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("ordinary permissions must allow write-open before the kernel denial probe: %v", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func wu23R1PublishedRuntime(t *testing.T, f fixture.Layout) (environmentsetup.RuntimeEnv, string) {
	t.Helper()
	wu23R1UnlockPublished(t, f.Home)
	target, err := environmentsetup.BeginInstall(f.Home, f.Work, environmentsetup.ScopeShared)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(target.Prefix(), "bin", "wu23-r1-shared")
	fixture.Write(t, bin, "#!/bin/sh\nprintf 'SHARED_EXEC=ok\\n'\n")
	if chmodErr := os.Chmod(bin, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if _, commitErr := target.Commit(); commitErr != nil {
		if abortErr := target.Abort(); abortErr != nil {
			t.Errorf("abort failed publication fixture: %v", abortErr)
		}
		t.Fatal(commitErr)
	}
	rt, err := runtimeEnvTurn(f.Work)
	if err != nil || len(rt.Shared) != 1 {
		t.Fatalf("real published/discovered generation: shared=%v err=%v", rt.Shared, err)
	}
	wu23R1OwnerWritable(t, bin)
	return rt, bin
}

func wu23R1DocumentLayout(t *testing.T, f fixture.Layout) documentruntime.Layout {
	t.Helper()
	layout, err := documentruntime.ResolveLayout(f.Home, documentruntime.ManifestRevision, "self")
	if err != nil {
		t.Fatal(err)
	}
	if _, provisionErr := documentruntime.ProvisionFirstParty(layout); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	return layout
}

func wu23R1Exec(t *testing.T, f fixture.Layout, god bool) *ExecTool {
	t.Helper()
	tool, err := NewExecToolWithDeps(f.AgentHome, true, nil, ExecToolDeps{GodMode: god})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func wu23R1Bash(t *testing.T, tool *ExecTool, ctx context.Context, command string) *ToolResult {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{"command": command, "timeout_seconds": float64(20)})
	if result == nil {
		t.Fatal("bash returned no result")
	}
	return result
}

func wu23R1Install(t *testing.T, f fixture.Layout, god bool, command, scope string) (*EnvironmentSetupTool, string, *ToolResult) {
	t.Helper()
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: f.Home, AgentWorkDir: f.AgentHome, GodMode: god, Store: storageSeamStore{}})
	args := map[string]any{"command": command, "purpose": "WU23 real install-and-run acceptance", "timeout_seconds": float64(20)}
	if scope != "" {
		args["scope"] = scope
	}
	done := make(chan *ToolResult, 1)
	start := tool.ExecuteAsync(wu23Context(f), args, func(_ context.Context, result *ToolResult) { done <- result })
	if start == nil || start.IsError {
		t.Fatalf("real environment_setup start refused: %+v", start)
	}
	id := extractSessionID(t, start.ForLLM)
	var completed *ToolResult
	select {
	case completed = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("real setup completion not observed; not a runtime denial receipt")
	}
	if completed == nil {
		t.Fatal("setup completed without a result")
	}
	return tool, id, completed
}

func wu23R1PolicyChild(t *testing.T, policy *sandbox.SandboxPolicy, script string, args ...string) string {
	t.Helper()
	if policy == nil {
		t.Fatal("production policy seam returned nil despite registered enforcing base")
	}
	argv := append([]string{"-c", script, "wu23-r1"}, args...)
	cmd := exec.Command("/bin/sh", argv...)
	cmd.Env = sandbox.ScrubGatewayEnv()
	if err := sandbox.NewSeatbeltBackend().ApplyToCmd(cmd, *policy); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("production-policy child failed before complete receipt: %v output=%s", err, out)
	}
	return string(out)
}

func wu23R1DocumentArtifact(t *testing.T, path string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	part, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	// Inspector contract: one body paragraph + one table-cell paragraph,
	// one table, no revisions/fields/relationships/media. No visual QA claim.
	xml := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>First</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Second</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`
	if _, writeErr := part.Write([]byte(xml)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := archive.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if writeErr := os.WriteFile(path, data.Bytes(), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	return append([]byte(nil), data.Bytes()...)
}

func wu23R1DocumentJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("real bundled inspector returned invalid JSON: %v output=%q", err, raw)
	}
	return result
}
