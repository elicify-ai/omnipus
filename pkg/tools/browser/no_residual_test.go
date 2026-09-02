package browser

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// no_residual_test.go — the two repo-wide structural guards.
//
// Both exist for the same reason: the symbols they forbid were DELETED, not
// deprecated, and the way they come back is a merge. A branch cut before this
// change still contains the whole shared-session identity and the whole tab-cap
// family, so `git merge` re-adds them as ordinary, conflict-free additions with
// nothing failing. A guard that runs on every build is the only thing that
// notices.

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
	t.Fatal("could not locate the module root")
	return ""
}

// scanGoFiles calls visit for every .go file in the repo, INCLUDING _test.go
// files. Vendored and generated trees are skipped; nothing else is.
func scanGoFiles(t *testing.T, visit func(path string, content string)) {
	t.Helper()
	root := repoRoot(t)
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, "dist": true,
		".gitnexus": true, ".claude": true,
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// An unreadable entry is not this guard's business: skipping it
			// cannot hide a residual symbol, because a file the guard cannot
			// open is also a file the compiler cannot compile.
			return nil //nolint:nilerr // deliberate: an unreadable entry is skipped, not fatal
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil //nolint:nilerr // same rationale as the walk error above
		}
		rel, _ := filepath.Rel(root, path)
		visit(rel, string(body))
		return nil
	})
	require.NoError(t, err)
}

// TestNoResidualDefaultSessionID is FR-002b's structural half.
//
// DefaultSessionID / defaultSessionID were the single hardcoded browser
// identity every tool addressed. They are deleted. If either name reappears —
// as a constant, an alias, or a "temporary" compatibility shim — every browser
// tool in the process is once again addressing one shared tab set, and nothing
// fails: a shared browser behaves exactly like a working one right up to the
// point where two workspaces' logins are in one cookie jar.
//
// This file is its own only exception, and it is named explicitly rather than
// pattern-matched.
func TestNoResidualDefaultSessionID(t *testing.T) {
	pattern := regexp.MustCompile(`\b[Dd]efaultSessionID\b`)
	allowed := map[string]bool{
		filepath.Join("pkg", "tools", "browser", "no_residual_test.go"): true,
		filepath.Join("pkg", "tools", "browser", "testsupport_test.go"): true, // the migration helper's doc comment
		filepath.Join("docs", "internal"):                               true,
	}

	var offenders []string
	scanGoFiles(t, func(path, content string) {
		if allowed[path] {
			return
		}
		for i, line := range strings.Split(content, "\n") {
			if pattern.MatchString(line) {
				offenders = append(offenders, path+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	})

	require.Empty(t, offenders,
		"the deleted shared-session identity is back in %d place(s). It is deleted, not deprecated: "+
			"every browser tool must address sessionKey(BrowsingKey, TabOwner) instead (FR-002b).\n%s",
		len(offenders), strings.Join(offenders, "\n"))
}

// TestNoResidualTabCap is FR-059's structural half.
//
// ADR-072 D1.5a deleted EVERY browser tab counter: the per-agent courtesy cap,
// the global cross-agent budget, the in-flight reservation bookkeeping and both
// halves of the reserve/release protocol. The only limit is live memory, checked
// at each tab open (FR-060).
//
// A counter coming back is worse than it looks. It is a number that has to be
// right on every host, and — because a refusal from it names a limit — it sends
// an operator looking for a setting to raise that this build does not have.
func TestNoResidualTabCap(t *testing.T) {
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\bMaxTabs\b`),
		regexp.MustCompile(`\bMaxTotalTabs\b`),
		regexp.MustCompile(`\bmaxTotalTabs\b`),
		regexp.MustCompile(`\bTryOpenTab\b`),
		regexp.MustCompile(`\bReleaseTab\b`),
		regexp.MustCompile(`\breservedTabs\b`),
		regexp.MustCompile(`\breserveGlobalTab\b`),
		regexp.MustCompile(`\breleaseGlobalTab\b`),
		regexp.MustCompile(`\btotalOpenTabsLocked\b`),
		regexp.MustCompile(`\bSetMaxTotalTabs\b`),
		regexp.MustCompile(`\bmaxTabsReachedErr\b`),
		regexp.MustCompile(`\btabAdoptReasonMaxTabs\b`),
		regexp.MustCompile(`"max_tabs"`),
		regexp.MustCompile(`"max_total_tabs"`),
	}
	allowed := map[string]bool{
		filepath.Join("pkg", "tools", "browser", "no_residual_test.go"): true,
	}

	var offenders []string
	scanGoFiles(t, func(path, content string) {
		if allowed[path] {
			return
		}
		for i, line := range strings.Split(content, "\n") {
			for _, pat := range forbidden {
				if pat.MatchString(line) {
					offenders = append(offenders, path+":"+itoa(i+1)+": "+strings.TrimSpace(line))
					break
				}
			}
		}
	})

	require.Empty(t, offenders,
		"a browser tab counter is back in %d place(s). Every counter was deleted by ADR-072 D1.5a; "+
			"the only limit is live memory, enforced by the FR-060 gate at each tab open.\n%s",
		len(offenders), strings.Join(offenders, "\n"))
}

// TestNoResidualGuards_CanActuallySeeAFile is the guard on the guards.
//
// Both tests above pass trivially if the walk visits nothing — a wrong root, a
// too-eager skip list, a rename. That failure mode is silent and permanent, and
// it is the exact shape of false green this repo's own notes warn about, so the
// walk's reach is asserted rather than assumed.
func TestNoResidualGuards_CanActuallySeeAFile(t *testing.T) {
	seen := 0
	sawThisFile := false
	sawOutsideThisPackage := false
	scanGoFiles(t, func(path, content string) {
		seen++
		if strings.HasSuffix(path, filepath.Join("browser", "no_residual_test.go")) {
			sawThisFile = true
		}
		if strings.HasPrefix(path, filepath.Join("pkg", "gateway")+string(filepath.Separator)) {
			sawOutsideThisPackage = true
		}
	})
	require.Greater(t, seen, 500, "the repo-wide walk visited only %d .go files — it is not reaching the repo", seen)
	require.True(t, sawThisFile, "the walk must include _test.go files, including this one")
	require.True(t, sawOutsideThisPackage, "the walk must reach packages other than pkg/tools/browser")
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
