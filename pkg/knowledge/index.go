// Omnipus — knowledge base full-text index (ADR-067 stage 2, unit B2).
//
// A bleve scorch index over a mounted collection of markdown notes. The engine
// choice, the rebuild-on-corruption behaviour and the reference-counted
// process-wide registry are copied from pkg/memrooms/index, which proved them.
// Four things here deliberately differ from that precedent, and each difference
// is a requirement rather than a preference:
//
//  1. WHERE IT LIVES (FR-030). pkg/memrooms/index writes to <root>/.index/bleve
//     — inside the corpus. That is exactly wrong for a knowledge base: the
//     corpus is the operator's own folder, very likely inside iCloud, Dropbox or
//     git. We do not leave our database in it. The index lives under
//     $OMNIPUS_HOME and the collection is left byte-for-byte untouched.
//
//  2. HOW IT IS IDENTIFIED (FR-031). The key is the collection root's resolved
//     REAL path, not a workspace or mount id. One host folder mounted into three
//     workspaces is one corpus and gets one index, shared and reference-counted;
//     the last release closes it, and no earlier release may.
//
//  3. WHAT A DOCUMENT IS (FR-034a) — the deviation most likely to be got wrong.
//     pkg/memrooms/index indexes ONE DOCUMENT PER FILE. Copying that shape here
//     would make peak memory a property of the single largest note in the
//     operator's collection, so a 200 MB note would either OOM the gateway or
//     have to be refused — and refusing is forbidden. Reading the file in chunks
//     does not fix it: chunked reading bounds the read buffer, while the index's
//     unit of work is the DOCUMENT, and a whole-note document is analysed whole
//     no matter how its bytes arrived. So a note over IndexSegmentSize becomes
//     several consecutive documents, each carrying the note's path and the
//     ABSOLUTE byte offset of its start. Search then collapses the segments of
//     one note back into ONE result, scored by its best segment. No note is ever
//     refused, skipped or truncated.
//
//  4. WHAT IS STORED (FR-050a). Note bodies are indexed but NOT stored, and
//     term vectors are off. Excerpts are produced by re-reading the file at
//     query time so they always match disk; an excerpt cached in the index would
//     be a copy that silently goes stale. The absolute offsets in (3) are what
//     let that re-read land in the right place.
//
// Attachments (FR-039a) are indexed by filename and path ONLY. The indexer never
// opens one, for any reason — not to hash it, not to sniff it, not to size it
// beyond the Lstat the walk already did. Every content read in this package goes
// through the single openFileForRead seam below, so "zero content reads from
// attachments" is a property a test can count rather than a claim a comment can
// make.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	bleveIndexAPI "github.com/blevesearch/bleve_index_api"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	// IndexSegmentSize is the size of ONE index document cut from a note
	// (FR-034a). It is a segmentation unit, NOT a size limit: a note of any size
	// is indexed in full, as ceil(size/this) consecutive segments.
	//
	// FR-034a states the requirement as "a note over 8 MB is indexed as
	// consecutive segments". This constant is deliberately SMALLER than that
	// 8 MB threshold. That satisfies the requirement a fortiori — every note
	// over 8 MB is certainly segmented — and it is smaller for a measured
	// reason rather than a cautious one.
	//
	// bleve's analysis-and-build path costs on the order of NINETY TIMES the
	// size of the document it is handed. Measured through this package, in a
	// fresh process, as heap obtained from the OS:
	//
	//	48 MiB note:   1 MiB segments →  96 MB    4 MiB → 377 MB
	//	               2 MiB segments → 192 MB    8 MiB → 721 MB
	//	200 MiB note:  512 KiB segments → 66 MB   1 MiB → 125 MB
	//
	// The dominant term is linear in the SEGMENT size and near-flat in the FILE
	// size — which is precisely the property FR-034a exists to produce. But the
	// constant of proportionality means an 8 MiB document peaks at 721 MB, which
	// blows both MV-2's 512 MB initial-index budget and spec test 62's 128 MB
	// ceiling. 512 KiB lands at 66 MB for a 200 MiB note: comfortably inside the
	// budget rather than 2% under it, which is the difference between a bound
	// and a coincidence.
	//
	// The practical effect on an ordinary collection is nil. A note has to exceed
	// half a million characters before it becomes more than one document at all.
	IndexSegmentSize = 512 << 10 // 512 KiB

	// IndexSegmentThreshold is the size FR-034a names as the point past which a
	// note MUST be segmented. It is kept as its own named constant so the
	// requirement stays testable independently of IndexSegmentSize, which is
	// smaller: any note larger than this must produce more than one index
	// document, whatever the segment size happens to be.
	IndexSegmentThreshold = 8 << 20 // 8 MiB

	// indexBatchMaxDocs and indexBatchMaxBytes bound one batch commit
	// (FR-034). Indexing NEVER accumulates a single whole-collection batch —
	// that is the shape ADR-067 §1.2 criticises Obsidian for, and it is what
	// pkg/memrooms/index's rebuildLocked does.
	indexBatchMaxDocs  = 128
	indexBatchMaxBytes = IndexSegmentSize

	// indexDirMode and indexFileMode are FR-032. bleve already creates its own
	// directories 0700 and its zap/bolt files 0600, but its index_meta.json is
	// created 0666&umask — so the modes are re-asserted rather than assumed.
	indexDirMode  fs.FileMode = 0o700
	indexFileMode fs.FileMode = 0o600

	// indexHomeSubdir is the directory under $OMNIPUS_HOME that holds every
	// collection index. Derived data: safe to delete, rebuilt on next open.
	indexHomeSubdir = "knowledge"

	// indexBleveSubdir separates the bleve index from the manifest that sits
	// beside it, so removing a corrupt index never removes its own record of
	// what to rebuild from.
	indexBleveSubdir = "bleve"

	// indexFormatFileName is the format sidecar, written beside the manifest.
	// It records which on-disk index format wrote the segments under
	// indexBleveSubdir. See indexFormatVersion.
	indexFormatFileName = "index_format.json"

	// boltOpenTimeout bounds the wait for scorch's process-exclusive root.bolt
	// lock. The registry means we open each index once, so this only ever fires
	// on a genuinely stuck or stale lock — where an error beats a hang.
	boltOpenTimeout = "5s"

	// segmentIDSeparator joins a note's path and its segment ordinal into a
	// bleve document id. U+001F (unit separator) cannot occur in a filename
	// this package will accept.
	segmentIDSeparator = "\x1f"

	fieldPath   = "path"
	fieldName   = "name"
	fieldKind   = "kind"
	fieldOffset = "offset"
	fieldBody   = "body"
)

// openFileForRead is the SINGLE seam through which this package reads file
// contents. It exists so a test can count content reads by path and prove
// FR-039a/MV-19 — "indexing 100,000 attachments reads zero content bytes from
// them" — instead of asserting the absence of a behaviour, which no ordinary
// test can do. Production always uses os.Open.
var openFileForRead = func(path string) (*os.File, error) { return os.Open(path) } //nolint:gosec // collection paths are operator-owned and contained by the caller

// IndexHit is one search result: exactly one per NOTE (or attachment), never
// one per segment.
type IndexHit struct {
	// Path is the collection-relative, slash-separated path.
	Path string
	// Kind is note or attachment.
	Kind ScanKind
	// Score is the relevance score of the file's BEST segment. It is a BM25
	// score because buildIndexMapping sets the scoring model explicitly; bleve's
	// default is TF-IDF (ADR-068 D21.1). Scores are comparable only within one
	// result set — BM25 is not normalised across queries or across indexes.
	Score float64
	// Offset is the absolute byte offset, within the file, of the start of the
	// best-scoring segment. FR-050a's query-time excerpt re-read starts here;
	// it is absolute precisely so segmentation cannot misdirect it.
	Offset int64
	// Segment is the ordinal of the best-scoring segment (0 for any file that
	// produced a single document).
	Segment int
}

