// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests for the workspace mount lifecycle (spec FR-5 through FR-8, ADR-061
// D4/D6). Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'Mount' -p 1 ./pkg/workspace/

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testWorkspaceSeq gives every newTestWorkspace call a unique id, even when
// called more than once from the SAME test (t.Name() alone is identical for
// every call within one (sub)test, which would otherwise silently collide
// two "different" test workspaces onto one file).
var testWorkspaceSeq atomic.Uint64

// newTestWorkspace seeds a minimal, valid workspace record under home and
// returns its id.
func newTestWorkspace(t *testing.T, home string) string {
	t.Helper()
	seq := testWorkspaceSeq.Add(1)
	id := sanitizeForID(t.Name()) + "-" + strconv.FormatUint(seq, 10)
	now := time.Now().UTC().Format(time.RFC3339)
	ws := Workspace{ID: id, Name: "Test", Status: "active", CreatedAt: now, UpdatedAt: now}
	require.NoError(t, saveWorkspaceRecord(home, ws))
	return id
}

func sanitizeForID(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// FR-5.2 — name validation and collisions
// ---------------------------------------------------------------------------

func TestValidateMountName(t *testing.T) {
	cases := []struct {
		name    string
		valid   bool
		wantErr error
	}{
		{name: "client-repo", valid: true},
		{name: "a", valid: true},
		{name: "", valid: false, wantErr: ErrInvalidMountName},
		{name: "..", valid: false, wantErr: ErrInvalidMountName},
		{name: "../escape", valid: false, wantErr: ErrInvalidMountName},
		{name: "foo/bar", valid: false, wantErr: ErrInvalidMountName},
		{name: `foo\bar`, valid: false, wantErr: ErrInvalidMountName},
		{name: "foo..bar", valid: false, wantErr: ErrInvalidMountName},
		{name: ".", valid: false, wantErr: ErrInvalidMountName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMountName(tc.name)
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, tc.wantErr), "got %v, want wrapping %v", err, tc.wantErr)
		})
	}
}

// TestCreateMount_NameCollisions is test-dataset #7/#8/#9: a traversal name
// is rejected, a name colliding with an existing work/ entry is rejected,
// and creating the same mount name twice is rejected.
func TestCreateMount_NameCollisions(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()

	t.Run("#7 traversal name rejected", func(t *testing.T) {
		_, _, err := CreateMount(home, id, "../escape", target)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrInvalidMountName))
	})

	t.Run("#8 collides with existing work/ entry", func(t *testing.T) {
		workDir, err := EnsureWorkDir(home, id)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(workDir, "already-here.txt"), []byte("x"), 0o600))

		_, _, err = CreateMount(home, id, "already-here.txt", target)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrMountNameCollision), "got %v", err)
	})

	t.Run("#9 two mounts, same name", func(t *testing.T) {
		otherTarget := t.TempDir()
		_, _, err := CreateMount(home, id, "dupe", target)
		require.NoError(t, err)

		_, _, err = CreateMount(home, id, "dupe", otherTarget)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrMountNameCollision), "got %v", err)
	})
}

// ---------------------------------------------------------------------------
// FR-5.3/FR-5.4 — realpath resolution and symlink materialization
// ---------------------------------------------------------------------------

