// subturn_identity_test.go: tests for resolve the target agent's identity for a delegated sub-turn (ADR-032 - never inherit from the parent).

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

func writeSkillWithName(t *testing.T, workspace, slug, displayName string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + slug +
		"\ndescription: A test skill with a sufficiently long description to validate.\n" +
		"metadata:\n  display_name: " + displayName + "\n---\n\n# " + displayName + "\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- moved from subturn.go tests 2026-09-15 ---

// TestResolveRequestedSkillForChild_DirectOutcomes (ADR-072 Finding D) is a
// function-level regression test for resolveRequestedSkillForChild itself,
// covering all three outcomes plus the edge cases the top-level spawnSubTurn
// tests above don't isolate (nil ContextBuilder, no skillsLoader, empty/
// whitespace input). This is the direct-level counterpart to Finding D's
// fix: resolveRequestedSkillForChild used to call the uncached, full-
// directory-scanning SkillsLoader.ListSkills() TWICE on every denied/
// not-found outcome — once implicitly inside cb.ResolveSkillName, once again
// explicitly in the fallback loop. It now fetches the list ONCE
// (cb.skillsLoader.ListSkills()) and reuses it for both the resolution
// attempt (via the extracted cb.resolveSkillNameWithList helper) and the
// fallback membership check — verified here by inspection of the single
// remaining call site in resolveRequestedSkillForChild's body (subturn.go),
// since SkillsLoader has no seam to inject a call-counting double without
// changing production code for the sake of a test. This test instead proves
// the fix did not change behaviour: every outcome the pre-fix double-scan
// implementation produced is still produced identically.
func TestResolveRequestedSkillForChild_DirectOutcomes(t *testing.T) {
	t.Run("granted: slug installed and allowed", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"finance-news"})

		slug, outcome := resolveRequestedSkillForChild(cb, "finance-news")
		if outcome != requestedSkillGranted {
			t.Fatalf("outcome = %v, want requestedSkillGranted", outcome)
		}
		if slug != "finance-news" {
			t.Fatalf("slug = %q, want %q", slug, "finance-news")
		}
	})

	t.Run("granted: matched by display name, canonical slug returned", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkillWithName(t, workspace, "deploy", "Deploy Helper")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"deploy"})

		slug, outcome := resolveRequestedSkillForChild(cb, "Deploy Helper")
		if outcome != requestedSkillGranted {
			t.Fatalf("outcome = %v, want requestedSkillGranted", outcome)
		}
		if slug != "deploy" {
			t.Fatalf("slug = %q, want canonical slug %q", slug, "deploy")
		}
	})

	t.Run("denied: slug installed but not granted", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"some-other-skill"})

		slug, outcome := resolveRequestedSkillForChild(cb, "finance-news")
		if outcome != requestedSkillDenied {
			t.Fatalf("outcome = %v, want requestedSkillDenied", outcome)
		}
		if slug != "" {
			t.Fatalf("slug = %q, want empty on denial", slug)
		}
	})

	t.Run("unresolvable: slug matches nothing on any shelf", func(t *testing.T) {
		workspace := t.TempDir()
		cb := NewContextBuilder(workspace).WithSkillAllowlist(nil)

		slug, outcome := resolveRequestedSkillForChild(cb, "totally-unresolvable-zzz")
		if outcome != requestedSkillUnresolvable {
			t.Fatalf("outcome = %v, want requestedSkillUnresolvable", outcome)
		}
		if slug != "" {
			t.Fatalf("slug = %q, want empty when unresolvable", slug)
		}
	})

	t.Run("nil ContextBuilder is unresolvable, not a panic", func(t *testing.T) {
		slug, outcome := resolveRequestedSkillForChild(nil, "anything")
		if outcome != requestedSkillUnresolvable || slug != "" {
			t.Fatalf("got (%q, %v), want (\"\", requestedSkillUnresolvable)", slug, outcome)
		}
	})

	t.Run("empty/whitespace requested is unresolvable", func(t *testing.T) {
		workspace := t.TempDir()
		writeSkill(t, workspace, "finance-news")
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"finance-news"})

		for _, in := range []string{"", "   "} {
			slug, outcome := resolveRequestedSkillForChild(cb, in)
			if outcome != requestedSkillUnresolvable || slug != "" {
				t.Fatalf("input %q: got (%q, %v), want (\"\", requestedSkillUnresolvable)", in, slug, outcome)
			}
		}
	})
}

// TestResolveDelegateSoul_WorkerHasCompiledPrompt proves the seed worker
// carries a real compiled persona.
//
// This test previously asserted the OPPOSITE — that the worker's soul is empty
// — and used `worker` as the canonical example of a soul-less delegate. That
// was a description of a defect, not a requirement: because
// prompts["worker"] was "", BuildSystemPrompt fell through both the
// compiled-prompt and SOUL branches to the generic "You are Worker, a helpful
// AI assistant" fallback. The single most-used delegation target was the only
// seeded agent with no role-specific guidance, and it produced unbounded
// output that looked like a hang.
//
// The empty-soul invariant those tests really protected is preserved below in
// TestResolveDelegateSoul_SoullessAgentReturnsEmpty, with a subject that is
// genuinely soul-less.
func TestResolveDelegateSoul_WorkerHasCompiledPrompt(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, string(coreagent.IDWorker))
	if got == "" {
		t.Fatal("resolveDelegateSoul(worker) is empty; the worker must carry execution-discipline guidance")
	}
	// Must not have regressed to the legacy generic wrapper.
	if containsAny(got, "You are a subagent", "subagent to complete", "Complete the given task") {
		t.Fatalf("resolveDelegateSoul(worker) leaked the legacy subagent wrapper: %q", got)
	}
	// Must not be the generic assistant fallback the defect produced.
	if containsAny(got, "a helpful AI assistant powered by Omnipus") {
		t.Fatalf("resolveDelegateSoul(worker) returned the generic fallback identity: %q", got)
	}
}

