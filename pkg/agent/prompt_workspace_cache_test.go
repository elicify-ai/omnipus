// Omnipus — ADR-072 D8/D8.1: the (agent x workspace) prompt cache
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// These tests cover the static-prompt cache becoming keyed per (agent,
// workspace id) instead of per agent (ADR-072 D8), bounded by a per-agent
// LRU variant cap OR an aggregate byte budget, whichever binds first
// (D8.1), with two explicit invalidation triggers the mtime-based
// staleness check cannot see on its own: workspace-membership change and
// mount deletion. Spec: skill-activation-and-loading-spec.md FR-046/
// FR-046a/FR-046b, test rows 36-39 and 51h/51i.

package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
)

// makeMountShelf creates a temp mount with n project skills named
// "<prefix>-000".."<prefix>-0(n-1)", each carrying a fixed-length
// description, and returns the merged ProjectShelf for that single mount.
// Because content never embeds the workspace id, two calls with the same
// (mountName, prefix, n) that are then wired to different workspace ids via
// WithProjectShelfResolver produce byte-identical assembled prompts — which
// several tests below rely on for exact, non-magic-number size arithmetic.
func makeMountShelf(t *testing.T, mountName, prefix string, n int) skills.ProjectShelf {
	t.Helper()
	mountRoot := t.TempDir()
	for i := 0; i < n; i++ {
		slug := fmt.Sprintf("%s-%03d", prefix, i)
		dir := filepath.Join(mountRoot, ".claude", "skills", slug)
		writeProjectSkillFile(t, dir, slug, strings.Repeat("d", 120))
	}
	shelf, collisions := skills.MergeProjectSkills([]skills.ProjectMount{{Name: mountName, Root: mountRoot}})
	if len(collisions) != 0 {
		t.Fatalf("unexpected collisions building test shelf: %v", collisions)
	}
	return shelf
}

// TestTurnAssembly_MenuDiffersPerWorkspace covers ADR-072 D8's key
// requirement itself (spec test row 36): the same agent, acting in two
// different workspaces, sees a different "# Skills" menu in each — because
// different mounts contribute different project skills — and a turn
// assembled via BuildMessages (not just the lower-level prompt builder)
// carries that difference through to the system message the model sees.
func TestTurnAssembly_MenuDiffersPerWorkspace(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	shelfA := makeMountShelf(t, "mount-a", "alpha-skill", 1)
	shelfB := makeMountShelf(t, "mount-b", "beta-skill", 1)

	cb.WithProjectShelfResolver(func(workspaceID string) skills.ProjectShelf {
		switch workspaceID {
		case "ws-a":
			return shelfA
		case "ws-b":
			return shelfB
		default:
			return nil
		}
	})

	msgsA := cb.BuildMessages(nil, "hi", nil, "ws-a", "test", "chat1", "", "", "", nil)
	msgsB := cb.BuildMessages(nil, "hi", nil, "ws-b", "test", "chat1", "", "", "", nil)

	sysA := msgsA[0].Content
	sysB := msgsB[0].Content

	if !strings.Contains(sysA, "alpha-skill-000") {
		t.Fatalf("ws-a's assembled turn must list its own project skill:\n%s", sysA)
	}
	if strings.Contains(sysA, "beta-skill-000") {
		t.Fatalf("ws-a's assembled turn must NOT list ws-b's project skill:\n%s", sysA)
	}
	if !strings.Contains(sysB, "beta-skill-000") {
		t.Fatalf("ws-b's assembled turn must list its own project skill:\n%s", sysB)
	}
	if strings.Contains(sysB, "alpha-skill-000") {
		t.Fatalf("ws-b's assembled turn must NOT list ws-a's project skill:\n%s", sysB)
	}

	// Re-fetching ws-a's static prompt directly must return the identical,
	// still ws-a-scoped content — an (agent x workspace) cache hit (D8),
	// not a coincidence of always rebuilding the same way.
	again := cb.BuildSystemPromptWithCacheForWorkspace("ws-a")
	if !strings.Contains(again, "alpha-skill-000") || strings.Contains(again, "beta-skill-000") {
		t.Fatalf("re-fetching ws-a's cached prompt must still show only ws-a's menu:\n%s", again)
	}
}

