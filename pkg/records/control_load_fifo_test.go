//go:build linux || darwin

// Special-file loading boundary (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A FIFO named like a view file neither blocks the load nor is opened (regular files only).
func TestLoadControlChecked_FifoNamedLikeAViewNeverBlocksOrOpens(t *testing.T) {
	vault := controlBoundaryVault(t)
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "ok.yaml"), controlBoundaryView("ok"))
	require.NoError(t, syscall.Mkfifo(filepath.Join(vault, controlBoundaryMarker, "views", "pipe.yaml"), 0o600))

	spy := controlBoundaryRecorder(t, vault)
	type result struct {
		got CheckedControl
		err error
	}
	done := make(chan result, 1)
	go func() {
		got, err := LoadControlChecked(context.Background(), spy, vault, ListingLoadLimits())
		done <- result{got: got, err: err}
	}()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.Equal(t, []string{"ok"}, r.got.Views.Names())
		require.Len(t, r.got.ViewReport.Rejections, 1)
		require.Equal(t, RejectViewUnreadable, r.got.ViewReport.Rejections[0].Code)
		require.Equal(t, []string{filepath.Join(vault, controlBoundaryMarker, "views", "pipe.yaml")}, r.got.ViewReport.Rejections[0].Paths)
		require.NotContains(t, spy.opened, controlBoundaryMarker+"/views/pipe.yaml", "a special file is refused before it is opened")
	case <-time.After(30 * time.Second):
		t.Fatal("the load blocked on a FIFO named like a view file")
	}
}
