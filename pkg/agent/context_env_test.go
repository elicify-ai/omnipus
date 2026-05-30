// context_env_test.go — Fix A (env-awareness) integration tests for ContextBuilder.
// Traces to: env-awareness-and-memory-spec.md (spec v7)

package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent/envcontext"
)

// ---------------------------------------------------------------------------
// mockEnvProvider — test double implementing envcontext.Provider.
// ---------------------------------------------------------------------------

type mockEnvProvider struct {
	sandboxMode    string
	outbound       bool
	workspacePath  string
	omnipusHome    string
	activeWarnings []string
}

func (m *mockEnvProvider) Platform() (envcontext.Platform, error) {
	return envcontext.Platform{GOOS: "linux", GOARCH: "amd64", Kernel: "6.8"}, nil
}
func (m *mockEnvProvider) SandboxMode() (string, error) { return m.sandboxMode, nil }
func (m *mockEnvProvider) NetworkPolicy() envcontext.NetworkPolicy {
	return envcontext.NetworkPolicy{OutboundAllowed: m.outbound}
}
func (m *mockEnvProvider) WorkspacePath() string    { return m.workspacePath }
func (m *mockEnvProvider) OmnipusHome() string      { return m.omnipusHome }
func (m *mockEnvProvider) ActiveWarnings() []string { return m.activeWarnings }

// ---------------------------------------------------------------------------
// #58 — TestContextBuilder_GetEnvironmentContext_TopOfPrompt
// Traces to: env-awareness-and-memory-spec.md FR-051
// Env preamble is parts[0] in BuildSystemPrompt output.
// ---------------------------------------------------------------------------

func TestContextBuilder_GetEnvironmentContext_TopOfPrompt(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)
	cb.WithEnvironmentProvider(&mockEnvProvider{
		sandboxMode:   "fallback",
		outbound:      false,
		workspacePath: dir,
		omnipusHome:   dir,
	})

	prompt := cb.BuildSystemPrompt()
	if prompt == "" {
		t.Fatal("BuildSystemPrompt() returned empty string")
	}

	// The env preamble must be first — it starts with "## Environment".
	if !strings.HasPrefix(strings.TrimSpace(prompt), "## Environment") {
		t.Errorf("BuildSystemPrompt() does not start with '## Environment';\nfirst 200 chars: %q",
			truncateStr(prompt, 200))
	}
}

// ---------------------------------------------------------------------------
// #58b — TestContextBuilder_BuildSystemPrompt_StructureAfterEnvPrepend
// Traces to: env-awareness-and-memory-spec.md FR-051
// Full layout: "## Environment" … "\n\n---\n\n" … identity marker.
// ---------------------------------------------------------------------------

func TestContextBuilder_BuildSystemPrompt_StructureAfterEnvPrepend(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)
	cb.WithEnvironmentProvider(&mockEnvProvider{
		sandboxMode:   "fallback",
		outbound:      false,
		workspacePath: dir,
		omnipusHome:   dir,
	})

	prompt := cb.BuildSystemPrompt()

	// Must start with env section.
	if !strings.HasPrefix(strings.TrimSpace(prompt), "## Environment") {
		t.Errorf("prompt does not begin with '## Environment'; starts: %q", truncateStr(prompt, 100))
	}

	// The separator "\n\n---\n\n" must appear between env and identity sections.
	sep := "\n\n---\n\n"
	if !strings.Contains(prompt, sep) {
		t.Errorf("prompt missing separator %q between sections", sep)
	}

	// Env section must precede the separator.
	envIdx := strings.Index(prompt, "## Environment")
	sepIdx := strings.Index(prompt, sep)
	if envIdx >= sepIdx {
		t.Errorf("env section (idx=%d) must appear before separator (idx=%d)", envIdx, sepIdx)
	}

	// After the separator there must be identity/workspace content.
	afterSep := prompt[sepIdx+len(sep):]
	if strings.TrimSpace(afterSep) == "" {
		t.Error("nothing after the first separator — identity/workspace section is missing")
	}
}

// ---------------------------------------------------------------------------
// #59 — TestContextBuilder_GetEnvironmentContext_AboveSoul
// Traces to: env-awareness-and-memory-spec.md FR-051
// With a custom agent + SOUL.md, env preamble precedes SOUL section.
// ---------------------------------------------------------------------------

