// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deny set must stay inside $OMNIPUS_HOME when the home is spelled through
// a symlink — the case every other fixture in this package accidentally
// excludes.
//
// # What shipped, and why nothing caught it
//
// KernelDeniedPathsFor and KernelDeniedNodesFor decide "is the work dir inside
// this root?" with isProperDescendant, which compares by FILESYSTEM IDENTITY
// (os.SameFile), and then build the chain with a purely LEXICAL filepath.Rel.
// The work dir arrives symlink-resolved (EffectiveFSPolicy calls realpath on
// it); the root is built from $OMNIPUS_HOME exactly as the operator wrote it.
// When those two spellings differ, Rel returns a "../.."-prefixed path and the
// walk ASCENDS instead of descending, denying every directory entry it meets on
// the way to "/".
//
// This is not a corner case on macOS: /tmp is a firmlink to /private/tmp and
// /var to /private/var, so any $OMNIPUS_HOME under either — including the
// t.TempDir() this test uses — triggers it. MEASURED against a real child under
// /usr/bin/sandbox-exec with OMNIPUS_HOME=/tmp/omnipus-uat-home: the rendered
// Seatbelt profile grew from ~50 KB to 663 KB / 12,184 deny rules and carried
//
//	(deny file-read* (subpath "/bin"))     (deny file-read* (subpath "/usr"))
//	(deny file-read* (subpath "/System"))  (deny file-read* (subpath "/private/var"))
//	(deny file-write* (literal "/"))       (deny file-write* (literal "/private"))
//
// Every child then failed with EPERM on paths the same profile allowed a few
// lines earlier: "sh: ls: command not found", "Error opening
// /private/var/select/sh: Operation not permitted", and "getcwd: cannot access
// parent directories" on EVERY invocation. ADR-062's blanket
// (allow file-read*) does not rescue it — per the measured precedence table in
// pkg/sandbox/seatbelt_profile.go an unfiltered blanket allow never overrides a
// filtered deny, in either order.
//
// Every existing test in this package builds its home with
// filepath.EvalSymlinks(t.TempDir()), so the declared and resolved spellings
// always agreed and the ascending walk was unreachable. That is why a bug that
// denied the entire filesystem passed a suite specifically written to assert
// this function's exactness. This test deliberately does NOT resolve the home.
func TestKernelDenies_SymlinkedHomeSpelling_StaysInsideHome(t *testing.T) {
	rawHome := t.TempDir()
	resolvedHome, err := filepath.EvalSymlinks(rawHome)
	require.NoError(t, err)
	if resolvedHome == rawHome {
		t.Skip("platform TempDir is not symlinked; this test needs a home whose spellings differ")
	}

	for _, dir := range []string{
		filepath.Join(rawHome, "agents", "self"),
		filepath.Join(rawHome, "agents", "victim"),
		filepath.Join(rawHome, "workspaces", "w1", "work"),
		filepath.Join(rawHome, "workspaces", "w2", "work"),
		filepath.Join(rawHome, "entities"),
		filepath.Join(rawHome, "system"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	for _, f := range []string{"master.key", "credentials.json", "config.json", "cli.token"} {
		require.NoError(t, os.WriteFile(filepath.Join(rawHome, f), []byte("x"), 0o600))
	}

	// Both turn shapes the product produces. home is the DECLARED spelling
	// (as an operator writes $OMNIPUS_HOME); workDir is the RESOLVED one
	// (as EffectiveFSPolicy hands it over). That mismatch is the whole point.
	shapes := []struct {
		name    string
		workDir string
	}{
		{"agent-rooted", filepath.Join(resolvedHome, "agents", "self")},
		{"workspace-rooted", filepath.Join(resolvedHome, "workspaces", "w1", "work")},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			denied, err := KernelDeniedPathsFor(rawHome, shape.workDir)
			require.NoError(t, err)
			nodes := KernelDeniedNodesFor(rawHome, shape.workDir)

			// THE INVARIANT. Every entry the kernel is told to deny must name
			// something at or under $OMNIPUS_HOME, in one spelling or the
			// other. A deny on "/", "/bin" or "/private/var" is not a stricter
			// policy — it is a broken one, and it bricks every child.
			for _, entry := range append(append([]string{}, denied...), nodes...) {
				clean := filepath.Clean(entry)
				insideHome := clean == filepath.Clean(rawHome) ||
					clean == resolvedHome ||
					strings.HasPrefix(clean, filepath.Clean(rawHome)+string(filepath.Separator)) ||
					strings.HasPrefix(clean, resolvedHome+string(filepath.Separator))
				assert.True(t, insideHome,
					"deny entry %q escapes $OMNIPUS_HOME (declared %q / resolved %q); "+
						"an ascending chain walk is denying paths outside the secret set",
					entry, rawHome, resolvedHome)
			}

			// Named explicitly, because these are the exact entries the
			// shipped bug produced and they are what made the shell unusable.
			for _, systemPath := range []string{
				"/", "/bin", "/usr", "/usr/bin", "/System", "/etc", "/dev",
				"/private", "/private/var", "/private/var/select", "/Users",
			} {
				assert.False(t, kernelDenies(t, denied, systemPath),
					"deny list covers system path %q", systemPath)
				assert.False(t, kernelDenies(t, nodes, systemPath),
					"deny NODE list covers system path %q", systemPath)
			}

			// The fix must not have bought correctness by dropping protection:
			// the secret set and the cross-agent / cross-workspace boundary
			// still have to be covered, judged the way both backends judge a
			// deny entry (on-or-under containment).
			// Coverage is judged in EITHER spelling, which is how both
			// backends judge it: pkg/sandbox's Seatbelt renderer emits every
			// denied path twice (declared and symlink-resolved), and Landlock
			// resolves a rule path when it opens it. Asserting only the
			// resolved form would fail on entries DeniedPathsFor legitimately
			// returns in the declared one.
			denies := func(list []string, rel string) bool {
				return kernelDenies(t, list, filepath.Join(rawHome, rel)) ||
					kernelDenies(t, list, filepath.Join(resolvedHome, rel))
			}
			for _, secret := range []string{
				"master.key", "credentials.json", "config.json", "cli.token", "entities",
			} {
				assert.True(t, denies(denied, secret), "secret %q is no longer denied", secret)
			}
			if shape.name == "agent-rooted" {
				assert.True(t, denies(denied, filepath.Join("agents", "victim")),
					"another agent's home is no longer denied")
			} else {
				assert.True(t, denies(denied, filepath.Join("workspaces", "w2")),
					"another workspace is no longer denied")
			}
			assert.False(t, kernelDenies(t, denied, shape.workDir),
				"the turn's own work dir must stay reachable")
		})
	}
}

