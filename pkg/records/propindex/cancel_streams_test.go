// Omnipus — cancellation of the non-candidate row streams.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// cancelStreamNotes is how many notes the cancellation tests index. Each note
// contributes at least two rows to every stream, so every stream holds well
// over cancelAfter rows.
const (
	cancelStreamNotes = 6
	cancelAfter       = 2
)

// cancelStreamCorpus indexes notes that carry a checkbox pair, a relation, two
// tags and two links each, so Tasks, Relations, Tags, Links and AllPaths all
// have rows to stream.
func cancelStreamCorpus(t *testing.T, store Store) {
	t.Helper()
	sc := plantSchema(t)
	for i := range cancelStreamNotes {
		src := fmt.Sprintf(`---
type: plant
id: PL-%04d
species: Fern
bed: "[[Bed %d]]"
tags: [greenhouse, care/watering]
---

Tagged #indoor here, linked to [[Bed %d]] and [[Rosa]].

- [ ] mist
- [x] feed
`, 7000+i, i, i)
		mustUpsert(t, store, note(t, fmt.Sprintf("garden/cancel-%02d.md", i), sc, src))
	}
}

// TestStreams_CancellationIsObservedByEveryRowLoop.
//
// database/sql closes a result set on cancellation from a watcher goroutine, so
// until that goroutine is scheduled rows.Next() keeps returning rows and a loop
// that does not look at ctx itself finishes as a silent full success. The
// context here shows its cancellation through Err() ONLY (Done() is nil, so the
// watcher is never armed): nothing but the loop's own per-row check can stop it.
//
// Each loop must stop at the row after the cancellation and return an error
// wrapping context.Canceled.
func TestStreams_CancellationIsObservedByEveryRowLoop(t *testing.T) {
	store, _ := openIndex(t, Options{})
	cancelStreamCorpus(t, store)

	streams := []struct {
		name string
		run  func(ctx context.Context, visit func()) error
	}{
		{"AllPaths", func(ctx context.Context, visit func()) error {
			return store.AllPaths(ctx, func(IndexedNote) error { visit(); return nil })
		}},
		{"Tasks", func(ctx context.Context, visit func()) error {
			return store.Tasks(ctx, Selector{}, func(TaskHit) error { visit(); return nil })
		}},
		{"Relations", func(ctx context.Context, visit func()) error {
			return store.Relations(ctx, Selector{}, func(RelationHit) error { visit(); return nil })
		}},
		{"Tags", func(ctx context.Context, visit func()) error {
			return store.Tags(ctx, Selector{}, func(TagHit) error { visit(); return nil })
		}},
		{"Links", func(ctx context.Context, visit func()) error {
			return store.Links(ctx, Selector{}, func(LinkHit) error { visit(); return nil })
		}},
	}

	for _, s := range streams {
		t.Run(s.name, func(t *testing.T) {
			// Instrument check: uncancelled, the stream must hold more rows
			// than the cancellation point, or the test below proves nothing.
			total := 0
			if err := s.run(context.Background(), func() { total++ }); err != nil {
				t.Fatalf("%s uncancelled: %v", s.name, err)
			}
			if total <= cancelAfter {
				t.Fatalf("%s holds only %d rows; the fixture cannot show a stop after %d", s.name, total, cancelAfter)
			}

			ctx := &errOnlyContext{Context: context.Background()}
			seen := 0
			err := s.run(ctx, func() {
				seen++
				if seen == cancelAfter {
					ctx.cancelled.Store(true)
				}
			})
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s: a cancelled stream returned %v, want an error wrapping context.Canceled", s.name, err)
			}
			if seen > cancelAfter {
				t.Errorf("%s visited %d rows, want it to stop at %d — the row after the cancellation", s.name, seen, cancelAfter)
			}
		})
	}
}
