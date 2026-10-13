package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// Chief classification ruling 2026-10-13: these are dedicated, store-relative
// tools, not arbitrary host-file tools. Store access remains allowed; escapes
// into gateway internals must be refused, with no change to target bytes.
func TestWU23_DedicatedKnowledgeStoreCannotEscape(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("USERPROFILE", isolated)
	home, ws, root := a4Fixture(t, "wu23-kb")
	t.Setenv("OMNIPUS_HOME", home)
	ctx := a4Ctx("self", ws)
	deps, _ := a4Deps(home)
	reader := NewReadTool(ToolDeps{Home: home})
	edit := NewEditTool(deps)
	move := NewRestructureTool(deps)
	fixture.Write(t, filepath.Join(root, "Control.md"), "---\nstatus: draft\n---\nWU23_KB_CONTROL\n")
	t.Run("dedicated_store_positive", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"collection": "wu23-kb", "path": "Control.md"})
		if res.IsError || !strings.Contains(res.ForLLM, "WU23_KB_CONTROL") {
			t.Fatalf("dedicated store read control failed: %s", res.ForLLM)
		}
		res = edit.Execute(ctx, map[string]any{"collection": "wu23-kb", "op": "set_property", "path": "Control.md", "property": "status", "value": "active", "expect_version": a4Version(t, root, "Control.md")})
		if res.IsError {
			t.Fatalf("dedicated edit control failed: %s", res.ForLLM)
		}
		res = move.Execute(ctx, map[string]any{"collection": "wu23-kb", "op": "rename", "path": "Control.md", "new_name": "Moved.md"})
		if res.IsError {
			t.Fatalf("dedicated restructure control failed: %s", res.ForLLM)
		}
		b, err := os.ReadFile(filepath.Join(root, "Moved.md"))
		if err != nil || string(b) != "---\nstatus: active\n---\nWU23_KB_CONTROL\n" {
			t.Fatalf("dedicated control exact bytes=%q err=%v", b, err)
		}
	})
	for i, rel := range []string{"credentials.json", "config.json", "sessions/private.md", "agents/other/SOUL.md"} {
		t.Run(rel, func(t *testing.T) {
			target := filepath.Join(home, filepath.FromSlash(rel))
			fixture.Write(t, target, fixture.Sentinel)
			relative, err := filepath.Rel(root, target)
			if err != nil {
				t.Fatal(err)
			}
			alias := "escape-" + strings.ReplaceAll(rel, "/", "-") + ".md"
			if err := os.Symlink(target, filepath.Join(root, alias)); err != nil {
				t.Fatalf("dedicated-store alias fixture %d unavailable: %v", i, err)
			}
			for _, path := range []string{target, filepath.ToSlash(relative), alias} {
				for _, tool := range []tools.Tool{reader, edit, move} {
					args := map[string]any{"collection": "wu23-kb", "path": path}
					if tool == edit {
						args["op"], args["property"], args["value"], args["expect_version"] = "set_property", "status", "bad", NoteContentVersion([]byte(fixture.Sentinel))
					}
					if tool == move {
						args["op"], args["new_name"] = "rename", "Stolen.md"
					}
					res := tool.Execute(ctx, args)
					if !res.IsError || strings.Contains(res.ForLLM, "WU23_PROTECTED_SENTINEL") {
						t.Errorf("dedicated %s escaped its store using %q", tool.Name(), path)
					}
				}
			}
			b, err := os.ReadFile(target)
			if err != nil || string(b) != fixture.Sentinel {
				t.Fatalf("internal target changed via dedicated tool: bytes=%q err=%v", b, err)
			}
		})
	}
}
