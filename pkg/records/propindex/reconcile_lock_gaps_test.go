// Omnipus — tests for the two gaps silent-failure review 2026-09-14 F7 found
// in the per-index reconcile lock (reconcile.go): the schema-mismatch rebuild
// did not take the lock at all, and a direct write waiting on the lock ignored
// its caller's context.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// setLockWaitProbe installs fn as the lock-wait probe for one test.
func setLockWaitProbe(t *testing.T, fn func(indexPath string)) {
	t.Helper()
	lockWaitProbe = fn
	t.Cleanup(func() { lockWaitProbe = nil })
}

func pathsIn(t *testing.T, s Store) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	if err := s.AllPaths(context.Background(), func(n IndexedNote) error {
		got[n.Path] = true
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	return got
}

type openOutcome struct {
	store Store
	err   error
}

// TestSchemaRebuild_WaitsForAnInFlightReconcile — H2a. Opening an index whose
// file carries an older schema version drops every table and re-creates them.
// That is the most destructive write this package makes, and it must queue
// behind a reconcile in flight exactly like a single-path write does:
// otherwise the reconcile's rows vanish (or its next statement hits "no such
// table") underneath it.
//
// Deterministic, no sleeps: the reconcile starts the Open and then waits for
// whichever comes first — the Open finishing (the defect: it ran inside the
// reconcile's lock) or the lock-wait probe reporting the Open is queued. Only
// in the second case does it check, still holding the lock, that its data is
// intact.
func TestSchemaRebuild_WaitsForAnInFlightReconcile(t *testing.T) {
	ctx := context.Background()
	store, path := openIndex(t, Options{})
	if err := store.UpsertNote(ctx, NoteRows{Path: "kept.md", Kind: KindNote}); err != nil {
		t.Fatalf("seeding a row: %v", err)
	}
	// Make the file look like it was written by an older schema, so the next
	// Open must discard and rebuild it.
	ix, ok := store.(*Index)
	if !ok {
		t.Fatalf("openIndex returned %T, want *Index", store)
	}
	if _, err := ix.exec(ctx, PhaseOpen, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion-1)); err != nil {
		t.Fatalf("marking the file as an older schema: %v", err)
	}

	queued := make(chan struct{}, 1)
	setLockWaitProbe(t, func(p string) {
		if p == path {
			select {
			case queued <- struct{}{}:
			default:
			}
		}
	})

	opened := make(chan openOutcome, 1)
	recErr := store.Reconcile(func(s Store) error {
		go func() {
			st, err := Open(ctx, path, Options{})
			opened <- openOutcome{st, err}
		}()
		select {
		case r := <-opened:
			opened <- r // hand it back so the test can close it
			return fmt.Errorf("the schema rebuild finished (err=%w) while a reconcile held the index lock", r.err)
		case <-queued:
		case <-time.After(10 * time.Second):
			return errors.New("the schema rebuild neither finished nor queued for the lock within 10s")
		}
		// Still holding the lock: the rebuild is queued, so nothing may have
		// been dropped yet.
		if !pathsIn(t, s)["kept.md"] {
			return errors.New("the reconcile's row vanished while it still held the lock")
		}
		return nil
	})

	var r openOutcome
	select {
	case r = <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued schema rebuild never finished after the reconcile released the lock")
	}
	if r.store != nil {
		defer func() { _ = r.store.Close() }()
	}
	if recErr != nil {
		t.Fatal(recErr)
	}
	if r.err != nil {
		t.Fatalf("the schema rebuild failed after the reconcile released: %v", r.err)
	}
	// The rebuild did run once the lock was free.
	if !r.store.NeedsFullIndex() {
		t.Error("after the rebuild the index should report it needs a full re-index")
	}
	if got := pathsIn(t, r.store); len(got) != 0 {
		t.Errorf("after the rebuild the index should be empty, holds %v", got)
	}
}

