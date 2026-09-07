// Index retention after the last mount is revoked (ADR-067 US-16 AS-3;
// FR-109, FR-109a, MV-18).
//
// # The requirement, and why it needed a home
//
// FR-109: a collection's index is deleted only when its LAST mount is revoked
// AND a grace period has elapsed. FR-109a fixes that period at exactly seven
// days, and says why it was written down as a requirement at all: "The number
// lived only in §17, where nothing could enforce it." A number in prose is a
// number that drifts to three days or thirty the first time someone guesses.
//
// So it lives here, as one constant with one decision function, and MV-18 is
// testable as a boundary PAIR: at 7d−1m the index is retained, at 7d+1m it is
// not. A one-sided test survives both wrong answers.
//
// # What this file is, and what it is not
//
// It is the POLICY: when was the last mount revoked, has the grace period
// elapsed, and which index directories are now reclaimable. It owns the small
// marker file that records the revocation instant, because "when was the last
// mount revoked" is not otherwise recoverable after a restart — mount records
// are gone by then, which is exactly what makes it the last revoke.
//
// It is NOT the lifecycle manager. Nothing here unwatches drift, closes an
// open index handle or knows which workspaces hold what: that is the gateway's
// KnowledgeLifecycle, and it is the caller. This package deliberately cannot
// see it.
//
// # Two rules the marker has to obey
//
//  1. RE-ATTACHING INSIDE THE WINDOW CLEARS THE MARK. US-16 AS-3 is "re-mount
//     within the grace period and no full rebuild occurs" — a collection that
//     is mounted again is not awaiting reclamation, and leaving the mark would
//     delete a live index the moment the clock passed seven days.
//  2. A MARK THAT CANNOT BE READ NEVER AUTHORISES A DELETE. Every unreadable,
//     malformed or absent mark answers "retain". Deleting an index is
//     recoverable only by a full rebuild of the operator's whole collection;
//     retaining one costs disk. The asymmetry decides every ambiguous case.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// IndexRetentionGracePeriod is FR-109a: exactly seven days, measured from the
// moment the collection's last mount was revoked.
//
// Written as 7*24h rather than 168h so the seven is visible in the source the
// way it is visible in the requirement.
const IndexRetentionGracePeriod = 7 * 24 * time.Hour

// indexRevocationFileName is the marker inside a collection's index directory.
// Inside the INDEX directory, under $OMNIPUS_HOME — never inside the
// operator's collection, for FR-030's reasons (a file there would be synced,
// versioned and backed up as though it were their data).
const indexRevocationFileName = "revoked.json"

// indexRevocation is the marker's on-disk shape.
type indexRevocation struct {
	// RevokedAt is when the last mount was revoked, in UTC.
	RevokedAt time.Time `json:"revoked_at"`
	// CollectionRoot is the resolved root this index belongs to, recorded so
	// a marker found under an unexpected directory is visibly not ours.
	CollectionRoot string `json:"collection_root"`
}

// ErrIndexNotRevoked reports that a collection's index carries no revocation
// mark: it is either still mounted or was never mounted.
var ErrIndexNotRevoked = errors.New("knowledge: index is not marked revoked")

// MarkIndexRevoked records that a collection's LAST mount has just been
// revoked, starting the grace period.
//
// Call it only for the last mount. Calling it while another workspace still
// holds the collection would start a clock on an index that is still being
// searched (US-16 AS-2, the mount whose search must keep working).
//
// An existing mark is NOT overwritten: the grace period runs from the FIRST
// revoke that left the collection unmounted, and re-stamping it on every pass
// of a sweep would keep the index alive forever.
func MarkIndexRevoked(home, collectionRoot string, at time.Time) error {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			// No index, nothing to retain or reclaim.
			return nil
		}
		return fmt.Errorf("knowledge: index directory %q: %w", dir, statErr)
	}
	markPath := filepath.Join(dir, indexRevocationFileName)
	if _, statErr := os.Stat(markPath); statErr == nil {
		return nil
	}
	realRoot, err := ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	payload, err := json.Marshal(indexRevocation{
		RevokedAt: when.UTC(), CollectionRoot: realRoot,
	})
	if err != nil {
		return fmt.Errorf("knowledge: encode revocation mark: %w", err)
	}
	if err := fileutil.WriteFileAtomic(markPath, payload, markerFilePerm); err != nil {
		return fmt.Errorf("knowledge: write revocation mark: %w", err)
	}
	return nil
}

// ClearIndexRevocation removes the mark, which is what re-attaching a mount
// inside the grace period must do (US-16 AS-3).
//
// Idempotent: clearing an index that carries no mark is not an error, because
// the caller's intent — "this collection is mounted, do not reclaim it" — is
// already true.
func ClearIndexRevocation(home, collectionRoot string) error {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, indexRevocationFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("knowledge: clear revocation mark: %w", err)
	}
	return nil
}

