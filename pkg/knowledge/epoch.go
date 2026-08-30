// Omnipus — ADR-068 / vault-records spec §"index_epoch": the properties
// index's generation counter, and the three structural changes that move it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ---------------------------------------------------------------------------
// WHAT index_epoch IS, PER SPEC
//
// "An index_epoch is a monotonic per-collection integer, stored beside the
// index, incremented ONLY on a structural change: a full rebuild, a schema
// create/edit/delete, or a note leaving the index (trash). An ordinary note
// re-index does NOT bump it." (vault-records-spec-2026-08-25.md)
//
// It exists so knowledge_find's cursor can detect "the shape of the index
// changed under you" (a rebuild, a schema edit, a trashed note) without
// treating every ordinary re-index — which happens continuously as an
// operator edits notes — as invalidating every outstanding cursor.
//
// THIS FILE IS THE ONLY PLACE index_epoch IS READ FROM OR WRITTEN TO DISK.
// The three call sites that bump it (knowledge.OpenIndex on a genuine
// rebuild; knowledge_configure.go's three record-type mutations;
// knowledge_restructure.go's execTrash) all go through BumpIndexEpoch, never
// through their own file write — a second writer of this file is a second
// chance for the "no more, no fewer than three sites" rule to be violated
// silently.
// ---------------------------------------------------------------------------

// indexEpochFileName is the epoch counter's file, stored beside the bleve
// index and the properties database under IndexDirFor's directory — FR-030's
// "outside the collection", the same directory PropertiesIndexPath already
// uses for properties.db.
const indexEpochFileName = "epoch.json"

// epochFile is the on-disk shape. A struct rather than a bare integer so a
// file that is not this format (or a future format change) is DETECTABLY
// wrong rather than silently parsed as some unrelated integer.
type epochFile struct {
	Epoch int64 `json:"epoch"`
}

// epochLocks is the IN-PROCESS half of BumpIndexEpoch's concurrency
// guarantee, keyed by the epoch file's own resolved path. It exists
// alongside fileutil.WithFlock below, not instead of it — see
// BumpIndexEpoch's doc comment for why one alone is not the whole story.
var (
	epochLocksMu sync.Mutex
	epochLocks   = map[string]*sync.Mutex{}
)

func epochLockFor(path string) *sync.Mutex {
	epochLocksMu.Lock()
	defer epochLocksMu.Unlock()
	m, ok := epochLocks[path]
	if !ok {
		m = &sync.Mutex{}
		epochLocks[path] = m
	}
	return m
}

// indexEpochPath resolves the epoch file's path for one collection root,
// reusing IndexDirFor so the epoch, the properties index and the bleve index
// are always found beside one another under one directory.
func indexEpochPath(home, collectionRoot string) (string, error) {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, indexEpochFileName), nil
}

// IndexEpoch reads a collection's current index_epoch.
//
// ABSENCE VS CORRUPTION MUST NOT COLLAPSE TO THE SAME RETURN, AND THIS IS
// NOT A STYLE CHOICE.
//
// A collection that has never undergone a structural change — nothing has
// ever rebuilt its index, created/edited/deleted a schema, or trashed a note
// out of it — has NO epoch file at all, and 0 is its true, honest baseline:
// a fresh collection, not a guess about one that is broken. A collection
// whose epoch file EXISTS but cannot be read or parsed is a different fact
// entirely — something is wrong with this collection's index directory —
// and reporting 0 for it would tell a cursor check "nothing has changed"
// about a file that could not be read at all.
//
// This distinction is drawn deliberately because this branch has already
// shipped its twin defect twice: an unset index phase that compared equal to
// nothing and reported "indexing" forever, and an unreadable manifest that
// silently returned as an EMPTY one, leaving deleted notes searchable
// indefinitely. IndexEpoch returns (0, nil) for "never touched" and
// (0, non-nil error) for "unreadable" — a caller MUST check the error before
// trusting the 0, and every caller in this tree does (BumpIndexEpoch below;
// the knowledge_find wiring in pkg/gateway treats a non-nil error as a
// refusal, never as epoch zero).
func IndexEpoch(home, collectionRoot string) (int64, error) {
	path, err := indexEpochPath(home, collectionRoot)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from IndexDirFor, not caller input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No structural change has ever happened to this collection.
			// This is the honest, fresh baseline — not a guess.
			return 0, nil
		}
		return 0, fmt.Errorf("knowledge: reading index epoch %s: %w", path, err)
	}
	var ef epochFile
	if err := json.Unmarshal(data, &ef); err != nil {
		return 0, fmt.Errorf("knowledge: index epoch %s is unreadable: %w", path, err)
	}
	if ef.Epoch < 0 {
		return 0, fmt.Errorf("knowledge: index epoch %s holds a negative value %d", path, ef.Epoch)
	}
	return ef.Epoch, nil
}

