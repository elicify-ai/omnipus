package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// setupWorkspace creates a temporary workspace with standard directories and optional files.
// Returns the tmpDir path; caller should defer os.RemoveAll(tmpDir).
func setupWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "omnipus-test-*")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "skills"), 0o755)
	for name, content := range files {
		dir := filepath.Dir(filepath.Join(tmpDir, name))
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmpDir
}

// TestSingleSystemMessage verifies that BuildMessages always produces exactly one
// system message regardless of history variations.
// Fix: multiple system messages break Anthropic (top-level system param) and
// Codex (only reads last system message as instructions).
func TestSingleSystemMessage(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	tests := []struct {
		name    string
		history []providers.Message
		message string
	}{
		{
			name:    "no history",
			message: "hello",
		},
		{
			name: "with history",
			history: []providers.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello"},
			},
			message: "new message",
		},
		{
			name: "long history",
			history: []providers.Message{
				{Role: "user", Content: strings.Repeat("Long user text. ", 50)},
				{Role: "assistant", Content: strings.Repeat("Long reply text. ", 50)},
			},
			message: "new message",
		},
		{
			name: "system message in history is filtered",
			history: []providers.Message{
				{Role: "system", Content: "stale system prompt from previous session"},
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "hello"},
			},
			message: "new message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := cb.BuildMessages(tt.history, tt.message, nil, "", "test", "chat1", "", "", "", nil)

			systemCount := 0
			for _, m := range msgs {
				if m.Role == "system" {
					systemCount++
				}
			}
			if systemCount != 1 {
				t.Errorf("expected exactly 1 system message, got %d", systemCount)
			}
			if msgs[0].Role != "system" {
				t.Errorf("first message should be system, got %s", msgs[0].Role)
			}
			if msgs[len(msgs)-1].Role != "user" {
				t.Errorf("last message should be user, got %s", msgs[len(msgs)-1].Role)
			}

			// System message must contain identity (static) and time (dynamic)
			sys := msgs[0].Content
			if !strings.Contains(sys, "omnipus") {
				t.Error("system message missing identity")
			}
			if !strings.Contains(sys, "Current Time") {
				t.Error("system message missing dynamic time context")
			}

			// The legacy summariser is decommissioned: BuildMessages must
			// never emit a CONTEXT_SUMMARY block for any input.
			if strings.Contains(sys, "CONTEXT_SUMMARY:") {
				t.Error("CONTEXT_SUMMARY must never appear — the legacy summariser is decommissioned")
			}
		})
	}
}

func TestBuildMessages_CurrentSenderDynamicContext(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"IDENTITY.md": "# Identity\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	tests := []struct {
		name              string
		senderID          string
		senderDisplayName string
		wantLine          string
		wantSection       bool
	}{
		{
			name:              "both id and display name",
			senderID:          "feishu:ou_xxx",
			senderDisplayName: "Zhang San",
			wantLine:          "Current sender: Zhang San (ID: feishu:ou_xxx)",
			wantSection:       true,
		},
		{
			name:              "display name only",
			senderDisplayName: "Alice",
			wantLine:          "Current sender: Alice",
			wantSection:       true,
		},
		{
			name:        "id only",
			senderID:    "discord:123",
			wantLine:    "Current sender: discord:123",
			wantSection: true,
		},
		{
			name:        "no sender info",
			wantSection: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := cb.BuildMessages(
				nil,
				"hello",
				nil,
				"",
				"discord",
				"chat1",
				tt.senderID,
				tt.senderDisplayName,
				"",
				nil,
			)
			sys := msgs[0].Content

			if tt.wantSection {
				if !strings.Contains(sys, "## Current Sender") {
					t.Fatalf("system prompt missing Current Sender section:\n%s", sys)
				}
				if !strings.Contains(sys, tt.wantLine) {
					t.Fatalf("system prompt missing sender line %q:\n%s", tt.wantLine, sys)
				}
				return
			}

			if strings.Contains(sys, "## Current Sender") {
				t.Fatalf("system prompt should omit Current Sender section:\n%s", sys)
			}
		})
	}
}

