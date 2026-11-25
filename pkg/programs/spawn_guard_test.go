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
// CHECK F10) for child-start sites, regardless of violations. Classification
// is per SITE (CHECK R06): each table row lists the launches its function
// holds, so a launch added to a listed function fails; a row no discovered
// launch matches is flagged as vacuous; helper-split flows are discovered
// (spawn_guard_discovery_test.go); and the program-variable-without-Attach
// rule also runs over the whole tree (spawn_guard_tree_test.go).
//
// A site is the LAUNCH of a command constructed from exec.Command or
// exec.CommandContext (resolved through the file's own import table, so an
// aliased os/exec is caught; direct chains exec.Command(...).Run() count
// too). The launch is the call that actually starts the child: a
// Start/Run/Output/CombinedOutput method on the constructed command, or the
// command handed to a known start sink — the sandbox's StartLocked /
// StartLockedWithPolicy or the tree's own startGodFloorChild wrapper over
// them (this tree's mandatory runners launch through the sandbox, CHECK
// R06: ordering is checked against the launch, not the construction). The
// Attach rule is per COMMAND: the launch is compliant only when a programs.Attach call
// whose first argument is that same command variable sits between the
// command's construction and the launch — an Attach of a different command
// blesses nothing, and an Attach after the launch blesses nothing.
// Same-package bare Attach calls (this package's own composition code) are
// recognised alongside the imported alias.
//
// The site table (file, function) rows were read off THIS tree with an
// independent enumeration, not off any behaviour; the mandatory set comes
// from the spec's S1 list. The account check's site is runCheck, which
// attaches and then hands its command to the launcher startCurrentCheck. The
// git version probe starts through sandbox.Run, which builds its own command
// from an argv, so no command is constructed in the scanned code and it has
// no row here; its program environment comes only from the limits' Programs,
// attached inside pkg/sandbox, and the runtime campaign in pkg/gateway
// observes its sinks.
//
// The negative controls feed in-memory test-only files (the repository is
// never modified): the clean constructor→Attach→launch shape must be
// recognised per command; a wrong-command Attach, a late Attach, an
// unattached indirect composition and a new site in a listed non-program
// file must each fail or record non-compliance as asserted. Part (b) runs
// every child kind of the current slice's mandatory inventory (bash
// foreground, bash background, God Mode, environment_setup, account check,
// git version probe) with the D-9 sentinel through its real harness and
// asserts the sentinel reaches no sink. Foreground/background share the actual
// seven-sink campaign in pkg/gateway::TestProgramMasking_ActualSevenSinks;
// actual God Mode uses its TestProgramMasking_ActualGodModeSinks companion.
// Account check runs here. Setup and git-version-probe use their actual
// TestProgramMasking_ActualOtherChildSinks campaigns in pkg/gateway. These
// observe all seven sinks, not merely token delivery or a source guard.
//
// Oracle: I-1 (Attach is the only way to add program variables to a child;
// one masking writer per output stream in the same call) and S-106.

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

	"github.com/stretchr/testify/require"
)

// spawnSiteRow is one row of the spawn-site table: the exact site (file and
// enclosing function, repo-relative forward-slash file) and its
// classification. Classification is per site — a function not listed here
// that launches a child is unlisted no matter how clean its file is, and a
// listed function must hold exactly the launches its row lists.
type spawnSiteRow struct {
	File     string
	Func     string
	Kind     string // "mandatory-s1", "non-program" or "launcher"
	Launches int    // how many launches the function holds (the per-site inventory, S-106/R06)
	Note     string
}