// SyncStats reports what one reconcile actually did. Every field is a count a
// test can assert on; "it worked" is not an oracle.
type SyncStats struct {
	// Scanned is every file the walk found.
	Scanned int
	// Indexed is the files that were (re-)parsed into index documents.
	Indexed int
	// Unchanged is the files skipped because nothing changed (FR-033).
	Unchanged int
	// Removed is the files that were in the manifest but no longer on disk.
	Removed int
	// Segments is the total number of index documents written this run. It
	// exceeds Indexed exactly when some note was larger than IndexSegmentSize.
	Segments int
	// BatchCommits is how many bounded batches were committed (FR-034). One
	// commit for a large collection would mean a single whole-collection batch.
	BatchCommits int
	// Problems carries the walk's skipped symlinks and unreadable paths.
	Problems []ScanProblem
}

// SyncOptions tunes one reconcile.
type SyncOptions struct {
	// Deep makes the reconcile verify NOTE contents by hash rather than trust
	// size and mtime — FR-033's third criterion, for the drift check. It costs
	// a full read of every note, so it is never the default path.
	//
	// It does NOT read attachments. FR-039a has no exception for verification.
	Deep bool

	// OnProgress reports how far this reconcile has got, WHILE it runs.
	//
	// indexed is how many of the walk's files this run has reconciled so far —
	// newly indexed plus verified-unchanged — and total is how many files the
	// walk found. It is the same arithmetic the run's final SyncStats reports
	// (Indexed + Unchanged against Scanned), so the last call of a successful
	// run states exactly the numbers the caller will read off the return value,
	// and a caller never has to reconcile two different definitions of "done".
	//
	// A file that could not be read moves neither number: it is counted in
	// Problems, not in indexed, so the count can pause without ever going
	// backwards. indexed never exceeds total and never decreases.
	//
	// The call is COALESCED — see the SyncWith doc comment for the rule and the
	// reason. A caller that turns each call into a WebSocket frame therefore
	// gets a bounded stream rather than one frame per file, and does not have to
	// invent a throttle of its own (nor remember to). Nil disables reporting.
	//
	// It is called from the goroutine running SyncWith, with the index's write
	// lock held: it must not call back into this Index, and it should not block.
	OnProgress func(indexed, total int)

	// ProgressInterval is the shortest wall-clock gap between two OnProgress
	// calls. Zero means DefaultProgressInterval. The final call of a run ignores
	// it, so a run shorter than one interval still reports its result exactly
	// once rather than not at all.
	ProgressInterval time.Duration
}

const (
	// DefaultProgressInterval is the coalescing window for SyncOptions.
	// OnProgress: at most one call per this much wall-clock time.
	//
	// 200ms is five updates a second. A person reading a number cannot absorb
	// more than that, and the cost of the frames is paid whether they can or
	// not, so anything faster buys nothing and spends bandwidth and main-thread
	// time on the client.
	DefaultProgressInterval = 200 * time.Millisecond

	// maxProgressUpdates bounds how many OnProgress calls one run may make
	// regardless of how long it takes.
	//
	// Time alone is not a sufficient bound: a very large collection can index
	// for an hour, and 5/s for an hour is 18,000 frames. A count bound makes the
	// worst case a property of the RUN rather than of its duration — one update
	// per 0.1% of the collection, which is finer than any progress bar can
	// render and finer than any reader can notice.
	maxProgressUpdates = 1000
)

// progressStride is the minimum number of files that must be reconciled between
// two OnProgress calls, so a run makes at most maxProgressUpdates of them.
//
// It rounds UP, so the bound holds for every total: a 1,500-file collection
// gets a stride of 2 (≤750 updates), not a stride of 1 (1,500 updates).
func progressStride(total int) int {
	if total <= maxProgressUpdates {
		return 1
	}
	return (total + maxProgressUpdates - 1) / maxProgressUpdates
}

// progressCoalescer applies the two bounds above to a stream of absolute counts.
//
// Both must be satisfied for an update to go out, which is what makes the two
// bounds compose: a run emits at most min(maxProgressUpdates, elapsed/interval)
// updates, plus one final flush. The final flush is unconditional on both
// bounds — without it a run would routinely stop short of its own total (the
// last few files rarely land exactly on a stride boundary at the moment an
// interval expires), and a progress number that stops at 99,940 of 100,000 is
// precisely the confidently-wrong report ADR-067 exists to prevent.
type progressCoalescer struct {
	fn       func(indexed, total int)
	total    int
	stride   int
	interval time.Duration
	now      func() time.Time
	lastAt   time.Time
	lastN    int
}

func newProgressCoalescer(
	fn func(indexed, total int), total int, interval time.Duration, now func() time.Time,
) *progressCoalescer {
	if now == nil {
		now = time.Now
	}
	if interval <= 0 {
		interval = DefaultProgressInterval
	}
	return &progressCoalescer{
		fn:       fn,
		total:    total,
		stride:   progressStride(total),
		interval: interval,
		now:      now,
		// The clock starts now, so the first update waits a whole interval:
		// the caller has just been told the run began and does not need to be
		// told again in the same millisecond.
		lastAt: now(),
	}
}

// update offers an absolute count. It reports only if both bounds allow it.
func (c *progressCoalescer) update(indexed int) {
	if c == nil || c.fn == nil {
		return
	}
	if indexed-c.lastN < c.stride {
		return
	}
	at := c.now()
	if at.Sub(c.lastAt) < c.interval {
		return
	}
	c.report(indexed, at)
}

// flush reports the run's final count, whatever the bounds say — unless that
// exact count has already gone out, in which case there is nothing left to say.
func (c *progressCoalescer) flush(indexed int) {
	if c == nil || c.fn == nil || indexed == c.lastN {
		return
	}
	c.report(indexed, c.now())
}

func (c *progressCoalescer) report(indexed int, at time.Time) {
	if indexed > c.total {
		indexed = c.total
	}
	if indexed < 0 {
		indexed = 0
	}
	c.lastN = indexed
	c.lastAt = at
	c.fn(indexed, c.total)
}

// Index is an open scorch index over one collection.
//
// A single *Index is SHARED process-wide by every mount naming the same
// resolved real path (FR-031) and reference counted; Close releases this
// holder's reference and physically closes the handle only when the last holder
// lets go.
type Index struct {
	idx          bleve.Index
	dir          string // <home>/knowledge/<key>
	blevePath    string // <dir>/bleve
	manifestPath string // <dir>/manifest.json
	formatPath   string // <dir>/index_format.json
	root         string // collection root, resolved real path

	// rebuildReason is why the index on disk was discarded and recreated when
	// this handle was opened, or "" if it was opened as it stood. It is set
	// once, inside OpenIndex, before the handle is published to the registry,
	// and never written again — so it needs no lock, and a second holder of a
	// shared handle correctly reads the reason of the open that created it.
	rebuildReason string

	mu sync.Mutex // serializes writes (Sync); scorch is read-safe concurrently

	// regKey is the registry key (the resolved real root) this handle is shared
	// under. Empty for a handle the registry does not manage.
	regKey string
}

// IndexDirFor returns the directory under $OMNIPUS_HOME that holds the index and
// manifest for a collection root — FR-030's "outside the collection". The name
// is derived from the root's resolved real path, so two mounts of one folder
// name one directory (FR-031) and two different folders can never collide.
func IndexDirFor(home, collectionRoot string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("knowledge: omnipus home is empty")
	}
	realRoot, err := ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, indexHomeSubdir, indexKeyFor(realRoot)), nil
}

// indexKeyFor is the stable directory name for a resolved collection root.
func indexKeyFor(realRoot string) string {
	sum := sha256.Sum256([]byte(realRoot))
	return hex.EncodeToString(sum[:])[:32]
}

