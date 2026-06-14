// memory_integration_test.go — full-stack memory tests that exercise the
// production MemoryStore + MemoryStoreAdapter + tools.* chain.
//
// These tests verify FR-7.1 (room routing), FR-7.2 (full frontmatter),
// and FR-7.3 (3 tools re-pointed to rooms).
//
// GREENFIELD: tests work against the new per-memory .md file format.
// Old MEMORY.md tests removed per FR-7.6 / operator D2 greenfield decision.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestRememberTool_RealAdapter_PersistsViaMemoryStore verifies the full
// remember→room→.md file path.
func TestRememberTool_RealAdapter_PersistsViaMemoryStore(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)

	tool := tools.NewRememberTool(adapter, nil)

	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(context.Background(), "sess-real-001"),
		"agent-real",
	)

	res := tool.Execute(ctx, map[string]any{
		"content":  "single source of truth fact",
		"category": "key_decision",
	})
	if res == nil || res.IsError {
		t.Fatalf("Execute failed: %+v", res)
	}

	// Memory should land in the private room (no workspace_id set).
	memoriesDir := filepath.Join(workspace, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		t.Fatalf("read memories dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 memory file, got %d", len(entries))
	}

	// Read and verify frontmatter.
	data, err := os.ReadFile(filepath.Join(memoriesDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "type: decision") {
		t.Errorf("memory missing type:decision in frontmatter; got:\n%s", text)
	}
	if !strings.Contains(text, "single source of truth fact") {
		t.Errorf("memory missing content; got:\n%s", text)
	}
	if !strings.Contains(text, "status: active") {
		t.Errorf("memory missing status:active; got:\n%s", text)
	}

	// All required frontmatter keys must be present (NFR-7 — full schema always).
	requiredKeys := []string{"id:", "title:", "type:", "tags:", "confidence:", "status:", "supersedes:", "author:", "born_in:"}
	for _, k := range requiredKeys {
		if !strings.Contains(text, k) {
			t.Errorf("memory file missing frontmatter key %q; got:\n%s", k, text)
		}
	}
}

// TestRememberTool_RealAdapter_RejectsInjection confirms the MemoryStore's
// validation gates survive routing through the tool.
func TestRememberTool_RealAdapter_RejectsInjection(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)
	tool := tools.NewRememberTool(adapter, nil)

	ctx := context.Background()

	res := tool.Execute(ctx, map[string]any{
		"content":  "smuggled <!-- next --> marker inside content",
		"category": "reference",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for content containing HTML comment markers")
	}

	// No memory file should exist.
	memoriesDir := filepath.Join(workspace, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	entries, _ := os.ReadDir(memoriesDir)
	if len(entries) > 0 {
		t.Error("memory file should not exist after a rejected AppendLongTerm")
	}
}

// TestRetrospectiveTool_RealAdapter_WritesRetroOnDisk exercises the real
// AppendRetro path.
func TestRetrospectiveTool_RealAdapter_WritesRetroOnDisk(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)
	tool := tools.NewRetrospectiveTool(adapter, nil)

	ctx := tools.WithTranscriptSessionID(context.Background(), "01HX9Y8ZABCDEFGHJKMNPQRSTV")

	res := tool.Execute(ctx, map[string]any{
		"went_well":         []any{"write-path stayed flocked"},
		"needs_improvement": []any{"log less"},
	})
	if res == nil || res.IsError {
		t.Fatalf("Execute retrospective: %+v", res)
	}

	// Retros live in <agentWorkspace>/.omnipus/retros/<date>/<sessionID>_retro.md
	retrosBase := filepath.Join(workspace, memrooms.OmnipusRoomDir, "retros")
	dateEntries, err := os.ReadDir(retrosBase)
	if err != nil {
		t.Fatalf("read retros base dir: %v", err)
	}
	if len(dateEntries) != 1 {
		t.Fatalf("expected exactly one date-directory, got %d", len(dateEntries))
	}
	retroPath := filepath.Join(retrosBase, dateEntries[0].Name(),
		"01HX9Y8ZABCDEFGHJKMNPQRSTV_retro.md")
	data, err := os.ReadFile(retroPath)
	if err != nil {
		t.Fatalf("read retro file: %v", err)
	}
	retro := string(data)

	requiredSubstrings := []string{
		"trigger=joined",
		"## Session recap",
		"### Went well",
		"- write-path stayed flocked",
		"### Needs improvement",
		"- log less",
	}
	for _, s := range requiredSubstrings {
		if !strings.Contains(retro, s) {
			t.Errorf("retro file missing %q; got:\n%s", s, retro)
		}
	}
}

// TestRetrospectiveTool_RealAdapter_RejectsPathTraversal confirms the
// pkg/validation.EntityID gate is reached through the adapter.
func TestRetrospectiveTool_RealAdapter_RejectsPathTraversal(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)
	tool := tools.NewRetrospectiveTool(adapter, nil)

	ctx := tools.WithTranscriptSessionID(context.Background(), "../../etc/passwd")

	res := tool.Execute(ctx, map[string]any{
		"went_well":         []any{"anything"},
		"needs_improvement": []any{},
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for path-traversal sessionID")
	}

	// No .md files should have leaked.
	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			t.Errorf("unexpected markdown file written despite traversal: %s", path)
		}
		return nil
	})
}