// spawnSiteTable is the spawn-site inventory (S-106's Given). S1 mandatory:
// bash foreground/background/God Mode (the shell runner and its background
// runner), environment_setup, the account check. The git version probe starts
// through sandbox.Run, which builds its own command, so it has no site here
// (the runtime campaign in pkg/gateway observes it). Everything else found in
// the scoped packages is a non-program site (drivers, hooks, probes, worktree
// git, taskkill, channel media decoder, browser) or a launcher helper that
// starts a command its caller attached — enumerated site by site, with the
// number of launches each function holds, read off the tree function by
// function (not off the scanner's output).
var spawnSiteTable = []spawnSiteRow{
	// S1 mandatory sites:
	{"pkg/tools/shell.go", "runUnconstrained", "mandatory-s1", 2, "bash foreground and God Mode share the shell runner (the closure start and the sandbox locked start)"},
	{"pkg/tools/shell_bg.go", "runBackground", "mandatory-s1", 2, "bash background (the God-floor wrapper and the sandbox locked start)"},
	{"pkg/tools/environment_setup_runner.go", "startSession", "mandatory-s1", 2, "environment_setup (its two sandbox locked starts)"},
	{"pkg/programs/check.go", "runCheck", "mandatory-s1", 1, "account check (FR-005/S-106): attaches, then hands the command to startCurrentCheck"},
	// Launcher helpers: they start a command their caller constructed and
	// attached, so the callers' sites above carry the Attach check.
	{"pkg/programs/check.go", "startCurrentCheck", "launcher", 1, "starts the account check's command (runCheck attached it)"},
	{"pkg/tools/shell_bg.go", "startGodFloorChild", "launcher", 1, "God-floor start of the background command (runBackground attached it)"},
	// Non-program sites (this tree's inventory):
	{"pkg/agent/hook_process.go", "NewProcessHook", "non-program", 1, "lifecycle hooks carry no program variables"},
	{"pkg/agent/runner/driver_stream.go", "detectCLIVersion", "non-program", 1, "driver version probe"},
	{"pkg/agent/runner/driver_claude.go", "Run", "non-program", 1, "third-party worker driver"},
	{"pkg/agent/runner/driver_opencode.go", "Run", "non-program", 1, "third-party worker driver"},
	{"pkg/agent/runner/driver_codex.go", "Run", "non-program", 1, "third-party worker driver"},
	{"pkg/agent/runner/conntest.go", "probeVersion", "non-program", 1, "connection test"},
	{"pkg/agent/runner/worktree.go", "runGit", "non-program", 1, "worktree git operations"},
	{"pkg/agent/testutil/home_isolation.go", "IsolateProcessHome", "non-program", 1, "test helper (go env)"},
	{"pkg/tools/session_process_windows.go", "killProcessGroup", "non-program", 1, "taskkill"},
	{"pkg/tools/browser/exec_resolver.go", "chromeMajorVersion", "non-program", 1, "browser version probe"},
	{"pkg/tools/browser/manager_session_startup.go", "probeChromiumBinaryWithTimeout", "non-program", 1, "browser version probe"},
	{"pkg/tools/browser/cdppipe/allocator.go", "start", "non-program", 1, "chrome allocation"},
	{"pkg/channels/weixin/media.go", "tryTranscodeSilkToWAV", "non-program", 1, "channel media decoder"},
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

// spawnLaunchMethods are the exec.Cmd methods that START the child. A call
// to one of these on a constructed command is the site (CHECK R06: the
// launch, not the construction, is what must be preceded by the Attach).
var spawnLaunchMethods = map[string]bool{
	"Start": true, "Run": true, "Output": true, "CombinedOutput": true,
}

// spawnSinkSelectors are the sandbox start wrappers this tree actually
// launches children through (read off pkg/sandbox/hardened_exec.go's API —
// StartLocked and StartLockedWithPolicy take the *exec.Cmd). The constructor
// alone is not a site: the sink call that starts the child is.
var spawnSinkSelectors = map[string]bool{
	"StartLocked": true, "StartLockedWithPolicy": true,
}

// spawnLocalStartWrappers are the scanned tree's own launcher helpers: the
// functions the table lists with kind "launcher" (the God-floor start of the
// background runner, the account check's start). A caller that hands such a
// helper its constructed command has launched it; a helper added to the tree
// is an unlisted site until its row says so, and the row then makes its
// callers visible. One list, not two.
var spawnLocalStartWrappers = spawnLauncherNames(spawnSiteTable)

// spawnSite is one discovered child-start site, whether or not it violates
// the table or the Attach rule. Classification sees all discovered sites,
// but discovery is intraprocedural and not a complete typed call graph.
type spawnSite struct {
	File              string // repo-relative, forward slashes
	Line              int    // the launch call's line
	Func              string // the enclosing function's name
	AttachSameCommand bool   // programs.Attach(<this command>) sits between its construction and this launch
	EnvProgramNameLit bool   // a literal program-token name appears in the function
	ReadsProgramToken bool   // the function reads a program-owned token through the credentials API
	Mandatory         bool   // a mandatory-s1 table row matches (file, function)
}

// spawnOmnipusPrefix is the module's import-path prefix.
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
		var name string
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
	return spawnScanRoots(t, root, scopedSpawnRoots, "", extraFiles)
}

