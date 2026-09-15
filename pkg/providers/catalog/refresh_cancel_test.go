// License: MIT
// Copyright (c) 2026 Omnipus contributors
package catalog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// cancelOnPullPuller returns a VALID document but cancels the caller's
// context while doing so — the "pull completed, process already shutting
// down" ordering that wrote providers_catalog.json into removed data dirs on
// 2026-09-12.
type cancelOnPullPuller struct {
	data   []byte
	cancel context.CancelFunc
}

func (p *cancelOnPullPuller) Pull(context.Context) ([]byte, error) {
	p.cancel()
	return p.data, nil
}
func (p *cancelOnPullPuller) LastPullDegraded() (bool, error) { return false, nil }

type countingStore struct{ writes atomic.Int32 }

func (s *countingStore) Read(context.Context) ([]byte, error) { return nil, errors.New("empty") }
func (s *countingStore) Write(context.Context, []byte) error {
	s.writes.Add(1)
	return nil
}

// TestRefresh_CanceledContextDoesNotPersist pins that a refresh whose
// context is cancelled by the time the pull returns never writes the file.
// DIES ON: removing the ctx.Err() check before store.Write in refreshLocked.
func TestRefresh_CanceledContextDoesNotPersist(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &countingStore{}
	c := New()
	c.puller = &cancelOnPullPuller{data: loadFixture(t), cancel: cancel}
	c.store = store

	err := c.Refresh(ctx)
	require.ErrorIs(t, err, context.Canceled, "a cancelled refresh must report the cancellation")
	require.Equal(t, int32(0), store.writes.Load(), "no persist after cancellation")

	// The pulled document was still applied in memory for the rest of the run.
	s, ok := c.Served()
	require.True(t, ok)
	require.Equal(t, ServedPulled, s.From)
}

// TestFileStore_WriteRefusesCanceledContext: the production Store is the
// last line — it must not write for a cancelled context even if a caller
// forgot to check.
func TestFileStore_WriteRefusesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fs := NewFileStore(t.TempDir())
	err := fs.Write(ctx, []byte("{}"))
	require.ErrorIs(t, err, context.Canceled)
	_, rerr := fs.Read(context.Background())
	require.Error(t, rerr, "nothing may have been written")
}