func TestCreateMount_MaterializesSymlinkToResolvedPath(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)

	// target itself contains a symlinked component so realpath-resolution is
	// actually exercised (FR-5.3: "resolved via realpath at creation time —
	// the resolved form is what is stored").
	realTarget := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(realTarget, "hello.txt"), []byte("hi"), 0o600))
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "alias")
	require.NoError(t, os.Symlink(realTarget, alias))

	m, warning, err := CreateMount(home, id, "proj", alias)
	require.NoError(t, err)
	require.Empty(t, warning)
	require.Equal(t, "proj", m.Name)

	resolvedRealTarget, err := filepath.EvalSymlinks(realTarget)
	require.NoError(t, err)
	require.Equal(t, resolvedRealTarget, m.HostPath, "HostPath must be the REALPATH-resolved form, not the raw alias path")

	workDir, err := SafeWorkDir(home, id)
	require.NoError(t, err)
	linkPath := filepath.Join(workDir, "proj")
	fi, err := os.Lstat(linkPath)
	require.NoError(t, err, "work/proj must exist")
	require.True(t, fi.Mode()&os.ModeSymlink != 0, "work/proj must be a symlink (FR-5.4)")

	// Read through the symlink and confirm it really reaches the operator's
	// content — the mount is functionally live, not just recorded.
	data, err := os.ReadFile(filepath.Join(linkPath, "hello.txt"))
	require.NoError(t, err)
	require.Equal(t, "hi", string(data))

	// The record persisted to disk carries the same resolved HostPath.
	loaded, ok := LoadMounts(home, id)
	require.True(t, ok)
	require.Len(t, loaded, 1)
	require.Equal(t, m, loaded[0])
}

// ---------------------------------------------------------------------------
// FR-7.5 — $OMNIPUS_HOME refusal, including the symlink form (#11/#11a/#12)
// ---------------------------------------------------------------------------

func TestCheckMountTarget_RefusesOmnipusHome(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "entities", "agents"), 0o700))

	t.Run("#11 target IS omnipus home", func(t *testing.T) {
		_, _, err := CheckMountTarget(home, home)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrMountRefused))
	})

	t.Run("#11a target INSIDE omnipus home (entities/)", func(t *testing.T) {
		_, _, err := CheckMountTarget(filepath.Join(home, "entities"), home)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrMountRefused))
	})

	t.Run("#12 target is a SYMLINK to omnipus home", func(t *testing.T) {
		parent := t.TempDir()
		sneaky := filepath.Join(parent, "sneaky")
		require.NoError(t, os.Symlink(home, sneaky))

		_, _, err := CheckMountTarget(sneaky, home)
		require.Error(t, err, "a symlink to $OMNIPUS_HOME must be refused too (FR-7.5) — the form nobody reaches for directly is the one that matters")
		require.True(t, errors.Is(err, ErrMountRefused))
	})

	t.Run("CreateMount end-to-end refuses the symlink form", func(t *testing.T) {
		id := newTestWorkspace(t, home)
		parent := t.TempDir()
		sneaky := filepath.Join(parent, "sneaky2")
		require.NoError(t, os.Symlink(home, sneaky))

		_, _, err := CreateMount(home, id, "sneaky-mount", sneaky)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrMountRefused))

		// Nothing must have been materialized or persisted on the refusal
		// path.
		workDir := WorkDir(home, id)
		_, statErr := os.Lstat(filepath.Join(workDir, "sneaky-mount"))
		require.True(t, os.IsNotExist(statErr), "refused mount must not leave a symlink on disk")
		mounts, _ := LoadMounts(home, id)
		require.Empty(t, mounts)
	})
}

// ---------------------------------------------------------------------------
// FR-7.6/FR-7.4 — warn and allow for $HOME / broad targets (#13/#14)
// ---------------------------------------------------------------------------

func TestCheckMountTarget_WarnsAndAllowsBroadTargets(t *testing.T) {
	// omnipusHome nested under a fake "$HOME" — mirrors the real default
	// install shape (~/.omnipus).
	fakeHome := t.TempDir()
	omnipusHome := filepath.Join(fakeHome, ".omnipus")
	require.NoError(t, os.MkdirAll(omnipusHome, 0o700))

	t.Run("#13 target is $HOME (contains omnipus home)", func(t *testing.T) {
		resolved, warning, err := CheckMountTarget(fakeHome, omnipusHome)
		require.NoError(t, err, "must NOT be refused (operator decision: warn and allow applies to all but the omnipus directory)")
		require.NotEmpty(t, warning, "must warn — mounting a target that contains $OMNIPUS_HOME")
		resolvedFakeHome, evalErr := filepath.EvalSymlinks(fakeHome)
		require.NoError(t, evalErr)
		require.Equal(t, resolvedFakeHome, resolved)
	})

	if runtime.GOOS == "windows" {
		t.Skip("#14 (mount target '/') is POSIX-specific; Windows has no single filesystem root")
	}
	t.Run("#14 target is / (contains omnipus home, and is canonically broad)", func(t *testing.T) {
		resolved, warning, err := CheckMountTarget("/", omnipusHome)
		require.NoError(t, err, "/ must NOT be refused")
		require.NotEmpty(t, warning, "/ must warn")
		require.Equal(t, "/", resolved)
	})
}