// spawnScanRoots is spawnScan over an explicit list of roots. When
// onlyContaining is not empty, a file whose source lacks that text is skipped
// without being parsed (the tree-wide scan skips every file that cannot
// construct a command, because no site needs a file that never imports
// os/exec). A root absent from the checkout is skipped.
func spawnScanRoots(t *testing.T, root string, roots []string, onlyContaining string, extraFiles [][2]string) []spawnSite {
	t.Helper()
	var sites []spawnSite
	fset := token.NewFileSet()
	scanOne := func(label, src string) {
		fileSites := scanFileForSpawnSites(t, fset, label, src)
		sites = append(sites, fileSites...)
	}
	for _, dir := range roots {
		pkgDir := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Stat(pkgDir); err != nil {
			continue // root absent from this checkout
		}
		walkErr := filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
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
			if onlyContaining != "" && !strings.Contains(string(src), onlyContaining) {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			scanOne(filepath.ToSlash(rel), string(src))
			return nil
		})
		if walkErr != nil {
			t.Fatalf("instrument: walk %s: %v", dir, walkErr)
		}
	}
	for _, pair := range extraFiles {
		scanOne(pair[0], pair[1])
	}
	return sites
}

// spawnCtor records one construction of a command variable: the variable's
// name and the constructor call's position. A later construction of the same
// variable starts a new span (the earlier Attach cannot bless it).
type spawnCtor struct {
	pos token.Pos
}

