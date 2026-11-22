// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramSpawnGuard (order 8, Unit + Integration) — S-106:
// no child gets program variables without masking.
//
// Part (a) is a source guard over the packages that compose agent
// environments, mirroring the S-111 producer-guard instrument. It scans the
// production tree (non-test Go files, recursively under every scoped root —
// CHECK F10) for child-start sites (exec.Command/exec.CommandContext,
// resolved through the file's own import table so an aliased os/exec is
// caught), and reports EVERY site regardless of violations: the classification
// (S1 mandatory / non-program / unlisted) runs on the full enumeration, so a
// clean site in an unlisted file still fails the guard. An S1 mandatory
// site's violation is per command (CHECK F10): the enclosing function must
// call programs.Attach BEFORE the launch position — one late or absent Attach
// per function is not compliance. The negative controls feed in-memory
// test-only files (the repository is never modified): a literal GH_TOKEN
// composition without Attach, an INDIRECT composition (the variable name not
// a literal), and a clean exec.Command in an unlisted file — each must fail
// naming its file.
//
// Part (b) runs every child kind of the current slice's mandatory inventory
// (bash foreground, bash background, God Mode, environment_setup, account
// check, git version probe) with the D-9 sentinel through its real harness
// and asserts the sentinel reaches no sink. The env-injection seam (the
// agent environment builder handing program variables to each kind's
// harness, FR-011) does not exist at RED: each kind reports BLOCKED naming
// its seam, never a skip.
//
// Oracle: I-1 (Attach is the only way to add program variables to a child;
// one masking writer per output stream in the same call) and S-106. The
// spawn-site table's file rows were read off THIS tree (site inventory), not
// off any behaviour; the mandatory/non-program classification comes from the
// spec's S1 list. GREEN's new sites (account check, git version probe) ride
// the pending rows and are reconciled by the same test author.

package programs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// spawnSiteRow is one row of the spawn-site table: the file the site lives
// in (repo-relative, forward slashes) and its classification.
type spawnSiteRow struct {
	File string
	Kind string // "mandatory-s1" or "non-program"
	Note string
}

// spawnSiteTable is the spawn-site inventory (S-106's Given). S1 mandatory:
// bash foreground/background/God Mode (the shell runner and its background
// runner), environment_setup. The account check and the git version probe
// are S1 NEW code: their rows are pending until GREEN creates the sites,
// and the reconciliation adds their file paths then. Everything else found
// in the scoped packages today is a non-program site (drivers, hooks,
// probes, worktree git, taskkill, channel media decoder, browser).
var spawnSiteTable = []spawnSiteRow{
	// S1 mandatory, existing sites:
	{"pkg/tools/shell.go", "mandatory-s1", "bash foreground and God Mode share the shell runner"},
	{"pkg/tools/shell_bg.go", "mandatory-s1", "bash background"},
	{"pkg/tools/environment_setup_runner.go", "mandatory-s1", "environment_setup"},
	// S1 mandatory, sites GREEN creates (pending reconciliation):
	{"pkg/programs/check_account.go", "mandatory-s1", "account check (GREEN new site)"},
	{"pkg/programs/check_version.go", "mandatory-s1", "git version probe (GREEN new site)"},
	// Non-program sites found in the scoped packages today:
	{"pkg/agent/hook_process.go", "non-program", "lifecycle hooks carry no program variables"},
	{"pkg/agent/runner/driver_stream.go", "non-program", "driver version probe"},
	{"pkg/agent/runner/driver_claude.go", "non-program", "third-party worker driver"},
	{"pkg/agent/runner/driver_opencode.go", "non-program", "third-party worker driver"},
	{"pkg/agent/runner/driver_codex.go", "non-program", "third-party worker driver"},
	{"pkg/agent/runner/conntest.go", "non-program", "connection test"},
	{"pkg/agent/runner/worktree.go", "non-program", "worktree git operations"},
	{"pkg/agent/testutil/home_isolation.go", "non-program", "test helper (go env)"},
	{"pkg/tools/session_process_windows.go", "non-program", "taskkill"},
	{"pkg/tools/browser/exec_resolver.go", "non-program", "browser version probe"},
	{"pkg/tools/browser/manager_session_startup.go", "non-program", "browser version probe"},
	{"pkg/tools/browser/cdppipe/allocator.go", "non-program", "chrome allocation"},
	{"pkg/channels/weixin/media.go", "non-program", "channel media decoder"},
}

