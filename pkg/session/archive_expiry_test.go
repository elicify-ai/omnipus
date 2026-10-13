// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the FR-007 expiry/repair pass over the addressed archive
// (archive_expiry.go). Oracles come from the spec — FR-007 and BDD-02.3
// (older/equal/younger mtime fixtures, "first retained complete group ... or
// empty same-ID window") — never from the implementation.

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// assistantCallRecord builds an assistant payload record carrying one tool call,
// so a following tool result forms a complete group.
func assistantCallRecord(t *testing.T, id, day, callID string) ArchiveRecord {
	t.Helper()
	mp, err := EncodeModelPayload(providers.Message{
		Role:    "assistant",
		Content: "calling",
		ToolCalls: []providers.ToolCall{{
			ID: callID, Type: "function",
			Function: &providers.FunctionCall{Name: "t", Arguments: "{}"},
		}},
	})
	if err != nil {
		t.Fatalf("encode assistant: %v", err)
	}
	ts, _ := time.Parse(transcriptDayLayout, day)
	return ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: id, Role: "assistant", Content: "calling",
			AgentID: "mia", ViewMembership: ViewMembershipBoth, Timestamp: ts},
		ModelMessage: &mp,
		Source:       &EntrySource{Kind: "agent", Principal: "mia"},
	}
}

// toolResultRecord builds a role "tool" payload that completes assistantID's group.
func toolResultRecord(t *testing.T, id, day, callID, assistantID string) ArchiveRecord {
	t.Helper()
	mp, err := EncodeModelPayload(providers.Message{Role: "tool", ToolCallID: callID, Content: "result"})
	if err != nil {
		t.Fatalf("encode tool: %v", err)
	}
	ts, _ := time.Parse(transcriptDayLayout, day)
	return ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: id, Role: "tool", Content: "result",
			AgentID: "mia", ViewMembership: ViewMembershipBoth, Timestamp: ts},
		ModelMessage:  &mp,
		ToolResultFor: &ToolResultFor{AssistantEntryID: assistantID, ToolCallID: callID},
	}
}

// rollDayOne seeds a complete day-1 content day (assistant call + tool result +
// user message) and rolls to day 2, leaving day-1 in a rolled partition. It
// returns the day-1 addresses, the surviving day-2 address, and the rolled path.
func rollDayOne(t *testing.T, store *ArchiveDayStore) (day1 []ArchiveAddress, day2 ArchiveAddress, rolled string) {
	t.Helper()
	atDay(store, "2026-03-04")
	for _, rec := range []ArchiveRecord{
		assistantCallRecord(t, "d1-asst", "2026-03-04", "call_0"),
		toolResultRecord(t, "d1-tool", "2026-03-04", "call_0", "d1-asst"),
		payloadRecord(t, "d1-user", "2026-03-04", "day one user"),
	} {
		a, err := store.Append(rec)
		if err != nil {
			t.Fatalf("append day1: %v", err)
		}
		day1 = append(day1, a)
	}
	atDay(store, "2026-03-05")
	a, err := store.Append(payloadRecord(t, "d2-user", "2026-03-05", "day two user"))
	if err != nil {
		t.Fatalf("append day2: %v", err)
	}
	return day1, a, filepath.Join(store.dir(), "2026-03-04.jsonl")
}

