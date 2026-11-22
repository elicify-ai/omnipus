// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramSpawnGuard (order 8, Unit + Integration) — S-106:
// no child gets program variables without masking.
//
// Part (a) is a source guard over the packages that compose agent
// environments, mirroring the S-111 producer-guard instrument: it scans the
// production tree (non-test Go files) for child-start sites
// (exec.Command/exec.CommandContext), classifies each against the spawn-site
// table (S1 mandatory / non-program / S2-later), and fails on any unlisted
// site. An S1 mandatory site must compose the child environment through
// programs.Attach in its enclosing function — at RED no site does, so the
// guard fails naming each file. The negative control feeds an in-memory
// test-only file that adds GH_TOKEN to an exec.Cmd environment without
// Attach; the guard must fail naming that file (spec S-106's planted-file
// run, in-memory so the repository is never modified).
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

// scopedSpawnPackages are the packages that compose agent environments (the
// guard's scan roots, repo-relative).
var scopedSpawnPackages = []string{
	"pkg/agent", "pkg/agent/runner", "pkg/tools", "pkg/tools/browser",
	"pkg/tools/browser/cdppipe", "pkg/programs", "pkg/channels/weixin",
}

// programTokenVarNames are the program-owned variable names the guard
// recognises in an environment composition: the spec preset table's
// token_variables for github (the S1 preset data), plus the D-9 sentinel
// prefix. GREEN reads the same names from the loaded catalog.
var programTokenVarNames = []string{"GH_TOKEN", "GITHUB_TOKEN", "sentinel_9f3c2a7e5b1d"}

// spawnScan walks the scoped packages and reports, for every child-start
// site found, the repo-relative file, whether the enclosing function calls
// programs.Attach, and whether the env composition mentions a program-owned
// name. extraSource is an optional in-memory file injected as
// <test-only>/planted.go (the negative control's instrument).
type spawnSiteFinding struct {
	File       string
	AttachCall bool
	PlantedEnv bool
}

func spawnScan(t *testing.T, root string, extraSource string) []spawnSiteFinding {
	t.Helper()
	var findings []spawnSiteFinding
	fset := token.NewFileSet()
	for _, dir := range scopedSpawnPackages {
		pkgDir := filepath.Join(root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			continue // package absent from this checkout
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(pkgDir, name)
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("instrument: read %s: %v", path, rerr)
			}
			file := dir + "/" + name
			fileFindings := scanFileForSpawnSites(t, fset, file, string(src))
			findings = append(findings, fileFindings...)
		}
	}
	if extraSource != "" {
		fileFindings := scanFileForSpawnSites(t, fset, "<test-only>/planted.go", extraSource)
		findings = append(findings, fileFindings...)
	}
	return findings
}

// scanFileForSpawnSites parses one file and reports one finding per
// child-start call whose enclosing function composes an environment with a
// program-owned name without programs.Attach, plus a finding per
// child-start call in a mandatory file that does not call programs.Attach
// at all.
func scanFileForSpawnSites(t *testing.T, fset *token.FileSet, file, src string) []spawnSiteFinding {
	t.Helper()
	var findings []spawnSiteFinding
	f, perr := parser.ParseFile(fset, file, src, 0)
	if perr != nil {
		if strings.HasPrefix(file, "<test-only>") {
			return []spawnSiteFinding{{File: file, PlantedEnv: true}}
		}
		t.Fatalf("instrument: parse %s: %v", file, perr)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		attach := false
		envProgramNames := false
		starts := 0
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			switch call := m.(type) {
			case *ast.CallExpr:
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Attach" {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "programs" {
						attach = true
					}
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
					(sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "exec" {
						starts++
					}
				}
			case *ast.BasicLit:
				if call.Kind == token.STRING {
					for _, name := range programTokenVarNames {
						if strings.Contains(call.Value, name) {
							envProgramNames = true
						}
					}
				}
			}
			return true
		})
		if starts == 0 {
			return true
		}
		mandatory := false
		for _, row := range spawnSiteTable {
			if row.File == file && row.Kind == "mandatory-s1" {
				mandatory = true
			}
		}
		if envProgramNames && !attach {
			findings = append(findings, spawnSiteFinding{File: file, PlantedEnv: true})
			return true
		}
		if mandatory && !attach {
			findings = append(findings, spawnSiteFinding{File: file})
		}
		return true
	})
	return findings
}

func TestProgramSpawnGuard(t *testing.T) {
	t.Run("a_every_mandatory_site_attaches_through_programs_Attach", func(t *testing.T) {
		root := repoRootForSpawnGuard(t)
		findings := spawnScan(t, root, "")
		// Classify: every scanned site must be in the table.
		for _, dir := range scopedSpawnPackages {
			pkgDir := filepath.Join(root, filepath.FromSlash(dir))
			entries, err := os.ReadDir(pkgDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
					continue
				}
				file := dir + "/" + e.Name()
				hasStart := false
				for _, f := range findings {
					if f.File == file {
						hasStart = true
					}
				}
				if !hasStart {
					continue // no child-start site in this file
				}
				listed := false
				for _, row := range spawnSiteTable {
					if row.File == file {
						listed = true
					}
				}
				if !listed {
					t.Errorf("unlisted child-start site %q — add it to the spawn-site table (S-106)", file)
				}
			}
		}
		// The S1 mandatory sites must compose their environment through
		// programs.Attach (I-1). RED: none does.
		var violations []string
		for _, f := range findings {
			if f.PlantedEnv {
				violations = append(violations, f.File+" composes a program variable into a child environment without programs.Attach")
				continue
			}
			violations = append(violations, f.File+" does not attach program variables through programs.Attach")
		}
		if len(violations) > 0 {
			t.Fatalf("S-106/I-1: every S1 mandatory child kind must add program variables only via programs.Attach (and install the masking writers in the same call); violating sites: %v", violations)
		}
	})

	t.Run("a_planted_env_without_attach_fails_naming_the_file", func(t *testing.T) {
		// Instrument check (S-106): the same guard run over the tree PLUS a
		// test-only file that adds GH_TOKEN to an exec.Cmd environment
		// without programs.Attach must fail naming that file. In-memory; the
		// repository is never modified.
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
		findings := spawnScan(t, root, testOnly)
		found := false
		for _, f := range findings {
			if f.File == "<test-only>/planted.go" && f.PlantedEnv {
				found = true
			}
		}
		if !found {
			t.Fatalf("instrument check: the planted exec.Cmd environment with GH_TOKEN and no programs.Attach must fail the guard naming <test-only>/planted.go; findings: %v", findings)
		}
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
