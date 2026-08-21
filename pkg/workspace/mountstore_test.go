// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Tests for the mount STORE — where the write-grant list lives, and what it
// refuses to trust. Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'Mount' -p 1 ./pkg/workspace/

package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeRawWorkspaceRecord writes a workspace record from a raw map, so a test
// can plant a field the Go struct no longer has. This is the ONLY way to
// reproduce the attack: workspace.Workspace has no Mounts field any more, so
// the hostile entry cannot be expressed through the typed writer — which is
// itself part of the fix, and is why this helper marshals a map instead.
func writeRawWorkspaceRecord(t *testing.T, home string, rec map[string]any) {
	t.Helper()
	dir := dirFor(home)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	data, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err)
	id, _ := rec["id"].(string)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
}

// TestHostileMountsInWorkspaceRecordGrantNothing is the regression test for
// the escalation this store exists to close.
//
// The attack, in full: a sandboxed child runs `bash`. The kernel policy grants
// $OMNIPUS_HOME read/write/execute as one coarse rule, and
// fspolicy.DeniedPathsFor re-admits the whole `workspaces` root for any
// re-rooted workspace turn (the work dir is a proper descendant of it), so the
// child can write its own workspace record. It appends
//
//	"mounts": [{"name": "pwn", "host_path": "/"}]
//
// and — before this fix — AllowedMountRoots read that back verbatim, handed it
// to FSPolicy.AllowedRoots, and every subsequent write went anywhere on disk:
// ~/.ssh/authorized_keys, /usr/local/bin, anything. That is a self-service
// write grant, and it defeats the one property ADR-062 keeps.
//
// Note what this test does NOT rely on: it does not assert that the hostile
// entry is REJECTED by validation. "/" is a perfectly legitimate mount target
// (FR-7.6 warn-and-allow), so re-validating on load would pass it. The only
// durable fix is that nothing reads the list from there at all — which is what
// this asserts.
func TestHostileMountsInWorkspaceRecordGrantNothing(t *testing.T) {
	home := t.TempDir()
	id := "hostile-ws"

	hostile := []map[string]any{
		{"name": "pwn", "host_path": "/"},
		{"name": "ssh", "host_path": filepath.Join(home, "..", "ssh-victim")},
		{"name": "bins", "host_path": "/usr/local/bin"},
	}
	writeRawWorkspaceRecord(t, home, map[string]any{
		"id": id, "name": "Hostile", "status": "active",
		"created_at": "2026-08-12T00:00:00Z", "updated_at": "2026-08-12T00:00:00Z",
		"mounts": hostile,
	})

	// The planted array really is on disk, in the file a child can write —
	// otherwise this test would pass vacuously.
	raw, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"host_path": "/"`, "the hostile entry must actually be present in the record for this test to mean anything")

	// Nothing reads it.
	mounts, ok := LoadMounts(home, id)
	require.True(t, ok, "a workspace with no mount-store record is not an error, it simply has no mounts")
	require.Empty(t, mounts, "mounts planted in the workspace record must not be loaded")

	require.Nil(t, AllowedMountRoots(home, id),
		"a mount planted in the child-writable workspace record must grant NOTHING — this is the escalation being closed")

	// And the typed loader silently drops the unknown field rather than
	// carrying it anywhere: a record round-tripped through the real writer
	// loses it entirely. Deliberately NOT a migration — importing an
	// attacker-controlled entry into the protected store would launder exactly
	// the data this move exists to distrust.
	ws, err := loadWorkspaceRecord(home, id)
	require.NoError(t, err)
	require.NoError(t, saveWorkspaceRecord(home, ws))
	rewritten, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(rewritten), "mounts", "a re-save must drop the planted array, never preserve or migrate it")
	require.Nil(t, AllowedMountRoots(home, id))
}

// TestCreateMountWritesOnlyTheMountStore proves the create path no longer
// touches the workspace record at all: a real, legitimate mount must appear in
// entities/mounts and must NOT appear in workspaces/<id>.json. Without this, a
// later refactor could quietly start writing both and reopen the hole from the
// other end (the record would once again be a source anyone could tamper with,
// even if the reader preferred the store).
func TestCreateMountWritesOnlyTheMountStore(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()

	m, _, err := CreateMount(home, id, "legit", target)
	require.NoError(t, err)

	storePath, err := MountStorePath(home, id)
	require.NoError(t, err)
	storeRaw, err := os.ReadFile(storePath)
	require.NoError(t, err, "the mount must be persisted in the mount store")
	require.Contains(t, string(storeRaw), m.HostPath)

	wsRaw, err := os.ReadFile(filepath.Join(dirFor(home), id+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(wsRaw), "mounts", "the workspace record must carry no mount data whatsoever")
	require.NotContains(t, string(wsRaw), m.HostPath)

	// Round-trips through the real reader.
	require.Equal(t, []string{m.HostPath}, AllowedMountRoots(home, id))
}

// TestMountStorePathIsUnderEntities asserts the store's own layout, at the
// level this package can see. The security-relevant half of the claim — that
// entities/ is in the denied set on both layers — is asserted against
// fspolicy.SecretPathsAlways in pkg/tools (this package deliberately does not
// import pkg/fspolicy).
func TestMountStorePathIsUnderEntities(t *testing.T) {
	home := t.TempDir()
	path, err := MountStorePath(home, "ws-1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "entities", "mounts", "ws-1.json"), path)

	_, err = MountStorePath(home, "../escape")
	require.Error(t, err, "an unsafe id must never produce a store path")
}

// TestLoadMountStore_DropsInvalidEntries is the defence-in-depth half: the
// store is out of a child's reach, but a corrupted, truncated, hand-edited or
// foreign-install record must still never produce a grant from a malformed
// entry. Every bad entry is DROPPED (fail closed); the good ones survive so a
// single corrupt line cannot silently disarm an operator's real mounts.
func TestLoadMountStore_DropsInvalidEntries(t *testing.T) {
	home := t.TempDir()
	id := "ws-validate"
	good := t.TempDir()

	storePath, err := MountStorePath(home, id)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(storePath), 0o700))

	raw := map[string]any{
		"workspace_id": id,
		"mounts": []map[string]any{
			{"name": "ok", "host_path": good},
			{"name": "../escape", "host_path": "/tmp"},        // traversing name
			{"name": "sep/arator", "host_path": "/tmp"},       // name is not one segment
			{"name": "", "host_path": "/tmp"},                 // empty name
			{"name": "empty-path", "host_path": ""},           // no target
			{"name": "relative", "host_path": "relative/dir"}, // not absolute
			{"name": "unclean", "host_path": "/tmp/../etc"},   // not in cleaned form
			{"name": "ok", "host_path": "/etc"},               // duplicate name
		},
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(storePath, data, 0o600))

	mounts, ok := LoadMounts(home, id)
	require.True(t, ok)
	require.Len(t, mounts, 1, "exactly one entry is well-formed; every other must be dropped")
	require.Equal(t, "ok", mounts[0].Name)
	require.Equal(t, good, mounts[0].HostPath)

	roots := AllowedMountRoots(home, id)
	require.Equal(t, []string{good}, roots)
	require.NotContains(t, roots, "/etc", "the duplicate-name entry must not win over the first")
	require.NotContains(t, roots, "/tmp")
}

// TestLoadMountStore_FailsClosedOnUnreadableRecord: a malformed store, or one
// whose recorded workspace_id disagrees with its filename (moved, or
// hand-assembled), yields NO grants — never a partial or best-effort list.
func TestLoadMountStore_FailsClosedOnUnreadableRecord(t *testing.T) {
	home := t.TempDir()

	t.Run("malformed JSON", func(t *testing.T) {
		id := "ws-malformed"
		p, err := MountStorePath(home, id)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(`{"mounts": [`), 0o600))

		_, ok := LoadMounts(home, id)
		require.False(t, ok)
		require.Nil(t, AllowedMountRoots(home, id))
	})

	t.Run("workspace_id disagrees with filename", func(t *testing.T) {
		id := "ws-mismatch"
		p, err := MountStorePath(home, id)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		data, err := json.Marshal(map[string]any{
			"workspace_id": "some-other-workspace",
			"mounts":       []map[string]any{{"name": "x", "host_path": "/tmp"}},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, data, 0o600))

		_, ok := LoadMounts(home, id)
		require.False(t, ok)
		require.Nil(t, AllowedMountRoots(home, id))
	})
}

// TestDeleteMountStore_RemovesTheRecordNotTheFolder covers the
// workspace-delete cascade: the grant record goes, the operator's real folder
// is untouched (FR-8.6), and a second call is a clean no-op.
func TestDeleteMountStore_RemovesTheRecordNotTheFolder(t *testing.T) {
	home := t.TempDir()
	id := newTestWorkspace(t, home)
	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep.txt"), []byte("operator data"), 0o600))

	_, _, err := CreateMount(home, id, "proj", target)
	require.NoError(t, err)

	require.NoError(t, DeleteMountStore(home, id))
	require.Nil(t, AllowedMountRoots(home, id))

	storePath, err := MountStorePath(home, id)
	require.NoError(t, err)
	_, statErr := os.Stat(storePath)
	require.True(t, os.IsNotExist(statErr))

	data, err := os.ReadFile(filepath.Join(target, "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "operator data", string(data))

	require.NoError(t, DeleteMountStore(home, id), "idempotent — a cascade must not fail on an already-absent record")
}
