package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildSkillsSummaryFunc_CapsByPrecedenceNotMTime covers finding 8
// (context-audit 2026-08): BuildSkillsSummaryFunc used to emit an uncapped
// XML dump of every allowed skill. This proves the cap actually bites — with
// more than maxSkillsInSummary eligible skills, only the cap's worth are
// rendered — and (medium finding, 3 reviewers) proves the survivors are now
// chosen by ListSkills' own workspace>global>builtin precedence, tie-broken
// by name, NOT by SKILL.md mtime: every skill here is stamped with mtimes in
// the OPPOSITE order from its name (skill-00 newest, skill-(total-1) oldest).
// If the old mtime-based ranking were still in effect, that would flip which
// skills survive the cap; the fact that the alphabetically-first
// maxSkillsInSummary skills survive regardless proves mtime no longer drives
// ranking at all (and, since the comparator never touches the filesystem,
// that there is no per-sort os.Stat cost). A truncation footer names how many
// were cut and points at find_skills.
func TestBuildSkillsSummaryFunc_CapsByPrecedenceNotMTime(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")

	total := maxSkillsInSummary + 5
	base := time.Now().Add(-time.Hour)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		createSkillDir(t, builtin, name, name, "desc "+name)
		// Deliberately REVERSED relative to name order: skill-00 is the
		// newest file, skill-(total-1) is the oldest. A mtime-based ranking
		// would keep the LAST maxSkillsInSummary names here, not the first.
		mtime := base.Add(time.Duration(total-i) * time.Minute)
		skillFile := filepath.Join(builtin, name, "SKILL.md")
		require.NoError(t, os.Chtimes(skillFile, mtime, mtime))
	}

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	// Exactly maxSkillsInSummary <skill> blocks are rendered.
	assert.Equal(t, maxSkillsInSummary, strings.Count(out, "<skill>"),
		"expected exactly the capped count of <skill> blocks; got:\n%s", out)

	// The alphabetically-first maxSkillsInSummary names survive (single
	// source here, so precedence ties break on name) despite having the
	// OLDEST mtimes; the alphabetically-last 5 are dropped despite having
	// the NEWEST mtimes.
	for i := 0; i < maxSkillsInSummary; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		assert.Contains(t, out, "<name>"+name+"</name>",
			"name-precedence-first skill %q should survive the cap despite its newer mtime", name)
	}
	for i := maxSkillsInSummary; i < total; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		assert.NotContains(t, out, "<name>"+name+"</name>",
			"name-precedence-last skill %q should have been evicted by the cap despite its older mtime", name)
	}

	// Truncation footer names the count and points at find_skills.
	assert.Contains(t, out, "5 more installed skills not shown")
	assert.Contains(t, out, "find_skills")
}

// TestBuildSkillsSummaryFunc_PrecedenceOrdersWorkspaceOverGlobalOverBuiltin
// proves the ranking BuildSkillsSummaryFunc now uses is the same
// workspace > global > builtin precedence ListSkills already uses to resolve
// slug collisions (the "semantically meaningful precedence" identified as an
// alternative to mtime): given more eligible skills than the cap, spread
// across all three sources, the cap must keep every workspace skill first,
// then fill remaining slots from global, then builtin — never dropping a
// higher-precedence skill in favor of a lower-precedence one.
func TestBuildSkillsSummaryFunc_PrecedenceOrdersWorkspaceOverGlobalOverBuiltin(t *testing.T) {
	tmp := t.TempDir()
	workspaceSkills := filepath.Join(tmp, "skills") // NewSkillsLoader joins workspace+"/skills"
	global := filepath.Join(tmp, "global")
	builtin := filepath.Join(tmp, "builtin")

	// 2 workspace, 2 global, and (maxSkillsInSummary+2) builtin skills — more
	// than enough total to force truncation, with builtin alone already
	// exceeding the cap.
	createSkillDir(t, workspaceSkills, "ws-b", "ws-b", "workspace skill b")
	createSkillDir(t, workspaceSkills, "ws-a", "ws-a", "workspace skill a")
	createSkillDir(t, global, "gl-b", "gl-b", "global skill b")
	createSkillDir(t, global, "gl-a", "gl-a", "global skill a")
	builtinTotal := maxSkillsInSummary + 2
	for i := 0; i < builtinTotal; i++ {
		name := fmt.Sprintf("bi-%02d", i)
		createSkillDir(t, builtin, name, name, "builtin "+name)
	}

	sl := NewSkillsLoader(tmp, global, builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	assert.Equal(t, maxSkillsInSummary, strings.Count(out, "<skill>"))

	// Every workspace and global skill must survive (4 total, well under the
	// cap) since they outrank any builtin skill.
	for _, name := range []string{"ws-a", "ws-b", "gl-a", "gl-b"} {
		assert.Contains(t, out, "<name>"+name+"</name>",
			"higher-precedence skill %q must survive the cap", name)
	}

	// Exactly maxSkillsInSummary-4 builtin skills fill the remaining slots,
	// name-ordered (bi-00 first).
	remaining := maxSkillsInSummary - 4
	for i := 0; i < remaining; i++ {
		name := fmt.Sprintf("bi-%02d", i)
		assert.Contains(t, out, "<name>"+name+"</name>",
			"name-first builtin skill %q should fill a remaining slot", name)
	}
	for i := remaining; i < builtinTotal; i++ {
		name := fmt.Sprintf("bi-%02d", i)
		assert.NotContains(t, out, "<name>"+name+"</name>",
			"lowest-precedence overflow skill %q should have been evicted", name)
	}
}

// TestBuildSkillsSummaryFunc_UnderCap_NoTruncationFooter proves the footer is
// absent when the eligible set fits within the cap — no false "more skills"
// claim when there aren't any.
func TestBuildSkillsSummaryFunc_UnderCap_NoTruncationFooter(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")

	createSkillDir(t, builtin, "solo-skill", "solo-skill", "the only one")

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	assert.Equal(t, 1, strings.Count(out, "<skill>"))
	assert.NotContains(t, out, "not shown above")
	assert.NotContains(t, out, "find_skills")
}

// TestBuildSkillsSummaryFunc_CapAppliesAfterAllowlist proves the cap and
// truncation count are computed on the ALLOWED (post-allowlist) set, not the
// full installed catalog — an agent whose allowlist admits fewer skills than
// the cap must not see a truncation footer for skills it was never going to
// see anyway.
func TestBuildSkillsSummaryFunc_CapAppliesAfterAllowlist(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")

	total := maxSkillsInSummary + 5
	allowedName := "skill-allowed"
	createSkillDir(t, builtin, allowedName, allowedName, "the only allowed one")
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("skill-denied-%02d", i)
		createSkillDir(t, builtin, name, name, "desc "+name)
	}

	sl := NewSkillsLoader(tmp, "", builtin)
	allow := func(id string) bool { return id == allowedName }
	out := sl.BuildSkillsSummaryFunc(allow)

	assert.Equal(t, 1, strings.Count(out, "<skill>"))
	assert.Contains(t, out, "<name>"+allowedName+"</name>")
	assert.NotContains(t, out, "not shown above",
		"the single allowed skill fits well under the cap; no truncation footer expected")
}
