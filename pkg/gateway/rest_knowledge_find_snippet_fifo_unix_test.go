//go:build unix

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVaultSearchReadNoteHead_ToleratesAShortReadWithoutEOF is F8's direct
// regression: a single f.Read call legally returns fewer bytes than
// requested WITHOUT io.EOF — reproduced here with a FIFO, whose Read()
// returns whatever is CURRENTLY buffered in the pipe rather than waiting to
// fill the caller's buffer. The writer delivers the note in two separate
// writes with a pause between them, so a single Read only ever sees the
// first write; io.ReadFull must keep reading until the second write (or a
// real EOF) arrives.
//
// syscall.Mkfifo is POSIX-only and does not exist on GOOS=windows, so this
// file is unix-only (build tag unix) rather than gated by a runtime
// runtime.GOOS check — a runtime t.Skip still requires the file to compile
// for windows, which it cannot.
func TestVaultSearchReadNoteHead_ToleratesAShortReadWithoutEOF(t *testing.T) {
	dir := t.TempDir()
	const name = "slow-note.md"
	fifoPath := filepath.Join(dir, name)
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	go func() {
		wf, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer func() { _ = wf.Close() }()
		// First write: whatever is available when the reader's (possibly
		// single) Read() call fires.
		_, _ = wf.WriteString("head-marker ")
		// Give a single-Read implementation time to have already returned
		// with just the first write before the rest arrives — reproducing
		// the exact "short read, no EOF yet" window F8 is about.
		time.Sleep(150 * time.Millisecond)
		_, _ = wf.WriteString("tail-marker")
	}()

	body, ok := vaultSearchReadNoteHead(dir, name)
	require.True(t, ok, "a legitimately short-but-not-EOF read must still succeed")
	assert.Contains(t, body, "head-marker")
	assert.Contains(t, body, "tail-marker",
		"a short read that returns fewer bytes than the scan window without EOF must not be "+
			"treated as the whole file — the reader must keep reading until the writer catches up")
}