func ageFileTo(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// FR-007 / RetentionSweep parity: retentionDays <= 0 is the DISABLED case — no
// deletion, no repair, a non-expired notice (BDD-02.3 "disabled means no
// deletion/manufactured repair").
func TestArchiveExpiry_DisabledRetentionIsANoOp(t *testing.T) {
	store := mustStore(t, t.TempDir())
	_, _, rolled := rollDayOne(t, store)
	ageFileTo(t, rolled, time.Now().Add(-365*24*time.Hour))

	notice, err := store.SweepExpired(0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if notice.Expired || notice.RemovedPartitions != 0 || notice.WindowEmptied {
		t.Fatalf("disabled retention must be a no-op; got %#v", notice)
	}
	if _, err := os.Stat(rolled); err != nil {
		t.Fatalf("disabled retention deleted a partition: %v", err)
	}
}

// FR-007 / BDD-02.3: STRICTLY older-than cutoff expires.
func TestArchiveExpiry_StrictlyOlderThanCutoffExpires(t *testing.T) {
	store := mustStore(t, t.TempDir())
	_, _, rolled := rollDayOne(t, store)
	store.now = func() time.Time { return time.Now() }
	ageFileTo(t, rolled, time.Now().Add(-91*24*time.Hour))

	notice, err := store.SweepExpired(90)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !notice.Expired || notice.RemovedPartitions != 1 {
		t.Fatalf("an older-than-cutoff partition must expire; got %#v", notice)
	}
	if _, err := os.Stat(rolled); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired partition should be gone; stat err=%v", err)
	}
}

// FR-007 / BDD-02.3 B05: equal mtime is NOT older-than — the partition survives.
//
// QA-CHECK-final-gate Q05: the earlier form left store.now at 2026-03-05 (set by
// rollDayOne) while ageing the file from the REAL clock, so the fixture never
// constructed equality — the cutoff was derived from one instant and the mtime
// from another, and a "delete at the equal cutoff" mutant survived. Both sides
// now come from ONE fixed clock, and the three cases are derived from it.
func TestArchiveExpiry_EqualMtimeIsRetained(t *testing.T) {
	base := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	const retention = 30
	cutoff := base.Add(-retention * 24 * time.Hour)

	for _, tc := range []struct {
		name    string
		mtime   time.Time
		expired bool
	}{
		{"strictly older than the cutoff", cutoff.Add(-time.Second), true},
		{"exactly at the cutoff", cutoff, false},
		{"younger than the cutoff", cutoff.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := mustStore(t, t.TempDir())
			_, _, rolled := rollDayOne(t, store)
			// ONE clock: the cutoff SweepExpired derives and this fixture's mtime
			// are the same instant, which is what makes the middle case equality.
			store.now = func() time.Time { return base }
			ageFileTo(t, rolled, tc.mtime)

			notice, err := store.SweepExpired(retention)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if tc.expired {
				if notice.RemovedPartitions != 1 {
					t.Fatalf("a partition older than the cutoff must expire; got %#v", notice)
				}
				if _, err := os.Stat(rolled); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("expired partition should be gone; stat err=%v", err)
				}
				return
			}
			if notice.RemovedPartitions != 0 {
				t.Fatalf("a partition not strictly older than the cutoff must be retained; got %#v", notice)
			}
			if _, err := os.Stat(rolled); err != nil {
				t.Fatalf("partition at/after the cutoff was deleted: %v", err)
			}
		})
	}
}

// FR-007: the expired mark advances to the FIRST RETAINED COMPLETE GROUP, and the
// notice names it. The window's leading group lived in the swept partition; a
// surviving day-2 slot remains.
func TestArchiveExpiry_ExpiredMarkAdvancesToFirstRetainedGroup(t *testing.T) {
	store := mustStore(t, t.TempDir())
	day1, day2, rolled := rollDayOne(t, store)
	store.now = func() time.Time { return time.Now() }

	w := AddressedWindow{HasStart: true, Start: day1[0], HasEnd: true, End: day2,
		Model: []ArchiveAddress{day1[0], day1[1], day1[2], day2}}
	if err := store.SaveWindow(w); err != nil {
		t.Fatalf("save window: %v", err)
	}
	ageFileTo(t, rolled, time.Now().Add(-200*24*time.Hour))

	notice, err := store.SweepExpired(90)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !notice.Expired || notice.WindowEmptied {
		t.Fatalf("window should survive with retained content; got %#v", notice)
	}
	if notice.FirstRetained == nil || notice.FirstRetained.EntryID != "d2-user" {
		t.Fatalf("first retained slot = %#v, want d2-user", notice.FirstRetained)
	}
	got, found, err := store.LoadWindow()
	if err != nil || !found {
		t.Fatalf("load window: found=%v err=%v", found, err)
	}
	if len(got.Model) != 1 || got.Model[0].EntryID != "d2-user" {
		t.Fatalf("repaired model order = %#v, want the single retained slot", got.Model)
	}
	if !got.HasStart || got.Start.EntryID != "d2-user" {
		t.Fatalf("repaired start = %#v, want d2-user", got.Start)
	}
}