// TestCreateMount_HomeGrantsWriteButOmnipusHomeStaysProtected is the
// CreateMount-level companion to pkg/fspolicy's
// TestIsCarveOut_IgnoresAllowedRoots (the structural FR-7.7 proof) — this
// confirms the MOUNT ITSELF materializes normally for a $HOME-shaped target
// (warn-and-allow, FR-7.6) while never touching anything under
// $OMNIPUS_HOME on disk.
func TestCreateMount_HomeGrantsWriteButOmnipusHomeStaysProtected(t *testing.T) {
	// fakeHome stands in for the operator's real $HOME; omnipusHome — a
	// child of it, matching the real default install shape (~/.omnipus) —
	// is what CreateMount is actually given as its `home` (=$OMNIPUS_HOME)
	// parameter, exactly as production wires it (the workspace store root
	// and the refusal/warning check's anchor are the SAME value).
	fakeHome := t.TempDir()
	omnipusHome := filepath.Join(fakeHome, ".omnipus")
	require.NoError(t, os.MkdirAll(omnipusHome, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(omnipusHome, "master.key"), []byte("secret"), 0o600))

	id := newTestWorkspace(t, omnipusHome)

	m, warning, err := CreateMount(omnipusHome, id, "home-mount", fakeHome)
	require.NoError(t, err)
	require.NotEmpty(t, warning, "mounting a target that CONTAINS $OMNIPUS_HOME must warn (FR-7.6)")

	resolvedFakeHome, evalErr := filepath.EvalSymlinks(fakeHome)
	require.NoError(t, evalErr)
	require.Equal(t, resolvedFakeHome, m.HostPath)

	// The grant is real: master.key is reachable THROUGH the mount symlink
	// at the plain filesystem level (no policy enforced at this layer) —
	// this is exactly why fspolicy's own carve-out check must (and, per the
	// sibling test, does) ignore AllowedRoots entirely.
	workDir, err := SafeWorkDir(omnipusHome, id)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(workDir, "home-mount", ".omnipus", "master.key"))
	require.NoError(t, err)
	require.Equal(t, "secret", string(data))
}

// ---------------------------------------------------------------------------
// FR-8.2/FR-8.3/FR-8.5 — broken mounts are inert, never recreated, never
// silently re-bound
// ---------------------------------------------------------------------------

func TestMountStatus_BrokenTargetNeverRecreated(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()

	m, _, err := CreateMount(home, id, "gone", target)
	require.NoError(t, err)
	require.Equal(t, "ok", MountStatus(m))

	// Simulate the target vanishing (unplugged drive, deleted folder,
	// restored on another machine — FR-8.2/FR-8.5).
	require.NoError(t, os.RemoveAll(target))

	require.Equal(t, "broken", MountStatus(m))

	// FR-8.3: NEVER silently recreated as an empty directory.
	_, statErr := os.Stat(target)
	require.True(t, os.IsNotExist(statErr), "a broken mount's target must NEVER be silently recreated")

	// Calling MountStatus again must not change that.
	require.Equal(t, "broken", MountStatus(m))
	_, statErr = os.Stat(target)
	require.True(t, os.IsNotExist(statErr))
}