// scopedSpawnRoots are the packages that compose agent environments (the
// guard's scan roots, repo-relative; walked RECURSIVELY — every subpackage
// under a root is scanned, so a site cannot hide in a subdirectory).
var scopedSpawnRoots = []string{
	"pkg/agent", "pkg/tools", "pkg/programs", "pkg/channels/weixin",
}

// programTokenVarNames are the program-owned variable names the guard
// recognises in an environment composition: the spec preset table's
// token_variables for github (the S1 preset data), plus the D-9 sentinel
// prefix. GREEN reads the same names from the loaded catalog.
var programTokenVarNames = []string{"GH_TOKEN", "GITHUB_TOKEN", "sentinel_9f3c2a7e5b1d"}

// spawnSite is one discovered child-start site, whether or not it violates
// the table or the Attach rule (CHECK F10: the enumeration is complete —
// classification happens on the full list, never on violations alone).
type spawnSite struct {
	File               string // repo-relative, forward slashes
	Line               int    // the launch call's line
	Func               string // the enclosing function's name
	AttachBeforeLaunch bool   // a programs.Attach call precedes this launch in the same function
	EnvProgramNameLit  bool   // a literal program-token name appears in the function
	Mandatory          bool   // the file is an S1 mandatory row of the table
}

// omnipusImportPrefix is the module's import-path prefix.
const spawnOmnipusPrefix = "github.com/elicify-ai/omnipus"

// spawnImportAliases maps a file's local qualifier to the omnipus or std
// package dir suffix it imports (e.g. `bb` → /pkg/bus, `sh` → /os/exec).
// Only these two trees matter to the guard.
func spawnImportAliases(f *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var suffix string
		if i := strings.Index(path, spawnOmnipusPrefix+"/pkg/"); i >= 0 {
			suffix = path[i+len(spawnOmnipusPrefix):] // "/pkg/programs"
		} else if path == "os/exec" || strings.HasSuffix(path, "/os/exec") {
			suffix = "/os/exec"
		} else {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			name = path[strings.LastIndex(path, "/")+1:]
		}
		aliases[name] = suffix
	}
	return aliases
}

// spawnScan walks every scoped root recursively (skipping test files and the
// repository's non-Go trees) and returns every child-start site found.
// extraFiles (label, source pairs) are scanned as additional files labelled
// by their label.
func spawnScan(t *testing.T, root string, extraFiles [][2]string) []spawnSite {
	t.Helper()
	var sites []spawnSite
	fset := token.NewFileSet()
	scanOne := func(label, src string) {
		fileSites := scanFileForSpawnSites(t, fset, label, src)
		sites = append(sites, fileSites...)
	}
	for _, dir := range scopedSpawnRoots {
		pkgDir := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Stat(pkgDir); err != nil {
			continue // root absent from this checkout
		}
		requireWalk := filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", "dist", "spa", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return fmt.Errorf("instrument: read %s: %w", path, rerr)
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			scanOne(filepath.ToSlash(rel), string(src))
			return nil
		})
		if requireWalk != nil {
			t.Fatalf("instrument: walk %s: %v", dir, requireWalk)
		}
	}
	for _, pair := range extraFiles {
		scanOne(pair[0], pair[1])
	}
	return sites
}

