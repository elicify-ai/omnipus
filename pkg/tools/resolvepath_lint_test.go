// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — FR-034 lint deliverable (ADR-046 US-2). Modeled on
// pkg/channels/webhook_signature_test.go's AST-walk pattern.

package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// allowlistedRawFSFiles are the pkg/tools/*.go files (top-level package)
// permitted to call a raw os.Open/OpenFile/ReadFile/WriteFile/ReadDir/
// Remove/RemoveAll/MkdirAll or filepath.EvalSymlinks directly, because none
// of them is a path-taking TOOL's Execute path resolving a caller-supplied
// "path" argument without going through ResolvePath — FR-034's actual
// target.
var allowlistedRawFSFiles = map[string]string{
	"resolvepath.go": "the sanctioned I/O layer itself — PathHandle's methods " +
		"(and their host-mode branches) are the only place raw os/os.Root I/O may live.",
	"filesystem.go": "resolveAbsPath's own filepath.EvalSymlinks call, used " +
		"only by guardMetadataPath (the agents/<id>/(SOUL|HEARTBEAT|MEMORY|" +
		"AGENT).md fail-closed guard) to resolve a caller-supplied path " +
		"argument for a basename match BEFORE the real ResolvePath-backed " +
		"I/O runs — it performs no data I/O itself and cannot grant " +
		"filesystem access on its own.",
	"skills_install.go": "installs into the GLOBAL, identifier-validated " +
		"skills registry ($OMNIPUS_HOME/skills/<slug>/) — a fixed, install-" +
		"wide directory the tool's own doc comment explicitly says is " +
		"'NOT routed through ResolvePath' (ResolvePath resolves a per-turn " +
		"WorkDir; this is always the same global directory, never a caller-" +
		"supplied \"path\" argument), so there is no per-turn confinement " +
		"decision for FR-034 to enforce here at all.",
	"normalization.go": "stores TOOL-RETURNED inline base64 media at a " +
		"system-computed temp/uploads path (media.TempDir() or the session " +
		"uploads dir) — never a caller-supplied \"path\" tool argument.",
	"mcp_tool.go": "stores MCP-returned inline media at a system-computed " +
		"temp path (media.TempDir()) — same non-path-argument shape as " +
		"normalization.go.",
	"path_audit.go": "canonicalDeniedPath's EvalSymlinks call is best-effort " +
		"AUDIT-LOG string canonicalization of a path ResolvePath has ALREADY " +
		"denied — it performs no data I/O and cannot itself grant filesystem " +
		"access.",
}

// allowlistedRawFSFilesBrowser is allowlistedRawFSFiles' counterpart for
// pkg/tools/browser/*.go (item #9, ADR-046 P1 review: FR-034's AST walk
// originally scanned only the top-level pkg/tools package; browser_screenshot
// (browser/tools.go) is the one path-taking tool in this subpackage and it
// already routes through tools.ResolvePath, so recursing here adds real
// coverage rather than immediately needing a browser/tools.go entry).
var allowlistedRawFSFilesBrowser = map[string]string{
	"installer.go": "downloads and unpacks the pinned Chromium build into a " +
		"system-managed, version-keyed install directory " +
		"($OMNIPUS_HOME/browser/<version>/) driven entirely by the pinned " +
		"version string and the install root — never a caller-supplied " +
		"\"path\" tool argument.",
	"manager.go": "manages the browser's own operator-configured profile " +
		"directory (cfg.ProfileDir) lifecycle (create on start, clean on " +
		"shutdown) — never a caller-supplied \"path\" tool argument; " +
		"browser_screenshot (tools.go), the one path-taking tool in this " +
		"package, already routes through tools.ResolvePath.",
}

// bannedOSFuncs is the FR-034 banned-call list for the "os" package
// selector. Deliberately excludes Stat/Lstat (metadata-only, not data
// access) — the spec's own wording enumerates exactly these eight.
var bannedOSFuncs = map[string]bool{
	"Open":      true,
	"OpenFile":  true,
	"ReadFile":  true,
	"WriteFile": true,
	"ReadDir":   true,
	"Remove":    true,
	"RemoveAll": true,
	"MkdirAll":  true,
}

// scanDirForRawFSIO AST-walks every non-test .go file directly inside dir
// (no further recursion) that is not named in allowlist, and reports every
// raw os.<bannedOSFuncs> or filepath.EvalSymlinks call found as a FR-034
// violation string ("<file>:<line>: ...").
func scanDirForRawFSIO(t *testing.T, dir string, allowlist map[string]string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var violations []string

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, allowed := allowlist[name]; allowed {
			continue
		}

		path := filepath.Join(dir, name)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch pkgIdent.Name {
			case "os":
				if bannedOSFuncs[sel.Sel.Name] {
					pos := fset.Position(call.Pos())
					violations = append(violations, fmt.Sprintf(
						"%s:%d: direct os.%s call outside ResolvePath — route through ResolvePath+PathHandle instead",
						filepath.Base(pos.Filename), pos.Line, sel.Sel.Name,
					))
				}
			case "filepath":
				if sel.Sel.Name == "EvalSymlinks" {
					pos := fset.Position(call.Pos())
					violations = append(violations, fmt.Sprintf(
						"%s:%d: direct filepath.EvalSymlinks call outside ResolvePath — route through ResolvePath+PathHandle instead",
						filepath.Base(pos.Filename), pos.Line,
					))
				}
			}
			return true
		})
	}

	return violations
}

// TestFSTools_NoDirectFilesystemIO is the FR-034 lint deliverable: every
// path-taking tool in pkg/tools (and, per item #9 of the ADR-046 P1 review,
// pkg/tools/browser) must resolve through ResolvePath (and do I/O via the
// os.Root-backed PathHandle it returns) rather than gluing a raw
// os.Open/OpenFile/ReadFile/WriteFile/ReadDir/Remove/RemoveAll/MkdirAll or
// filepath.EvalSymlinks call directly onto a caller-supplied path — the
// exact CWE-357 TOCTOU shape ResolvePath exists to close (ADR-046 US-2).
func TestFSTools_NoDirectFilesystemIO(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	toolsDir := filepath.Dir(thisFile)
	browserDir := filepath.Join(toolsDir, "browser")

	var violations []string
	violations = append(violations, scanDirForRawFSIO(t, toolsDir, allowlistedRawFSFiles)...)
	violations = append(violations, scanDirForRawFSIO(t, browserDir, allowlistedRawFSFilesBrowser)...)

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf(
			"FR-034: path-taking tool(s) bypass the mandatory ResolvePath chokepoint:\n  %s",
			strings.Join(violations, "\n  "),
		)
	}
}