// BumpIndexEpoch increments a collection's index_epoch by exactly one and
// persists the new value, returning it.
//
// WHEN TO CALL THIS, AND ONLY THIS. The spec names exactly three structural
// changes: a full index rebuild (knowledge.OpenIndex, when its
// RebuildReason() is non-empty — NOT a first-ever build, which returns "" on
// purpose; see openOrRebuild's own comment), a schema create/edit/delete
// (knowledge_configure.go's three record-type mutations, never its two view
// mutations — a saved view is not part of the properties index schema), and
// a note LEAVING the index via trash (knowledge_restructure.go's execTrash,
// never execRestore — a note RE-ENTERING the index is not the event the spec
// names). An ordinary note re-index does not bump it. A caller found calling
// this from a fourth site is a correctness bug in the caller, not a missing
// case here.
//
// CONCURRENCY GUARANTEE, STATED RATHER THAN IMPLIED. This pairs an
// in-process mutex (epochLockFor, real on every platform) with
// fileutil.WithFlock on a SIDECAR lock file (real on POSIX; a documented
// no-op on Windows — pkg/fileutil/flock_windows.go). So: two goroutines in
// one process can never lose a bump, on any platform. Two Omnipus PROCESSES
// sharing one $OMNIPUS_HOME can lose a bump only on Windows — which matches
// every file-store package in this tree audited under ADR-054 §5 (pkg/task,
// pkg/plan, pkg/session, pkg/credentials, pkg/auth, pkg/agentstore all pair
// an in-process mutex with the same no-op WithFlock on that platform;
// pkg/entity documents the identical POSIX-only cross-process guarantee).
// This is not a new gap this file introduces; it is the existing, documented
// shape of every comparable store in this codebase.
//
// The lock is a SIDECAR file (path+".lock"), never the epoch.json data file
// itself, because WithFlock holds an open read/write handle for its
// duration and an open handle on the destination file blocks
// WriteFileAtomic's rename on some platforms — the exact reason
// fileutil.WithFlock's own doc comment gives for using a sidecar.
//
// A write that fails leaves the PREVIOUS epoch file on disk untouched
// (fileutil.WriteFileAtomic never leaves a half-written file), so a failed
// bump is a loud error to the caller, never a silently corrupted counter.
func BumpIndexEpoch(home, collectionRoot string) (int64, error) {
	path, err := indexEpochPath(home, collectionRoot)
	if err != nil {
		return 0, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("knowledge: create index dir %s: %w", dir, err)
	}
	lockPath := path + ".lock"

	local := epochLockFor(path)
	local.Lock()
	defer local.Unlock()

	var next int64
	err = fileutil.WithFlock(lockPath, func() error {
		cur, rerr := IndexEpoch(home, collectionRoot)
		if rerr != nil {
			return rerr
		}
		next = cur + 1
		data, merr := json.Marshal(epochFile{Epoch: next})
		if merr != nil {
			return fmt.Errorf("knowledge: encode index epoch: %w", merr)
		}
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// bumpIndexEpochOrWarn is BumpIndexEpoch for the three non-fatal call sites:
// the mutation that TRIGGERS a bump (a rebuild that has already replaced the
// index; a schema write that has already landed; a trash that has already
// moved the note) has already applied regardless of whether the counter can
// be updated afterwards, so a bump failure is reported LOUDLY and the caller
// proceeds. Refusing an already-applied schema write because a secondary
// cache-invalidation counter could not be written would be a worse failure
// than a stale counter — the counter's only consequence is that a cursor
// issued before the change may be honoured a little longer than ideal, never
// that a write is lost or a wrong answer is returned.
func bumpIndexEpochOrWarn(component, home, collectionRoot string) {
	if _, err := BumpIndexEpoch(home, collectionRoot); err != nil {
		slog.Error("knowledge: could not bump index epoch after a structural change; "+
			"a knowledge_find cursor issued before this change may be accepted as still valid",
			"component", component, "root", collectionRoot, "error", err)
	}
}
