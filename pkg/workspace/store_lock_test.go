// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package workspace

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// The workspace record, the mount store and the delegation store are each
// written with fileutil.WriteFileAtomic under a cross-process fileutil.WithFlock
// lock that used to be taken on the record file itself. WithFlock opens its
// path with O_CREATE, so a record's first write created an EMPTY file and left
// it there until the rename replaced it. For the mount and delegation stores an
// empty record is "malformed" — the workspace is treated as having no mounts
// and no delegation edges — and for the workspace record it is a workspace that
// fails to load. A crash in that window left the empty file for good.

const lockTestWorkspaceID = "ws-lock-test"

type recordStore struct {
	name   string
	path   func(t *testing.T, home string) string
	write  func(home string) error
	remove func(home string) error
}

func recordStores() []recordStore {
	return []recordStore{
		{
			name: "mount store",
			path: func(t *testing.T, home string) string {
				p, err := MountStorePath(home, lockTestWorkspaceID)
				require.NoError(t, err)
				return p
			},
			write: func(home string) error {
				return saveMountStore(home, lockTestWorkspaceID, []Mount{{Name: "data", HostPath: "/srv/data"}})
			},
			remove: func(home string) error { return DeleteMountStore(home, lockTestWorkspaceID) },
		},
		{
			name: "delegation store",
			path: func(t *testing.T, home string) string {
				p, err := DelegationStorePath(home, lockTestWorkspaceID)
				require.NoError(t, err)
				return p
			},
			write: func(home string) error {
				return SaveDelegation(home, lockTestWorkspaceID, []DelegationEdge{{FromAgent: "jim", ToAgent: "ava"}})
			},
			remove: func(home string) error { return DeleteDelegationStore(home, lockTestWorkspaceID) },
		},
	}
}

func TestWorkspaceRecords_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	stores := append(recordStores(), recordStore{
		name: "workspace record",
		path: func(_ *testing.T, home string) string {
			return filepath.Join(home, "workspaces", lockTestWorkspaceID+".json")
		},
		write: func(home string) error {
			return SaveRecord(home, Workspace{ID: lockTestWorkspaceID, Name: "Lock test", Status: "active"})
		},
	})
	for _, st := range stores {
		t.Run(st.name, func(t *testing.T) {
			home := t.TempDir()
			target := st.path(t, home)
			parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, filepath.Base(target))

			done := make(chan error, 1)
			go func() { done <- st.write(home) }()
			require.Equal(t, target, fileutiltest.WaitParked(t, parked))
			fileutiltest.RequireAbsentOrCompleteJSON(t, target)

			release()
			fileutiltest.WaitDone(t, done)
			fileutiltest.RequireCompleteJSON(t, target)
			require.FileExists(t, fileutil.SidecarLockPath(target), "the write must take its lock on the sidecar")
		})
	}
}

// Removing a record — deleting the workspace's store, or saving an empty list,
// which removes the file so "none" has one on-disk form — must remove its lock
// file too.
func TestMountAndDelegationStores_RemovingARecordRemovesItsLockFile(t *testing.T) {
	for _, st := range recordStores() {
		removals := map[string]func(home string) error{
			"delete": st.remove,
		}
		switch st.name {
		case "mount store":
			removals["save empty"] = func(home string) error { return saveMountStore(home, lockTestWorkspaceID, nil) }
		case "delegation store":
			removals["save empty"] = func(home string) error { return SaveDelegation(home, lockTestWorkspaceID, nil) }
		}
		for how, remove := range removals {
			t.Run(st.name+"/"+how, func(t *testing.T) {
				home := t.TempDir()
				target := st.path(t, home)
				lock := fileutil.SidecarLockPath(target)
				require.NoError(t, st.write(home))
				require.FileExists(t, lock, "precondition: the record's write created its sidecar lock file")

				require.NoError(t, remove(home))
				require.NoFileExists(t, target)
				require.NoFileExists(t, lock, "a removed record must not leave its lock file behind")

				require.NoError(t, remove(home), "removing an absent record stays a success")
				require.NoFileExists(t, lock, "removing an absent record must not create a lock file")
			})
		}
	}
}