// TestMtimeAutoInvalidation verifies that the cache detects source file changes
// via mtime without requiring explicit InvalidateCache().
// Fix: original implementation had no auto-invalidation — edits to bootstrap files,
// memory, or skills were invisible until process restart.
func TestMtimeAutoInvalidation(t *testing.T) {
	tests := []struct {
		name       string
		file       string // relative path inside workspace
		contentV1  string
		contentV2  string
		checkField string // substring to verify in rebuilt prompt
	}{
		{
			name:       "bootstrap file change",
			file:       "AGENT.md",
			contentV1:  "# Original Agent",
			contentV2:  "# Updated Agent",
			checkField: "Updated Agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := setupWorkspace(t, map[string]string{tt.file: tt.contentV1})
			defer os.RemoveAll(tmpDir)

			cb := NewContextBuilder(tmpDir)

			sp1 := cb.BuildSystemPromptWithCache()

			// Overwrite file and set future mtime to ensure detection.
			// Use 2s offset for filesystem mtime resolution safety (some FS
			// have 1s or coarser granularity, especially in CI containers).
			fullPath := filepath.Join(tmpDir, tt.file)
			os.WriteFile(fullPath, []byte(tt.contentV2), 0o644)
			future := time.Now().Add(2 * time.Second)
			os.Chtimes(fullPath, future, future)

			// Verify sourceFilesChangedLocked detects the mtime change
			cb.systemPromptMutex.RLock()
			changed := cb.sourceFilesChangedLocked()
			cb.systemPromptMutex.RUnlock()
			if !changed {
				t.Fatalf("sourceFilesChangedLocked() should detect %s change", tt.file)
			}

			// Should auto-rebuild without explicit InvalidateCache()
			sp2 := cb.BuildSystemPromptWithCache()
			if sp1 == sp2 {
				t.Errorf("cache not rebuilt after %s change", tt.file)
			}
			if !strings.Contains(sp2, tt.checkField) {
				t.Errorf("rebuilt prompt missing expected content %q", tt.checkField)
			}
		})
	}

	// Skills directory mtime change
	t.Run("skills dir change", func(t *testing.T) {
		tmpDir := setupWorkspace(t, nil)
		defer os.RemoveAll(tmpDir)

		cb := NewContextBuilder(tmpDir)
		_ = cb.BuildSystemPromptWithCache() // populate cache

		// Touch skills directory (simulate new skill installed)
		skillsDir := filepath.Join(tmpDir, "skills")
		future := time.Now().Add(2 * time.Second)
		os.Chtimes(skillsDir, future, future)

		// Verify sourceFilesChangedLocked detects it (cache is rebuilt)
		// We confirm by checking internal state: a second call should rebuild.
		cb.systemPromptMutex.RLock()
		changed := cb.sourceFilesChangedLocked()
		cb.systemPromptMutex.RUnlock()
		if !changed {
			t.Error("sourceFilesChangedLocked() should detect skills dir mtime change")
		}
	})

	// Spec-5: private memories directory mtime change (FR-7.1 / FR-021).
	// sourcePaths() now tracks .omnipus/memories/ (dir mtime) instead of
	// the old memory/MEMORY.md file. Touching the dir simulates a new .md
	// memory file being written (which updates the directory mtime).
	t.Run("memories dir mtime change", func(t *testing.T) {
		tmpDir := setupWorkspace(t, nil)
		defer os.RemoveAll(tmpDir)

		cb := NewContextBuilder(tmpDir)
		_ = cb.BuildSystemPromptWithCache() // populate cache

		// Ensure .omnipus/memories/ exists first (MustEnsureRoom creates it at ContextBuilder init).
		memoriesDir := filepath.Join(tmpDir, ".omnipus", "memories")
		if err := os.MkdirAll(memoriesDir, 0o700); err != nil {
			t.Fatalf("create memories dir: %v", err)
		}
		future := time.Now().Add(2 * time.Second)
		os.Chtimes(memoriesDir, future, future)

		cb.systemPromptMutex.RLock()
		changed := cb.sourceFilesChangedLocked()
		cb.systemPromptMutex.RUnlock()
		if !changed {
			t.Error("sourceFilesChangedLocked() should detect .omnipus/memories/ mtime change")
		}
	})
}

// TestExplicitInvalidateCache verifies that InvalidateCache() forces a rebuild
// even when source files haven't changed (useful for tests and reload commands).
func TestExplicitInvalidateCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Test Agent",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	sp1 := cb.BuildSystemPromptWithCache()
	cb.InvalidateCache()
	sp2 := cb.BuildSystemPromptWithCache()

	if sp1 != sp2 {
		t.Error("prompt should be identical after invalidate+rebuild when files unchanged")
	}

	// Verify cachedAt was reset
	cb.InvalidateCache()
	cb.systemPromptMutex.RLock()
	if !cb.cachedAt.IsZero() {
		t.Error("cachedAt should be zero after InvalidateCache()")
	}
	cb.systemPromptMutex.RUnlock()
}

