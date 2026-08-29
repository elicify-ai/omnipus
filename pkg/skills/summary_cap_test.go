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

// TestBuildSkillsSummaryFunc_CapsAndKeepsMostRecent covers finding 8
// (context-audit 2026-08): BuildSkillsSummaryFunc used to emit an uncapped
// XML dump of every allowed skill. This proves the cap actually bites — with
// more than maxSkillsInSummary eligible skills, only the cap's worth are
// rendered, the MOST RECENTLY MODIFIED ones survive (oldest dropped first),
// and a truncation footer names how many were cut and points at find_skills.
func TestBuildSkillsSummaryFunc_CapsAndKeepsMostRecent(t *testing.T) {
	tmp := t.TempDir()
	builtin := filepath.Join(tmp, "builtin")

	total := maxSkillsInSummary + 5
	base := time.Now().Add(-time.Hour)
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		createSkillDir(t, builtin, name, name, "desc "+name)
		// Stamp mtimes so skill-00 is oldest and skill-(total-1) is newest —
		// deterministic recency ranking independent of directory scan order.
		mtime := base.Add(time.Duration(i) * time.Minute)
		skillFile := filepath.Join(builtin, name, "SKILL.md")
		require.NoError(t, os.Chtimes(skillFile, mtime, mtime))
	}

	sl := NewSkillsLoader(tmp, "", builtin)
	out := sl.BuildSkillsSummaryFunc(nil)

	// Exactly maxSkillsInSummary <skill> blocks are rendered.
	assert.Equal(t, maxSkillsInSummary, strings.Count(out, "<skill>"),
		"expected exactly the capped count of <skill> blocks; got:\n%s", out)

	// The 5 oldest (skill-00..skill-04) must be dropped; the newest
	// maxSkillsInSummary (skill-05..skill-(total-1)) must survive.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		assert.NotContains(t, out, "<name>"+name+"</name>",
			"oldest skill %q should have been evicted by the cap", name)
	}
	for i := 5; i < total; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		assert.Contains(t, out, "<name>"+name+"</name>",
			"most-recently-modified skill %q should survive the cap", name)
	}

	// Truncation footer names the count and points at find_skills.
	assert.Contains(t, out, "5 more installed skills not shown")
	assert.Contains(t, out, "find_skills")
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
