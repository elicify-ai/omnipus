// Omnipus — tests for the per-index reconcile lock (reconcile.go, Codex
// review 2026-09-14 finding 4).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestReconcile_ExcludesAnotherReconcile — two Reconciles on the same index
// file never run at the same time. Deterministic: the first holds the lock on
// a channel; the second must be still running (blocked) when checked.
func TestReconcile_ExcludesAnotherReconcile(t *testing.T) {
	store, _ := openIndex(t, Options{})

	inside := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- store.Reconcile(func(_ Store) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside // the first reconcile holds the lock

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.Reconcile(func(_ Store) error { return nil })
	}()

	select {
	case <-secondDone:
		t.Fatal("a second Reconcile ran to completion while the first still held the lock")
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
}

// TestDirectWrite_QueuesBehindAReconcile — the half that closes the finding
// without the direct-write call sites knowing: a plain UpsertNote from a
// SEPARATELY OPENED handle (the way every direct writer, including
// pkg/knowledge's instant refresh, actually writes) blocks while a Reconcile
// holds the same index file, and commits once it releases.
func TestDirectWrite_QueuesBehindAReconcile(t *testing.T) {
	_, path := openIndex(t, Options{})
	ctx := context.Background()

	// A second handle on the SAME file — the cross-instance case that makes a
	// per-instance flag useless and a per-path lock the only correct key.
	direct, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("opening the second handle: %v", err)
	}
	defer func() { _ = direct.Close() }()

	inside := make(chan struct{})
	release := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- direct.Reconcile(func(_ Store) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside // the reconcile holds the lock

	written := make(chan error, 1)
	go func() {
		written <- direct.UpsertNote(ctx, NoteRows{Path: "queued.md", Kind: KindNote})
	}()

	select {
	case err := <-written:
		t.Fatalf("a direct write committed while a reconcile held the lock (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if err := <-reconcileDone; err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := <-written; err != nil {
		t.Fatalf("the direct write failed after the reconcile released: %v", err)
	}

	found := false
	if err := direct.AllPaths(ctx, func(n IndexedNote) error {
		if n.Path == "queued.md" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	if !found {
		t.Fatal("the queued write did not land after the reconcile released")
	}
}

// TestReconcile_WritesThroughTheScopedStore — the store a Reconcile hands its
// function writes without deadlocking (its write methods skip the lock the
// reconcile holds), and what it writes is visible afterwards.
func TestReconcile_WritesThroughTheScopedStore(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()

	err := store.Reconcile(func(s Store) error {
		if err := s.UpsertNote(ctx, NoteRows{Path: "inside.md", Kind: KindNote}); err != nil {
			return fmt.Errorf("upsert note: %w", err)
		}
		if err := s.DeleteNote(ctx, "inside.md"); err != nil {
			return fmt.Errorf("delete note: %w", err)
		}
		return s.UpsertNote(ctx, NoteRows{Path: "inside.md", Kind: KindNote})
	})
	if err != nil {
		t.Fatalf("reconcile-scoped writes: %v", err)
	}

	found := false
	if err := store.AllPaths(ctx, func(n IndexedNote) error {
		if n.Path == "inside.md" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	if !found {
		t.Fatal("the write made inside the reconcile scope did not land")
	}
}

// TestReconcile_ReleasesTheLockOnFnError — a failed reconcile must not leave
// the index unwritable for the next writer.
func TestReconcile_ReleasesTheLockOnFnError(t *testing.T) {
	store, _ := openIndex(t, Options{})
	ctx := context.Background()

	wantErr := errFakeReconcile
	if err := store.Reconcile(func(Store) error { return wantErr }); err == nil {
		t.Fatal("Reconcile swallowed the function's error")
	}
	if err := store.UpsertNote(ctx, NoteRows{Path: "after-failure.md", Kind: KindNote}); err != nil {
		t.Fatalf("a write after a failed reconcile was blocked: %v", err)
	}
}

type fakeReconcileError struct{}

func (fakeReconcileError) Error() string { return "fake reconcile failure" }

var errFakeReconcile = fakeReconcileError{}