// scanFileForSpawnSites parses one file and returns EVERY child-start site:
// one per exec.Command/exec.CommandContext call whose qualifier resolves to
// os/exec through the file's import table. Attach-before-launch is judged
// per site: a programs.Attach call in the same enclosing function at an
// earlier position.
func scanFileForSpawnSites(t *testing.T, fset *token.FileSet, file, src string) []spawnSite {
	t.Helper()
	f, perr := parser.ParseFile(fset, file, src, 0)
	if perr != nil {
		t.Fatalf("instrument: parse %s: %v", file, perr)
	}
	aliases := spawnImportAliases(f)

	qualifierSuffix := func(x ast.Expr) string {
		if id, ok := x.(*ast.Ident); ok {
			return aliases[id.Name]
		}
		return ""
	}
	isExecStart := func(call *ast.CallExpr) bool {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
			return false
		}
		return qualifierSuffix(sel.X) == "/os/exec"
	}
	isAttach := func(call *ast.CallExpr) bool {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name != "Attach" {
			return false
		}
		return qualifierSuffix(sel.X) == "/pkg/programs"
	}
	mandatory := func() bool {
		for _, row := range spawnSiteTable {
			if row.File == file && row.Kind == "mandatory-s1" {
				return true
			}
		}
		return false
	}()

	var sites []spawnSite
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		var attachPositions []token.Pos
		envProgramNameLit := false
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			switch node := m.(type) {
			case *ast.CallExpr:
				if isAttach(node) {
					attachPositions = append(attachPositions, node.Pos())
				}
			case *ast.BasicLit:
				if node.Kind == token.STRING {
					for _, name := range programTokenVarNames {
						if strings.Contains(node.Value, name) {
							envProgramNameLit = true
						}
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok || !isExecStart(call) {
				return true
			}
			before := false
			for _, p := range attachPositions {
				if p < call.Pos() {
					before = true
					break
				}
			}
			sites = append(sites, spawnSite{
				File:               file,
				Line:               fset.Position(call.Pos()).Line,
				Func:               fn.Name.Name,
				AttachBeforeLaunch: before,
				EnvProgramNameLit:  envProgramNameLit,
				Mandatory:          mandatory,
			})
			return true
		})
		return true
	})
	return sites
}

// spawnClassify returns the guard's violations for a full site enumeration:
// unlisted files, mandatory sites without a preceding programs.Attach, and
// any site whose function composes a literal program-token name without a
// preceding Attach (I-1: Attach is the only way program variables reach a
// child).
func spawnClassify(sites []spawnSite) []string {
	var violations []string
	listed := map[string]bool{}
	for _, row := range spawnSiteTable {
		listed[row.File] = true
	}
	seenUnlisted := map[string]bool{}
	for _, s := range sites {
		if !listed[s.File] && !seenUnlisted[s.File] {
			seenUnlisted[s.File] = true
			violations = append(violations,
				fmt.Sprintf("unlisted child-start site %s (%s) — add it to the spawn-site table (S-106)", s.File, s.Func))
			continue
		}
		if s.EnvProgramNameLit && !s.AttachBeforeLaunch {
			violations = append(violations,
				fmt.Sprintf("%s:%d (%s) composes a program variable into a child environment without a preceding programs.Attach", s.File, s.Line, s.Func))
			continue
		}
		if s.Mandatory && !s.AttachBeforeLaunch {
			violations = append(violations,
				fmt.Sprintf("%s:%d (%s) does not attach program variables through a preceding programs.Attach", s.File, s.Line, s.Func))
		}
	}
	return violations
}

