// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Shared fixture for the #920 read-boundary RED pack
// (docs/internal/specs/read-boundary-consistency-spec.md).
//
// The layout is the spec's BDD Background, verbatim:
//
//   - agent "A" in workspace "W" with working folder <WS> and one mount named
//     `docs-mount` whose host folder is <MNT>;
//   - a folder <EXT> outside <WS>, <MNT> and $OMNIPUS_HOME, containing
//     `ext/notes.txt` with the line "needle outside";
//   - <WS>/notes/a.md and <MNT>/src/b.md, each containing "needle";
//   - every expected path is a REALPATH derived at run time (macOS /tmp is
//     /private/tmp), never hard-coded.
//
// The three reading tools are registered through a REAL ToolRegistry that
// carries a REAL audit logger (FR-020): no logger is ever set by hand on a
// tool here, so a grep that does not satisfy auditLoggerAware writes no
// audit row and the audit assertions go red for exactly that reason.
//
// Every expected value in the tests that use this fixture comes from the
// spec's datasets (DS-1..DS-5) and scenarios (S-x.y) or the interview
// decisions D1-D12 — never from running the current code.
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const (
	rbWorkspaceID = "ws-rb-w"
	rbAgentID     = "agent-rb-a"
	rbMountName   = "docs-mount"

	// rbSearchRootsEvent is the event name the spec fixes (Ambiguity
	// Warning 2, MV-3). Written as a literal on purpose: the production
	// constant does not exist yet, and referencing it would break the
	// compilation of the whole pkg/tools test binary.
	rbSearchRootsEvent = "path.search_roots"
	// rbAccessDeniedEvent is path_audit.go's existing event (MV-2).
	rbAccessDeniedEvent = "path.access_denied"
)

// rbFixture is one isolated spec Background.
type rbFixture struct {
	home string // realpath of $OMNIPUS_HOME
	ws   string // realpath of <WS>
	mnt  string // realpath of <MNT>
	ext  string // realpath of <EXT>

	ctx  context.Context
	reg  *ToolRegistry
	grep *GrepTool
	read *ReadFileTool
	list *ListDirTool

	auditLog  *audit.Logger
	auditDir  string
	rowsTaken bool
}

func rbRealpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return r
}

func rbWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
}

// newRBFixture builds the Background. The audit logger is attached to the
// registry BEFORE the tools are registered (Register's propagation path);
// TestGrepTool_RegistryWiresAuditLogger covers the after-registration path.
func newRBFixture(t *testing.T) *rbFixture {
	t.Helper()
	homeRaw := t.TempDir()
	t.Setenv(config.EnvHome, homeRaw)

	workRaw := seedGrepWorkspace(t, homeRaw, rbWorkspaceID, rbAgentID)
	mntRaw := t.TempDir()
	if _, _, err := workspace.CreateMount(homeRaw, rbWorkspaceID, rbMountName, mntRaw); err != nil {
		t.Fatalf("create mount %q: %v", rbMountName, err)
	}
	extRaw := t.TempDir()

	rbWrite(t, filepath.Join(workRaw, "notes", "a.md"), "needle\n")
	rbWrite(t, filepath.Join(mntRaw, "src", "b.md"), "needle\n")
	rbWrite(t, filepath.Join(extRaw, "ext", "notes.txt"), "needle outside\n")

	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	f := &rbFixture{
		home:     rbRealpath(t, homeRaw),
		ws:       rbRealpath(t, workRaw),
		mnt:      rbRealpath(t, mntRaw),
		ext:      rbRealpath(t, extRaw),
		ctx:      WithTurnWorkspaceDir(WithAgentID(context.Background(), rbAgentID), workRaw),
		reg:      NewToolRegistry(),
		grep:     NewGrepTool(workRaw, true),
		read:     NewReadFileTool(workRaw, true, MaxReadFileSize),
		list:     NewListDirTool(workRaw, true),
		auditLog: logger,
		auditDir: auditDir,
	}
	f.reg.SetAuditLogger(logger)
	f.reg.Register(f.grep)
	f.reg.Register(f.read)
	f.reg.Register(f.list)
	t.Cleanup(func() {
		if !f.rowsTaken {
			_ = f.auditLog.Close()
		}
	})
	return f
}

// rows closes the audit logger (flush) and returns every row. One call per
// fixture: a second call would read a closed logger.
func (f *rbFixture) rows(t *testing.T) []auditRow {
	t.Helper()
	if f.rowsTaken {
		t.Fatal("rbFixture.rows called twice — build a fresh fixture per audited call sequence")
	}
	f.rowsTaken = true
	if _, err := os.Stat(filepath.Join(f.auditDir, "audit.jsonl")); errors.Is(err, os.ErrNotExist) {
		_ = f.auditLog.Close()
		return nil
	}
	return readAuditRows(t, f.auditLog, f.auditDir)
}

func rbRowsFor(rows []auditRow, event, tool string) []auditRow {
	var out []auditRow
	for _, r := range rows {
		if r.Event == event && (tool == "" || r.Tool == tool) {
			out = append(out, r)
		}
	}
	return out
}

// grepCall runs grep with pattern "needle" and, when pathArg is non-nil,
// the given `path` argument.
func (f *rbFixture) grepCall(pattern string, pathArg *string) *ToolResult {
	args := map[string]any{"pattern": pattern}
	if pathArg != nil {
		args["path"] = *pathArg
	}
	return f.grep.Execute(f.ctx, args)
}

func rbStr(s string) *string { return &s }

// rbSlash is the spec's absolute match-path form: forward slashes on every
// platform (FR-004).
func rbSlash(p string) string { return filepath.ToSlash(p) }

var (
	rbContentHitLine = regexp.MustCompile(`^(.+?):(\d+):  `)
	rbTruncatedLine  = regexp.MustCompile(`truncated: true \(reason: ([a-z_]+)`)
)

// rbHitPaths returns the distinct match paths in a rendered grep result, in
// first-seen order — both content hits ("path:line:  excerpt") and name
// hits ("path  (name match)").
func rbHitPaths(out string) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, "  (name match)") {
			add(strings.TrimSuffix(line, "  (name match)"))
			continue
		}
		if m := rbContentHitLine.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
	}
	return paths
}

func rbSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func rbSameSet(got, want []string) bool {
	g, w := rbSorted(got), rbSorted(want)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

// rbTruncation returns the engine's truncation reason, "" when the result
// says "truncated: false".
func rbTruncation(out string) string {
	if m := rbTruncatedLine.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// rbStringSlice decodes a JSON array detail into []string ("" entries for
// non-strings, so a wrong type still fails a comparison loudly).
func rbStringSlice(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, _ := e.(string)
		out[i] = s
	}
	return out, true
}
