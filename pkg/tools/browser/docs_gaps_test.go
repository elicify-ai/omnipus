package browser

// docs_gaps_test.go — two gaps this change DECLARES rather than defaults
// through, and the tests that keep the declarations from quietly evaporating.
//
// FR-066 and FR-074 both follow the same pattern, and it is a deliberate one:
// when a limitation cannot be fixed here, the requirement is not "fix it
// anyway" and not "say nothing" — it is to write it down where the person it
// will bite would look. A gap nobody wrote down is indistinguishable from a
// gap nobody knew about, and the first person to find it finds it as a full
// disk or a browser that will not start.
//
// Asserting on prose is unusual and worth justifying: these two lines have no
// runtime behaviour to test. Their entire value is that they exist and are
// findable, so their existence is the only thing there is to assert. Deleting
// the line as "stale documentation" during a later cleanup is exactly how this
// kind of declaration gets lost, and this is what notices.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readRepoDoc(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	require.NoError(t, err, "%s must exist — it is where a gap this change cannot close is declared", rel)
	return string(body)
}

// TestDocs_WindowsBrowserGapIsDocumented is FR-066.
//
// The gap: pkg/config has no memory reader for Windows, so
// AvailableMemoryBytes reports "cannot determine" there, and the pool's
// unmeasurable-host branch holds at a floor of one browser regardless of how
// much RAM the machine has. No amount of configuration raises it.
//
// That is a real limitation with no workaround, so an operator must be able to
// find it before they spend an afternoon on it.
func TestDocs_WindowsBrowserGapIsDocumented(t *testing.T) {
	for _, doc := range []string{"CHANGELOG.md", "docs/configuration.md"} {
		body := strings.ToLower(readRepoDoc(t, doc))
		require.Contains(t, body, "windows",
			"%s must name Windows — the browser floor there is one instance whatever the machine's RAM", doc)
		require.True(t,
			strings.Contains(body, "degraded and unsupported") || strings.Contains(body, "degraded-unsupported"),
			"%s must say the Windows browser posture is degraded and unsupported, not merely that a "+
				"reader is missing — the consequence is what an operator needs, not the cause", doc)
	}
}

// TestDocs_ContinuousDriveGapIsDocumented is FR-074.
//
// The gap: nothing is trimmed while a browser is live, so a workspace driven
// with no idle gap grows its cache without bound. Both candidate fixes are
// design changes nobody here is authorised to pick — a --disk-cache-size value
// nobody has measured, or a mid-session trim that closes a browser somebody is
// using — so the gap stands.
//
// The specific failure this guards against is the config documentation implying
// that cache_trim_interval bounds anything. It does not; it sets how often
// CLOSED profiles are swept. An operator who read it as a size limit would
// conclude their disk was safe.
func TestDocs_ContinuousDriveGapIsDocumented(t *testing.T) {
	cfgDoc := readRepoDoc(t, "docs/configuration.md")
	require.Contains(t, cfgDoc, "cache_trim_interval",
		"the config documentation must name the key")
	require.Contains(t, strings.ToLower(cfgDoc), "does not bound",
		"the documentation must say outright that cache_trim_interval does NOT bound profile size — "+
			"an operator who read it as a size limit would conclude their disk was safe")
	require.Contains(t, strings.ToLower(cfgDoc), "continuously",
		"it must name the case that escapes the trim: a workspace driven with no idle gap")

	changelog := strings.ToLower(readRepoDoc(t, "CHANGELOG.md"))
	require.Contains(t, changelog, "cache_trim_interval",
		"the release notes must carry the same limitation — this is the document an upgrader reads")
}

// TestDocs_UpgradeLogoutIsDocumented is FR-043b.
//
// No workspace inherits the old shared profiles/default/ directory, so the
// first boot after this change signs every workspace out of everything. That
// is unavoidable — the shared profile is one cookie jar and there is no
// principled way to split it between N workspaces — but it is not something a
// user should discover by being logged out.
func TestDocs_UpgradeLogoutIsDocumented(t *testing.T) {
	changelog := strings.ToLower(readRepoDoc(t, "CHANGELOG.md"))
	require.Contains(t, changelog, "logs every workspace out",
		"the release notes must say plainly that upgrading signs every workspace out once")
	require.Contains(t, changelog, "profiles/default/",
		"they must also say the old shared profile is LEFT on disk rather than deleted, so nobody "+
			"assumes their data was destroyed")
}