func TestContextBuilder_GetEnvironmentContext_AboveSoul(t *testing.T) {
	dir := t.TempDir()

	// Write a SOUL.md file so the builder picks it up.
	soulContent := "# Custom Agent Soul\n\nI am a custom agent soul."
	if err := writeFile(dir, "SOUL.md", soulContent); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}

	cb := NewContextBuilder(dir)
	cb.WithEnvironmentProvider(&mockEnvProvider{
		sandboxMode:   "fallback",
		outbound:      false,
		workspacePath: dir,
		omnipusHome:   dir,
	})

	prompt := cb.BuildSystemPrompt()

	envIdx := strings.Index(prompt, "## Environment")
	soulIdx := strings.Index(prompt, "Custom Agent Soul")

	if envIdx < 0 {
		t.Fatal("prompt does not contain '## Environment' section")
	}
	if soulIdx < 0 {
		t.Fatal("prompt does not contain SOUL.md content")
	}
	if envIdx >= soulIdx {
		t.Errorf("env preamble (idx=%d) must appear before SOUL content (idx=%d)", envIdx, soulIdx)
	}
}

// ---------------------------------------------------------------------------
// #62 — TestRestConfigChange_InvalidatesAllContextBuilders
// Traces to: env-awareness-and-memory-spec.md FR-061
// ---------------------------------------------------------------------------

func TestRestConfigChange_InvalidatesAllContextBuilders(t *testing.T) {
	dir := t.TempDir()

	reg := NewContextBuilderRegistry()

	// Create two context builders and warm their caches.
	cb1 := NewContextBuilder(dir)
	cb2 := NewContextBuilder(dir)

	// Prime the cache on both.
	_ = cb1.BuildSystemPromptWithCache()
	_ = cb2.BuildSystemPromptWithCache()

	reg.Register("agent-1", cb1)
	reg.Register("agent-2", cb2)

	// Verify both have a non-empty cache.
	cb1.systemPromptMutex.RLock()
	cached1 := cb1.cachedSystemPrompt
	cb1.systemPromptMutex.RUnlock()

	cb2.systemPromptMutex.RLock()
	cached2 := cb2.cachedSystemPrompt
	cb2.systemPromptMutex.RUnlock()

	if cached1 == "" {
		t.Fatal("cb1 cache is empty before invalidation — precondition failed")
	}
	if cached2 == "" {
		t.Fatal("cb2 cache is empty before invalidation — precondition failed")
	}

	// Invalidate all.
	reg.InvalidateAllContextBuilders()

	// Both caches must now be cleared.
	cb1.systemPromptMutex.RLock()
	afterClear1 := cb1.cachedSystemPrompt
	cb1.systemPromptMutex.RUnlock()

	cb2.systemPromptMutex.RLock()
	afterClear2 := cb2.cachedSystemPrompt
	cb2.systemPromptMutex.RUnlock()

	if afterClear1 != "" {
		t.Errorf("cb1 cache should be empty after InvalidateAllContextBuilders, got non-empty")
	}
	if afterClear2 != "" {
		t.Errorf("cb2 cache should be empty after InvalidateAllContextBuilders, got non-empty")
	}
}

// ---------------------------------------------------------------------------
// #23 — TestContextBuilder_SourcePaths_IncludesLastSession
// Traces to: env-awareness-and-memory-spec.md FR-021
// sourcePaths() must include memory/sessions/LAST_SESSION.md.
// ---------------------------------------------------------------------------

func TestContextBuilder_SourcePaths_IncludesLastSession(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)

	paths := cb.sourcePaths()

	found := false
	for _, p := range paths {
		if strings.HasSuffix(p, "LAST_SESSION.md") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("sourcePaths() does not include LAST_SESSION.md; got: %v", paths)
	}
}

// ---------------------------------------------------------------------------
// #24 — TestContextBuilder_Cache_InvalidatesOnLastSessionWrite
// Traces to: env-awareness-and-memory-spec.md FR-021
// Writing LAST_SESSION.md must cause the cache to rebuild.
// ---------------------------------------------------------------------------