// descendingComponents is the fail-closed backstop for the bug above: even if a
// caller forgets to anchor the root, a chain that would step outside it must be
// refused rather than walked. Asserted directly, because the anchoring fix and
// the backstop are independent defences and a test that only exercises the
// happy path would let the backstop rot away unnoticed.
func TestDescendingComponents_RefusesAscendingChain(t *testing.T) {
	_, err := descendingComponents("/tmp/home/agents", "/private/tmp/home/agents/self")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the root")

	got, err := descendingComponents("/tmp/home/agents", "/tmp/home/agents/self/sessions")
	require.NoError(t, err)
	assert.Equal(t, []string{"self", "sessions"}, got)
}

// chainRootFor must prefer the declared spelling, fall back to the resolved
// one, and refuse when neither descends — the three-way decision the callers
// depend on to never produce an upward walk.
func TestChainRootFor_PrefersDeclaredThenResolvedThenRefuses(t *testing.T) {
	rawHome := t.TempDir()
	resolvedHome, err := filepath.EvalSymlinks(rawHome)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(rawHome, "agents", "self"), 0o750))

	declaredRoot := filepath.Join(rawHome, "agents")
	resolvedRoot := filepath.Join(resolvedHome, "agents")

	// Declared spelling already descends -> used as-is.
	got, err := chainRootFor(declaredRoot, filepath.Join(rawHome, "agents", "self"))
	require.NoError(t, err)
	assert.Equal(t, declaredRoot, got)

	if resolvedHome != rawHome {
		// Declared does not descend, resolved does -> resolved is used. This
		// is the production case on macOS.
		got, err = chainRootFor(declaredRoot, filepath.Join(resolvedHome, "agents", "self"))
		require.NoError(t, err)
		assert.Equal(t, resolvedRoot, got)
	}

	// Neither descends -> refuse, so the caller fails closed instead of
	// walking upward.
	_, err = chainRootFor(declaredRoot, filepath.Join(t.TempDir(), "elsewhere"))
	require.Error(t, err)
}
