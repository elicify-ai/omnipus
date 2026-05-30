// memory_integration_test.go — full-stack memory tests that exercise the
// production MemoryStore + MemoryStoreAdapter + tools.* chain.
//
// The pkg/tools unit tests use an in-package simpleMemStore to avoid a
// pkg/tools → pkg/agent import cycle. That is the correct choice for the tool
// unit tests, but it means regressions in the REAL MemoryStore's flock
// discipline, validation gates, format bytes, or retro layout never fail a
// tool test. This file closes that gap from the agent side by wiring the
// production adapter into the real tools and asserting full-path behavior.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestRememberTool_RealAdapter_PersistsViaMemoryStore wires the real
// MemoryStoreAdapter into tools.NewRememberTool and verifies the file on disk
// has exactly the FR-002 format bytes. A regression in MemoryStore.AppendLongTerm
// (e.g., dropped flock, wrong separator, missing ts/cat fields) fails here.
func TestRememberTool_RealAdapter_PersistsViaMemoryStore(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
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

	// Read the real MEMORY.md and assert it matches FR-002 exactly.
	data, err := os.ReadFile(filepath.Join(workspace, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	text := string(data)

	if !strings.Contains(text, "cat=key_decision") {
		t.Errorf("MEMORY.md missing cat=key_decision marker; got:\n%s", text)
	}
	if !strings.Contains(text, "single source of truth fact") {
		t.Errorf("MEMORY.md missing content; got:\n%s", text)
	}
	// FR-002: first entry has no leading "<!-- next -->" separator.
	if strings.HasPrefix(text, "<!-- next -->") {
		t.Error("first entry should not have leading separator")
	}

	// Second write — must prepend the separator.
	res2 := tool.Execute(ctx, map[string]any{
		"content":  "second distinct fact",
		"category": "reference",
	})
	if res2 == nil || res2.IsError {
		t.Fatalf("Execute second: %+v", res2)
	}
	data2, _ := os.ReadFile(filepath.Join(workspace, "memory", "MEMORY.md"))
	text2 := string(data2)
	if !strings.Contains(text2, "<!-- next -->") {
		t.Errorf("second entry should prepend separator; got:\n%s", text2)
	}

	// Both entries must be readable back via the store's own parser — proves
	// the wire format is self-consistent.
	entries, err := store.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ReadLongTermEntries returned %d entries, want 2", len(entries))
	}
}

// TestRememberTool_RealAdapter_RejectsInjection confirms the MemoryStore's
// validation gates (FR-003) survive routing through the tool. An HTML-comment
// injection attempt must be rejected before any byte hits disk.
func TestRememberTool_RealAdapter_RejectsInjection(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
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

	// The file must not have been created — no bytes written on rejection.
	if _, err := os.Stat(filepath.Join(workspace, "memory", "MEMORY.md")); err == nil {
		t.Error("MEMORY.md should not exist after a rejected AppendLongTerm")
	}
}

// TestRetrospectiveTool_RealAdapter_WritesRetroOnDisk exercises the real
// AppendRetro path, asserting path-traversal rejection and file-format bytes.
func TestRetrospectiveTool_RealAdapter_WritesRetroOnDisk(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
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

	// Locate the retro file — date-named directory under workspace/memory/sessions.
	sessionsDir := filepath.Join(workspace, "memory", "sessions")
	dateEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	if len(dateEntries) != 1 {
		t.Fatalf("expected exactly one date-directory, got %d", len(dateEntries))
	}
	retroPath := filepath.Join(sessionsDir, dateEntries[0].Name(),
		"01HX9Y8ZABCDEFGHJKMNPQRSTV_retro.md")
	data, err := os.ReadFile(retroPath)
	if err != nil {
		t.Fatalf("read retro file: %v", err)
	}
	retro := string(data)

	// FR-015 + spec retro format.
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

	// Explicit non-behavior: no user_confirmed field on disk.
	if strings.Contains(retro, "user_confirmed") {
		t.Error("retro file contains user_confirmed — FR-015 forbids it")
	}
}

// TestRetrospectiveTool_RealAdapter_RejectsPathTraversal confirms the
// pkg/validation.EntityID gate is reached through the adapter.
func TestRetrospectiveTool_RealAdapter_RejectsPathTraversal(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
	adapter := NewMemoryStoreAdapter(store)
	tool := tools.NewRetrospectiveTool(adapter, nil)

	// Inject a malicious sessionID via the transcript context.
	ctx := tools.WithTranscriptSessionID(context.Background(), "../../etc/passwd")

	res := tool.Execute(ctx, map[string]any{
		"went_well":         []any{"anything"},
		"needs_improvement": []any{},
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for path-traversal sessionID")
	}

	// Walk the workspace and assert no .md files leaked anywhere.
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

// TestRecallMemoryTool_RealAdapter_SearchesRealStore wires the real adapter
// into RecallMemoryTool and asserts the search returns entries previously
// written via RememberTool — end-to-end round-trip.
func TestRecallMemoryTool_RealAdapter_SearchesRealStore(t *testing.T) {
	workspace := t.TempDir()
	store := NewMemoryStore(workspace)
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

	// Mtime cache: a second recall within the same mtime window must still
	// return the same content without re-reading disk (behavior ultimately
	// owned by MemoryStore.ReadLongTermEntries).
	res2 := recall.Execute(ctx, map[string]any{"query": "reconciler"})
	if res2 == nil || res2.IsError || res2.ForLLM != res.ForLLM {
		t.Errorf("recall second call diverged from first; first=%q second=%v",
			res.ForLLM, res2)
	}

	// Sanity: newly-written entries after a mtime advance are visible.
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