// TestCacheStability verifies that the static prompt is stable across repeated calls
// when no files change (regression test for issue #607).
func TestCacheStability(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nContent",
		"SOUL.md":  "# Soul\nContent",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	results := make([]string, 5)
	for i := range results {
		results[i] = cb.BuildSystemPromptWithCache()
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("cached prompt changed between call 0 and %d", i)
		}
	}

	// Static prompt must NOT contain per-request data
	if strings.Contains(results[0], "Current Time") {
		t.Error("static cached prompt should not contain time (added dynamically)")
	}
}

// TestNewFileCreationInvalidatesCache verifies that creating a source file that
// did not exist when the cache was built triggers a cache rebuild.
// This catches the "from nothing to something" edge case that the old
// modifiedSince (return false on stat error) would miss.
func TestNewFileCreationInvalidatesCache(t *testing.T) {
	tests := []struct {
		name       string
		file       string // relative path inside workspace
		content    string
		checkField string // substring to verify in rebuilt prompt
	}{
		{
			name:       "new bootstrap file",
			file:       "SOUL.md",
			content:    "# Soul\nBe kind and helpful.",
			checkField: "Be kind and helpful",
		},
		// Spec-5: per-memory .md files live in .omnipus/memories/.
		// Creating the first .md file there updates the directory mtime,
		// which sourcePaths() tracks. The content appears in the prompt
		// via GetMemoryContext(); tested in TestContextBuilder_GetMemoryContext_BothSections.
		// Here we just check that a new .md in the memories dir invalidates the cache.
		{
			name:       "new memory .md file",
			file:       ".omnipus/memories/newentry.md",
			content:    "---\nid: test-id\ntitle: dark mode pref\ntype: note\ntags: []\nconfidence: 0.0000\nstatus: active\nsupersedes: \"\"\nauthor: test\nborn_in: \"\"\n---\n\nUser prefers dark mode.\n",
			checkField: "dark mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start with an empty workspace (no bootstrap/memory files)
			tmpDir := setupWorkspace(t, nil)
			defer os.RemoveAll(tmpDir)

			cb := NewContextBuilder(tmpDir)

			// Populate cache — file does not exist yet
			sp1 := cb.BuildSystemPromptWithCache()
			if strings.Contains(sp1, tt.checkField) {
				t.Fatalf("prompt should not contain %q before file is created", tt.checkField)
			}

			// Create the file after cache was built
			fullPath := filepath.Join(tmpDir, tt.file)
			os.MkdirAll(filepath.Dir(fullPath), 0o755)
			if err := os.WriteFile(fullPath, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			// Set future mtime to guarantee detection.
			// Also touch the parent directory — for memory .md files, sourcePaths()
			// tracks the .omnipus/memories/ directory mtime (not individual files).
			future := time.Now().Add(2 * time.Second)
			os.Chtimes(fullPath, future, future)
			os.Chtimes(filepath.Dir(fullPath), future, future)

			// Cache should auto-invalidate because file went from absent -> present
			sp2 := cb.BuildSystemPromptWithCache()
			if !strings.Contains(sp2, tt.checkField) {
				t.Errorf("cache not invalidated on new file creation: expected %q in prompt", tt.checkField)
			}
		})
	}
}