// OpenIndex opens (or creates) the shared index for a collection root.
//
// The returned *Index is reference counted: opening the same collection twice —
// including by two different paths that resolve to it — returns the SAME handle
// with a second reference, and only the last Close closes it. That is FR-031,
// and it is also what stops the second open from deadlocking on scorch's
// process-exclusive bolt lock.
//
// A corrupt index is removed and recreated. Its manifest is removed with it, so
// the following Sync rebuilds from the collection rather than trusting a record
// of an index that no longer exists.
//
// So is an index that CAN be opened but must not be trusted — see
// openOrRebuild. RebuildReason reports which of the two happened.
func OpenIndex(home, collectionRoot string) (*Index, error) {
	realRoot, err := ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return nil, err
	}
	dir, err := IndexDirFor(home, realRoot)
	if err != nil {
		return nil, err
	}

	return acquireSharedIndex(realRoot, func() (*Index, error) {
		ix := &Index{
			dir:          dir,
			blevePath:    filepath.Join(dir, indexBleveSubdir),
			manifestPath: filepath.Join(dir, ManifestFileName),
			formatPath:   filepath.Join(dir, indexFormatFileName),
			root:         realRoot,
		}
		if mkErr := os.MkdirAll(dir, indexDirMode); mkErr != nil {
			return nil, fmt.Errorf("knowledge: create index dir %s: %w", dir, mkErr)
		}

		bidx, reason, openErr := ix.openOrRebuild()
		if openErr != nil {
			return nil, openErr
		}
		ix.idx = bidx
		ix.rebuildReason = reason

		if permErr := enforceIndexPermissions(dir); permErr != nil {
			closeIndexQuietly(bidx, ix.blevePath)
			return nil, permErr
		}
		return ix, nil
	})
}

// RebuildReason reports, in one sentence a person can act on, why the index on
// disk was discarded and recreated when this handle was opened. It is "" when
// the index was opened as it stood, and "" on a first open that had nothing to
// discard.
//
// It is the seam the index-state surface reads: a caller that shows index state
// can say WHY a collection is being re-read from zero instead of leaving the
// operator to guess. Nothing in this package renders it.
//
// Between OpenIndex returning a non-empty reason and the next Sync completing,
// the index holds NO documents. That is not a new state — it is exactly the
// state a first-ever open leaves behind, and the manifest is removed with the
// index so callers that gate on the manifest (knowledge_search reports
// index_state "not_built" and refuses to answer) already treat it correctly
// rather than reporting a confident zero results.
func (ix *Index) RebuildReason() string { return ix.rebuildReason }

// openOrRebuild opens the bleve index under ix.blevePath, rebuilding it from
// scratch when what is on disk cannot be trusted. It returns the open index and
// the rebuild reason ("" if none).
//
// THE POINT OF THIS FUNCTION IS THAT AN UNTRUSTWORTHY INDEX CANNOT BE OPENED
// QUIETLY. There are three ways an index reaches us in a state that must not be
// searched, and each fails differently:
//
//  1. It will not open at all — corruption bleve itself detects. This was
//     already handled and still is.
//  2. It opens fine and its segments are silently wrong. This is ADR-068 F-0:
//     zapx v17.1.2 miscalculates chunk offsets while WRITING, so a search over
//     a 100,000-document index panics with a slice bound out of range — a panic
//     that is not recovered anywhere in bleve's call stack, so in the gateway it
//     is a process crash. Pinning zapx ≥ v17.1.4 fixes new writes and does
//     nothing whatever for segments already on disk. Only guard G1, the format
//     version, can see this: the bytes look valid until they are read.
//  3. It opens fine and its MAPPING is not the mapping the code now builds.
//     bleve.OpenUsing takes no mapping argument — the mapping persisted at
//     creation is authoritative forever after — so a field the code has since
//     added, or whose analyzer it has since changed, produces zero hits and NO
//     ERROR. Guard G2 catches this.
//
// G1 and G2 are both here because neither subsumes the other. G1 depends on a
// human remembering to bump indexFormatVersion; G2 depends on nobody
// remembering anything, and catches exactly the case where the bump was
// forgotten. G1 catches the case G2 cannot see at all — segments that are wrong
// while the mapping is right.
func (ix *Index) openOrRebuild() (bleve.Index, string, error) {
	if _, statErr := os.Stat(ix.blevePath); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("knowledge: stat index %s: %w", ix.blevePath, statErr)
		}
		// Nothing on disk to distrust. Not a rebuild: there was no index.
		bidx, err := ix.createFreshIndex()
		return bidx, "", err
	}

	reason := ix.formatStaleReason() // G1
	if reason == "" {
		bidx, err := bleve.OpenUsing(ix.blevePath, bleveOpenConfig())
		switch {
		case err != nil:
			reason = fmt.Sprintf("the index could not be opened (%v)", err)
		default:
			if drift := mappingDrift(bidx.Mapping()); drift != "" { // G2
				closeIndexQuietly(bidx, ix.blevePath)
				reason = "the index was written with a different document mapping: " + drift
			} else {
				return bidx, "", nil
			}
		}
	}

	slog.Warn("knowledge: index on disk cannot be trusted; discarding it and rebuilding from the collection",
		"path", ix.blevePath, "root", ix.root, "reason", reason)
	if rmErr := os.RemoveAll(ix.blevePath); rmErr != nil {
		return nil, "", fmt.Errorf("knowledge: remove untrusted index %s: %w", ix.blevePath, rmErr)
	}
	if rmErr := os.Remove(ix.formatPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("knowledge: remove stale index format %s: %w", ix.formatPath, rmErr)
	}
	bidx, err := ix.createFreshIndex()
	if err != nil {
		return nil, "", err
	}
	return bidx, reason, nil
}

// createFreshIndex creates an empty index with the CURRENT mapping and stamps
// the current format version beside it.
//
// It removes the manifest first, and that removal is load-bearing rather than
// tidy: the manifest is what makes Sync incremental, so a manifest that
// outlives its index makes the next Sync skip every file as "unchanged" against
// documents that no longer exist — an empty index that reports itself complete,
// which is precisely the silent no-op this whole path exists to make impossible.
func (ix *Index) createFreshIndex() (bleve.Index, error) {
	if mkErr := os.MkdirAll(filepath.Dir(ix.blevePath), indexDirMode); mkErr != nil {
		return nil, fmt.Errorf("knowledge: create index parent dir %s: %w", filepath.Dir(ix.blevePath), mkErr)
	}
	if rmErr := os.Remove(ix.manifestPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("knowledge: remove stale manifest %s: %w", ix.manifestPath, rmErr)
	}
	bidx, err := bleve.NewUsing(ix.blevePath, buildIndexMapping(), scorch.Name, scorch.Name, bleveOpenConfig())
	if err != nil {
		return nil, fmt.Errorf("knowledge: create index %s: %w", ix.blevePath, err)
	}
	// The stamp is written AFTER the index exists and its failure is fatal: an
	// index with no stamp is an index this function would rebuild again on the
	// next open, forever, and a rebuild loop nobody is told about is worse than
	// a failed open somebody is.
	if err := writeIndexFormat(ix.formatPath); err != nil {
		closeIndexQuietly(bidx, ix.blevePath)
		return nil, err
	}
	return bidx, nil
}

// bleveOpenConfig is the runtime config every open and create passes to scorch.
func bleveOpenConfig() map[string]any {
	return map[string]any{"bolt_timeout": boltOpenTimeout}
}

// closeIndexQuietly closes an index we are abandoning. The close error cannot be
// returned — we are already on an error path and the caller's error is the one
// that explains what happened — but it is not discarded either: a close that
// fails leaves a bolt lock held, which is the next thing that will go wrong.
func closeIndexQuietly(bidx bleve.Index, path string) {
	if bidx == nil {
		return
	}
	if err := bidx.Close(); err != nil {
		slog.Warn("knowledge: closing abandoned index failed", "path", path, "error", err)
	}
}