// scanFileForSpawnSites parses one file for same-function child-start sites:
// one per launch (Start/Run/Output/CombinedOutput) of a command constructed
// from exec.Command/exec.CommandContext in the same enclosing function —
// including direct chains exec.Command(...).Run(). Attach-before-launch is
// judged PER COMMAND: a programs.Attach whose first argument is that
// command's variable, positioned between the construction and the launch.
// Same-package bare Attach calls count (this package's own code).
func scanFileForSpawnSites(t *testing.T, fset *token.FileSet, file, src string) []spawnSite {
	t.Helper()
	f, perr := parser.ParseFile(fset, file, src, 0)
	if perr != nil {
		t.Fatalf("instrument: parse %s: %v", file, perr)
	}
	aliases := spawnImportAliases(f)
	samePackage := f.Name != nil && f.Name.Name == "programs"

	// The recognisers live in spawn_guard_discovery_test.go so the parameter
	// pass and this one cannot drift apart.
	isExecCtor := func(call *ast.CallExpr) bool { return spawnIsExecCtor(aliases, call) }
	attachVar := func(call *ast.CallExpr) string { return spawnAttachVar(aliases, samePackage, call) }
	isSinkCall := func(call *ast.CallExpr) bool { return spawnIsSinkCall(aliases, call) }
	mandatory := func(funcName string) bool { return spawnIsMandatory(file, funcName) }

	var sites []spawnSite
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		ctors := map[string][]spawnCtor{}
		attaches := map[string][]token.Pos{} // every Attach, even of untracked vars
		envProgramNameLit := false
		type launchEvent struct {
			cmdVar string
			pos    token.Pos
		}
		var launches []launchEvent

		ast.Inspect(fn.Body, func(m ast.Node) bool {
			switch node := m.(type) {
			case *ast.AssignStmt:
				// cmd, cmd2 := exec.Command(...), exec.CommandContext(...)
				// and single assignments alike: each RHS constructor starts
				// a span for the ident it names.
				if len(node.Lhs) == len(node.Rhs) && (node.Tok == token.DEFINE || node.Tok == token.ASSIGN) {
					for i, rhs := range node.Rhs {
						call, ok := rhs.(*ast.CallExpr)
						if !ok || !isExecCtor(call) {
							continue
						}
						lhs, ok := node.Lhs[i].(*ast.Ident)
						if !ok {
							continue
						}
						ctors[lhs.Name] = append(ctors[lhs.Name], spawnCtor{pos: call.Pos()})
					}
				}
			case *ast.CallExpr:
				if v := attachVar(node); v != "" {
					attaches[v] = append(attaches[v], node.Pos())
				}
				// Direct method launch: cmd.Start()/Run()/Output()/CombinedOutput().
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && spawnLaunchMethods[sel.Sel.Name] {
					if id, ok := sel.X.(*ast.Ident); ok && len(ctors[id.Name]) > 0 {
						launches = append(launches, launchEvent{cmdVar: id.Name, pos: node.Pos()})
					}
				}
				// Sink launch: the constructed command is handed to a known
				// starting sink (the sandbox's locked starts, or the tree's
				// own wrapper over them).
				if isSinkCall(node) {
					for _, arg := range node.Args {
						if id, ok := arg.(*ast.Ident); ok && len(ctors[id.Name]) > 0 {
							launches = append(launches, launchEvent{cmdVar: id.Name, pos: node.Pos()})
						}
					}
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

		// A launch whose constructor is outside this function is currently
		// OMITTED. A constructor-only helper emits no site either. The separate
		// helper-split boundary test blocks any completeness claim (R06).
		for _, launch := range launches {
			varName := launch.cmdVar
			// the construction this launch belongs to: the latest one before it
			var ctor *spawnCtor
			list := ctors[varName]
			for i := range list {
				if list[i].pos < launch.pos {
					ctor = &list[i]
				}
			}
			attached := false
			if ctor != nil {
				for _, a := range attaches[varName] {
					if ctor.pos < a && a < launch.pos {
						attached = true
						break
					}
				}
			}
			sites = append(sites, spawnSite{
				File:              file,
				Line:              fset.Position(launch.pos).Line,
				Func:              fn.Name.Name,
				AttachSameCommand: attached,
				EnvProgramNameLit: envProgramNameLit,
				Mandatory:         mandatory(fn.Name.Name),
			})
		}
		// Direct chains exec.Command(...).Run(): the selector's X is the
		// constructor call itself — one site, attachable only before the
		// chain (nothing can precede it in the same expression).
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !spawnLaunchMethods[sel.Sel.Name] {
				return true
			}
			inner, ok := sel.X.(*ast.CallExpr)
			if !ok || !isExecCtor(inner) {
				return true
			}
			sites = append(sites, spawnSite{
				File:              file,
				Line:              fset.Position(call.Pos()).Line,
				Func:              fn.Name.Name,
				AttachSameCommand: false, // nothing can Attach inside the chain
				EnvProgramNameLit: envProgramNameLit,
				Mandatory:         mandatory(fn.Name.Name),
			})
			return true
		})
		return true
	})
	return spawnFinishSites(fset, file, f, aliases, samePackage, sites)
}

// spawnClassify returns the guard's violations for a site enumeration:
// unlisted file+function pairs (a new function in a listed file is unlisted),
// listed functions that hold more or fewer launches than their row lists
// (spawnCountViolations: a second launch inherits no exemption), mandatory
// sites whose launch has no same-command Attach between construction and
// launch, and any site whose function composes a literal program-token name
// without that Attach (I-1: Attach is the only way program variables reach a
// child).
func spawnClassify(sites []spawnSite) []string {
	var violations []string
	listed := map[[2]string]bool{}
	for _, row := range spawnSiteTable {
		listed[[2]string{row.File, row.Func}] = true
	}
	seenUnlisted := map[[2]string]bool{}
	for _, s := range sites {
		key := [2]string{s.File, s.Func}
		if !listed[key] && !seenUnlisted[key] {
			seenUnlisted[key] = true
			violations = append(violations,
				fmt.Sprintf("unlisted child-start site %s:%d (%s) — add it to the spawn-site table (S-106)", s.File, s.Line, s.Func))
			continue
		}
		if s.EnvProgramNameLit && !s.AttachSameCommand {
			violations = append(violations,
				fmt.Sprintf("%s:%d (%s) composes a program variable into a child environment without a same-command programs.Attach before the launch", s.File, s.Line, s.Func))
			continue
		}
		if s.Mandatory && !s.AttachSameCommand {
			violations = append(violations,
				fmt.Sprintf("%s:%d (%s) does not attach program variables through a same-command programs.Attach before the launch", s.File, s.Line, s.Func))
		}
	}
	return append(violations, spawnCountViolations(sites)...)
}

