//go:build darwin || linux

package preview

// Oracles over the accepted test seam (spec §3.4 D8d ruling): a converter
// start's captured SandboxPolicy and its post-wrap command line. Everything
// here reads what the seam captured — never a policy or profile the test
// builds — and computes expected values independently from those real paths.

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// seatbeltProfileOf returns the profile text of the captured start: the
// argument after `-p` on the post-wrap command line
// (`sandbox-exec -p <profile> -- …`), the exact profile this start used.
func seatbeltProfileOf(t *testing.T, cs converterStart) string {
	t.Helper()
	for i := 1; i < len(cs.args); i++ {
		if cs.args[i] == "-p" && i+1 < len(cs.args) {
			return cs.args[i+1]
		}
	}
	require.Fail(t, "T-11/T-102: the captured start carries no -p profile argument",
		"args: %v", cs.args)
	return ""
}

// previewLookupParentFolders computes, independently of any production code,
// every folder above each listed folder of the captured policy (filesystem
// rules and unix-socket rules alike), from the real (symlink-resolved) path
// of each rule, up to but not including `/` (spec §3.4 D8d). Sorted, deduped.
func previewLookupParentFolders(t *testing.T, policy sandbox.SandboxPolicy) []string {
	t.Helper()
	seen := map[string]struct{}{}
	consider := func(path string) {
		resolved, err := filepath.EvalSymlinks(path)
		require.NoError(t, err, "T-102/T-11: a listed folder of the captured policy must exist to compute its parents: %s", path)
		for cur := resolved; cur != "/" && cur != "." && cur != string(filepath.Separator); cur = filepath.Dir(cur) {
			seen[cur] = struct{}{}
		}
	}
	for _, rule := range policy.FilesystemRules {
		consider(rule.Path)
	}
	for _, rule := range policy.UnixSocketRules {
		consider(rule.Path)
	}
	parents := make([]string, 0, len(seen))
	for path := range seen {
		parents = append(parents, path)
	}
	sort.Strings(parents)
	return parents
}

// previewAllowLine is one parsed `(allow …)` line of a rendered profile.
type previewAllowLine struct {
	verb string // file-read*, file-read-metadata, file-write*, process-exec, network-bind, network-outbound, mach-lookup
	path string // the quoted path token, unquoted; "" for blanket lines
	raw  string
}

// previewQuotedPath returns the first complete quoted token of rest,
// unquoted, or "" when there is none.
func previewQuotedPath(rest string) string {
	open := strings.Index(rest, "\"")
	if open < 0 {
		return ""
	}
	for end := open + 1; end < len(rest); end++ {
		if rest[end] == '\\' {
			end++
			continue
		}
		if rest[end] == '"' {
			path, err := strconv.Unquote(rest[open : end+1])
			if err != nil {
				return ""
			}
			return path
		}
	}
	return ""
}

// previewParseAllows scans a rendered profile into its allow lines. Deny
// lines, comments and the preamble's fixed lines are not allows and are
// skipped; the mach-lookup count is taken from the raw text separately.
func previewParseAllows(profile string) []previewAllowLine {
	var lines []previewAllowLine
	for _, raw := range strings.Split(profile, "\n") {
		raw = strings.TrimSpace(raw)
		if !strings.HasPrefix(raw, "(allow ") {
			continue
		}
		rest := strings.TrimPrefix(raw, "(allow ")
		verb := rest
		for i := 0; i < len(rest); i++ {
			if rest[i] == ' ' || rest[i] == '(' || rest[i] == ')' {
				verb = strings.TrimSpace(rest[:i])
				break
			}
		}
		lines = append(lines, previewAllowLine{verb: verb, path: previewQuotedPath(rest), raw: raw})
	}
	return lines
}

// previewAllowLineExact returns the exact rendered line the spec names for a
// look-up folder: `(allow file-read-metadata (literal "<folder>"))`, quoted
// as the renderer quotes paths.
func previewAllowLineExact(folder string) string {
	return "(allow file-read-metadata (literal " + strconv.Quote(folder) + "))"
}

// previewWithinDir reports whether path lies inside dir (both resolved).
func previewWithinDir(dir, path string) bool {
	if dir == path {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