// indexFormatVersion is the CURRENT on-disk index format — guard G1.
//
// BUMP THIS WHENEVER SEGMENTS WRITTEN BY OLDER CODE MUST NOT BE SEARCHED. That
// covers two different things and both are real:
//
//   - the WRITER changed in a way that makes older bytes wrong. Version 1 is
//     this case. Everything written before it may have been written by
//     zapx v17.1.2, which miscalculates chunk offsets and produces segments
//     that panic the process on a search once a collection is large enough to
//     force a big merge (ADR-068 F-0, ~100,000 documents). The version pin to
//     zapx ≥ v17.1.4 fixes what is written next and repairs nothing already
//     written; this constant is the other half of that fix.
//   - the MAPPING changed such that old documents lack fields, or carry them
//     under a different analyzer. G2 (mappingDrift) also catches that, and is
//     the guard that does not depend on this constant being bumped — but a bump
//     is cheaper to reason about and fires before the index is even opened.
//
// Version 2 is the second case: ADR-068 D21.1 set the mapping's ScoringModel to
// BM25, having found that bleve was scoring TF-IDF everywhere while thirteen
// places in the tree said otherwise. The scoring model is a property of the
// PERSISTED mapping, so an index written under version 1 keeps scoring TF-IDF
// however the code is compiled. Nothing fails; the ranking is simply not the
// one the code asks for. That is why it is both a bump here and a comparison in
// mappingDrift — a scoring change that does not force a rebuild is a change
// that has not happened.
//
// A rebuild costs one full re-index of the collection and never costs an answer:
// the notes on disk are the source of truth and the index is derived data.
// Getting this wrong in the cautious direction is a slow start-up. Getting it
// wrong in the other direction is a crash in the gateway.
const indexFormatVersion = 2

// indexFormat is the sidecar's content. It is deliberately one integer: a
// record with more in it is a record with more ways to disagree with itself,
// and everything else worth knowing (the bleve and zapx versions in force) is
// in go.mod, where it cannot drift from what is actually linked.
type indexFormat struct {
	Version int `json:"version"`
}

// readIndexFormat reports the format version recorded beside the index.
//
// A MISSING SIDECAR IS VERSION 0, NOT AN ERROR, AND 0 IS NEVER CURRENT. Every
// index written before this file existed has no sidecar, and those are exactly
// the indexes that may hold the corrupt segments. "Absent" must therefore mean
// "rebuild", never "assume fine".
//
// A sidecar that exists but cannot be read or parsed returns its error, and the
// caller rebuilds on that too: an unreadable record of what wrote the index is
// no better than no record.
func readIndexFormat(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is Omnipus-owned, under $OMNIPUS_HOME
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read index format %s: %w", path, err)
	}
	var f indexFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("parse index format %s: %w", path, err)
	}
	return f.Version, nil
}

// writeIndexFormat stamps the current format version, atomically and 0600
// (FR-032, same rules as the manifest it sits beside).
func writeIndexFormat(path string) error {
	data, err := json.MarshalIndent(indexFormat{Version: indexFormatVersion}, "", "  ")
	if err != nil {
		return fmt.Errorf("knowledge: encode index format: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: write index format %s: %w", path, err)
	}
	if err := os.Chmod(path, indexFileMode); err != nil {
		return fmt.Errorf("knowledge: set mode on index format %s: %w", path, err)
	}
	return nil
}

// formatStaleReason is guard G1: it reports, in a sentence, why the index on
// disk is not in the current format — or "" when it is.
func (ix *Index) formatStaleReason() string {
	got, err := readIndexFormat(ix.formatPath)
	if err != nil {
		return fmt.Sprintf("its format record could not be read (%v)", err)
	}
	switch {
	case got == indexFormatVersion:
		return ""
	case got == 0:
		return fmt.Sprintf(
			"it carries no format record, so it was written before the index format was tracked and may hold "+
				"segments from a writer that corrupts them at scale (current format is %d)", indexFormatVersion)
	default:
		return fmt.Sprintf("it was written in index format %d and the current format is %d", got, indexFormatVersion)
	}
}

