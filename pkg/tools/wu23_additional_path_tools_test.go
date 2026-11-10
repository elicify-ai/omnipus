package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

func TestWU23_AdditionalHostPathTools(t *testing.T) {
	f := fixture.New(t)
	seedGrepWorkspace(t, f.Home, "alpha", "self")
	ctx := wu23Context(f)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	send := NewSendFileTool(f.AgentHome, false, MaxReadFileSize, store)
	send.SetContext("webchat", "wu23")
	libRead := NewLibraryReadTool(f.AgentHome, false, MaxReadFileSize)
	libList := NewLibraryListTool(f.AgentHome, false)
	fixture.Write(t, filepath.Join(f.Work, libraryDirName, "allowed.txt"), "WU23_ALLOWED_CONTROL\n")
	serve := NewWebServeTool(f.AgentHome, "self", testGetConfig("http://127.0.0.1:5001", nil), &stubServedSubdirs{token: "wu23-fixture"}, nil, WebServeDevConfig{}, nil, nil, 60, 86400)
	mount := NewRequestMountTool(f.Home)
	t.Run("positive_controls", func(t *testing.T) {
		for _, item := range []struct {
			tool Tool
			path string
		}{
			{send, filepath.Join(f.Work, "allowed.txt")}, {libRead, "allowed.txt"}, {libList, "."}, {serve, f.Work}, {mount, f.Shared},
		} {
			res := item.tool.Execute(ctx, map[string]any{"path": item.path})
			if res.IsError {
				t.Errorf("D3 %s positive failed: %s", item.tool.Name(), res.ForLLM)
			}
			if item.tool == send {
				if len(res.Media) != 1 {
					t.Fatal("send_file control produced no actual media reference")
				}
				path, _, err := store.ResolveWithMeta(res.Media[0])
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(path)
				if err != nil || string(b) != "WU23_ALLOWED_CONTROL\n" {
					t.Fatalf("send control bytes=%q err=%v", b, err)
				}
			}
		}
	})
	for i, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(f.Home, filepath.FromSlash(rel))
			for _, tool := range []Tool{send, libRead, libList, serve, mount} {
				res := tool.Execute(ctx, map[string]any{"path": path})
				if !res.IsError {
					t.Errorf("D6: %s admitted internal path %s", tool.Name(), rel)
				}
			}
			// A relative library alias reaches the inner file tool: lexical
			// rejection of an absolute path alone is not enough.
			alias := "internal-" + strings.ReplaceAll(rel, "/", "-")
			aliasPath := filepath.Join(f.Work, libraryDirName, alias)
			if err := os.Symlink(path, aliasPath); err != nil {
				t.Fatalf("library alias fixture %d failed: %v", i, err)
			}
			res := libRead.Execute(ctx, map[string]any{"path": alias})
			if !res.IsError || strings.Contains(res.ForLLM, "WU23_PROTECTED_SENTINEL") {
				t.Errorf("D6 library_read served internal alias of %s", rel)
			}
		})
	}
}