// TestMountStatus_NeverSilentlyRebound is #24: a same-named path existing
// elsewhere (or even recreated at a DIFFERENT location that happens to
// share the mount's leaf name) must never cause the mount to silently
// re-point at it. HostPath is immutable once stored; only the live status
// check changes.
func TestMountStatus_NeverSilentlyRebound(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)

	targetParent := t.TempDir()
	target := filepath.Join(targetParent, "myproj")
	require.NoError(t, os.Mkdir(target, 0o700))

	m, _, err := CreateMount(home, id, "proj", target)
	require.NoError(t, err)
	originalHostPath := m.HostPath

	require.NoError(t, os.RemoveAll(target))
	require.Equal(t, "broken", MountStatus(m))

	// A DIFFERENT directory elsewhere on disk that happens to share the leaf
	// name "myproj" — this must have zero effect on the stored mount.
	otherParent := t.TempDir()
	otherSameLeafName := filepath.Join(otherParent, "myproj")
	require.NoError(t, os.Mkdir(otherSameLeafName, 0o700))

	loaded, ok := LoadMounts(home, id)
	require.True(t, ok)
	require.Len(t, loaded, 1)
	require.Equal(t, originalHostPath, loaded[0].HostPath, "HostPath must NEVER change on its own — FR-8.5 forbids silent re-binding")
	require.NotEqual(t, otherSameLeafName, loaded[0].HostPath)
	require.Equal(t, "broken", MountStatus(loaded[0]), "still broken — the coincidentally-same-named directory elsewhere must not resurrect it")
}

// ---------------------------------------------------------------------------
// FR-8.4 — two workspaces may mount the same folder
// ---------------------------------------------------------------------------

func TestCreateMount_TwoWorkspacesSameTarget(t *testing.T) {
	home := t.TempDir()
	idA := newTestWorkspace(t, home)
	idB := newTestWorkspace(t, home)
	target := t.TempDir()

	mA, _, errA := CreateMount(home, idA, "shared", target)
	require.NoError(t, errA)
	mB, _, errB := CreateMount(home, idB, "shared", target)
	require.NoError(t, errB, "FR-8.4: two workspaces MAY mount the same folder, no locking/coordination")

	require.Equal(t, mA.HostPath, mB.HostPath)
}

// ---------------------------------------------------------------------------
// FR-8.6 — deleting/removing a workspace never touches the mounted folder
// ---------------------------------------------------------------------------

func TestDeleteMount_NeverTouchesTargetContents(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep-me.txt"), []byte("operator data"), 0o600))

	_, _, err := CreateMount(home, id, "proj", target)
	require.NoError(t, err)

	require.NoError(t, DeleteMount(home, id, "proj"))

	// The operator's folder and its contents must be fully intact.
	data, err := os.ReadFile(filepath.Join(target, "keep-me.txt"))
	require.NoError(t, err)
	require.Equal(t, "operator data", string(data))

	// The symlink and the record are both gone.
	workDir := WorkDir(home, id)
	_, statErr := os.Lstat(filepath.Join(workDir, "proj"))
	require.True(t, os.IsNotExist(statErr))
	mounts, _ := LoadMounts(home, id)
	require.Empty(t, mounts)

	// Deleting again is a clean not-found, not a panic/second removal.
	err = DeleteMount(home, id, "proj")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrMountNotFound))
}

// TestWorkspaceDirectoryRemoval_NeverTouchesMountTarget is #22/FR-8.6's
// whole-workspace-deletion shape: pkg/gateway's handleWorkspaceDelete
// unconditionally os.RemoveAll's the workspace directory (which contains
// work/<mountName>, a symlink). This proves the underlying mechanism
// directly: os.RemoveAll on a tree containing a symlink NEVER follows it —
// it only unlinks the link entry itself — so the operator's real folder and
// every file inside it survive a full workspace-directory wipe.
func TestWorkspaceDirectoryRemoval_NeverTouchesMountTarget(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "important.txt"), []byte("do not delete"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(target, "subdir"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "subdir", "nested.txt"), []byte("also keep"), 0o600))

	_, _, err := CreateMount(home, id, "client-repo", target)
	require.NoError(t, err)

	wsDir := WorkspaceDir(home, id)
	require.NoError(t, os.RemoveAll(wsDir), "mirrors pkg/gateway's unconditional workspace-directory wipe on delete")

	_, statErr := os.Stat(wsDir)
	require.True(t, os.IsNotExist(statErr), "the workspace's own directory must be gone")

	data, err := os.ReadFile(filepath.Join(target, "important.txt"))
	require.NoError(t, err, "the operator's mounted folder must survive a full workspace-directory delete (FR-8.6)")
	require.Equal(t, "do not delete", string(data))
	nested, err := os.ReadFile(filepath.Join(target, "subdir", "nested.txt"))
	require.NoError(t, err)
	require.Equal(t, "also keep", string(nested))
}