// TestPromptCache_EvictsOnWorkspaceMembershipChange covers ADR-072 D8's
// second invalidation trigger (spec test row 37): removing this agent from
// a workspace's membership must evict that workspace's cached prompt
// variant, even though nothing under any tracked skill root changed (the
// mtime-based staleness check cannot see membership at all). The test first
// proves the pre-revocation entry really is served from cache (not an
// always-live resolver) before showing that the explicit eviction call is
// what makes the revocation take effect.
func TestPromptCache_EvictsOnWorkspaceMembershipChange(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	shelf := makeMountShelf(t, "team-mount", "team-skill", 1)
	isMember := true
	cb.WithProjectShelfResolver(func(workspaceID string) skills.ProjectShelf {
		if workspaceID == "ws-team" && isMember {
			return shelf
		}
		return nil
	})

	first := cb.BuildSystemPromptWithCacheForWorkspace("ws-team")
	if !strings.Contains(first, "team-skill-000") {
		t.Fatalf("expected the team's project skill in the menu while a member:\n%s", first)
	}

	// Membership is revoked, but nothing has told the cache yet.
	isMember = false
	stillCached := cb.BuildSystemPromptWithCacheForWorkspace("ws-team")
	if !strings.Contains(stillCached, "team-skill-000") {
		t.Fatal("a cache hit must still serve the pre-revocation snapshot until something explicitly invalidates it — otherwise this test cannot tell the eviction call below apart from an always-live resolver")
	}

	// The membership-change trigger (ADR-072 D8 item 2) fires.
	cb.EvictWorkspacePrompt("ws-team")

	rebuilt := cb.BuildSystemPromptWithCacheForWorkspace("ws-team")
	if strings.Contains(rebuilt, "team-skill-000") {
		t.Fatalf("after membership revocation + eviction, the menu must no longer offer the workspace's project skill:\n%s", rebuilt)
	}
}

// TestPromptCache_EvictsOnMountDeletion covers ADR-072 D8's third
// invalidation trigger (spec test row 38): deleting a mount from a
// workspace must evict that workspace's cached prompt variant. A mount
// record lives in the entity store, not under any tracked skill root, so
// its removal is invisible to the mtime sweep too — distinct from (but
// mechanically similar to) the membership-change trigger above.
func TestPromptCache_EvictsOnMountDeletion(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	mountRoot := t.TempDir()
	slug := "mount-skill-000"
	writeProjectSkillFile(t, filepath.Join(mountRoot, ".claude", "skills", slug), slug, "A skill from a mount that will be deleted")

	mounts := []skills.ProjectMount{{Name: "acme", Root: mountRoot}}
	cb.WithProjectShelfResolver(func(workspaceID string) skills.ProjectShelf {
		if workspaceID != "ws-mounts" {
			return nil
		}
		shelf, _ := skills.MergeProjectSkills(mounts)
		return shelf
	})

	first := cb.BuildSystemPromptWithCacheForWorkspace("ws-mounts")
	if !strings.Contains(first, slug) {
		t.Fatalf("expected the mount's project skill in the menu before deletion:\n%s", first)
	}

	// The mount is deleted from the workspace's registry (the resolver's
	// backing data shrinks), but nothing has told the cache yet.
	mounts = nil
	stillCached := cb.BuildSystemPromptWithCacheForWorkspace("ws-mounts")
	if !strings.Contains(stillCached, slug) {
		t.Fatal("a cache hit must still serve the pre-deletion snapshot until something explicitly invalidates it — otherwise this test cannot tell the eviction call below apart from an always-live resolver")
	}

	// The mount-deletion trigger (ADR-072 D8 item 3) fires.
	cb.EvictWorkspacePrompt("ws-mounts")

	rebuilt := cb.BuildSystemPromptWithCacheForWorkspace("ws-mounts")
	if strings.Contains(rebuilt, slug) {
		t.Fatalf("after mount deletion + eviction, the menu must no longer offer the deleted mount's skill:\n%s", rebuilt)
	}
}