// TestRecallMemoryTool_RealAdapter_SearchesRealStore verifies end-to-end
// remember → recall round-trip through the room-based store.
func TestRecallMemoryTool_RealAdapter_SearchesRealStore(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)

	remember := tools.NewRememberTool(adapter, nil)
	recall := tools.NewRecallMemoryTool(adapter)
	ctx := context.Background()

	// Seed via the real tool.
	if res := remember.Execute(ctx, map[string]any{
		"content":  "kubernetes reconciler backoff notes",
		"category": "reference",
	}); res == nil || res.IsError {
		t.Fatalf("seed remember: %+v", res)
	}

	// Query via the real tool.
	res := recall.Execute(ctx, map[string]any{"query": "reconciler"})
	if res == nil {
		t.Fatal("recall returned nil")
	}
	if res.IsError {
		t.Fatalf("recall reported error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "reconciler") {
		t.Errorf("recall output missing stored content; got:\n%s", res.ForLLM)
	}

	// Second recall must still return the same content.
	res2 := recall.Execute(ctx, map[string]any{"query": "reconciler"})
	if res2 == nil || res2.IsError {
		t.Fatalf("recall second call failed: %+v", res2)
	}
	if !strings.Contains(res2.ForLLM, "reconciler") {
		t.Errorf("second recall diverged: %s", res2.ForLLM)
	}

	// Sanity: newly-written entries are visible.
	time.Sleep(20 * time.Millisecond) // ensure mtime-granularity advance
	if res := remember.Execute(ctx, map[string]any{
		"content":  "operator rotation lesson",
		"category": "lesson_learned",
	}); res == nil || res.IsError {
		t.Fatalf("second remember: %+v", res)
	}
	res3 := recall.Execute(ctx, map[string]any{"query": "rotation"})
	if res3 == nil || res3.IsError || !strings.Contains(res3.ForLLM, "rotation") {
		t.Errorf("recall after second write did not see new content; got %v", res3)
	}
}

// TestMemory_PrivateVsSharedRoom_ByWorkspace verifies FR-7.1 room routing:
// no workspace_id → private; workspace_id set → shared.
func TestMemory_PrivateVsSharedRoom_ByWorkspace(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)

	remember := tools.NewRememberTool(adapter, nil)

	// Write a private memory (no workspace_id in ctx).
	ctxPrivate := context.Background()
	res := remember.Execute(ctxPrivate, map[string]any{
		"content":  "private agent memory",
		"category": "reference",
		"room":     "private",
	})
	if res == nil || res.IsError {
		t.Fatalf("private write failed: %+v", res)
	}

	// Verify it landed in the private room.
	privateMemDir := filepath.Join(workspace, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	privateEntries, err := os.ReadDir(privateMemDir)
	if err != nil {
		t.Fatalf("read private mem dir: %v", err)
	}
	if len(privateEntries) != 1 {
		t.Fatalf("expected 1 file in private room, got %d", len(privateEntries))
	}

	// Write a shared memory (with workspace_id in ctx).
	workspaceID := "test-workspace-01"
	ctxShared := tools.WithWorkspaceID(context.Background(), workspaceID)
	res2 := remember.Execute(ctxShared, map[string]any{
		"content":  "shared workspace memory",
		"category": "key_decision",
		"room":     "shared",
	})
	if res2 == nil || res2.IsError {
		t.Fatalf("shared write failed: %+v", res2)
	}

	// Verify it landed in the workspace shared room.
	sharedMemDir := filepath.Join(home, memrooms.WorkspacesDirName, workspaceID,
		memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	sharedEntries, err := os.ReadDir(sharedMemDir)
	if err != nil {
		t.Fatalf("read shared mem dir: %v", err)
	}
	if len(sharedEntries) != 1 {
		t.Fatalf("expected 1 file in shared room, got %d", len(sharedEntries))
	}

	// The private room must still have only 1 file (not 2).
	privateEntries2, _ := os.ReadDir(privateMemDir)
	if len(privateEntries2) != 1 {
		t.Errorf("private room should still have 1 file, got %d", len(privateEntries2))
	}
}

// TestMemory_FileFormat_FullFrontmatter asserts that every required frontmatter
// key is present in a newly-written memory file (FR-7.2 / NFR-7).
func TestMemory_FileFormat_FullFrontmatter(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	store := NewMemoryStore(workspace, home)
	adapter := NewMemoryStoreAdapter(store)

	remember := tools.NewRememberTool(adapter, nil)
	res := remember.Execute(context.Background(), map[string]any{
		"content":  "test frontmatter completeness",
		"category": "lesson_learned",
	})
	if res == nil || res.IsError {
		t.Fatalf("remember failed: %+v", res)
	}

	memoriesDir := filepath.Join(workspace, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	entries, err := os.ReadDir(memoriesDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no memory file: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(memoriesDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read memory file: %v", err)
	}
	text := string(data)

	// All 9 required frontmatter fields (FR-7.2).
	requiredKeys := []string{
		"id:", "title:", "type:", "tags:", "confidence:",
		"status:", "supersedes:", "author:", "born_in:",
	}
	for _, k := range requiredKeys {
		if !strings.Contains(text, k) {
			t.Errorf("memory file missing required frontmatter key %q; got:\n%s", k, text)
		}
	}

	// Must open and close with --- delimiters.
	if !strings.HasPrefix(text, "---\n") {
		t.Errorf("memory file must start with ---; got:\n%s", text[:min(len(text), 50)])
	}

	// Parse round-trip.
	mf, err := memrooms.ReadMemoryFile(memoriesDir, strings.TrimSuffix(entries[0].Name(), ".md"))
	if err != nil {
		t.Fatalf("parse memory file: %v", err)
	}
	if mf.Frontmatter.Type != memrooms.MemoryTypeLesson {
		t.Errorf("type mismatch: got %q, want %q", mf.Frontmatter.Type, memrooms.MemoryTypeLesson)
	}
	if mf.Frontmatter.Status != memrooms.MemoryStatusActive {
		t.Errorf("status mismatch: got %q, want %q", mf.Frontmatter.Status, memrooms.MemoryStatusActive)
	}
}