// TestResolveDelegateSoul_SoullessAgentReturnsEmpty preserves the original
// invariant: an agent with no compiled prompt and no SOUL.md resolves to an
// empty soul, with no panic and no fallback wrapper. Soul remains OPTIONAL.
func TestResolveDelegateSoul_SoullessAgentReturnsEmpty(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, soullessAgentID)
	if got != "" {
		t.Fatalf("resolveDelegateSoul(%s) = %q, want empty (soul is OPTIONAL)", soullessAgentID, got)
	}
}

// TestResolveDelegateSoul_BaseAgentUsesCompiledPrompt proves the resolver
// returns the SEEDED base agent's compiled prompt when one is set. Jim, Mia,
// etc. carry compiled-in personas that take precedence over an on-disk SOUL.md
// (they are LOCKED, the SOUL.md on disk is the agent's identity anchor only
// for non-base agents).
func TestResolveDelegateSoul_BaseAgentUsesCompiledPrompt(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, "jim")
	if got == "" {
		t.Fatal("resolveDelegateSoul(jim) returned empty; Jim must have a compiled persona")
	}
	// Sanity: must NOT be the legacy "You are a subagent" string.
	if containsAny(got, "You are a subagent", "subagent to complete", "Complete the given task") {
		t.Fatalf("resolveDelegateSoul(jim) leaked the legacy subagent wrapper: %q", got)
	}
}

// TestComposeDelegateInput_EmptySoulReturnsTaskOnly proves the external path's
// input is the TASK ALONE when the delegate's soul is empty. No persona, no
// wrapper, no "## System" header.
//
// The subject moved from `worker` to a genuinely soul-less agent when the
// worker gained a compiled persona; the invariant under test is unchanged.
func TestComposeDelegateInput_EmptySoulReturnsTaskOnly(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", soullessAgentID)
	if got != "summarize this file" {
		t.Fatalf("composeDelegateInput(soul-less, empty soul) = %q, want %q", got, "summarize this file")
	}
	if containsAny(got, "## System") {
		t.Fatalf("composeDelegateInput must not add a wrapper for an empty soul: %q", got)
	}
}

// TestComposeDelegateInput_WorkerPrependsSoul is the counterpart: now that the
// worker has a persona, the external path composes (soul, task) for it exactly
// as it does for a base agent.
func TestComposeDelegateInput_WorkerPrependsSoul(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", string(coreagent.IDWorker))
	if got == "summarize this file" {
		t.Fatal("composeDelegateInput(worker, task) returned task only; the worker's soul should prepend")
	}
	if !containsAny(got, "## System", "## Task", "summarize this file") {
		t.Fatalf("composeDelegateInput(worker) lost the (soul, task) composition: %q", got)
	}
}

// TestComposeDelegateInput_BaseAgentPrependsSoul proves the external path
// composes (soul, task) when the delegate has a soul, mirroring the native
// path's (system, user) split.
func TestComposeDelegateInput_BaseAgentPrependsSoul(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := composeDelegateInput(al, "summarize this file", "", "jim")
	if got == "summarize this file" {
		t.Fatal("composeDelegateInput(jim, task) returned task only; Jim's soul should prepend")
	}
	if !containsAny(got, "Jim") {
		t.Fatalf("composeDelegateInput(jim, task) missing Jim's persona marker: %q", got)
	}
	if !containsAny(got, "summarize this file") {
		t.Fatalf("composeDelegateInput(jim, task) missing the task: %q", got)
	}
}

// TestComposeDelegateInput_ExplicitActualSystemTakesPrecedence proves the
// legacy `ActualSystemPrompt` from the caller (e.g., tests, future callers)
// overrides the resolved delegate soul. The dispatch site is a single
// composition point and the caller wins.
func TestComposeDelegateInput_ExplicitActualSystemTakesPrecedence(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	explicit := "EXPLICIT_SYSTEM"
	got := composeDelegateInput(al, "task", explicit, "jim")
	if !containsAny(got, "EXPLICIT_SYSTEM") {
		t.Fatalf("composeDelegateInput: explicit ActualSystemPrompt must take precedence, got: %q", got)
	}
}

// TestResolveDelegateSoul_OnDiskSoulMdForCustomAgent proves the resolver
// falls back to the agent's on-disk SOUL.md when no compiled prompt is
// present. A custom agent with SOUL.md on disk gets its persona injected
// as the system role; without SOUL.md the soul is empty.
func TestResolveDelegateSoul_OnDiskSoulMdForCustomAgent(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	tmp := t.TempDir()
	soulPath := filepath.Join(tmp, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("I am a custom worker persona."), 0o600); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}
	cfg := al.GetConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "custom-worker", Type: config.AgentTypeWorker, Home: tmp, Locked: true},
	}

	got := resolveDelegateSoul(al, "custom-worker")
	if got != "I am a custom worker persona." {
		t.Fatalf("resolveDelegateSoul(custom-worker) = %q, want SOUL.md content", got)
	}
}

// TestResolveDelegateSoul_UnknownAgentReturnsEmpty proves an unresolved
// target never falls back to a generic "You are a subagent" wrapper. The
// caller (spawnSubTurn) treats an empty soul as "no persona" and the
// composition proceeds with task-only input.
func TestResolveDelegateSoul_UnknownAgentReturnsEmpty(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	got := resolveDelegateSoul(al, "nope-not-an-agent")
	if got != "" {
		t.Fatalf("resolveDelegateSoul(unknown) = %q, want empty (no wrapper fallback)", got)
	}
}