// TestPromptCache_BoundedUnderWorkspaceChurn covers ADR-072 D8's hard
// per-agent LRU cap on cached variant count (spec test row 39): switching
// this agent across many more workspaces than the cap allows never grows
// the cache past the cap, and the survivor set is genuinely
// least-recently-used, not an arbitrary truncation — touching an older
// entry again must protect it from the very next eviction.
func TestPromptCache_BoundedUnderWorkspaceChurn(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	cb.workspacePromptCache.maxVariants = 3
	cb.workspacePromptCache.maxBytes = 100 * 1024 * 1024 // effectively unbounded: isolate the count bound

	shelf := makeMountShelf(t, "churn-mount", "churn-skill", 1)
	cb.WithProjectShelfResolver(func(string) skills.ProjectShelf { return shelf })

	build := func(id string) { cb.BuildSystemPromptWithCacheForWorkspace(id) }

	build("ws0")
	build("ws1")
	build("ws2")
	// Touch ws0 again: it becomes most-recently-used, so the NEXT eviction
	// (triggered by inserting a 4th distinct workspace) must take ws1 — now
	// the least-recently-used — not ws0.
	build("ws0")
	build("ws3")

	cb.workspacePromptCache.mu.Lock()
	count := len(cb.workspacePromptCache.variants)
	_, hasWs0 := cb.workspacePromptCache.variants["ws0"]
	_, hasWs1 := cb.workspacePromptCache.variants["ws1"]
	_, hasWs2 := cb.workspacePromptCache.variants["ws2"]
	_, hasWs3 := cb.workspacePromptCache.variants["ws3"]
	cb.workspacePromptCache.mu.Unlock()

	if count != 3 {
		t.Fatalf("expected exactly 3 retained variants (the cap), got %d", count)
	}
	if hasWs1 {
		t.Error("ws1 should have been evicted as the least-recently-used entry")
	}
	if !hasWs0 || !hasWs2 || !hasWs3 {
		t.Errorf("expected ws0 (re-touched), ws2, and ws3 to remain: ws0=%v ws2=%v ws3=%v", hasWs0, hasWs2, hasWs3)
	}

	// Continued churn across many more workspaces must never exceed the cap.
	for i := 4; i < 20; i++ {
		build(fmt.Sprintf("ws%d", i))
		cb.workspacePromptCache.mu.Lock()
		n := len(cb.workspacePromptCache.variants)
		cb.workspacePromptCache.mu.Unlock()
		if n > 3 {
			t.Fatalf("cache exceeded its cap of 3 after building ws%d: holds %d variants", i, n)
		}
	}
}

// TestPromptCache_ByteBudgetEvicts covers ADR-072 D8.1 / spec FR-046a
// (CRIT-002, spec test row 51h): the aggregate byte budget forces eviction
// well before the count cap would ever bind, for a very large workspace
// whose menu (many project skills, D1.2's 5000-skill anticipated case) is
// individually large. Sizes are measured from the real assembled prompt
// rather than hardcoded, so the assertions hold regardless of exact prompt
// scaffolding size.
func TestPromptCache_ByteBudgetEvicts(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	cb.workspacePromptCache.maxVariants = 1000 // never binds here — isolate the byte bound

	// A "very large workspace" menu: 50 project skills is enough to make one
	// variant's size (and therefore the eviction arithmetic below) clearly
	// non-trivial without the test itself allocating megabytes of filler.
	shelf := makeMountShelf(t, "large-mount", "skill", 50)
	cb.WithProjectShelfResolver(func(string) skills.ProjectShelf { return shelf })

	// Measure one variant's real assembled size, then evict it so the
	// budget-driven part of the test starts from a clean cache.
	probe := cb.BuildSystemPromptWithCacheForWorkspace("probe")
	variantBytes := len(probe)
	if variantBytes == 0 {
		t.Fatal("test setup invariant violated: probe variant must not be empty")
	}
	cb.EvictWorkspacePrompt("probe")

	// A budget that fits exactly two same-size variants but not three.
	cb.workspacePromptCache.maxBytes = 2*variantBytes + variantBytes/2

	cb.BuildSystemPromptWithCacheForWorkspace("ws0")
	cb.BuildSystemPromptWithCacheForWorkspace("ws1")

	cb.workspacePromptCache.mu.Lock()
	before := len(cb.workspacePromptCache.variants)
	cb.workspacePromptCache.mu.Unlock()
	if before != 2 {
		t.Fatalf("expected both same-size variants to fit under the budget before the third insertion, got %d", before)
	}

	// The third, same-size insertion pushes the aggregate over budget.
	cb.BuildSystemPromptWithCacheForWorkspace("ws2")

	cb.workspacePromptCache.mu.Lock()
	after := len(cb.workspacePromptCache.variants)
	totalAfter := cb.workspacePromptCache.totalBytes
	budget := cb.workspacePromptCache.maxBytes
	_, hasWs0 := cb.workspacePromptCache.variants["ws0"]
	_, hasWs1 := cb.workspacePromptCache.variants["ws1"]
	_, hasWs2 := cb.workspacePromptCache.variants["ws2"]
	cb.workspacePromptCache.mu.Unlock()

	if totalAfter > budget {
		t.Fatalf("aggregate retained bytes (%d) exceeded the byte budget (%d) — the cache is not staying within budget", totalAfter, budget)
	}
	if after != 2 {
		t.Fatalf("byte budget should have forced exactly one eviction (well under the count cap of 1000): holds %d variants (%d bytes, budget %d)", after, totalAfter, budget)
	}
	if hasWs0 {
		t.Error("ws0 (least-recently-used) should have been evicted once the byte budget was exceeded")
	}
	if !hasWs1 || !hasWs2 {
		t.Errorf("ws1 and ws2 should remain cached: ws1=%v ws2=%v", hasWs1, hasWs2)
	}
}