// TestSchemaRebuild_SkipsWhenAnotherHandleAlreadyRebuilt — the rebuild
// re-reads the schema version once it holds the lock. Two handles opening the
// same old file at once both read the old version before either takes the
// lock; the second must not drop what the first already rebuilt and started
// filling.
func TestSchemaRebuild_SkipsWhenAnotherHandleAlreadyRebuilt(t *testing.T) {
	ctx := context.Background()
	store, path := openIndex(t, Options{})
	ix, ok := store.(*Index)
	if !ok {
		t.Fatalf("openIndex returned %T, want *Index", store)
	}
	if _, err := ix.exec(ctx, PhaseOpen, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion-1)); err != nil {
		t.Fatalf("marking the file as an older schema: %v", err)
	}

	// Stands for a concurrent Open that has already read the OLD version and
	// is about to queue on the lock. It shares the first handle's pool; it is
	// closed with it.
	late := &Index{db: ix.db, path: path}

	// Another handle rebuilds first and writes a row.
	first, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	defer func() { _ = first.Close() }()
	if err := first.UpsertNote(ctx, NoteRows{Path: "written-after-rebuild.md", Kind: KindNote}); err != nil {
		t.Fatalf("writing after the first rebuild: %v", err)
	}

	// The late one now gets the lock and must see the rebuild already happened.
	if err := late.discardIncompatible(ctx); err != nil {
		t.Fatalf("second rebuild attempt: %v", err)
	}
	if !pathsIn(t, first)["written-after-rebuild.md"] {
		t.Fatal("a second handle's rebuild dropped rows written after the first handle had already rebuilt")
	}
}

// TestDirectWrite_StopsWaitingWhenItsContextEnds — H2b. A direct write that
// finds a reconcile holding the lock must give up when its caller's context
// ends, with an error that says what it was waiting for, instead of hanging
// the request goroutine past the gateway's write timeout (a silent "socket
// hang up"). Covers every public write method, both deadline and cancel.
func TestDirectWrite_StopsWaitingWhenItsContextEnds(t *testing.T) {
	store, path := openIndex(t, Options{})
	ctx := context.Background()
	if err := store.UpsertNote(ctx, NoteRows{Path: "held.md", Kind: KindNote}); err != nil {
		t.Fatalf("seeding a row: %v", err)
	}
	direct, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("opening the second handle: %v", err)
	}
	defer func() { _ = direct.Close() }()
	directIx, ok := direct.(*Index)
	if !ok {
		t.Fatalf("Open returned %T, want *Index", direct)
	}

	inside := make(chan struct{})
	release := make(chan struct{})
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- store.Reconcile(func(Store) error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	released := false
	defer func() {
		if !released {
			close(release)
			<-reconcileDone
		}
	}()

	writes := []struct {
		name  string
		write func(context.Context) error
	}{
		{"UpsertNote", func(c context.Context) error {
			return direct.UpsertNote(c, NoteRows{Path: "late.md", Kind: KindNote})
		}},
		{"UpsertNotes", func(c context.Context) error {
			return directIx.UpsertNotes(c, []NoteRows{{Path: "late-batch.md", Kind: KindNote}})
		}},
		{"DeleteNote", func(c context.Context) error {
			return direct.DeleteNote(c, "held.md")
		}},
		{"RefreshNoteStat", func(c context.Context) error {
			_, err := direct.RefreshNoteStat(c, "held.md", 42, 1_700_000_000_000_000_000, 0, false)
			return err
		}},
	}

	const promptly = 3 * time.Second
	for _, w := range writes {
		for _, mode := range []struct {
			name string
			ctx  func() (context.Context, context.CancelFunc)
			want error
		}{
			{"deadline", func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, 100*time.Millisecond)
			}, context.DeadlineExceeded},
			{"cancel", func() (context.Context, context.CancelFunc) {
				c, cancel := context.WithCancel(ctx)
				time.AfterFunc(100*time.Millisecond, cancel)
				return c, cancel
			}, context.Canceled},
		} {
			wctx, cancel := mode.ctx()
			errc := make(chan error, 1)
			go func() { errc <- w.write(wctx) }()
			select {
			case err := <-errc:
				cancel()
				if !errors.Is(err, mode.want) {
					t.Fatalf("%s (%s): got err=%v, want an error wrapping %v", w.name, mode.name, err, mode.want)
				}
				if !strings.Contains(err.Error(), "write lock") {
					t.Errorf("%s (%s): the error must say it was waiting for the index write lock, got %q", w.name, mode.name, err)
				}
			case <-time.After(promptly):
				cancel()
				t.Fatalf("%s (%s): still waiting for the lock %v after its context ended", w.name, mode.name, promptly)
			}
		}
	}

	close(release)
	released = true
	if err := <-reconcileDone; err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Nothing an abandoned write carried landed, and the lock is not left
	// held by a writer that gave up.
	got := pathsIn(t, direct)
	if !got["held.md"] {
		t.Error("an abandoned DeleteNote deleted its row anyway")
	}
	if got["late.md"] || got["late-batch.md"] {
		t.Errorf("an abandoned upsert landed anyway: %v", got)
	}
	afterCtx, cancel := context.WithTimeout(ctx, promptly)
	defer cancel()
	if err := direct.UpsertNote(afterCtx, NoteRows{Path: "after.md", Kind: KindNote}); err != nil {
		t.Fatalf("a write after abandoned waiters could not take the lock: %v", err)
	}
}