// ---------------------------------------------------------------------------
// FR-6.1/FR-6.3 — grants: AllowedMountRoots
// ---------------------------------------------------------------------------

func TestAllowedMountRoots(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)

	require.Nil(t, AllowedMountRoots(home, id), "no mounts yet -> nil, not an empty non-nil slice")

	targetA := t.TempDir()
	targetB := t.TempDir()
	mA, _, err := CreateMount(home, id, "a", targetA)
	require.NoError(t, err)
	mB, _, err := CreateMount(home, id, "b", targetB)
	require.NoError(t, err)

	roots := AllowedMountRoots(home, id)
	require.ElementsMatch(t, []string{mA.HostPath, mB.HostPath}, roots)

	// FR-6.3: mounts apply to every agent on the workspace — AllowedMountRoots
	// takes no agent parameter at all, so there is no per-agent filtering to
	// even get wrong.
}

func TestAllowedMountRoots_IncludesBrokenMount(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()

	m, _, err := CreateMount(home, id, "proj", target)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(target))
	require.Equal(t, "broken", MountStatus(m))

	roots := AllowedMountRoots(home, id)
	require.Contains(t, roots, m.HostPath, "a broken mount still contributes a grant — nothing resolves there so nothing is writable, and this avoids a second drifting notion of 'live'")
}

// ---------------------------------------------------------------------------
// FR-6.4 — the mount boundary falls out of realpath resolution
// ---------------------------------------------------------------------------

// TestMountBoundary_RealpathResolutionDistinguishesEscapes verifies, using
// the exact containment primitive (isWithinOrEqualPath) and realpath
// resolution (filepath.EvalSymlinks) this package uses, that a symlink
// planted INSIDE a mounted directory and pointing OUTSIDE every granted
// root resolves to a location the containment check correctly rejects —
// while a symlink pointing to a location genuinely inside a granted root is
// correctly accepted. This is the mechanism spec FR-6.4 says "falls out of
// realpath resolution rather than needing new logic" — this test is the
// verification the spec asks for, at the level this package owns (the live
// enforcement call site, pkg/tools.ResolvePath's isWithinAnyRoot, is owned
// by a different wave and reuses the identical containment idiom — see
// resolvepath.go's own isWithinWorkspace/isWithinAnyRoot).
func TestMountBoundary_RealpathResolutionDistinguishesEscapes(t *testing.T) {
	mountRoot := t.TempDir() // the granted root (a mount's resolved HostPath)
	sibling := t.TempDir()   // NOT granted
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "escaped.txt"), []byte("x"), 0o600))

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cases := []struct {
		name       string
		makeTarget func(t *testing.T) string // returns an absolute path INSIDE mountRoot to realpath-resolve and test
		wantInside bool
	}{
		{
			name: "plain file inside the mount",
			makeTarget: func(t *testing.T) string {
				p := filepath.Join(mountRoot, "plain.txt")
				require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
				return p
			},
			wantInside: true,
		},
		{
			name: "symlink to a sibling directory (escape)",
			makeTarget: func(t *testing.T) string {
				link := filepath.Join(mountRoot, "escape-link")
				require.NoError(t, os.Symlink(sibling, link))
				return filepath.Join(link, "escaped.txt")
			},
			wantInside: false,
		},
		{
			name: "symlink to $HOME (escape)",
			makeTarget: func(t *testing.T) string {
				link := filepath.Join(mountRoot, "home-link")
				require.NoError(t, os.Symlink(home, link))
				return link
			},
			wantInside: false,
		},
		{
			name: "symlink chain that stays inside the mount",
			makeTarget: func(t *testing.T) string {
				real := filepath.Join(mountRoot, "real-target")
				require.NoError(t, os.Mkdir(real, 0o700))
				link1 := filepath.Join(mountRoot, "link1")
				require.NoError(t, os.Symlink(real, link1))
				link2 := filepath.Join(mountRoot, "link2")
				require.NoError(t, os.Symlink(link1, link2))
				return link2
			},
			wantInside: true,
		},
		{
			name: "symlink chain that escapes at the last hop",
			makeTarget: func(t *testing.T) string {
				link1 := filepath.Join(mountRoot, "chain1")
				require.NoError(t, os.Symlink(sibling, link1))
				link2 := filepath.Join(mountRoot, "chain2")
				require.NoError(t, os.Symlink(link1, link2))
				return link2
			},
			wantInside: false,
		},
		{
			name: "relative ../.. escape",
			makeTarget: func(t *testing.T) string {
				nested := filepath.Join(mountRoot, "a", "b")
				require.NoError(t, os.MkdirAll(nested, 0o700))
				// Lexically escapes mountRoot entirely via "..".
				return filepath.Join(nested, "..", "..", "..", filepath.Base(sibling))
			},
			wantInside: false,
		},
	}

	resolvedMountRoot, err := filepath.EvalSymlinks(mountRoot)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.makeTarget(t)
			resolved, evalErr := filepath.EvalSymlinks(filepath.Clean(target))
			if evalErr != nil {
				// A target that doesn't exist on disk (the ../.. lexical
				// escape case, which points outside any tree this test
				// created) cannot be realpath-resolved by definition —
				// that is itself the correct "not inside" outcome: it can
				// never be classified as inside the granted root.
				require.False(t, tc.wantInside, "EvalSymlinks failed for a case expected to resolve inside the mount: %v", evalErr)
				return
			}
			inside := isWithinOrEqualPath(resolved, resolvedMountRoot)
			require.Equal(t, tc.wantInside, inside, "resolved=%q root=%q", resolved, resolvedMountRoot)
		})
	}
}

