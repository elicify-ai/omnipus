package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
	"github.com/stretchr/testify/assert"
)

func wu23Context(f fixture.Layout) context.Context {
	ctx := WithAgentID(context.Background(), "self")
	ctx = WithWorkspaceID(ctx, "alpha")
	ctx = WithTurnWorkspaceDir(ctx, f.Work)
	ctx = WithTranscriptSessionID(ctx, "acting")
	return WithToolRootChatSessionID(ctx, "root")
}

func wu23FileTools(f fixture.Layout) []Tool {
	return []Tool{NewReadFileTool(f.AgentHome, false, MaxReadFileSize), NewWriteFileTool(f.AgentHome, false), NewEditFileTool(f.AgentHome, false), NewAppendFileTool(f.AgentHome, false), NewListDirTool(f.AgentHome, false)}
}

// D6/T-FT: tool execution is real. Unrestricted writes isolate the protection
// rule from unrelated workspace-confinement refusals. God Mode's actual global
// policy floor is exercised, not a dummy boolean in a filesystem test.
func TestWU23_FileToolsProtectInternalInventory(t *testing.T) {
	f := fixture.New(t)
	ctx := wu23Context(f)
	for _, god := range []bool{false, true} {
		t.Run(fmt.Sprintf("god_%t", god), func(t *testing.T) {
			for _, tool := range wu23FileTools(f) {
				t.Run(tool.Name(), func(t *testing.T) {
					cfg := &ToolPolicyCfg{GodMode: god, GlobalPolicies: map[string]config.ToolPolicy{tool.Name(): config.ToolPolicyAllow}}
					if EffectiveToolPolicy(cfg, tool.Scope(), "core", tool.Name()) != string(config.ToolPolicyAllow) {
						t.Fatal("instrument: test tool not executable under current policy")
					}
					t.Run("workspace_positive", func(t *testing.T) {
						path := filepath.Join(f.Work, tool.Name()+"-control.txt")
						fixture.Write(t, path, "before")
						args := wu23PathArgs(tool.Name(), path)
						if tool.Name() == "list_directory" {
							args["path"] = f.Work
						}
						res := tool.Execute(ctx, args)
						if res.IsError {
							t.Fatalf("A1 control: %s", res.ForLLM)
						}
						want := "before"
						switch tool.Name() {
						case "write_file", "edit_file":
							want = "changed"
						case "append_file":
							want = "beforechanged"
						case "list_directory":
							if !strings.Contains(res.ForLLM, filepath.Base(path)) {
								t.Fatal("control: listing did not contain actual workspace file")
							}
						case "read_file":
							if !strings.Contains(res.ForLLM, "before") {
								t.Fatal("control: actual workspace content absent")
							}
						}
						b, err := os.ReadFile(path)
						if err != nil || string(b) != want {
							t.Fatalf("control: bytes=%q want=%q err=%v", b, want, err)
						}
					})
					for _, rel := range fixture.ProtectedRelative() {
						t.Run(rel, func(t *testing.T) {
							path := filepath.Join(f.Home, filepath.FromSlash(rel))
							fixture.Write(t, path, "before")
							args := wu23PathArgs(tool.Name(), path)
							if tool.Name() == "list_directory" {
								args["path"] = filepath.Dir(path)
							}
							res := tool.Execute(ctx, args)
							if !res.IsError {
								t.Errorf("D2: %s accepted protected %s", tool.Name(), rel)
							}
							if strings.Contains(res.ForLLM, "before") && !res.IsError {
								t.Errorf("D2: protected contents disclosed by %s", tool.Name())
							}
							b, err := os.ReadFile(path)
							if err != nil || string(b) != "before" {
								t.Errorf("D2: protected target changed: bytes=%q err=%v", b, err)
							}
						})
					}
				})
			}
		})
	}
}

func wu23PathArgs(name, path string) map[string]any {
	return map[string]any{"path": path, "content": "changed", "overwrite": true, "old_text": "before", "new_text": "changed"}
}

