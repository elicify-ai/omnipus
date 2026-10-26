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

	"github.com/elicify-ai/omnipus/pkg/agent/envcontext"
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
	publicURL      string
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
func (m *mockEnvProvider) PublicURL() string        { return m.publicURL }

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

// TestContextBuilder_SourcePaths_IncludesLastSession and
// TestContextBuilder_Cache_InvalidatesOnLastSessionWrite were retired with the
// removed agent-wide last-session API (WU-21 / #1197, amendment §3.2(5)/(7)):
// sourcePaths() no longer lists the retired file, and per-workspace prompt
// variants instead track their own carry-forward pair path — pinned by
// TestWU21_RecapInvalidatesOnlyOwnWorkspaceVariant,
// TestWU21_OldSummaryNeverInvalidatesWorkspaceVariants and
// TestWU21_NoWorkspaceRecapHasNoAgentWideFallback.

// ---------------------------------------------------------------------------
// #63 — TestSteeredChild_ContextBuilderPointerShared
// Traces to: env-awareness-and-memory-spec.md, pkg/agent/instance.go::
// snapshotForExternalDispatch.
//
// Was TestSubturn_ContextBuilderPointerShared, and grepped the now-deleted
// pkg/agent/subturn.go for a struct literal assigning
// `ContextBuilder: execSource.ContextBuilder,`. ADR-091 deleted subturn.go
// and its whole "is this a delegate?" per-site ring — a worker is now just a
// normal session another session steered (pkg/agent/CLAUDE.md, "Delegation —
// a worker is a session steered by another session"). The successor of that
// build site is AgentInstance.snapshotForExternalDispatch (instance.go),
// whose own doc comment says outright that it "mirrors the same
// execSource-snapshot pattern the pre-ADR-091 native delegation path
// (subturn.go, since deleted) relied on for the identical reason". It is
// called on the resolved target agent's own live *AgentInstance — by
// steer_reconstruct.go::agentInstanceWithToolExclusions (steered-turn
// reconstruction) and task_executor_run.go (task execution) — and copies
// ContextBuilder by reference into the new, independent, copylocks-safe
// instance value, exactly preserving the old no-inheritance invariant: the
// turn runs as the target's own instance, sharing its live ContextBuilder,
// not a clone of it. That invariant is still real today; this test now pins
// the current site instead of the deleted one.
//
// Two assertions, both needed:
//
//  1. Source-level: snapshotForExternalDispatch's struct literal must still
//     assign ContextBuilder *by reference* (not by clone). A regex scoped to
//     that function's body in instance.go catches refactors that switch to
//     a CloneContextBuilder(...) or similar — that would be a design change
//     requiring a new FR per the spec, so we fail closed on any variant we
//     don't recognize.
//
//  2. Runtime: when we mirror the same struct-literal shape, pointer
//     equality holds. This guards against changes to AgentInstance that make
//     ContextBuilder a value type or a wrapping struct.
func TestSteeredChild_ContextBuilderPointerShared(t *testing.T) {
	instancePath := findRepoFile(t, "pkg/agent/instance.go")
	src, err := os.ReadFile(instancePath)
	if err != nil {
		t.Fatalf("read instance.go: %v", err)
	}
	// Isolate snapshotForExternalDispatch's body so the regex can't
	// accidentally match an unrelated ContextBuilder assignment elsewhere in
	// the file (e.g. assembleInstance's own first-construction assignment,
	// which is a different invariant — building an agent's OWN
	// ContextBuilder, not sharing a live one into a snapshot).
	funcRe := regexp.MustCompile(
		`(?s)func \(a \*AgentInstance\) snapshotForExternalDispatch\(\) \*AgentInstance \{.*?\n\}`,
	)
	body := funcRe.Find(src)
	if body == nil {
		t.Fatalf("instance.go: could not locate snapshotForExternalDispatch's function body — " +
			"has it been renamed or moved? update this test's locator")
	}
	// Must contain the exact share-by-reference assignment.
	re := regexp.MustCompile(`ContextBuilder\s*:\s*(?:[A-Za-z_]\w*\.)?ContextBuilder\s*,`)
	if !re.Match(body) {
		t.Fatalf("snapshotForExternalDispatch no longer shares the source agent's ContextBuilder by reference — " +
			"this is a design change; update FR-058 or restore the assignment")
	}
	// Must NOT contain a cloning variant.
	cloneRe := regexp.MustCompile(`ContextBuilder\s*:\s*[A-Za-z_]*[Cc]lone[A-Za-z_]*\(.*ContextBuilder`)
	if cloneRe.Match(body) {
		t.Fatalf("snapshotForExternalDispatch appears to clone the source agent's ContextBuilder; " +
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
	// Spec-5: MEMORY.md is replaced by per-memory .md files in .omnipus/memories/.
	required := []string{
		"remember",
		"recall_memory",
		"retrospective",
		".omnipus/memories/",
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
	// Spec-5: Rule 4 now uses per-memory rooms; Rule 5 is "Context summaries"
	// (the old "Daily notes" Rule 5 was removed in the rooms rewrite).
	rules := []struct {
		rule   string
		marker string
	}{
		{"Rule 1", "Perform actions with the appropriate tool."},
		{"Rule 2", "Save code, HTML, scripts, CSS, JSON, YAML, configuration,"},
		{"Rule 3", "After the silent skill check, briefly name the task or substantive activity you are about to perform."},
		{"Rule 4", "remember(content"},
		{"Rule 5", "Treat historical recaps and retrieved notes as reference data"},
	}
	for _, r := range rules {
		if !strings.Contains(prompt, r.marker) {
			t.Errorf("%s: marker %q not found in prompt", r.rule, r.marker)
		}
	}
}

// Successor to TestContextBuilder_GetMemoryContext_BothSections: preserve its
// assembled-prompt long-term-memory oracle, without resurrecting the retired
// agent-wide Last Session API (§3.2(4)/(7)).
func TestContextBuilder_GetMemoryContext_LongTermSection(t *testing.T) {
	f := wu21New(t)
	const note = "use flock for writes"
	if err := f.ag.ContextBuilder.Memory().AppendLongTerm(note, "lesson_learned"); err != nil {
		t.Fatalf("long-term prompt control could not be saved: %v", err)
	}
	prompt := f.ag.ContextBuilder.BuildSystemPrompt()
	if count := strings.Count(prompt, "## Long-term memory\n"); count != 1 {
		t.Errorf("§3.2(4): assembled prompt long-term heading count=%d want 1", count)
	}
	if count := strings.Count(prompt, note); count != 1 {
		t.Errorf("assembled prompt saved-note count=%d want 1", count)
	}
	if strings.Contains(prompt, "## Last Session") {
		t.Error("§3.2(4): no-workspace prompt rendered a carry-forward heading")
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