// mappingDrift is guard G2: it compares the mapping PERSISTED inside the index
// against the mapping buildIndexMapping produces now, and returns the first
// difference as a sentence — or "" when they agree.
//
// It exists because bleve.OpenUsing takes no mapping argument. The mapping
// written at creation is authoritative for the life of the index, so code that
// declares a new field, or changes an existing field's analyzer, gets an index
// that quietly ignores the change: queries against the new field return zero
// hits and no error. There is no failure to notice — which is why this is a
// comparison and not an error check.
//
// It compares the settings that decide whether a query can work at all —
// type, analyzer, index, store, docvalues, term vectors, _all membership — not
// just field NAMES. A name-only comparison would pass an index whose `name`
// field was built with the keyword analyzer while the code now says `en`, and
// the same query would return a different number of hits depending on which
// mapping was actually in force, with no way for the caller to tell.
//
// It also compares in BOTH directions, and compares the dynamic settings: a
// field the code has stopped declaring, or an index built when dynamic mapping
// was on, are equally not the index this code expects.
func mappingDrift(persisted bleveMapping.IndexMapping) string {
	want := buildIndexMapping()

	declared := make([]string, 0, len(want.DefaultMapping.Properties))
	for name := range want.DefaultMapping.Properties {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	for _, name := range declared {
		got := persisted.FieldMappingForPath(name)
		if got.Type == "" {
			return fmt.Sprintf("field %q is absent from the persisted mapping", name)
		}
		if d := fieldMappingDrift(name, got, want.FieldMappingForPath(name)); d != "" {
			return d
		}
	}

	impl, ok := persisted.(*bleveMapping.IndexMappingImpl)
	if !ok {
		// Every field the code declares has been checked; only the reverse
		// direction, the dynamic settings and the scoring model are
		// unreachable. Say so out loud rather than reporting a clean
		// comparison that was not made.
		slog.Warn("knowledge: persisted mapping is not an IndexMappingImpl; "+
			"undeclared-field, dynamic-setting and scoring-model drift cannot be checked",
			"type", fmt.Sprintf("%T", persisted))
		return ""
	}
	if impl.DefaultMapping == nil {
		return "the persisted mapping has no default document mapping"
	}
	// The scoring model decides how every hit is RANKED, and bleve reads it from
	// this persisted mapping rather than from the mapping the code now builds
	// (index_impl.go loads the stored mapping at open; isBM25Enabled then asks
	// that object, not ours). An index written before ADR-068 D21.1 therefore
	// keeps scoring TF-IDF for the rest of its life with no error and no empty
	// result to notice — a silent wrong answer, which is the whole reason this
	// function is a comparison rather than an error check.
	//
	// Empty is compared as bleve resolves it, not as a string: "" means
	// DefaultScoringModel (TF-IDF), so an empty persisted model and an explicit
	// "tf-idf" are the same index and must not be reported as drift.
	if gotModel, wantModel := effectiveScoringModel(impl), effectiveScoringModel(want); gotModel != wantModel {
		return fmt.Sprintf("the persisted mapping scores with %q and the code declares %q",
			gotModel, wantModel)
	}
	if impl.DefaultMapping.Dynamic != want.DefaultMapping.Dynamic {
		return fmt.Sprintf("the persisted default document mapping has dynamic=%t, the code declares dynamic=%t",
			impl.DefaultMapping.Dynamic, want.DefaultMapping.Dynamic)
	}
	if impl.IndexDynamic != want.IndexDynamic {
		return fmt.Sprintf("the persisted mapping has index_dynamic=%t, the code declares index_dynamic=%t",
			impl.IndexDynamic, want.IndexDynamic)
	}
	if impl.StoreDynamic != want.StoreDynamic {
		return fmt.Sprintf("the persisted mapping has store_dynamic=%t, the code declares store_dynamic=%t",
			impl.StoreDynamic, want.StoreDynamic)
	}

	extra := make([]string, 0)
	for name := range impl.DefaultMapping.Properties {
		if _, still := want.DefaultMapping.Properties[name]; !still {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Sprintf("field %q is in the persisted mapping and the code no longer declares it", extra[0])
	}
	return ""
}

// effectiveScoringModel reports the scoring model an index mapping ACTUALLY
// ranks with, resolving the empty string the way bleve does rather than
// treating it as a distinct value. bleve's isBM25Enabled tests the field
// against "bm25" and everything else — empty included — falls through to
// index.DefaultScoringModel, which is TF-IDF.
//
// Comparing the raw strings instead would report drift between an index written
// with "" and code declaring "tf-idf" when the two rank identically, and a
// guard that fires on a difference that does not exist is a guard that gets
// switched off.
func effectiveScoringModel(m *bleveMapping.IndexMappingImpl) string {
	if m == nil || m.ScoringModel == "" {
		return bleveIndexAPI.DefaultScoringModel
	}
	return m.ScoringModel
}

// fieldMappingDrift compares one field's settings. The order of the checks is
// the order the differences are reported in, which keeps the message stable for
// a given pair of mappings.
func fieldMappingDrift(name string, got, want bleveMapping.FieldMapping) string {
	switch {
	case got.Type != want.Type:
		return fmt.Sprintf("field %q has type %q in the persisted mapping, the code declares %q",
			name, got.Type, want.Type)
	case got.Analyzer != want.Analyzer:
		return fmt.Sprintf("field %q uses analyzer %q in the persisted mapping, the code declares %q",
			name, got.Analyzer, want.Analyzer)
	case got.Index != want.Index:
		return fmt.Sprintf("field %q has index=%t in the persisted mapping, the code declares index=%t",
			name, got.Index, want.Index)
	case got.Store != want.Store:
		return fmt.Sprintf("field %q has store=%t in the persisted mapping, the code declares store=%t",
			name, got.Store, want.Store)
	case got.DocValues != want.DocValues:
		return fmt.Sprintf("field %q has docvalues=%t in the persisted mapping, the code declares docvalues=%t",
			name, got.DocValues, want.DocValues)
	case got.IncludeTermVectors != want.IncludeTermVectors:
		return fmt.Sprintf(
			"field %q has include_term_vectors=%t in the persisted mapping, the code declares include_term_vectors=%t",
			name, got.IncludeTermVectors, want.IncludeTermVectors)
	case got.IncludeInAll != want.IncludeInAll:
		return fmt.Sprintf(
			"field %q has include_in_all=%t in the persisted mapping, the code declares include_in_all=%t",
			name, got.IncludeInAll, want.IncludeInAll)
	}
	return ""
}

// enforceIndexPermissions asserts FR-032 over the whole index directory:
// directories 0700, files 0600. bleve gets most of this right on its own, but
// its index_meta.json is created 0666 and would otherwise be world-readable
// under a typical umask — and the index holds the full text of every note.
func enforceIndexPermissions(dir string) error {
	if err := filepath.WalkDir(dir, enforceEntryPermissions); err != nil {
		return fmt.Errorf("knowledge: enforce index permissions on %s: %w", dir, err)
	}
	return nil
}

// enforceEntryPermissions is the per-entry half of enforceIndexPermissions,
// split out so the vanished-file path below can be tested deterministically —
// the race that motivates it cannot be triggered on demand.
//
// A FILE THAT NO LONGER EXISTS IS NOT AN ERROR HERE, and that is the whole
// point of this function. The walk runs over a LIVE scorch index — SyncWith
// calls it immediately after batch.commit(), which is exactly when scorch's
// background merger fires and DELETES the segments it just merged away. So
// there are three moments where a .zap file can vanish underneath us:
//
//  1. walkErr — WalkDir could not stat an entry it had already enumerated.
//  2. d.Info() — DirEntry.Info lstats LAZILY, so the entry came from an
//     earlier ReadDir and the file may be gone by the time we ask.
//  3. os.Chmod — gone in the window between Info and the chmod itself.
//
// Before this, any one of them aborted the walk and failed the whole Sync for
// no real reason. Observed once for real (lstat .../000000000005.zap: no such
// file or directory) on a 500-note fixture; on a 100k-note collection the
// merger is not an edge case, it is the normal path.
//
// This does NOT weaken FR-032: a file that is not there cannot have the wrong
// permissions, and every file still present is still checked and chmod'ed.
func enforceEntryPermissions(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		return walkErr
	}
	want := indexFileMode
	if d.IsDir() {
		want = indexDirMode
	}
	info, statErr := d.Info()
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		return statErr
	}
	if info.Mode().Perm() == want {
		return nil
	}
	if err := os.Chmod(path, want); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

// buildIndexMapping defines the document shape.
//
// Body is INDEXED BUT NOT STORED and carries no term vectors: FR-050a requires
// excerpts to be re-read from disk at query time, so an index that could hand
// back note text would be a stale copy waiting to happen — and storing 100,000
// note bodies twice is the memory budget MV-2/MV-3 do not have.
//
// IncludeInAll is off on every field. The composite _all field would double the
// indexing cost to serve queries this package never issues: like
// pkg/memrooms/index, we query the real fields explicitly, because a match
// query against _all silently returns nothing when the field analyzers differ.
//
// ScoringModel is set EXPLICITLY to BM25 (ADR-068 D21.1). bleve's default is
// TF-IDF (`DefaultScoringModel = TFIDFScoring`, bleve_index_api
// indexing_options.go), and leaving this field empty is not "unspecified" — it
// is a positive choice of TF-IDF, which is what this package shipped with while
// its own comments claimed BM25. The difference is not cosmetic: BM25 saturates
// term frequency, so a note that repeats a term twenty times stops accruing
// score, whereas TF-IDF keeps rewarding it. Over a note collection that is the
// difference between ranking the note ABOUT a topic first and ranking the note
// that merely says the word most often first.
//
// This is also why indexFormatVersion is bumped alongside it and why
// mappingDrift compares it: the scoring model is read from the mapping
// PERSISTED IN THE INDEX (bleve resolves it via isBM25Enabled over the mapping
// loaded at open, not the mapping the code builds), so without a forced rebuild
// this line would change nothing whatsoever on any index already on disk.
func buildIndexMapping() *bleveMapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()
	m.ScoringModel = bleveIndexAPI.BM25Scoring

	body := bleve.NewTextFieldMapping()
	body.Analyzer = "en"
	body.Store = false
	body.IncludeTermVectors = false
	body.IncludeInAll = false
	body.DocValues = false

	name := bleve.NewTextFieldMapping()
	name.Analyzer = "en"
	name.Store = false
	name.IncludeTermVectors = false
	name.IncludeInAll = false
	name.DocValues = false

	pathField := bleve.NewTextFieldMapping()
	pathField.Analyzer = "keyword"
	pathField.Store = true
	pathField.IncludeTermVectors = false
	pathField.IncludeInAll = false
	pathField.DocValues = false

	kind := bleve.NewTextFieldMapping()
	kind.Analyzer = "keyword"
	kind.Store = true
	kind.IncludeTermVectors = false
	kind.IncludeInAll = false
	kind.DocValues = false

	offset := bleve.NewNumericFieldMapping()
	offset.Store = true
	offset.Index = false
	offset.IncludeInAll = false
	offset.DocValues = false

	doc := bleve.NewDocumentMapping()
	doc.AddFieldMappingsAt(fieldPath, pathField)
	doc.AddFieldMappingsAt(fieldName, name)
	doc.AddFieldMappingsAt(fieldKind, kind)
	doc.AddFieldMappingsAt(fieldOffset, offset)
	doc.AddFieldMappingsAt(fieldBody, body)
	doc.Dynamic = false

	m.DefaultMapping = doc
	m.IndexDynamic = false
	m.StoreDynamic = false
	return m
}

