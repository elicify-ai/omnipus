// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: S-106 (spawn guard), the tree-wide negative detector — architect
// ruling 2026-11-25 on the S-106 scan roots.
//
// Inventory membership (spawn_guard_test.go) stays on the four roots
// pkg/agent, pkg/tools, pkg/programs and pkg/channels/weixin. A child started
// elsewhere carries no program token today, because program tokens never
// enter the gateway environment; it can only get one if new code READS the
// program store and adds the value to the child's environment, and FR-048
// guards the mutation of program entries, not their reads. So the negative
// detector alone runs over all of pkg/, cmd/ and internal/: a site whose
// function composes a program-owned variable name (a literal such as
// GH_TOKEN) or reads a program-owned token through the credentials API, and
// whose launched command has no same-command programs.Attach before the
// launch, fails wherever it sits. The detector judges the evidence, never the
// file's membership in any inventory.
//
// The instruments below prove the detector can fail outside the four roots
// (planted files under pkg/mcp and cmd/, in memory and in a scratch tree that
// the real walker traverses), that its controls stay clean, that the widened
// walk reaches real launches outside the four roots, and that the one real
// production reader of a program token that starts a child is seen and
// attached. Limits are those of spawn_guard_discovery_test.go: one function
// body at a time, so a token read in one function and composed into a child's
// environment in another is not discovered here; the runtime campaigns in
// pkg/gateway observe the sinks.

package programs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// spawnTreeRoots are the roots of the negative detector: the whole production
// tree. A root absent from the checkout is skipped by the walker (internal/
// does not exist at the repository root today).
var spawnTreeRoots = []string{"pkg", "cmd", "internal"}

// spawnTreeFilter is the text a file must hold to be parsed by the tree-wide
// walk: a file that never mentions os/exec cannot construct a command.
const spawnTreeFilter = "os/exec"

// spawnTreeScan walks spawnTreeRoots (plus the given in-memory files) and
// returns every discovered child-start site.
func spawnTreeScan(t *testing.T, extraFiles [][2]string) []spawnSite {
	t.Helper()
	return spawnScanRoots(t, repoRootForSpawnGuard(t), spawnTreeRoots, spawnTreeFilter, extraFiles)
}

// spawnTreeViolations is the negative detector. A site is a violation when its
// function composes a literal program-owned variable name, or reads a
// program-owned token, and no programs.Attach of the launched command sits
// between that command's construction and its launch.
func spawnTreeViolations(sites []spawnSite) []string {
	var violations []string
	for _, s := range sites {
		if s.AttachSameCommand {
			continue
		}
		switch {
		case s.EnvProgramNameLit:
			violations = append(violations, fmt.Sprintf(
				"%s:%d (%s) composes a program variable into a child environment without a same-command programs.Attach before the launch (S-106, tree-wide)",
				s.File, s.Line, s.Func))
		case s.ReadsProgramToken:
			violations = append(violations, fmt.Sprintf(
				"%s:%d (%s) reads a program-owned token and starts a child without a same-command programs.Attach before the launch (S-106, tree-wide)",
				s.File, s.Line, s.Func))
		}
	}
	return violations
}

// The planted files live under pkg/mcp and cmd/: outside the four inventory
// roots, where only the tree-wide detector looks.
const (
	spawnTreeLiteralPlant = `package mcp

import (
	"os"
	"os/exec"
)

func plantLiteral() error {
	cmd := exec.Command("gh", "api", "user")
	cmd.Env = append(os.Environ(), "GH_TOKEN=planted-not-a-secret")
	return cmd.Run()
}
`
	spawnTreeReadPlant = `package mcp

import (
	"os"
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func plantRead(store *credentials.Store, reference string) error {
	value, _, err := credentials.ReadProgramCredential(store, reference)
	if err != nil {
		return err
	}
	cmd := exec.Command("gh", "api", "user")
	cmd.Env = append(os.Environ(), "TOKEN_FROM_STORE="+value)
	return cmd.Run()
}
`
	spawnTreeAttachedPlant = `package mcp

import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
)

func plantAttached(store *credentials.Store, reference string, set *programs.ProgramVariables) error {
	if _, _, err := credentials.ReadProgramCredential(store, reference); err != nil {
		return err
	}
	cmd := exec.Command("gh", "api", "user")
	programs.Attach(cmd, set)
	cmd.Env = append(cmd.Env, "GITHUB_TOKEN=planted-not-a-secret")
	return cmd.Run()
}
`
	spawnTreeWrongCommandPlant = `package mcp

import (
	"os"
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/programs"
)

func plantWrongCommand(set *programs.ProgramVariables) error {
	attached := exec.Command("gh", "api", "user")
	programs.Attach(attached, set)
	_ = attached.Run()

	other := exec.Command("gh", "api", "user")
	other.Env = append(os.Environ(), "GITHUB_TOKEN=planted-not-a-secret")
	return other.Run()
}
`
)

