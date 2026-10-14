//go:build windows

package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// D8 T-W: native Windows only, app-layer protection. No claim of a kernel floor.
func TestWU23_WindowsBestEffortInternalProtection(t *testing.T) {
	f := fixture.New(t)
	ctx := wu23Context(f)
	for _, god := range []bool{false, true} {
		tool, err := NewExecToolWithDeps(f.AgentHome, false, nil, ExecToolDeps{GodMode: god})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(f.Home, "SESSIONS", "private.txt")
		if msg := tool.guardCommand(ctx, "type \""+path+"\"", f.Work); msg == "" {
			t.Errorf("T-W: case-folded protected operand accepted in God=%t restrict=false", god)
		}
		res := NewReadFileTool(f.AgentHome, false, MaxReadFileSize).Execute(ctx, map[string]any{"path": path})
		if !res.IsError {
			t.Errorf("T-W: case-folded internal file readable (God=%t)", god)
		}
		t.Run("labelled_known_gap_dynamic_path", func(t *testing.T) {
			// D6 explicitly accepts that dynamically assembled paths cannot be
			// classified by a text check on Windows. Never describe this as protection.
			cmd := `powershell -NoProfile -Command "$p=Join-Path $env:OMNIPUS_HOME ('ses'+'sions'); Get-Content (Join-Path $p 'private.txt')"`
			if msg := tool.guardCommand(ctx, cmd, f.Work); msg != "" {
				t.Fatalf("documented dynamic-path residual changed: %s; update security docs and this contract together", msg)
			}
		})
	}
	t.Run("workspace_positive", func(t *testing.T) {
		res := NewReadFileTool(f.AgentHome, false, MaxReadFileSize).Execute(ctx, map[string]any{"path": filepath.Join(f.Work, "allowed.txt")})
		if res.IsError || !strings.Contains(res.ForLLM, "WU23_ALLOWED_CONTROL") {
			t.Fatalf("Windows workspace read control failed: %s", res.ForLLM)
		}
	})
}

func TestWU23_WindowsJunctionStaysProtected(t *testing.T) {
	f := fixture.New(t)
	junction := filepath.Join(f.Work, "junction")
	cmd := exec.Command("cmd", "/C", "mklink", "/J", junction, filepath.Join(f.Home, "sessions"))
	cmd.Env = append([]string{}, os.Environ()...) // test-only fixture, not a gateway spawn.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("junction instrument unavailable (not a skip): %v output=%s", err, out)
	}
	res := NewReadFileTool(f.AgentHome, false, MaxReadFileSize).Execute(wu23Context(f), map[string]any{"path": filepath.Join(junction, "private.txt")})
	if !res.IsError {
		t.Fatal("T-W: junction reopened protected session data")
	}
}