func TestWU23_ResolverAndPreflightAreNeverGrantable(t *testing.T) {
	f := fixture.New(t)
	p, err := ResolveTurnFSPolicy(wu23Context(f), f.AgentHome, false)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowedRoots = []string{f.Home, f.Shared}
	patterns := []*regexp.Regexp{regexp.MustCompile(".*")}
	t.Run("workspace_positive", func(t *testing.T) {
		h, err := ResolvePath(t.Context(), p, "read_file", "control", FSOpRead, filepath.Join(f.Work, "allowed.txt"))
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		b, err := h.ReadFile()
		if err != nil || string(b) != "WU23_ALLOWED_CONTROL\n" {
			t.Fatalf("control bytes=%q err=%v", b, err)
		}
	})
	for _, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(f.Home, filepath.FromSlash(rel))
			for _, op := range []FSOp{FSOpRead, FSOpWrite, FSOpList, FSOpSend, FSOpServe, FSOpExec} {
				h, err := ResolvePathAllowingPatterns(wu23Context(f), p, "wu23", "denial", op, path, patterns)
				if h != nil {
					h.Close()
				}
				// Skill instructions have their existing specialized gate.
				if !strings.HasPrefix(rel, "skills/") || err == nil {
					// Keep exact sentinel identity: wrapping is not accepted.
					assert.Same(t, ErrCarveOut, err, "D5 %s %s: want exact ErrCarveOut (inventory beats pattern)", op, rel)
				}
			}
			for _, access := range []uint64{fspolicy.PathGrantAccessRead, fspolicy.PathGrantAccessWrite, fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite} {
				v := EvaluateFSPreflight(p, path, access, false)
				if !v.Refused || v.Contained || v.NeedsEscalation() {
					t.Errorf("D5 preflight: %s: refused=%t contained=%t prompts=%t; want true/false/false", rel, v.Refused, v.Contained, v.NeedsEscalation())
				}
			}
		})
	}
}

// Direct symlinks and hardlinks must be refused by the real read tool too.
func TestWU23_ReadFileAliasesStayProtected(t *testing.T) {
	f := fixture.New(t)
	reader := NewReadFileTool(f.AgentHome, false, MaxReadFileSize)
	for _, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			src := filepath.Join(f.Home, filepath.FromSlash(rel))
			for _, kind := range []string{"symlink", "hardlink"} {
				alias := filepath.Join(f.Work, strings.ReplaceAll(rel, "/", "-")+"-"+kind)
				var err error
				if kind == "symlink" {
					err = os.Symlink(src, alias)
				} else {
					err = os.Link(src, alias)
				}
				if err != nil {
					t.Fatalf("alias fixture not available: %v", err)
				}
				res := reader.Execute(wu23Context(f), map[string]any{"path": alias})
				if !res.IsError || strings.Contains(res.ForLLM, fixture.Sentinel) {
					t.Errorf("D2: %s of %s disclosed protected data", kind, rel)
				}
			}
		})
	}
}

func TestWU23_AllowListReadOnlyAndSessionScoped(t *testing.T) {
	f := fixture.New(t)
	p, policyErr := ResolveTurnFSPolicy(wu23Context(f), f.AgentHome, false)
	if policyErr != nil {
		t.Fatal(policyErr)
	}
	for _, rel := range []string{"media/allowed.txt", "uploads/acting/allowed.txt", "uploads/root/allowed.txt", "skills/fixture/helper.txt"} {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(f.Home, filepath.FromSlash(rel))
			h, err := ResolvePath(wu23Context(f), p, "read_file", "allow", FSOpRead, path)
			if err != nil {
				t.Fatalf("D3 allow-list read: %v", err)
			}
			b, err := h.ReadFile()
			h.Close()
			if err != nil || string(b) != "WU23_ALLOWED_CONTROL\n" {
				t.Fatalf("D3 exact allowed bytes=%q err=%v", b, err)
			}
			h, err = ResolvePath(wu23Context(f), p, "write_file", "deny", FSOpWrite, path)
			if h != nil {
				h.Close()
			}
			if err == nil {
				t.Error("D3: read-only allow entry admitted a write")
			}
		})
	}
	path := filepath.Join(f.Home, "uploads", "other", "private.txt")
	h, err := ResolvePath(wu23Context(f), p, "read_file", "other", FSOpRead, path)
	if h != nil {
		h.Close()
	}
	if !errors.Is(err, ErrCarveOut) {
		t.Errorf("A4: other session upload got %v, want ErrCarveOut", err)
	}
}