// FR-007: a leading ORPHAN tool result (its producing assistant expired) is
// trimmed so the retained order begins at a complete-group boundary.
func TestArchiveExpiry_LeadingOrphanToolResultIsTrimmed(t *testing.T) {
	store := mustStore(t, t.TempDir())
	day1, day2, rolled := rollDayOne(t, store)
	store.now = func() time.Time { return time.Now() }

	// Model order starts with day-1's tool result (its assistant is day1[0]); the
	// whole day-1 partition is then swept, orphaning nothing at the head beyond
	// day2, but a window that STARTED at the tool result must not keep it.
	w := AddressedWindow{HasStart: true, Start: day1[1], Model: []ArchiveAddress{day1[1], day2}}
	if err := store.SaveWindow(w); err != nil {
		t.Fatalf("save window: %v", err)
	}
	ageFileTo(t, rolled, time.Now().Add(-200*24*time.Hour))

	notice, err := store.SweepExpired(90)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if notice.FirstRetained == nil || notice.FirstRetained.EntryID != "d2-user" {
		t.Fatalf("repaired head = %#v, want d2-user", notice.FirstRetained)
	}
}

// FR-007 / BDD-02.3: when no complete content remains the window is EMPTIED but
// keeps the same session identity (same session ID / directory).
func TestArchiveExpiry_NoCompleteContentEmptiesSameIDWindow(t *testing.T) {
	store := mustStore(t, t.TempDir())
	day1, _, rolled := rollDayOne(t, store)
	store.now = func() time.Time { return time.Now() }

	w := AddressedWindow{HasStart: true, Start: day1[0], HasEnd: true, End: day1[2],
		Model: []ArchiveAddress{day1[0], day1[1], day1[2]}}
	if err := store.SaveWindow(w); err != nil {
		t.Fatalf("save window: %v", err)
	}
	ageFileTo(t, rolled, time.Now().Add(-200*24*time.Hour))

	before := store.sessionID
	notice, err := store.SweepExpired(90)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !notice.WindowEmptied {
		t.Fatalf("all content expired should empty the window; got %#v", notice)
	}
	if store.sessionID != before {
		t.Fatalf("session identity changed: %q -> %q", before, store.sessionID)
	}
	got, found, err := store.LoadWindow()
	if err != nil || !found {
		t.Fatalf("load window: found=%v err=%v", found, err)
	}
	if got.HasStart || got.HasEnd || len(got.Model) != 0 {
		t.Fatalf("emptied window must carry no start/end/model; got %#v", got)
	}
}

// FR-007 idempotence: a second sweep over a repaired archive performs no repair.
func TestArchiveExpiry_SecondSweepIsIdempotent(t *testing.T) {
	store := mustStore(t, t.TempDir())
	day1, day2, rolled := rollDayOne(t, store)

	w := AddressedWindow{HasStart: true, Start: day1[0], Model: []ArchiveAddress{day1[0], day2}}
	if err := store.SaveWindow(w); err != nil {
		t.Fatalf("save window: %v", err)
	}
	ageFileTo(t, rolled, time.Now().Add(-200*24*time.Hour))

	if _, err := store.SweepExpired(90); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	n2, err := store.SweepExpired(90)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if n2.WindowEmptied || n2.FirstRetained != nil {
		t.Fatalf("second sweep must perform no repair; got %#v", n2)
	}
}
