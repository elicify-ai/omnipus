// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fileutil/fileutiltest"
)

// credentials.json is written with fileutil.WriteFileAtomic under a
// cross-process fileutil.WithFlock lock. That lock used to be taken on
// credentials.json itself, and WithFlock opens its path with O_CREATE: the
// store's first write created an EMPTY credentials.json and held it through the
// temp write and fsync. Anything reading the store in that window — another
// process, the boot path's Exists() check that decides whether this is a fresh
// install — saw a present but empty file, which loadFileInternal reports as
// corrupted and refuses to overwrite. A crash in that window left an install
// whose credential store could not be opened without manual repair.
func TestCredentialStore_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewStore(path)
	require.NoError(t, s.UnlockWithKey(bytes.Repeat([]byte{7}, keyLen)))
	parked, release := fileutiltest.HoldFirstWrite(t, &writeFileAtomicFn, "credentials.json")

	done := make(chan error, 1)
	go func() { done <- s.Set("LOCK_TEST_TOKEN", "placeholder-value") }()
	require.Equal(t, path, fileutiltest.WaitParked(t, parked))
	fileutiltest.RequireAbsentOrCompleteJSON(t, path)
	require.False(t, s.Exists(), "credentials.json must not exist before its first write lands")

	release()
	fileutiltest.WaitDone(t, done)
	fileutiltest.RequireCompleteJSON(t, path)
	got, err := s.Get("LOCK_TEST_TOKEN")
	require.NoError(t, err)
	require.Equal(t, "placeholder-value", got)

	info, err := os.Stat(fileutil.SidecarLockPath(path))
	require.NoError(t, err, "the store write must take its lock on the sidecar")
	require.Zerof(t, info.Mode().Perm()&0o077,
		"the credentials lock file must not be more open than credentials.json (0600), got %v", info.Mode().Perm())
}