// TestSkillFileContentChange verifies that modifying a skill file's content
// (not just the directory structure) invalidates the cache.
// This is the scenario where directory mtime alone is insufficient — on most
// filesystems, editing a file inside a directory does NOT update the parent
// directory's mtime.
func TestSkillFileContentChange(t *testing.T) {
	skillMD := `---
name: test-skill
description: "A test skill"
---
# Test Skill v1
Original content.`

	tmpDir := setupWorkspace(t, map[string]string{
		"skills/test-skill/SKILL.md": skillMD,
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Populate cache
	sp1 := cb.BuildSystemPromptWithCache()
	_ = sp1 // cache is warm

	// Modify the skill file content (without touching the skills/ directory)
	updatedSkillMD := `---
name: test-skill
description: "An updated test skill"
---
# Test Skill v2
Updated content.`

	skillPath := filepath.Join(tmpDir, "skills", "test-skill", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(updatedSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set future mtime on the skill file only (NOT the directory)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(skillPath, future, future)

	// Verify that sourceFilesChangedLocked detects the content change
	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Error("sourceFilesChangedLocked() should detect skill file content change")
	}

	// Verify cache is actually rebuilt with new content
	sp2 := cb.BuildSystemPromptWithCache()
	if sp1 == sp2 && strings.Contains(sp1, "test-skill") {
		// If the skill appeared in the prompt and the prompt didn't change,
		// the cache was not invalidated.
		t.Error("cache should be invalidated when skill file content changes")
	}
}

// TestGlobalSkillFileContentChange verifies that modifying a global skill
// (~/.omnipus/skills) invalidates the cached system prompt.
func TestGlobalSkillFileContentChange(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	globalSkillPath := filepath.Join(tmpHome, ".omnipus", "skills", "global-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(globalSkillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `---
name: global-skill
description: global-v1
---
# Global Skill v1`
	if err := os.WriteFile(globalSkillPath, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	// ADR-072 D5: a nil/empty allowlist now denies every skill (opt-in,
	// default none) — this test exercises cache invalidation on file content
	// change, not grant enforcement, so it needs an explicit grant for the
	// skill under test (previously relied on the retired "nil = unrestricted"
	// behavior).
	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"global-skill"})
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "global-v1") {
		t.Fatal("expected initial prompt to contain global skill description")
	}

	v2 := `---
name: global-skill
description: global-v2
---
# Global Skill v2`
	if err := os.WriteFile(globalSkillPath, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(globalSkillPath, future, future); err != nil {
		t.Fatalf("failed to update mtime for %s: %v", globalSkillPath, err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect global skill file content change")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "global-v2") {
		t.Error("rebuilt prompt should contain updated global skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when global skill file content changes")
	}
}

// TestBuiltinSkillFileContentChange verifies that modifying a builtin skill
// invalidates the cached system prompt.
func TestBuiltinSkillFileContentChange(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	builtinRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", builtinRoot)

	builtinSkillPath := filepath.Join(builtinRoot, "builtin-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(builtinSkillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `---
name: builtin-skill
description: builtin-v1
---
# Builtin Skill v1`
	if err := os.WriteFile(builtinSkillPath, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}

	// ADR-072 D5: see TestGlobalSkillFileContentChange's comment — an explicit
	// grant is now required for the skill to appear at all.
	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"builtin-skill"})
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "builtin-v1") {
		t.Fatal("expected initial prompt to contain builtin skill description")
	}

	v2 := `---
name: builtin-skill
description: builtin-v2
---
# Builtin Skill v2`
	if err := os.WriteFile(builtinSkillPath, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(builtinSkillPath, future, future); err != nil {
		t.Fatalf("failed to update mtime for %s: %v", builtinSkillPath, err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect builtin skill file content change")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "builtin-v2") {
		t.Error("rebuilt prompt should contain updated builtin skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when builtin skill file content changes")
	}
}

// TestSkillFileDeletionInvalidatesCache verifies that deleting a nested skill
// file invalidates the cached system prompt.
func TestSkillFileDeletionInvalidatesCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"skills/delete-me/SKILL.md": `---
name: delete-me
description: delete-me-v1
---
# Delete Me`,
	})
	defer os.RemoveAll(tmpDir)

	// ADR-072 D5: see TestGlobalSkillFileContentChange's comment — an explicit
	// grant is now required for the skill to appear at all.
	cb := NewContextBuilder(tmpDir).WithSkillAllowlist([]string{"delete-me"})
	sp1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp1, "delete-me-v1") {
		t.Fatal("expected initial prompt to contain skill description")
	}

	skillPath := filepath.Join(tmpDir, "skills", "delete-me", "SKILL.md")
	if err := os.Remove(skillPath); err != nil {
		t.Fatal(err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked() should detect deleted skill file")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if strings.Contains(sp2, "delete-me-v1") {
		t.Error("rebuilt prompt should not contain deleted skill description")
	}
	if sp1 == sp2 {
		t.Error("cache should be invalidated when skill file is deleted")
	}
}

// TestConcurrentBuildSystemPromptWithCache verifies that multiple goroutines
// can safely call BuildSystemPromptWithCache concurrently without producing
// empty results, panics, or data races.
// Run with: go test -race ./pkg/agent/ -run TestConcurrentBuildSystemPromptWithCache
func TestConcurrentBuildSystemPromptWithCache(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md":             "# Agent\nConcurrency test agent.",
		"SOUL.md":              "# Soul\nBe helpful.",
		"skills/demo/SKILL.md": "---\nname: demo\ndescription: \"demo skill\"\n---\n# Demo",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Spec-5: seed a memory via AppendLongTerm to exercise the memory path.
	//
	// CI-robustness: seed through cb.Memory() — the SAME MemoryStore the
	// ContextBuilder uses — rather than a separate NewMemoryStore(tmpDir, …).
	// Each MemoryStore lazily opens a bleve/scorch index under
	// <room.Root>/.index/bleve/, and scorch's bolt root store takes an
	// exclusive OS file lock with bolt's default INFINITE timeout. Two stores
	// on one path therefore deadlock: the seeding store opened+cached (never
	// closed) the index, then the ContextBuilder's store blocked forever in
	// bolt.Open waiting for that lock — a hang under any scheduling (it failed
	// the same way on baseline). Production never opens two stores per
	// workspace, so the second store was an unrealistic test artifact. Using
	// the builder's own store keeps the memory path fully exercised
	// (GetMemoryContext → SearchEntries → bleve) with no second lock holder.
	if err := cb.Memory().AppendLongTerm("user prefers Go", "reference"); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*iterations)

	for g := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range iterations {
				result := cb.BuildSystemPromptWithCache()
				if result == "" {
					errs <- "empty prompt returned"
					return
				}
				if !strings.Contains(result, "omnipus") {
					errs <- "prompt missing identity"
					return
				}

				// Also exercise BuildMessages concurrently
				msgs := cb.BuildMessages(nil, "hello", nil, "", "test", "chat", "", "", "", nil)
				if len(msgs) < 2 {
					errs <- "BuildMessages returned fewer than 2 messages"
					return
				}
				if msgs[0].Role != "system" {
					errs <- "first message not system"
					return
				}

				// Occasionally invalidate to exercise the write path
				if i%10 == 0 {
					cb.InvalidateCache()
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for errMsg := range errs {
		t.Errorf("concurrent access error: %s", errMsg)
	}
}

// BenchmarkBuildMessagesWithCache measures caching performance.

// TestEmptyWorkspaceBaselineDetectsNewFiles verifies that when the cache is
// built on an empty workspace (no tracked files exist), creating a file
// afterwards still triggers cache invalidation. This validates the
// time.Unix(1, 0) fallback for maxMtime: any real file's mtime is after epoch,
// so fileChangedSince correctly detects the absent -> present transition AND
// the mtime comparison succeeds even without artificially inflated Chtimes.
func TestEmptyWorkspaceBaselineDetectsNewFiles(t *testing.T) {
	// Empty workspace: no bootstrap files, no memory, no skills content.
	tmpDir := setupWorkspace(t, nil)
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Build cache — all tracked files are absent, maxMtime falls back to epoch.
	sp1 := cb.BuildSystemPromptWithCache()

	// Create a bootstrap file with natural mtime (no Chtimes manipulation).
	// The file's mtime should be the current wall-clock time, which is
	// strictly after time.Unix(1, 0).
	soulPath := filepath.Join(tmpDir, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("# Soul\nNewly created."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Cache should detect the new file via existedAtCache (absent -> present).
	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("sourceFilesChangedLocked should detect newly created file on empty workspace")
	}

	sp2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(sp2, "Newly created") {
		t.Error("rebuilt prompt should contain new file content")
	}
	if sp1 == sp2 {
		t.Error("cache should have been invalidated after file creation")
	}
}

// BenchmarkBuildMessagesWithCache measures caching performance.
func BenchmarkBuildMessagesWithCache(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "omnipus-bench-*")
	defer os.RemoveAll(tmpDir)

	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "skills"), 0o755)
	for _, name := range []string{"AGENT.md", "SOUL.md"} {
		os.WriteFile(filepath.Join(tmpDir, name), []byte(strings.Repeat("Content.\n", 10)), 0o644)
	}

	cb := NewContextBuilder(tmpDir)
	history := []providers.Message{
		{Role: "user", Content: "previous message"},
		{Role: "assistant", Content: "previous response"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.BuildMessages(history, "new message", nil, "", "cli", "test", "", "", "", nil)
	}
}