// TestPromptCache_OversizedVariantNotCached covers ADR-072 D8.1's
// qualification (spec test row 51i, FR-046b): a single variant whose
// assembled size exceeds the whole byte budget is never cached at all — it
// is rebuilt fresh on every call — rather than being cached and evicting
// every other entry on each insertion (which would degrade the cache into
// a thrashing single-slot buffer). Also proves the oversized case still
// returns the FULL, uncapped menu (D1.1 is not re-opened by any of this)
// and does not poison caching for a normal-sized workspace.
func TestPromptCache_OversizedVariantNotCached(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	cb.workspacePromptCache.maxVariants = 1000 // isolate the byte bound

	bigShelf := makeMountShelf(t, "huge-mount", "skill", 50)
	smallShelf := makeMountShelf(t, "small-mount", "tiny-skill", 1)

	cb.WithProjectShelfResolver(func(workspaceID string) skills.ProjectShelf {
		if strings.HasPrefix(workspaceID, "huge") {
			return bigShelf
		}
		return smallShelf
	})

	// Measure both variants' real sizes at the (large) default budget, then
	// clear them so the constrained-budget part of the test starts clean.
	hugeProbe := cb.BuildSystemPromptWithCacheForWorkspace("huge-probe")
	cb.EvictWorkspacePrompt("huge-probe")
	smallProbe := cb.BuildSystemPromptWithCacheForWorkspace("small-probe")
	cb.EvictWorkspacePrompt("small-probe")

	if len(hugeProbe) <= len(smallProbe) {
		t.Fatalf("test setup invariant violated: expected the 50-skill shelf (%d bytes) to exceed the 1-skill shelf (%d bytes)", len(hugeProbe), len(smallProbe))
	}

	// A budget between the two: enough for the small workspace's variant,
	// not enough for the huge one's.
	cb.workspacePromptCache.maxBytes = (len(hugeProbe) + len(smallProbe)) / 2

	first := cb.BuildSystemPromptWithCacheForWorkspace("huge-ws")
	if first == "" {
		t.Fatal("an oversized variant must still be built and returned in full, not blanked out")
	}
	if !strings.Contains(first, "skill-000") {
		t.Error("the oversized, uncached variant must still contain the full menu content (D1.1's no-cap rule is not affected by D8.1's cache bound)")
	}

	cb.workspacePromptCache.mu.Lock()
	count := len(cb.workspacePromptCache.variants)
	totalBytes := cb.workspacePromptCache.totalBytes
	cb.workspacePromptCache.mu.Unlock()
	if count != 0 || totalBytes != 0 {
		t.Fatalf("an oversized variant must never be cached: variants=%d totalBytes=%d", count, totalBytes)
	}

	// A repeat call rebuilds fresh (not cached) rather than erroring or
	// drifting from the first build.
	second := cb.BuildSystemPromptWithCacheForWorkspace("huge-ws")
	if second != first {
		t.Error("rebuilding the same uncached oversized variant should be stable (same source data, same output) even though it is never cached")
	}

	// The oversized workspace must not poison caching for a normal-sized
	// one (D8.1: "worse than not caching that variant at all" is exactly
	// what evicting everyone to make room would produce).
	cb.BuildSystemPromptWithCacheForWorkspace("small-ws")
	cb.workspacePromptCache.mu.Lock()
	_, smallCached := cb.workspacePromptCache.variants["small-ws"]
	cb.workspacePromptCache.mu.Unlock()
	if !smallCached {
		t.Error("a normal-sized workspace variant must still be cached even while an oversized one exists and is never cached")
	}
}