func TestContextBuilder_Cache_InvalidatesOnLastSessionWrite(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)

	// Prime the cache.
	first := cb.BuildSystemPromptWithCache()
	if first == "" {
		t.Fatal("first BuildSystemPromptWithCache returned empty")
	}

	// Write LAST_SESSION.md — this should mark the cache stale.
	ms := NewMemoryStore(dir)
	if err := ms.WriteLastSession("## Last session\nWe did useful things."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}

	// The cache must detect the change on the next call. sourceFilesChangedLocked
	// is called under a read lock inside BuildSystemPromptWithCache; a new mtime
	// on LAST_SESSION.md must trigger a rebuild.
	cb.systemPromptMutex.RLock()
	stale := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()

	if !stale {
		t.Error("cache should be stale after writing LAST_SESSION.md, but sourceFilesChangedLocked() returned false")
	}
}

// ---------------------------------------------------------------------------
// #63 — TestSubturn_ContextBuilderPointerShared
// Traces to: env-awareness-and-memory-spec.md, subturn.go:402
// Child agent struct literal assigns ContextBuilder: baseAgent.ContextBuilder.
// We verify pointer equality between parent and child CB by directly mirroring
// the struct-literal assignment pattern.
// ---------------------------------------------------------------------------

func TestSubturn_ContextBuilderPointerShared(t *testing.T) {
	// Two assertions, both needed:
	//
	// 1. Source-level: the subturn clone site must still assign
	//    ContextBuilder *by reference* (not by clone). A grep on subturn.go
	//    catches refactors that switch to CloneContextBuilder(...) or
	//    similar — that would be a design change requiring a new FR per the
	//    spec, so we fail closed on any variant we don't recognize.
	//
	// 2. Runtime: when we do mirror the same struct-literal shape, pointer
	//    equality holds. This guards against changes to AgentInstance that
	//    make ContextBuilder a value type or a wrapping struct.
	subturnPath := findRepoFile(t, "pkg/agent/subturn.go")
	src, err := os.ReadFile(subturnPath)
	if err != nil {
		t.Fatalf("read subturn.go: %v", err)
	}
	// Must contain the exact share-by-reference assignment.
	re := regexp.MustCompile(`ContextBuilder\s*:\s*baseAgent\.ContextBuilder\s*,`)
	if !re.Match(src) {
		t.Fatalf("subturn.go no longer shares parent ContextBuilder by reference — " +
			"this is a design change; update FR-058 or restore the assignment")
	}
	// Must NOT contain a cloning variant.
	cloneRe := regexp.MustCompile(`ContextBuilder\s*:\s*[A-Za-z_]*[Cc]lone[A-Za-z_]*\(.*ContextBuilder`)
	if cloneRe.Match(src) {
		t.Fatalf("subturn.go appears to clone the parent ContextBuilder; " +
			"pointer-sharing contract (FR-058) is broken")
	}

	// Runtime pointer-equality half of the contract.
	dir := t.TempDir()
	parentCB := NewContextBuilder(dir)
	child := &AgentInstance{
		ContextBuilder: parentCB,
	}
	if child.ContextBuilder != parentCB {
		t.Errorf("child.ContextBuilder (%p) != parent ContextBuilder (%p) — pointer not shared",
			child.ContextBuilder, parentCB)
	}
}