func TestProgramSpawnGuard(t *testing.T) {
	t.Run("a_every_mandatory_site_attaches_through_programs_Attach", func(t *testing.T) {
		root := repoRootForSpawnGuard(t)
		sites := spawnScan(t, root, nil)
		requireSitesScanned(t, sites)
		violations := append(spawnClassify(sites), spawnStaleRows(sites)...)
		if len(violations) > 0 {
			t.Fatalf("S-106/I-1: every S1 mandatory child kind must add program variables only via a same-command programs.Attach before the launch (and install the masking writers in the same call); violating sites: %v", violations)
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

	t.Run("a_planted_new_site_in_listed_non_program_file_fails", func(t *testing.T) {
		// Instrument check (CHECK R06): classification is per SITE — a new
		// child in a previously classified non-program file inherits NO
		// exemption: the file is listed but this function is not, and the
		// guard fails it.
		testOnly := `package main

import "os/exec"

func plantNewChildInListedNonProgramFile() {
	_ = exec.Command("go", "version").Run()
}
`
		root := repoRootForSpawnGuard(t)
		violations := spawnClassify(spawnScan(t, root, [][2]string{{"pkg/agent/hook_process.go", testOnly}}))
		requireViolationNaming(t, violations, "plantNewChildInListedNonProgramFile",
			"the planted new site in the listed non-program file pkg/agent/hook_process.go (the file-level exemption is gone)")
	})

	// ── Per-command Attach association (CHECK R06): unit-level site records ──

	t.Run("a_clean_constructor_attach_launch_is_recognised", func(t *testing.T) {
		// The correct shape — construct, Attach THAT command, launch — must
		// be recorded as compliant (the old any-Attach ordering falsely
		// rejected it; the new record proves the association).
		src := `package main

import (
	"os/exec"

	programs "github.com/elicify-ai/omnipus/pkg/programs"
)

func cleanAttach(set *programs.ProgramVariables) error {
	cmd := exec.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	return cmd.Run()
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.True(t, sites[0].AttachSameCommand,
			"the clean constructor→Attach→launch shape must record a same-command Attach before the launch")
	})

	t.Run("a_wrong_command_attach_blesses_nothing", func(t *testing.T) {
		// An Attach of a DIFFERENT command must not bless the unattached
		// command's launch.
		src := `package main

import (
	"os/exec"

	programs "github.com/elicify-ai/omnipus/pkg/programs"
)

func wrongCommand(set *programs.ProgramVariables) {
	attached := exec.Command("gh", "api", "user")
	programs.Attach(attached, set)
	_ = attached.Run()

	unmasked := exec.Command("gh", "api", "user")
	_ = unmasked.Run()
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 2)
		// Source order: the attached command launches first (lower line).
		sortSitesByLine(sites)
		require.True(t, sites[0].AttachSameCommand,
			"the attached command's launch must record its same-command Attach (line %d)", sites[0].Line)
		require.False(t, sites[1].AttachSameCommand,
			"the Attach of the OTHER command must bless nothing (line %d)", sites[1].Line)
	})

	t.Run("a_late_attach_blesses_nothing", func(t *testing.T) {
		// Attach AFTER the launch is not compliance.
		src := `package main

import (
	"os/exec"

	programs "github.com/elicify-ai/omnipus/pkg/programs"
)

func lateAttach(set *programs.ProgramVariables) {
	cmd := exec.Command("gh", "api", "user")
	_ = cmd.Run()
	programs.Attach(cmd, set)
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.False(t, sites[0].AttachSameCommand,
			"an Attach after the launch must not satisfy the ordering")
	})

	t.Run("a_same_package_bare_attach_is_recognised", func(t *testing.T) {
		// This package's own composition code attaches bare (no import
		// alias) — the association must still hold.
		src := `package programs

import "os/exec"

func ownComposition(set *ProgramVariables) error {
	cmd := exec.Command("gh", "api", "user")
	Attach(cmd, set)
	return cmd.Run()
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.True(t, sites[0].AttachSameCommand,
			"a same-package bare Attach must be recognised")
	})

	t.Run("a_all_launch_methods_are_recognised", func(t *testing.T) {
		// Start, Output and CombinedOutput are launches too — a guard that
		// only knew Run would miss them.
		src := `package main

import (
	"os/exec"

	programs "github.com/elicify-ai/omnipus/pkg/programs"
)

func withStart(set *programs.ProgramVariables) error {
	cmd := exec.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	return cmd.Start()
}

func withOutput(set *programs.ProgramVariables) ([]byte, error) {
	cmd := exec.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	return cmd.Output()
}

func withCombinedOutput(set *programs.ProgramVariables) ([]byte, error) {
	cmd := exec.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	return cmd.CombinedOutput()
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 3)
		for _, s := range sites {
			require.True(t, s.AttachSameCommand,
				"every launch method must be recognised as a site with its same-command Attach (line %d)", s.Line)
		}
	})

	t.Run("a_sandbox_sink_launch_is_recognised", func(t *testing.T) {
		// The tree's mandatory runners launch through the sandbox's locked
		// starts, not cmd.Start — a guard that only knew the direct methods
		// would miss the very rows it exists for.
		src := `package tools

import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func sinkLaunch(lim sandbox.Limits) error {
	cmd := exec.Command("gh", "api", "user")
	return sandbox.StartLockedWithPolicy(cmd, lim.KernelPolicy)
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.False(t, sites[0].AttachSameCommand,
			"this plant attaches nothing — the sink launch must still be a site")
	})

	t.Run("a_aliased_os_exec_is_caught", func(t *testing.T) {
		// An aliased os/exec import is resolved through the file's own import
		// table (CHECK F10 keeps this plant).
		src := `package main

import (
	exe "os/exec"

	programs "github.com/elicify-ai/omnipus/pkg/programs"
)

func aliasedExec(set *programs.ProgramVariables) {
	cmd := exe.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	_ = cmd.Run()
}
`
		sites := scanOneSpawnSource(t, src)
		require.Len(t, sites, 1)
		require.True(t, sites[0].AttachSameCommand,
			"the aliased os/exec constructor must still be tracked")
	})

	// Part (b): every mandatory kind needs actual execution and real sink
	// observations, not just a source guard. These two former marker rows are
	// relocated, not removed: b_bash_foreground_sentinel_reaches_no_sink and
	// b_bash_background_sentinel_reaches_no_sink both map to
	// pkg/gateway::TestProgramMasking_ActualSevenSinks/S48_every_sink_value_free.
	// That single real registered-child campaign covers each kind's success
	// and error paths, rotation and all seven sinks, including nonvacuous
	// transcript/model/published-output assertions. Do not replace its real
	// persistence failure with empty passing wrappers here.
	// b_bash_god_mode_sentinel_reaches_no_sink is relocated separately to
	// pkg/gateway::TestProgramMasking_ActualGodModeSinks/
	// S106_God_Mode_every_sink_value_free. That actual campaign requires both
	// runtime and registered-shell God Mode before exercising all seven sinks;
	// it is never inferred from the ordinary-mode companion.
	// b_environment_setup_sentinel_reaches_no_sink and
	// b_git_version_probe_sentinel_reaches_no_sink are relocated to
	// pkg/gateway::TestProgramMasking_ActualOtherChildSinks, with exact leaves
	// S106_environment_setup_every_sink_value_free and
	// S106_git_version_probe_every_sink_value_free. The registered setup child
	// really runs/read/callbacks; the git command triggers the actual FR014
	// probe before its child. Both observe all seven real sinks. No empty
	// passing wrapper or ordinary-bash surrogate discharges these rows.
	t.Run("b_account_check_sentinel_reaches_no_sink", func(t *testing.T) {
		assertAccountCheckSinks(t)
	})
}

// scanOneSpawnSource parses one in-memory source as its own file and returns
// its sites (unit-level controls never touch the tree or the table).
func scanOneSpawnSource(t *testing.T, src string) []spawnSite {
	t.Helper()
	return scanFileForSpawnSites(t, token.NewFileSet(), "<test-only>/unit.go", src)
}

// sortSitesByLine orders sites by their launch line (site records are
// visited in source order; the sort makes that explicit for assertions).
func sortSitesByLine(sites []spawnSite) {
	for i := 1; i < len(sites); i++ {
		for j := i; j > 0 && sites[j].Line < sites[j-1].Line; j-- {
			sites[j], sites[j-1] = sites[j-1], sites[j]
		}
	}
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