// indexDoc is one index document — one SEGMENT of a note, or one attachment.
type indexDoc struct {
	Path   string  `json:"path"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Offset float64 `json:"offset"`
	Body   string  `json:"body"`
}

// segmentDocID is the bleve document id for one segment of one file. The
// ordinal (not the byte offset) is what makes deletion possible: a file's
// documents are ids 0..segments-1, and the manifest remembers how many there
// were.
func segmentDocID(relPath string, ordinal int) string {
	return relPath + segmentIDSeparator + strconv.Itoa(ordinal)
}

// splitSegmentDocID recovers the path and ordinal from a document id.
func splitSegmentDocID(id string) (string, int) {
	i := strings.LastIndex(id, segmentIDSeparator)
	if i < 0 {
		return id, 0
	}
	ord, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return id[:i], 0
	}
	return id[:i], ord
}

// Dir returns the index directory under $OMNIPUS_HOME.
func (ix *Index) Dir() string { return ix.dir }

// Root returns the collection root's resolved real path.
func (ix *Index) Root() string { return ix.root }

// ManifestPath returns the path of the freshness manifest.
func (ix *Index) ManifestPath() string { return ix.manifestPath }

// DocCount returns the number of index documents — segments, not files.
func (ix *Index) DocCount() (uint64, error) { return ix.idx.DocCount() }

// Close releases THIS holder's reference. The underlying handle is closed only
// when the last holder releases it, so one revoked mount can never close an
// index another workspace is still searching.
func (ix *Index) Close() error {
	if ix.regKey == "" {
		return ix.closeUnderlying()
	}
	return releaseSharedIndex(ix.regKey)
}

func (ix *Index) closeUnderlying() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if err := ix.idx.Close(); err != nil {
		return fmt.Errorf("knowledge: close index %s: %w", ix.blevePath, err)
	}
	return nil
}

// batchState accumulates index documents into bounded batches (FR-034).
type batchState struct {
	ix      *Index
	batch   *bleve.Batch
	docs    int
	bytes   int
	commits int
}

func newBatchState(ix *Index) *batchState {
	return &batchState{ix: ix, batch: ix.idx.NewBatch()}
}

// add appends one document and commits the batch if either bound is reached.
// Because indexBatchMaxBytes equals IndexSegmentSize, a full-size note segment
// forces a commit as soon as it is added — which is what keeps peak memory a
// property of the SEGMENT size rather than of the largest file in the corpus.
func (b *batchState) add(id string, doc indexDoc) error {
	if err := b.batch.Index(id, doc); err != nil {
		return fmt.Errorf("knowledge: batch index %s: %w", id, err)
	}
	b.docs++
	b.bytes += len(doc.Body)
	if b.docs >= indexBatchMaxDocs || b.bytes >= indexBatchMaxBytes {
		return b.commit()
	}
	return nil
}

func (b *batchState) delete(id string) error {
	b.batch.Delete(id)
	b.docs++
	if b.docs >= indexBatchMaxDocs {
		return b.commit()
	}
	return nil
}

func (b *batchState) commit() error {
	if b.docs == 0 {
		return nil
	}
	if err := b.ix.idx.Batch(b.batch); err != nil {
		return fmt.Errorf("knowledge: commit batch: %w", err)
	}
	b.commits++
	b.batch = b.ix.idx.NewBatch()
	b.docs = 0
	b.bytes = 0
	return nil
}

// Sync reconciles the index with the collection on disk using the default
// (stat-based) freshness check.
func (ix *Index) Sync(ctx context.Context) (SyncStats, error) {
	return ix.SyncWith(ctx, SyncOptions{})
}

// SyncWith reconciles the index with the collection on disk.
//
// It re-parses ONLY files whose recorded size, modification time or content hash
// changed (FR-033), deletes the documents of files that are gone, indexes in
// bounded batches (FR-034), segments oversized notes (FR-034a), never opens an
// attachment (FR-039a), and persists the manifest so the next open — after a
// restart or not — repeats none of the work (FR-039).
//
// # Reporting progress, and why the throttle lives here
//
// opts.OnProgress is called as files are reconciled, so a caller can show a
// number that MOVES rather than a bar that sits still for minutes on a large
// collection. It is called at most once per file, and in practice far less:
//
//   - at most one call per opts.ProgressInterval of wall-clock time
//     (DefaultProgressInterval, 200ms — five a second, beyond which a reader
//     absorbs nothing), AND
//   - at most maxProgressUpdates calls for the whole run, one per
//     progressStride files (0.1% of the collection),
//
// with both conditions required, plus one unconditional final call so the count
// always lands on the run's true total. A 100,000-file collection therefore
// produces at most 1,001 calls however long it takes, against 100,000 from a
// naive per-file hook — and a caller that turns each call into a WebSocket
// frame gets a stream a client can keep up with.
//
// The coalescing lives HERE rather than in the caller for the same reason
// SyncTracked exists: a hook whose rate every future caller must remember to
// bound is a hook that will eventually be wired straight to a socket. The
// caller keeps the choice that is genuinely its own — how smooth it wants the
// stream — as opts.ProgressInterval, and cannot opt out of the bound.
//
// The number reported is Indexed + Unchanged so far, against the walk's total:
// a report of WORK DONE, not of durability. Documents become searchable when
// their batch commits, on the batch's own schedule and, for the last batch,
// after this loop ends. Whether a search may claim completeness is the progress
// tracker's question (SyncTracked), never this number's.
func (ix *Index) SyncWith(ctx context.Context, opts SyncOptions) (SyncStats, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	var stats SyncStats

	scan, err := Scan(ix.root)
	if err != nil {
		return stats, err
	}
	stats.Scanned = len(scan.Entries)
	stats.Problems = scan.Problems

	manifest, loadErr := LoadManifest(ix.manifestPath, ix.root)
	if loadErr != nil {
		// Not fatal: an unusable manifest costs a full rebuild, never a wrong
		// answer. It is logged rather than swallowed.
		slog.Warn("knowledge: manifest unusable; indexing from scratch",
			"path", ix.manifestPath, "error", loadErr)
	}

	batch := newBatchState(ix)
	seen := make(map[string]struct{}, len(scan.Entries))
	progress := newProgressCoalescer(opts.OnProgress, len(scan.Entries), opts.ProgressInterval, time.Now)

	for _, entry := range scan.Entries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stats, ctxErr
		}
		// Reported at the TOP of the iteration, covering the entries already
		// finished, so there is ONE call site rather than one before each of
		// the four ways this loop can reach its end. The final flush below is
		// what makes the last entry's completion visible.
		progress.update(stats.Indexed + stats.Unchanged)
		seen[entry.RelPath] = struct{}{}

		rec, hadRec := manifest.Get(entry.RelPath)

		if !opts.Deep && manifest.StatUnchanged(entry) {
			stats.Unchanged++
			continue
		}

		if opts.Deep && hadRec && rec.Kind == entry.Kind && entry.Kind == ScanKindNote {
			// FR-033's third criterion, and the only place it can be applied
			// without a stat change to trigger it: hash the note and skip it if
			// the bytes are identical after all. Attachments are excluded by
			// construction — hashing one would mean opening it (FR-039a).
			sum, hashErr := ix.hashFile(entry.RelPath)
			if hashErr == nil && sum == rec.Hash && rec.Hash != "" {
				stats.Unchanged++
				rec.Size = entry.Size
				rec.ModTimeNanos = entry.ModTimeNanos
				manifest.Put(rec)
				continue
			}
		}

		// The file changed (or is new). Remove whatever documents it produced
		// last time before writing the new ones: a note that shrank from five
		// segments to two would otherwise leave three orphans behind, findable
		// forever.
		if hadRec {
			for ord := 0; ord < rec.Segments; ord++ {
				if delErr := batch.delete(segmentDocID(entry.RelPath, ord)); delErr != nil {
					return stats, delErr
				}
			}
		}

		newRec, segErr := ix.indexEntry(batch, entry)
		if segErr != nil {
			// One unreadable file must not abort the collection. It is reported
			// and left OUT of the index — never indexed as empty, which would
			// be a confidently wrong answer.
			slog.Error("knowledge: indexing file failed",
				"collection", ix.root, "path", entry.RelPath, "error", segErr)
			stats.Problems = append(stats.Problems, ScanProblem{
				RelPath: entry.RelPath, Reason: ScanProblemUnreadable, Detail: segErr.Error(),
			})
			manifest.Remove(entry.RelPath)
			continue
		}
		manifest.Put(newRec)
		stats.Indexed++
		stats.Segments += newRec.Segments
	}

	// The run's own last word on its progress. Unthrottled on purpose: without
	// it the number would stop wherever the last throttled call happened to
	// land — short of the total, and indistinguishable from a stall.
	progress.flush(stats.Indexed + stats.Unchanged)

	// Files the manifest knows and the walk did not find are gone from disk.
	for relPath, rec := range manifest.Entries {
		if _, ok := seen[relPath]; ok {
			continue
		}
		for ord := 0; ord < rec.Segments; ord++ {
			if delErr := batch.delete(segmentDocID(relPath, ord)); delErr != nil {
				return stats, delErr
			}
		}
		manifest.Remove(relPath)
		stats.Removed++
	}

	if err := batch.commit(); err != nil {
		return stats, err
	}
	stats.BatchCommits = batch.commits

	if err := manifest.Save(ix.manifestPath); err != nil {
		return stats, err
	}
	if err := enforceIndexPermissions(ix.dir); err != nil {
		return stats, err
	}
	return stats, nil
}

// indexEntry writes the index documents for one file and returns its manifest
// record.
func (ix *Index) indexEntry(batch *batchState, entry ScanEntry) (ManifestEntry, error) {
	if entry.Kind == ScanKindAttachment {
		return ix.indexAttachment(batch, entry)
	}
	return ix.indexNote(batch, entry)
}

// indexAttachment records an attachment by filename and path ONLY (FR-039a).
//
// There is no read here and there must never be one: no body, no hash, no
// content type sniff. `diagram-v3.png` is findable because its NAME is indexed.
func (ix *Index) indexAttachment(batch *batchState, entry ScanEntry) (ManifestEntry, error) {
	doc := indexDoc{
		Path:   entry.RelPath,
		Name:   nameTokensFor(entry.RelPath),
		Kind:   string(ScanKindAttachment),
		Offset: 0,
		Body:   "",
	}
	if err := batch.add(segmentDocID(entry.RelPath, 0), doc); err != nil {
		return ManifestEntry{}, err
	}
	return ManifestEntry{
		Path:         entry.RelPath,
		Kind:         ScanKindAttachment,
		Size:         entry.Size,
		ModTimeNanos: entry.ModTimeNanos,
		Hash:         "", // never read, therefore never hashed
		Segments:     1,
	}, nil
}

// indexNote streams a note into consecutive segment documents (FR-034a).
//
// One pass over the file does three things: it hashes the bytes, it cuts them
// into segments of at most IndexSegmentSize, and it hands each segment to the
// bounded batch. Peak memory is a function of IndexSegmentSize, not of the
// file's size — which is the whole point, and the reason this does not simply
// read the note and index it as one document the way pkg/memrooms/index does.
//
// A note of ANY size is indexed in full. Nothing is refused, skipped or
// truncated.
func (ix *Index) indexNote(batch *batchState, entry ScanEntry) (ManifestEntry, error) {
	absPath := filepath.Join(ix.root, filepath.FromSlash(entry.RelPath))
	f, err := openFileForRead(absPath)
	if err != nil {
		return ManifestEntry{}, fmt.Errorf("open note %s: %w", entry.RelPath, err)
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	name := nameTokensFor(entry.RelPath)

	buf := make([]byte, IndexSegmentSize)
	carry := 0        // bytes held over from the previous read (a partial line)
	var offset int64  // absolute byte offset of the current segment's start
	ordinal := 0      // segment ordinal
	eof := false      // the reader reported io.EOF
	wroteAny := false // at least one document was written for this file
	var totalRead int // bytes actually read off disk, for the FR-111 check

	for !eof {
		n, readErr := io.ReadFull(f, buf[carry:])
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return ManifestEntry{}, fmt.Errorf("read note %s: %w", entry.RelPath, readErr)
		}
		if readErr != nil {
			eof = true
		}
		// Hash exactly the bytes just read, in file order, so the hash is over
		// the file's true contents regardless of how they were segmented.
		hasher.Write(buf[carry : carry+n])
		totalRead += n
		filled := carry + n

		if filled == 0 {
			break
		}

		cut := filled
		if !eof {
			// Prefer to end a segment on a line boundary so a term is not split
			// across two documents and lost from both. If a single line is
			// longer than a whole segment there is no boundary to use, and the
			// hard cut stands — the note is still indexed in full.
			if nl := lastIndexByte(buf[:filled], '\n'); nl > 0 {
				cut = nl + 1
			}
		}

		if err := batch.add(segmentDocID(entry.RelPath, ordinal), indexDoc{
			Path:   entry.RelPath,
			Name:   name,
			Kind:   string(ScanKindNote),
			Offset: float64(offset),
			Body:   string(buf[:cut]),
		}); err != nil {
			return ManifestEntry{}, err
		}
		wroteAny = true
		ordinal++
		offset += int64(cut)

		carry = filled - cut
		if carry > 0 {
			copy(buf, buf[cut:filled])
		}
	}

	// FR-111, and it has to be asked before the empty-note branch below or it
	// is not asked at all.
	//
	// There are two ways a cloud provider's placeholder reads as nothing. The
	// loud one — open or read returns an error — is already handled: the error
	// propagates, the sync loop records a ScanProblem and leaves the note out
	// of the index. The QUIET one is a clean EOF at zero bytes for a file stat
	// says has content, and it used to fall straight through into "an empty
	// note is still a note" and be indexed as an EMPTY document: the index
	// then answers "this note contains nothing" about a file that may contain
	// anything, which is exactly the outcome FR-111 forbids.
	//
	// The classification is lifecycle.go's, not a second copy of the rule.
	// Two independent classifications would drift, and the direction they
	// drift in is "one of them starts calling an evicted file empty".
	if cErr := ClassifyContentFailure(absPath, entry.Size, totalRead, nil); cErr != nil {
		return ManifestEntry{}, cErr
	}

	if !wroteAny {
		// An empty note is still a note: it must be addressable, carry an
		// outline and appear in the graph. It gets one empty document.
		if err := batch.add(segmentDocID(entry.RelPath, 0), indexDoc{
			Path:   entry.RelPath,
			Name:   name,
			Kind:   string(ScanKindNote),
			Offset: 0,
			Body:   "",
		}); err != nil {
			return ManifestEntry{}, err
		}
		ordinal = 1
	}

	return ManifestEntry{
		Path:         entry.RelPath,
		Kind:         ScanKindNote,
		Size:         entry.Size,
		ModTimeNanos: entry.ModTimeNanos,
		Hash:         hex.EncodeToString(hasher.Sum(nil)),
		Segments:     ordinal,
	}, nil
}

// hashFile streams a note through SHA-256 without holding it in memory. Used
// only by a deep reconcile; never called for an attachment.
func (ix *Index) hashFile(relPath string) (string, error) {
	absPath := filepath.Join(ix.root, filepath.FromSlash(relPath))
	f, err := openFileForRead(absPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var h hash.Hash = sha256.New()
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// lastIndexByte returns the index of the last occurrence of c, or -1.
func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// nameTokensFor turns a path into text that makes a file findable by NAME.
// `img/diagram-v3.png` becomes "img diagram-v3.png diagram v3 png", so a search
// for `diagram-v3` finds the attachment whether the query is analysed as one
// token or three.
func nameTokensFor(relPath string) string {
	base := filepath.Base(relPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	dir := strings.TrimSuffix(filepath.Dir(relPath), ".")
	parts := []string{base, stem}
	if dir != "" {
		parts = append(parts, strings.ReplaceAll(dir, "/", " "))
	}
	return strings.Join(parts, " ")
}

// indexSearchMaxFetch bounds how many raw segment hits one Search may pull while
// collapsing segments back into files. Without a bound, a query matching every
// segment of a very large note could pull the whole index into memory.
const indexSearchMaxFetch = 2048

// Search runs a query and returns at most limit results, ONE PER FILE. Hits are
// scored with BM25, which is in force because buildIndexMapping asks for it by
// name and the index was built under that mapping — bleve's default is TF-IDF
// (ADR-068 D21.1), and the model is read from the mapping persisted in the
// index, not from the one the code holds.
//
// FR-034a's segments are an implementation detail of bounded memory and must
// never reach the caller: a term appearing in three segments of one note is one
// result, scored by its best segment, carrying that segment's absolute byte
// offset so FR-050a's query-time excerpt re-read lands in the right place. The
// naive implementation returns three rows for one note and ranks them as three
// notes.
//
// limit is honoured as given — this layer does not silently clamp. FR-037's cap
// belongs to the tool/API layer, which must clamp AND report the clamping.
func (ix *Index) Search(query string, limit int) ([]IndexHit, error) {
	return ix.SearchFiltered(query, limit, nil)
}

// SearchFiltered is Search restricted to the paths keep returns true for. A nil
// keep is the whole collection and makes this identical to Search.
//
// The filter is applied to the RAW segment hits, inside the escalating-fetch
// loop and before the limit is applied — so "the best `limit` matches inside
// this folder" is what comes back, not "whichever of the best `limit` matches
// in the collection happen to be in this folder". Those two differ the moment
// the collection has more matches than the limit, and the second silently
// returns a subset. The loop keeps widening its fetch until it has `limit`
// surviving files or bleve reports there are no more matching segments to see,
// so a narrow folder in a large collection is answered fully rather than
// emptily.
func (ix *Index) SearchFiltered(query string, limit int, keep func(relPath string) bool) ([]IndexHit, error) {
	if limit <= 0 {
		limit = 20
	}

	fetch := limit * 4
	if fetch < 20 {
		fetch = 20
	}

	for {
		if fetch > indexSearchMaxFetch {
			fetch = indexSearchMaxFetch
		}
		hits, total, err := ix.searchRaw(query, fetch)
		if err != nil {
			return nil, err
		}
		if keep != nil {
			kept := hits[:0]
			for _, h := range hits {
				if keep(h.Path) {
					kept = append(kept, h)
				}
			}
			hits = kept
		}
		collapsed := collapseSegmentHits(hits)
		// Stop when we have enough distinct files, when we have already seen
		// every matching segment, or when the fetch bound is reached.
		if len(collapsed) >= limit || uint64(fetch) >= total || fetch >= indexSearchMaxFetch {
			if len(collapsed) > limit {
				collapsed = collapsed[:limit]
			}
			return collapsed, nil
		}
		fetch *= 4
	}
}

// searchRaw executes one bleve query and returns the raw per-SEGMENT hits.
func (ix *Index) searchRaw(query string, size int) ([]IndexHit, uint64, error) {
	var req *bleve.SearchRequest
	if strings.TrimSpace(query) == "" {
		req = bleve.NewSearchRequestOptions(bleve.NewMatchAllQuery(), size, 0, false)
	} else {
		// Explicit per-field match queries, for the reason pkg/memrooms/index
		// documents: a plain match query targets the composite _all field,
		// whose analyzer does not match the field-level mapping, and returns
		// nothing even when the terms are present.
		qs := make([]bleveQuery.Query, 0, 3)
		for _, field := range []string{fieldName, fieldPath, fieldBody} {
			mq := bleveQuery.NewMatchQuery(query)
			mq.SetField(field)
			qs = append(qs, mq)
		}
		req = bleve.NewSearchRequestOptions(bleve.NewDisjunctionQuery(qs...), size, 0, false)
	}
	req.Fields = []string{fieldPath, fieldKind, fieldOffset}
	req.SortBy([]string{"-_score", "_id"}) // deterministic ties (FR-046)

	res, err := ix.idx.Search(req)
	if err != nil {
		return nil, 0, fmt.Errorf("knowledge: search %q: %w", query, err)
	}
	out := make([]IndexHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		relPath, ordinal := splitSegmentDocID(h.ID)
		hit := IndexHit{Path: relPath, Score: h.Score, Segment: ordinal, Kind: ScanKindNote}
		if v, ok := h.Fields[fieldPath].(string); ok && v != "" {
			hit.Path = v
		}
		if v, ok := h.Fields[fieldKind].(string); ok && v != "" {
			hit.Kind = ScanKind(v)
		}
		if v, ok := h.Fields[fieldOffset].(float64); ok {
			hit.Offset = int64(v)
		}
		out = append(out, hit)
	}
	return out, res.Total, nil
}

// collapseSegmentHits folds every segment of a file into ONE result, keeping the
// best-scoring segment's score and offset, and preserves descending score order.
func collapseSegmentHits(hits []IndexHit) []IndexHit {
	best := make(map[string]IndexHit, len(hits))
	order := make([]string, 0, len(hits))
	for _, h := range hits {
		prev, seen := best[h.Path]
		if !seen {
			best[h.Path] = h
			order = append(order, h.Path)
			continue
		}
		if h.Score > prev.Score || (h.Score == prev.Score && h.Segment < prev.Segment) {
			best[h.Path] = h
		}
	}
	out := make([]IndexHit, 0, len(order))
	for _, p := range order {
		out = append(out, best[p])
	}
	return out
}

// ---------------------------------------------------------------------------
// Process-global, reference-counted registry of open collection indexes.
//
// WHY IT EXISTS — the same reason pkg/memrooms/index has one, plus one more.
//
// scorch keeps its root metadata in a bbolt file opened with a
// PROCESS-EXCLUSIVE, INFINITE-WAIT file lock. A second open of the same file
// blocks forever. And FR-031 makes a second open the NORMAL case here: one host
// folder can be mounted into several workspaces, and twice into one — CreateMount
// checks name collisions, never HostPath. So the same corpus is opened by
// several holders as a matter of routine, and each must get the one live handle.
//
// The key is the collection root's RESOLVED REAL PATH, not the index directory
// and not a workspace/mount id: two mounts naming the folder by different routes
// are the same corpus and must share the same index and the same refcount.
//
// Reference counting is the second half of FR-031: revoking one of two mounts
// must leave the other workspace's search working, so a Close that is not the
// last Close is pure bookkeeping.
// ---------------------------------------------------------------------------

var indexRegistry = struct {
	mu      sync.Mutex
	entries map[string]*indexRegistryEntry
}{entries: make(map[string]*indexRegistryEntry)}

type indexRegistryEntry struct {
	ix   *Index
	refs int
}

// acquireSharedIndex returns the shared *Index for key, calling open exactly
// once per key. open runs under the registry mutex so two concurrent first
// acquirers cannot both race into a bbolt open of the same file — which is the
// deadlock being avoided, not merely wasted work.
func acquireSharedIndex(key string, open func() (*Index, error)) (*Index, error) {
	indexRegistry.mu.Lock()
	defer indexRegistry.mu.Unlock()

	if e, ok := indexRegistry.entries[key]; ok {
		e.refs++
		return e.ix, nil
	}
	ix, err := open()
	if err != nil {
		return nil, err
	}
	ix.regKey = key
	indexRegistry.entries[key] = &indexRegistryEntry{ix: ix, refs: 1}
	return ix, nil
}

// releaseSharedIndex drops one reference and closes the underlying handle only
// when the last one goes. Releasing an unknown key is a safe no-op (a double
// close, or a handle the registry never managed).
func releaseSharedIndex(key string) error {
	indexRegistry.mu.Lock()
	e, ok := indexRegistry.entries[key]
	if !ok {
		indexRegistry.mu.Unlock()
		return nil
	}
	e.refs--
	if e.refs > 0 {
		indexRegistry.mu.Unlock()
		return nil
	}
	delete(indexRegistry.entries, key)
	indexRegistry.mu.Unlock()

	return e.ix.closeUnderlying()
}

// indexRegistryRefs reports the live holder count for a resolved collection
// root, or 0 when no handle is open. Test seam for FR-031's reference counting.
func indexRegistryRefs(key string) int {
	indexRegistry.mu.Lock()
	defer indexRegistry.mu.Unlock()
	if e, ok := indexRegistry.entries[key]; ok {
		return e.refs
	}
	return 0
}