// findRepoFile returns the absolute path to a repo-relative file. Like
// findRepoRoot it walks upward until it finds go.mod, then joins the relative
// path. Fails the test when the file is missing so callers can assume it
// exists.
func findRepoFile(t *testing.T, rel string) string {
	t.Helper()
	root := findRepoRoot(t)
	p := filepath.Join(root, rel)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("repo file %s not found: %v", rel, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// #20 — TestContextBuilder_Rule4_ExactText
// Traces to: env-awareness-and-memory-spec.md FR-018
// Rule 4 text (memory tools) must appear verbatim in BuildSystemPrompt output.
// ---------------------------------------------------------------------------

func TestContextBuilder_Rule4_ExactText(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)

	prompt := cb.BuildSystemPrompt()

	// Rule 4 specifies "remember", "recall_memory", and "retrospective".
	// These are woven into getWorkspaceAndRules via the sprintf format.
	required := []string{
		"remember",
		"recall_memory",
		"retrospective",
		"MEMORY.md",
	}
	for _, token := range required {
		if !strings.Contains(prompt, token) {
			t.Errorf("prompt missing Rule-4 token %q", token)
		}
	}
}

// ---------------------------------------------------------------------------
// #19 — TestContextBuilder_Rule4_PreservesOtherRules
// Traces to: env-awareness-and-memory-spec.md FR-018
// Rules 1, 2, 3, 5, 6 are all still present alongside Rule 4.
// ---------------------------------------------------------------------------

func TestContextBuilder_Rule4_PreservesOtherRules(t *testing.T) {
	dir := t.TempDir()
	cb := NewContextBuilder(dir)

	prompt := cb.BuildSystemPrompt()

	// Each rule has a recognizable anchor.
	rules := []struct {
		rule   string
		marker string
	}{
		{"Rule 1", "ALWAYS use tools"},
		{"Rule 2", "Artifacts over chat"},
		{"Rule 3", "Be helpful and accurate"},
		{"Rule 4", "remember(content"},
		{"Rule 5", "Daily notes"},
		{"Rule 6", "Context summaries"},
	}
	for _, r := range rules {
		if !strings.Contains(prompt, r.marker) {
			t.Errorf("%s: marker %q not found in prompt", r.rule, r.marker)
		}
	}
}

// ---------------------------------------------------------------------------
// #20 — TestContextBuilder_GetMemoryContext_BothSections
// Traces to: env-awareness-and-memory-spec.md FR-019
// Pre-seed LAST_SESSION.md + MEMORY.md; both markers appear in BuildSystemPrompt.
// ---------------------------------------------------------------------------

func TestContextBuilder_GetMemoryContext_BothSections(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	if err := ms.WriteLastSession("## Last session\nSomething useful happened."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}
	if err := ms.AppendLongTerm("use flock for writes", "lesson_learned"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	cb := NewContextBuilder(dir)
	prompt := cb.BuildSystemPrompt()

	if !strings.Contains(prompt, "## Last Session") {
		t.Errorf("prompt missing '## Last Session' from memory context;\nfirst 500 chars: %s", truncateStr(prompt, 500))
	}
	if !strings.Contains(prompt, "## Long-term memory") {
		t.Errorf(
			"prompt missing '## Long-term memory' from memory context;\nfirst 500 chars: %s",
			truncateStr(prompt, 500),
		)
	}
}

// ---------------------------------------------------------------------------
// TestNoLLMProviderIdentityReference — CI guard
// Traces to: env-awareness-and-memory-spec.md MAJ-006
// Asserts zero references to LLMProvider.Identity in the codebase (compile-time only).
// ---------------------------------------------------------------------------

func TestNoLLMProviderIdentityReference(t *testing.T) {
	// MAJ-006 guard: if anyone adds an Identity() method to LLMProvider or a
	// call site that depends on it, the Q3=C decision is being silently
	// reversed. A plain compile check is not enough because a method COULD be
	// added without this test failing — only references from production code
	// would. Instead, we walk the repo's Go source tree and fail on any
	// occurrence of "LLMProvider.Identity" or ".Identity()" on a known
	// provider-shaped name.
	repoRoot := findRepoRoot(t)

	const needle1 = "LLMProvider.Identity"
	needleRegexps := []*regexp.Regexp{
		regexp.MustCompile(`LLMProvider\s*\.\s*Identity`),
		regexp.MustCompile(`\binterface\s*\{[^}]*\bIdentity\s*\(\s*\)\s*\w*Identity\w*\b`),
	}

	var hits []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip vendored / generated / test-output dirs.
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == "dist" ||
				name == ".git" || name == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip the test file itself so the needle-in-a-comment doesn't trigger.
		if strings.HasSuffix(path, "context_env_test.go") {
			return nil
		}
		data, _ := os.ReadFile(path)
		if len(data) == 0 {
			return nil
		}
		text := string(data)
		if strings.Contains(text, needle1) {
			hits = append(hits, path+": contains "+needle1)
			return nil
		}
		for _, re := range needleRegexps {
			if re.MatchString(text) {
				hits = append(hits, path+": matches "+re.String())
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo for LLMProvider.Identity check: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("MAJ-006 guard violated — LLMProvider.Identity references found:\n  %s",
			strings.Join(hits, "\n  "))
	}
}

// findRepoRoot walks upward from the package dir until it finds go.mod.
// Falls back to the package dir if go.mod is absent (keeps the test local).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: the caller is running from somewhere without go.mod visible.
	cwd, _ := os.Getwd()
	return cwd
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func writeFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
