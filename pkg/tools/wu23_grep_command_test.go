package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// D6: grep must hide both contents and filename hits, not merely refuse opens.
func TestWU23_GrepNoInternalContentOrNameLeaks(t *testing.T) {
	f := fixture.New(t)
	seedGrepWorkspace(t, f.Home, "alpha", "self")
	ctx := wu23Context(f)
	grep := NewGrepTool(f.AgentHome, false)
	t.Run("workspace_positive", func(t *testing.T) {
		res := grep.Execute(ctx, map[string]any{"pattern": "WU23_ALLOWED_CONTROL", "path": f.Work})
		if res.IsError || !strings.Contains(res.ForLLM, "allowed.txt:1:") || !strings.Contains(res.ForLLM, "WU23_ALLOWED_CONTROL") {
			t.Fatalf("A1: grep failed to return actual workspace hit: %s", res.ForLLM)
		}
	})
	for _, pattern := range []string{"WU23_PROTECTED_SENTINEL", "private.txt", "audit.jsonl", "last-session.md", "SOUL.md"} {
		t.Run(pattern, func(t *testing.T) {
			res := grep.Execute(ctx, map[string]any{"pattern": pattern, "path": f.Home})
			if strings.Contains(res.ForLLM, "WU23_PROTECTED_SENTINEL") {
				t.Errorf("D6: home-root grep leaked protected content: %s", res.ForLLM)
			}
			for _, rel := range fixture.ProtectedRelative() {
				// A refusal can name its requested home; it must never enumerate
				// a protected child name or return a hidden filename hit.
				if strings.Contains(res.ForLLM, rel) || strings.Contains(res.ForLLM, filepath.FromSlash(rel)) {
					t.Errorf("D6: home-root grep leaked protected child name %s", rel)
				}
			}
		})
	}
}

func TestWU23_CommandGuardUnconditionalInternalProtection(t *testing.T) {
	f := fixture.New(t)
	ctx := wu23Context(f)
	for _, god := range []bool{false, true} {
		for _, restrict := range []bool{false, true} {
			t.Run(fmt.Sprintf("god_%t_restrict_%t", god, restrict), func(t *testing.T) {
				tool, err := NewExecToolWithDeps(f.AgentHome, restrict, nil, ExecToolDeps{GodMode: god})
				if err != nil {
					t.Fatal(err)
				}
				t.Run("workspace_positive", func(t *testing.T) {
					if msg := tool.guardCommand(ctx, "cat "+wu23Quote(filepath.Join(f.Work, "allowed.txt")), f.Work); msg != "" {
						t.Fatalf("A1: workspace control refused: %s", msg)
					}
				})
				for _, rel := range fixture.ProtectedRelative() {
					path := wu23Quote(filepath.Join(f.Home, filepath.FromSlash(rel)))
					for _, cmd := range []string{"cat " + path, "ls " + path, "printf changed > " + path, "rm " + path} {
						if msg := tool.guardCommand(ctx, cmd, f.Work); msg == "" {
							t.Errorf("D6: command operand %s not refused; god=%t restrict=%t command=%s", rel, god, restrict, cmd)
						}
					}
				}
			})
		}
	}
}

func TestWU23_NoSandboxReadOnlyDoesNotAutoRunProtectedOperands(t *testing.T) {
	f := fixture.New(t)
	t.Run("workspace_positive", func(t *testing.T) {
		if !commandIsNoSandboxReadOnly("cat " + wu23Quote(filepath.Join(f.Work, "allowed.txt"))) {
			t.Fatal("D6: ordinary workspace read remains on the no-sandbox read-only list")
		}
	})
	for _, rel := range append(fixture.ProtectedRelative(), "future-home-entry/not-yet-created.txt") {
		for _, head := range []string{"cat", "ls", "head"} {
			if commandIsNoSandboxReadOnly(head + " " + wu23Quote(filepath.Join(f.Home, filepath.FromSlash(rel)))) {
				t.Errorf("D6: no-sandbox read-only list auto-runs %s of %s; want false", head, rel)
			}
		}
	}
}

func wu23Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func TestWU23_DefaultDenyNewHomeEntries(t *testing.T) {
	f := fixture.New(t)
	path := filepath.Join(f.Home, "future-home-entry", "new.txt")
	fixture.Write(t, path, fixture.Sentinel)
	res := NewReadFileTool(f.AgentHome, false, MaxReadFileSize).Execute(wu23Context(f), map[string]any{"path": path})
	if !res.IsError {
		t.Fatal("D1: an unlisted future internal entry is readable; want default-deny")
	}
}
