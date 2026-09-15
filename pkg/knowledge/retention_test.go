// Index retention tests (ADR-067 FR-109, FR-109a, MV-18, US-16 AS-3).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rtFixture makes a collection with an index directory under a home.
func rtFixture(t *testing.T) (home, root, indexDir string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, MarkerDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A stand-in for the index's own files, so a delete is observable as
	// something more than removing an empty directory.
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, root, dir
}

// TestIndexRetention_GracePeriodIsExactlySevenDays is spec test 73 / MV-18.
//
// It is a BOUNDARY PAIR on an injected clock, and the pair is the requirement:
// FR-109a says "exactly 7 days" precisely because the number previously lived
// only in prose, "where nothing could enforce it". A one-sided test — only the
// inside, or only the outside — passes just as happily against three days or
// against thirty, which is every wrong answer anyone would plausibly write.
func TestIndexRetention_GracePeriodIsExactlySevenDays(t *testing.T) {
	if IndexRetentionGracePeriod != 7*24*time.Hour {
		t.Fatalf("IndexRetentionGracePeriod = %v, want exactly 7 days (FR-109a)", IndexRetentionGracePeriod)
	}

	revoked := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		now     time.Time
		expired bool
	}{
		{"one minute before seven days", revoked.Add(7*24*time.Hour - time.Minute), false},
		{"exactly seven days", revoked.Add(7 * 24 * time.Hour), false},
		{"one minute after seven days", revoked.Add(7*24*time.Hour + time.Minute), true},
		{"six days", revoked.Add(6 * 24 * time.Hour), false},
		{"eight days", revoked.Add(8 * 24 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IndexRetentionExpired(revoked, tc.now); got != tc.expired {
				t.Errorf("IndexRetentionExpired(%v) = %v, want %v", tc.now, got, tc.expired)
			}
		})
	}
}

// TestIndexRetention_ReclaimableOnlyAfterTheWindow walks the same boundary
// through the on-disk marker and the sweep, because the constant being right
// says nothing about whether anything consults it.
func TestIndexRetention_ReclaimableOnlyAfterTheWindow(t *testing.T) {
	home, root, indexDir := rtFixture(t)
	revoked := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	if err := MarkIndexRevoked(home, root, revoked); err != nil {
		t.Fatalf("MarkIndexRevoked: %v", err)
	}
	at, err := IndexRevokedAt(home, root)
	if err != nil {
		t.Fatalf("IndexRevokedAt: %v", err)
	}
	if !at.Equal(revoked) {
		t.Errorf("IndexRevokedAt = %v, want %v", at, revoked)
	}

	inside, err := ReclaimableIndexes(home, revoked.Add(7*24*time.Hour-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 0 {
		t.Errorf("inside the window ReclaimableIndexes = %v, want none — US-16 AS-3 re-mounts with no rebuild", inside)
	}

	outside, err := ReclaimableIndexes(home, revoked.Add(7*24*time.Hour+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 1 || outside[0].Dir != indexDir {
		t.Fatalf("outside the window ReclaimableIndexes = %v, want exactly the index dir %q", outside, indexDir)
	}

	if err := DeleteIndexDir(home, outside[0].Dir); err != nil {
		t.Fatalf("DeleteIndexDir: %v", err)
	}
	if _, statErr := os.Stat(indexDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the index directory still exists after reclamation: %v", statErr)
	}
}

// TestIndexRetention_ReattachingInsideTheWindowStopsTheClock is US-16 AS-3
// directly: re-mounting must not merely postpone reclamation, it must cancel
// it. Leaving the mark in place would delete a LIVE index the moment the clock
// happened to pass seven days.
func TestIndexRetention_ReattachingInsideTheWindowStopsTheClock(t *testing.T) {
	home, root, _ := rtFixture(t)
	revoked := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	if err := MarkIndexRevoked(home, root, revoked); err != nil {
		t.Fatal(err)
	}
	if err := ClearIndexRevocation(home, root); err != nil {
		t.Fatalf("ClearIndexRevocation: %v", err)
	}
	if _, err := IndexRevokedAt(home, root); !errors.Is(err, ErrIndexNotRevoked) {
		t.Errorf("IndexRevokedAt after re-attach = %v, want ErrIndexNotRevoked", err)
	}
	// Long past the window, and still not reclaimable.
	got, err := ReclaimableIndexes(home, revoked.Add(365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a re-attached collection's index is reclaimable a year later: %v", got)
	}
}

// TestIndexRetention_TheClockRunsFromTheFirstRevoke: a repeated mark must not
// restart the period, or a sweep that touches the marker keeps the index alive
// forever.
func TestIndexRetention_TheClockRunsFromTheFirstRevoke(t *testing.T) {
	home, root, _ := rtFixture(t)
	first := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := MarkIndexRevoked(home, root, first); err != nil {
		t.Fatal(err)
	}
	if err := MarkIndexRevoked(home, root, first.Add(6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	at, err := IndexRevokedAt(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(first) {
		t.Errorf("IndexRevokedAt = %v, want the FIRST revoke %v", at, first)
	}
}

// TestIndexRetention_AnUnreadableMarkNeverAuthorisesADelete: deleting an index
// costs a full rebuild of the operator's whole collection; retaining one costs
// disk. Every ambiguous case must resolve to "retain".
func TestIndexRetention_AnUnreadableMarkNeverAuthorisesADelete(t *testing.T) {
	home, root, indexDir := rtFixture(t)
	markPath := filepath.Join(indexDir, indexRevocationFileName)

	for _, tc := range []struct{ name, body string }{
		{"truncated json", "{\"revoked_at\":"},
		{"empty file", ""},
		{"no time in it", "{}"},
		{"zero time", "{\"revoked_at\":\"0001-01-01T00:00:00Z\"}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(markPath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := IndexRevokedAt(home, root); err == nil {
				t.Error("an unreadable mark was reported as a valid revocation time")
			}
			got, err := ReclaimableIndexes(home, time.Now().Add(10*365*24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("an index with an unreadable mark was listed as reclaimable: %v", got)
			}
		})
	}

	// Positive control: a WELL-FORMED, long-past mark in the same directory IS
	// reclaimable, so the four refusals above are the guard rather than a sweep
	// that never returns anything.
	if err := os.WriteFile(markPath, []byte(`{"revoked_at":"2020-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReclaimableIndexes(home, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the control mark was not reclaimed: %v — the assertions above prove nothing", got)
	}
}

// TestDeleteIndexDir_RefusesAnythingOutsideTheIndexArea: the argument arrives
// from a listing today, but a recursive delete driven by a path is one wiring
// mistake away from removing the wrong tree.
func TestDeleteIndexDir_RefusesAnythingOutsideTheIndexArea(t *testing.T) {
	home, root, indexDir := rtFixture(t)
	victim := filepath.Join(root, "keepme.md")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{root, home, filepath.Join(home, indexHomeSubdir), filepath.Dir(home)} {
		if err := DeleteIndexDir(home, bad); err == nil {
			t.Errorf("DeleteIndexDir(%q) was allowed", bad)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("a refused delete removed files anyway: %v", err)
	}
	// Positive control.
	if err := DeleteIndexDir(home, indexDir); err != nil {
		t.Fatalf("DeleteIndexDir on a real index dir failed: %v", err)
	}
}