func TestProgramSpawnGuard_TreeWide(t *testing.T) {
	t.Run("the_production_tree_has_no_unattached_program_variable_or_token_read", func(t *testing.T) {
		sites := spawnTreeScan(t, nil)
		requireSitesScanned(t, sites)
		violations := spawnTreeViolations(sites)
		if len(violations) > 0 {
			t.Fatalf("S-106 (tree-wide): a child start composes a program variable or follows a program-token read without a same-command programs.Attach; violating sites: %v", violations)
		}
	})

	t.Run("a_planted_literal_env_outside_the_four_roots_fails_naming_the_file", func(t *testing.T) {
		violations := spawnTreeViolations(spawnTreeScan(t, [][2]string{{"pkg/mcp/planted_program_env.go", spawnTreeLiteralPlant}}))
		requireViolationNaming(t, violations, "pkg/mcp/planted_program_env.go",
			"a GH_TOKEN environment composed into a child under pkg/mcp with no programs.Attach")
	})

	t.Run("a_planted_token_read_outside_the_four_roots_fails_naming_the_file", func(t *testing.T) {
		// No literal name appears: the value arrives from the program store.
		violations := spawnTreeViolations(spawnTreeScan(t, [][2]string{{"pkg/mcp/planted_program_read.go", spawnTreeReadPlant}}))
		requireViolationNaming(t, violations, "pkg/mcp/planted_program_read.go",
			"a program-token read followed by a child start under pkg/mcp with no programs.Attach")
	})

	t.Run("a_planted_wrong_command_attach_outside_the_four_roots_fails_once", func(t *testing.T) {
		// The Attach of the first command blesses nothing for the second; the
		// detector keeps the per-command association the inventory rule has.
		violations := spawnTreeViolations(spawnTreeScan(t, [][2]string{{"pkg/mcp/planted_wrong_command.go", spawnTreeWrongCommandPlant}}))
		requireViolationNaming(t, violations, "pkg/mcp/planted_wrong_command.go",
			"the second command, which no Attach covers")
		var named int
		for _, v := range violations {
			if strings.HasPrefix(v, "pkg/mcp/planted_wrong_command.go:") {
				named++
			}
		}
		require.Equal(t, 1, named, "exactly one violation (the unattached command's launch), not one per launch of the function: %v", violations)
	})

	t.Run("a_planted_attached_command_outside_the_four_roots_passes", func(t *testing.T) {
		// Control: the same evidence (a token read and a GITHUB_TOKEN literal)
		// with a same-command Attach before the launch is compliant, so the
		// detector is not an always-fail.
		violations := spawnTreeViolations(spawnTreeScan(t, [][2]string{{"pkg/mcp/planted_attached.go", spawnTreeAttachedPlant}}))
		for _, v := range violations {
			require.NotContains(t, v, "pkg/mcp/planted_attached.go", "the attached plant must not be flagged: %v", violations)
		}
	})
}

func TestProgramSpawnGuard_TreeWalkInstruments(t *testing.T) {
	t.Run("the_tree_scan_reaches_known_launches_outside_the_four_roots", func(t *testing.T) {
		// Read off the source, not off the scanner: each file below constructs
		// a command and launches it in the same function (gitevidence's
		// openSystemGitWorktree, daemon's spawnProcess, the launcher TUI's main).
		seen := map[string]bool{}
		for _, s := range spawnTreeScan(t, nil) {
			seen[s.File] = true
		}
		for _, want := range []string{
			"pkg/gitevidence/isolation.go",
			"pkg/daemon/daemon_unix.go",
			"cmd/omnipus-launcher-tui/main.go",
		} {
			require.True(t, seen[want], "instrument: the tree-wide scan found no launch in %s — the widened roots are not walked", want)
		}
	})

	t.Run("the_real_token_reader_that_starts_a_child_is_seen_and_attached", func(t *testing.T) {
		// pkg/programs/check.go::runCheck reads the account's token through
		// credentials.ReadProgramCredential and starts the check child. The
		// detector's token-read arm must see that read on real code, and the
		// same-command Attach must clear it (otherwise the arm is dead or the
		// production path is unattached).
		var found int
		for _, s := range spawnTreeScan(t, nil) {
			if s.File != "pkg/programs/check.go" || s.Func != "runCheck" {
				continue
			}
			found++
			require.True(t, s.ReadsProgramToken, "runCheck reads the program token (check.go) — the read arm must see it")
			require.True(t, s.AttachSameCommand, "runCheck attaches the command it hands to the launcher")
		}
		require.Equal(t, 1, found, "runCheck holds exactly one discovered launch")
	})

	t.Run("the_real_walker_finds_a_plant_outside_the_four_roots_that_the_four_root_scan_misses", func(t *testing.T) {
		// A scratch tree with the repository's layout, written to disk and
		// walked by the same walker the guard uses. The plants sit under
		// pkg/mcp and cmd/; internal/ is absent and must be tolerated; one
		// clean attached launch sits inside a scanned inventory root.
		root := t.TempDir()
		writeScratch := func(rel, src string) {
			path := filepath.Join(root, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
		}
		writeScratch("pkg/mcp/planted_program_env.go", spawnTreeLiteralPlant)
		writeScratch("cmd/planted/main.go", strings.Replace(spawnTreeReadPlant, "package mcp", "package main", 1))
		writeScratch("pkg/agent/inroot_attached.go", spawnTreeAttachedPlant)
		writeScratch("pkg/agent/no_exec.go", "package agent\n\nfunc noExec() {}\n")

		tree := spawnScanRoots(t, root, spawnTreeRoots, spawnTreeFilter, nil)
		violations := spawnTreeViolations(tree)
		requireViolationNaming(t, violations, "pkg/mcp/planted_program_env.go", "the literal plant under pkg/mcp on disk")
		requireViolationNaming(t, violations, "cmd/planted/main.go", "the token-read plant under cmd/ on disk")
		for _, v := range violations {
			require.NotContains(t, v, "pkg/agent/inroot_attached.go", "the attached plant inside an inventory root is compliant: %v", violations)
		}

		fourRoots := spawnScanRoots(t, root, scopedSpawnRoots, "", nil)
		require.Len(t, fourRoots, 1, "the four-root scan sees only the in-root file")
		require.Equal(t, "pkg/agent/inroot_attached.go", fourRoots[0].File)
		require.Empty(t, spawnTreeViolations(fourRoots), "so the four-root scan alone cannot see either plant")
	})
}