func TestProgramSpawnGuard(t *testing.T) {
	t.Run("a_every_mandatory_site_attaches_through_programs_Attach", func(t *testing.T) {
		root := repoRootForSpawnGuard(t)
		sites := spawnScan(t, root, nil)
		requireSitesScanned(t, sites)
		violations := spawnClassify(sites)
		if len(violations) > 0 {
			t.Fatalf("S-106/I-1: every S1 mandatory child kind must add program variables only via a preceding programs.Attach (and install the masking writers in the same call); violating sites: %v", violations)
		}
	})

	t.Run("a_planted_env_without_attach_fails_naming_the_file", func(t *testing.T) {
		// Instrument check (S-106): the same guard run over the tree PLUS a
		// test-only file that adds GH_TOKEN to an exec.Cmd environment
		// without programs.Attach must fail naming that file.
		testOnly := `package main

import (
	"os"
	"os/exec"
)

func plant() {
	cmd := exec.Command("gh", "api", "user")
	cmd.Env = append(os.Environ(), "GH_TOKEN=sentinel_9f3c2a7e5b1d_planted")
	_ = cmd.Run()
}
`
		root := repoRootForSpawnGuard(t)
		violations := spawnClassify(spawnScan(t, root, [][2]string{{"<test-only>/planted.go", testOnly}}))
		requireViolationNaming(t, violations, "<test-only>/planted.go",
			"the planted exec.Cmd environment with GH_TOKEN and no programs.Attach")
	})

	t.Run("a_planted_indirect_env_fails_naming_the_file", func(t *testing.T) {
		// Instrument check (CHECK F10): the token variable name is NOT a
		// literal (it arrives through a variable), so the literal scan cannot
		// see it — the guard must still fail, naming the file, through the
		// unlisted-site rule.
		testOnly := `package main

import (
	"os"
	"os/exec"
)

var tokenVarName = os.Getenv("TOKEN_VAR_NAME")

func plantIndirect() {
	cmd := exec.Command("gh", "api", "user")
	cmd.Env = append(os.Environ(), tokenVarName+"=value")
	_ = cmd.Run()
}
`
		root := repoRootForSpawnGuard(t)
		violations := spawnClassify(spawnScan(t, root, [][2]string{{"<test-only>/indirect.go", testOnly}}))
		requireViolationNaming(t, violations, "<test-only>/indirect.go",
			"the planted indirect env composition (unlisted site)")
	})

	t.Run("a_planted_clean_unlisted_site_fails_naming_the_file", func(t *testing.T) {
		// Instrument check (CHECK F10): the enumeration is complete — a CLEAN
		// exec.Command (no program names, no violations) in a file that is
		// not in the table still fails the guard: the classification runs on
		// the full enumeration, never on violations alone.
		testOnly := `package main

import "os/exec"

func plantClean() error {
	return exec.Command("go", "version").Run()
}
`
		root := repoRootForSpawnGuard(t)
		violations := spawnClassify(spawnScan(t, root, [][2]string{{"<test-only>/clean.go", testOnly}}))
		requireViolationNaming(t, violations, "<test-only>/clean.go",
			"the planted clean exec.Command in an unlisted file")
	})

	// Part (b): every child kind of the current slice's mandatory inventory
	// with the sentinel, asserting the sentinel reaches no sink. Each kind's
	// harness needs the env-injection seam (the agent environment builder
	// handing program variables to the kind, FR-011 runtime); where the seam
	// is missing the kind reports BLOCKED — never a skip.
	const s106Seam = "the agent environment builder cannot hand program variables to this child kind (FR-011 runtime attach) — required by S-106(b)"
	t.Run("b_bash_foreground_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the bash foreground harness has no program-variable seam yet", s106Seam)
	})
	t.Run("b_bash_background_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the bash background harness has no program-variable seam yet", s106Seam)
	})
	t.Run("b_bash_god_mode_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the God Mode shell path has no program-variable seam yet", s106Seam)
	})
	t.Run("b_environment_setup_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the environment_setup runner has no program-variable seam yet", s106Seam)
	})
	t.Run("b_account_check_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the account check (I-3/FR-005) is not implemented yet", s106Seam)
	})
	t.Run("b_git_version_probe_sentinel_reaches_no_sink", func(t *testing.T) {
		t.Fatalf("BLOCKED: %s; the git version probe (FR-014) is not implemented yet", s106Seam)
	})
}

// requireSitesScanned proves the enumeration is not trivially empty (a guard
// that scans nothing passes nothing).
func requireSitesScanned(t *testing.T, sites []spawnSite) {
	t.Helper()
	if len(sites) == 0 {
		t.Fatal("instrument: the scan found no child-start sites — the scoped roots are wrong or the walk failed")
	}
}

// requireViolationNaming asserts the violations name the wanted file.
func requireViolationNaming(t *testing.T, violations []string, want, what string) {
	t.Helper()
	for _, v := range violations {
		if strings.Contains(v, want) {
			return
		}
	}
	t.Fatalf("instrument check: %s must fail the guard naming %s; violations: %v", what, want, violations)
}

// repoRootForSpawnGuard locates the repository root from this test file's
// location (same instrument as the S-111 producer guard).
func repoRootForSpawnGuard(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("instrument: cannot locate this test file")
	}
	// thisFile is <root>/pkg/programs/spawn_guard_test.go
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("instrument: repository root not found from %s: %v", thisFile, err)
	}
	return root
}