// IndexRevokedAt reports when a collection's index was marked revoked.
//
// It returns ErrIndexNotRevoked when there is no mark. A mark that exists but
// cannot be read or parsed is also reported as an error, and never as "revoked
// at the zero time" — a zero time is older than every grace period, so that
// mistake would delete the index of every collection whose marker was
// truncated by a crash.
func IndexRevokedAt(home, collectionRoot string) (time.Time, error) {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return time.Time{}, err
	}
	return readIndexRevocation(dir)
}

func readIndexRevocation(indexDir string) (time.Time, error) {
	raw, err := os.ReadFile(filepath.Join(indexDir, indexRevocationFileName)) //nolint:gosec // path is derived from $OMNIPUS_HOME
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, ErrIndexNotRevoked
		}
		return time.Time{}, fmt.Errorf("knowledge: read revocation mark: %w", err)
	}
	var mark indexRevocation
	if err := json.Unmarshal(raw, &mark); err != nil {
		return time.Time{}, fmt.Errorf("knowledge: revocation mark is unreadable: %w", err)
	}
	if mark.RevokedAt.IsZero() {
		return time.Time{}, errors.New("knowledge: revocation mark carries no time")
	}
	return mark.RevokedAt.UTC(), nil
}

// IndexRetentionExpired decides FR-109a for one collection: has the grace
// period elapsed since revokedAt, as of now?
//
// The boundary is CLOSED at exactly seven days — an index is still retained at
// the instant the period ends and reclaimable strictly after it. Which side the
// exact instant falls on matters to nobody in practice and matters a great deal
// to a test, which is why it is stated rather than left to whichever comparison
// operator was typed first.
func IndexRetentionExpired(revokedAt, now time.Time) bool {
	if revokedAt.IsZero() {
		return false
	}
	return now.After(revokedAt.Add(IndexRetentionGracePeriod))
}

// ReclaimableIndex is one index directory whose grace period has elapsed.
type ReclaimableIndex struct {
	// Dir is the index directory under $OMNIPUS_HOME.
	Dir string
	// CollectionRoot is the collection it was built from, as recorded in the
	// mark. Informational: the folder may be long gone.
	CollectionRoot string
	// RevokedAt is when the last mount was revoked.
	RevokedAt time.Time
}

// ReclaimableIndexes lists every index under $OMNIPUS_HOME whose last mount was
// revoked more than the grace period ago.
//
// It reads and decides; it deletes nothing. Deletion is DeleteIndexDir, kept
// separate so a caller can log, count or dry-run the decision without the
// listing itself being destructive.
//
// A directory whose mark is missing (still mounted, or never mounted) or
// unreadable is skipped, per this file's second rule.
func ReclaimableIndexes(home string, now time.Time) ([]ReclaimableIndex, error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("knowledge: omnipus home is empty")
	}
	base := filepath.Join(home, indexHomeSubdir)
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("knowledge: list index directories: %w", err)
	}
	var out []ReclaimableIndex
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		revokedAt, readErr := readIndexRevocation(dir)
		if readErr != nil {
			continue
		}
		if !IndexRetentionExpired(revokedAt, now) {
			continue
		}
		root := ""
		if raw, rErr := os.ReadFile(filepath.Join(dir, indexRevocationFileName)); rErr == nil { //nolint:gosec // derived from $OMNIPUS_HOME
			var mark indexRevocation
			if json.Unmarshal(raw, &mark) == nil {
				root = mark.CollectionRoot
			}
		}
		out = append(out, ReclaimableIndex{Dir: dir, CollectionRoot: root, RevokedAt: revokedAt})
	}
	return out, nil
}

// DeleteIndexDir removes one index directory and everything under it.
//
// It refuses any path that is not inside $OMNIPUS_HOME's index area. The
// argument comes from ReclaimableIndexes today, but a recursive delete driven
// by a path is one wiring mistake away from removing the wrong tree, and the
// check costs a string comparison.
func DeleteIndexDir(home, dir string) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("knowledge: omnipus home is empty")
	}
	base := filepath.Join(home, indexHomeSubdir)
	clean := filepath.Clean(dir)
	if !isWithinOrEqual(base, clean) || clean == filepath.Clean(base) {
		return fmt.Errorf("%w: %q is not an index directory under %q", ErrOutsideCollection, dir, base)
	}
	if err := os.RemoveAll(clean); err != nil {
		return fmt.Errorf("knowledge: delete index directory %q: %w", clean, err)
	}
	return nil
}