// TestMountBoundary_HardlinkEscape_DocumentedGap is FR-6.5: "A hardlink
// inside a mount pointing outside it is a KNOWN, DOCUMENTED GAP — a
// hardlink has no separate path to resolve." This test asserts the gap
// exists (a hardlink is indistinguishable from a plain file by path alone)
// so it is never mistaken for coverage this package provides.
func TestMountBoundary_HardlinkEscape_DocumentedGap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Link semantics differ enough on Windows to not be worth asserting the POSIX gap here")
	}
	mountRoot := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "real.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside content"), 0o600))

	hardlinkInMount := filepath.Join(mountRoot, "looks-local.txt")
	if err := os.Link(outsideFile, hardlinkInMount); err != nil {
		t.Skipf("hardlinks not supported across these two temp dirs on this filesystem: %v", err)
	}

	// The documented gap: os.Lstat/EvalSymlinks on the hardlink path shows
	// nothing distinguishing it from an ordinary file physically inside the
	// mount — there is no symlink to resolve and no second path to compare
	// against. Realpath-based containment (isWithinOrEqualPath) therefore
	// reports it as "inside" the mount even though its content is shared
	// with a file that lives entirely outside it.
	resolved, err := filepath.EvalSymlinks(hardlinkInMount)
	require.NoError(t, err)
	resolvedMountRoot, err := filepath.EvalSymlinks(mountRoot)
	require.NoError(t, err)
	require.True(t, isWithinOrEqualPath(resolved, resolvedMountRoot),
		"documents the FR-6.5 gap: a hardlink's path is indistinguishable from an ordinary in-mount file to any path-based containment check")

	// Proof this is a REAL content-sharing gap, not just a naming quirk:
	// writing through the "local-looking" path mutates the outside file.
	require.NoError(t, os.WriteFile(hardlinkInMount, []byte("mutated via the mount"), 0o600))
	mutated, err := os.ReadFile(outsideFile)
	require.NoError(t, err)
	require.Equal(t, "mutated via the mount", string(mutated), "FR-6.5 gap confirmed: a write through the hardlink inside the mount reached a file OUTSIDE it")
}
