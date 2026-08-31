// Omnipus — the write side of pkg/vaultprops: the properties-index build
// pipeline, ADR-068 D16.2/D16.5.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// reader.go answers the two read questions pkg/knowledge asks of the derived
// properties index. Nothing anywhere ever asked the write question: walk a
// collection's notes, parse each against its declared record type, and put the
// resulting rows in the store. find_tool.go's own buildDeps says so in words —
// "PRODUCTION GAP ... nothing in this tree currently WRITES to the properties
// index outside a test harness" — and the consequence is that on a fresh
// install the store is always empty, so every knowledge_find call that reaches
// it refuses with "the properties index is not open, so no record can be
// read", and check_integrity's typed sweep is in the same state.
//
// This file is that write path. It lives here rather than in
// pkg/records/propindex for the same reason reader.go does: building a row
// needs pkg/knowledge (Scan, ReadNoteContent, CollectionRoot) AND
// pkg/records/propindex (Store, BuildNoteRows) joined together, and joining
// them from EITHER side is the import cycle reader.go's header already
// explains. pkg/vaultprops is the one package reachable from both without
// creating one.
//
// THE PATTERN THIS FOLLOWS is pkg/knowledge/index.go's own SyncWith: reconcile
// against what was indexed last, skip what has not changed, and remove what
// disk no longer has. Two differences, both deliberate:
//
//   - There is no second manifest file. SyncWith compares the walk against a
//     JSON manifest that can itself go missing, get corrupted, or (as an
//     earlier revision of THIS branch shipped) be read as silently empty on
//     an error the caller then ignored — deleted notes stayed searchable
//     forever. Sync instead asks the STORE what it already holds
//     (Store.AllPaths) before writing anything: the store is SQLite, opening
//     it either succeeds or fails loudly, and there is no second file for the
//     two to quietly disagree about. A store that cannot be listed aborts the
//     whole sync rather than proceeding as if it were empty.
//   - One code path serves both a first build and every later reconcile. The
//     "previously indexed" state is `Store.AllPaths` (empty on a fresh file,
//     populated on every later call), so a brand-new collection and a
//     ten-thousandth reconcile of an old one take the same branch: entries
//     with no prior record are new, entries whose hash has not moved are
//     skipped, entries the walk did not see this time are removed.
// ---------------------------------------------------------------------------

package vaultprops

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// SyncProblem is one path Sync could not fully account for. It is reported,
// never dropped: a note whose current content nobody could verify must not go
// on answering queries with stale data, and an operator reading a sync result
// must be able to see why a note is missing rather than infer it from an
// unexplained gap.
type SyncProblem struct {
	// RelPath is the collection-relative path the problem concerns. For a
	// rejected schema file it is the schema's own path (outside the note
	// tree), named the same way records.SchemaRejection names it.
	RelPath string
	// Reason is a stable, short cause.
	Reason string
	// Detail carries the underlying error text.
	Detail string
}

// SyncStats reports what one Sync run did.
type SyncStats struct {
	// Scanned is the number of note/attachment entries the walk found.
	Scanned int
	// Indexed is the number of paths written this run — new or changed.
	Indexed int
	// Unchanged is the number of paths whose stored hash already matched.
	Unchanged int
	// Removed is the number of paths deleted from the store: gone from disk
	// (deleted, renamed, moved out, or trashed — .trash is a skipped
	// directory, so a trashed note's old path simply stops appearing in the
	// walk and is removed the same way a deletion is) or unreadable this run.
	Removed int
	// StatRefreshed is FR-136's count: paths this run did NOT re-index —
	// their content was unchanged — whose stat had nevertheless drifted and
	// was corrected by a metadata-only UPDATE.
	//
	// It counts only the skip path. A path that was fully re-indexed this run
	// gets its stat stamped as part of that write and is reported under
	// Indexed, not here: "how many rows did we correct without re-reading
	// them" is the number FR-136 is about, and folding the re-indexed ones in
	// would make it unfalsifiable.
	StatRefreshed int
	// Problems is every path Sync could not fully account for, and every
	// schema file LoadSchemas rejected.
	Problems []SyncProblem
}

// SyncOptions configures Sync.
type SyncOptions struct {
	// Recorder, when set, is handed to the store so every statement this sync
	// executes is captured. Production callers leave it nil.
	//
	// It exists because FR-136's requirement is not "the stat ends up right" —
	// a full re-index would satisfy that and would be the bug. The requirement
	// is that a content-unchanged, stat-drifted file costs a METADATA-ONLY
	// update: no re-parse, no child-row rewrite. That is a statement about
	// which statements were executed, and nothing observable from the outside
	// of the store can distinguish it. Spec test 99 asserts it here.
	Recorder *propindex.Recorder
}

// storedNoteState is what the store already held for one path, read via
// Store.AllPaths before this run writes anything.
type storedNoteState struct {
	kind string
	hash string
}

// ---------------------------------------------------------------------------
// FR-136 — THE STAT SEAM, AND WHY IT IS SHAPED LIKE THIS
//
// Until Draft 11 the properties index held only {path, kind, record_type,
// record_id, source_hash}. Nothing in that row could go stale without the
// note's BYTES changing, so "same hash, skip it" was a complete freshness
// test. FR-130/FR-131 put mtime, ctime and size on the row, and that stopped
// being true in the same change: `git checkout`, `rsync`, `touch`, an iCloud
// resync and a `mv` back and forth all move a file's stat without moving one
// byte of its content.
//
// The consequence is the specific kind of defect this project's own
// false-green doc is about — there is no error, no refusal and no gap. `sort
// by file.mtime desc` (the commonest Bases view there is) simply returns a
// plausible, stable, WRONG order, frozen at whenever each note last changed
// content. Nobody looking at the result can tell.
//
// ATTACHMENTS ARE THE WORSE HALF. ADR-067's FR-039a forbids opening an
// attachment, so it carries no hash, so the skip above was unconditional: an
// attachment's row never changed once written. That was exactly right while
// the row held {Path, Kind} and nothing else — there was nothing about it that
// COULD change without its path changing. With size and mtime on the row it is
// false, and a picture replaced in place would report its FIRST-EVER size
// forever. Stat is not open, so the fix costs nothing FR-039a objects to: the
// walk already stat'd the file, and the refresh uses the walk's own numbers.
//
// THE COMPARISON LIVES INSIDE THE UPDATE, AND THAT IS WHY THIS SEAM IS ONE
// METHOD. Store.RefreshNoteStat sets the stat columns only where they differ
// and reports whether anything changed, so Sync needs no prior-stat read to
// make the decision with — which in turn is why Store.AllPaths keeps exactly
// the shape it has always had. The alternative (widen the maintenance walk to
// carry the stored stat, compare in Go) costs a signature change at every
// implementation and every caller for a comparison that is free where the row
// already is. FR-136's stated cost is met either way: one comparison per
// walked file, one UPDATE per drifted file.
//
// THE BOOL IS LOAD-BEARING. SyncStats.StatRefreshed counts the trues, and it
// is what makes this requirement falsifiable at all: "the mtime ends up
// correct" is equally satisfied by a full re-index, which is the bug rather
// than the fix. The count separates the two.
//
// CTIME IS NOW CARRIED TOO, AND THE SEAM THIS COMMENT USED TO NAME IS CLOSED.
// It read: "knowledge.ScanEntry carries Size and ModTimeNanos from Lstat and no
// birth time at all, so THE WALK HAS NO CTIME TO REFRESH FROM ... birth time
// becomes refreshable when the WALK carries it; that is a pkg/knowledge seam."
// The walk now carries it (knowledge.ScanEntry.CtimeNanos/HasCtime), read from
// the SAME os.FileInfo Size and ModTimeNanos come from.
//
// The refusal that comment was protecting is unchanged and still absolute: a
// birth time this platform did not record is ABSENT, never the misnamed POSIX
// inode-change time, which moves on a permission change and is routinely later
// than the modification time. What changed is only that there is now something
// honest to carry. The refresh applies it ONE WAY — it can fill in a row that
// has no birth time, and it never clears one, so a Linux pass without statx
// birth-time support cannot erase what a macOS pass over the same synced vault
// established (Store.RefreshNoteStat's contract states the rule).
//
// The gap was worth closing rather than documenting: `file.ctime` was NULL for
// every note on every platform, so it was not FR-133's honest absence on
// platforms that lack a birth time, it was a dead property everywhere. Every
// imported Bases view sorting or filtering on creation date returned nothing
// useful, and nothing reported why.
// ---------------------------------------------------------------------------

// refreshStatIfDrifted is FR-136's decision for one entry this run did NOT
// re-index: hand the store the stat the walk observed and let it correct the
// row if — and only if — that differs from what it holds.
//
// The numbers are the WALK's own. Nothing is re-stat'd, and for an attachment
// nothing is opened, so ADR-067's FR-039a is untouched: stat is not open.
func refreshStatIfDrifted(
	ctx context.Context,
	store propindex.Store,
	entry knowledge.ScanEntry,
	stats *SyncStats,
) error {
	changed, err := store.RefreshNoteStat(ctx, entry.RelPath,
		entry.Size, entry.ModTimeNanos, entry.CtimeNanos, entry.HasCtime)
	if err != nil {
		return fmt.Errorf("vaultprops: refreshing the stat of %q: %w", entry.RelPath, err)
	}
	if changed {
		stats.StatRefreshed++
	}
	return nil
}

// Sync builds or incrementally updates the properties index for one
// collection: FR-020/FR-021's rows, kept in step with the notes that produce
// them (ADR-068 D16.2, D16.5).
//
// On a build with no SQLite it is a deliberate no-op — records.
// RequirePropertyIndex has already logged the platform refusal once, by name,
// at every capability boundary; a maintenance job on an unsupported platform
// is not a second thing to fail loudly about, and plain-word search keeps
// working there regardless.
func Sync(ctx context.Context, home, collectionRoot string, opts SyncOptions) (SyncStats, error) {
	var stats SyncStats

	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		// Returned UNCHANGED (FR-020h) rather than swallowed into a quiet
		// success: RequirePropertyIndex has already logged the platform WARN
		// once, by name, and the caller (knowledge_lifecycle.go's reconcile)
		// is what decides this is non-fatal for a MOUNT — it must still be
		// able to tell the difference between "nothing needed indexing" and
		// "this platform cannot index it at all", which a nil error here
		// would erase.
		return stats, err
	}

	realRoot, err := knowledge.ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return stats, fmt.Errorf("vaultprops: resolving collection root %q: %w", collectionRoot, err)
	}
	fsys := knowledge.OSLinkFS()
	root, err := knowledge.NewCollectionRoot(fsys, realRoot)
	if err != nil {
		return stats, fmt.Errorf("vaultprops: collection root %q: %w", realRoot, err)
	}

	idxPath, err := knowledge.PropertiesIndexPath(home, realRoot)
	if err != nil {
		return stats, fmt.Errorf("vaultprops: locating the properties index: %w", err)
	}
	// The index directory is normally created already, by the SAME reconcile
	// that opens the text index (both live under knowledge.IndexDirFor's one
	// directory) — created here too so Sync is correct when called on its
	// own, e.g. from a test or a future standalone re-index command.
	if mkErr := os.MkdirAll(filepath.Dir(idxPath), 0o700); mkErr != nil {
		return stats, fmt.Errorf("vaultprops: creating the index directory: %w", mkErr)
	}

	store, err := propindex.Open(ctx, idxPath, propindex.Options{Recorder: opts.Recorder})
	if err != nil {
		// On a SQLite-capable build this is a real failure (disk full,
		// permissions, a corrupt file SQLite itself refuses) — returned
		// loudly, never treated as "nothing to index".
		return stats, fmt.Errorf("vaultprops: opening the properties index: %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			slog.Warn("vaultprops: closing the properties index after sync failed",
				"path", idxPath, "error", cerr)
		}
	}()

	schemas, schemaReport, err := records.LoadSchemas(root.Path())
	if err != nil {
		// Not a per-file rejection (those are schemaReport.Rejections,
		// handled below) — this is the schema directory itself being
		// unreadable, which means "which notes are records" cannot be
		// answered at all. Refusing loudly beats silently treating every
		// note in the vault as an ordinary one.
		return stats, fmt.Errorf("vaultprops: loading record schemas: %w", err)
	}
	for _, rej := range schemaReport.Rejections {
		relPath := rej.Type
		if len(rej.Paths) > 0 {
			relPath = rej.Paths[0]
		}
		stats.Problems = append(stats.Problems, SyncProblem{
			RelPath: relPath, Reason: "schema_rejected", Detail: rej.Reason,
		})
	}

	scan, err := knowledge.Scan(root.Path())
	if err != nil {
		return stats, fmt.Errorf("vaultprops: scanning the collection: %w", err)
	}
	stats.Scanned = len(scan.Entries)
	for _, p := range scan.Problems {
		stats.Problems = append(stats.Problems, SyncProblem{
			RelPath: p.RelPath, Reason: string(p.Reason), Detail: p.Detail,
		})
	}

	// THE STORE IS THE MANIFEST. Read what it already holds BEFORE writing
	// anything, so the diff below — both the per-entry skip and the removal
	// pass — compares against the state prior to this run. A failure here
	// aborts the whole sync: proceeding as though the store held nothing
	// would make the removal pass delete rows this very call just wrote, and
	// would re-parse and re-write every note on every single reconcile.
	previous := make(map[string]storedNoteState, stats.Scanned)
	if err := store.AllPaths(ctx, func(path, kind, hash string) error {
		previous[path] = storedNoteState{kind: kind, hash: hash}
		return nil
	}); err != nil {
		return stats, fmt.Errorf("vaultprops: listing the previously indexed paths: %w", err)
	}

	seen := make(map[string]struct{}, stats.Scanned)

	for _, entry := range scan.Entries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stats, ctxErr
		}
		seen[entry.RelPath] = struct{}{}

		// FR-044, applied to the write side the same way it is applied to
		// read and author (tools.go's retrievalPath, author.go's
		// authorWriteTarget): resolve through the collection root and refuse
		// a path that reaches its target only through a symbolic link. Scan
		// itself never descends into a symlinked directory, but it also
		// never re-checks a path component that turned into one BETWEEN the
		// walk and this read — the TOCTOU window ResolveContainedNoSymlink
		// exists to close. An Lstat on an already-resolved path could never
		// observe this; that is why the resolution happens here, not a stat.
		abs, resolveErr := root.ResolveContainedNoSymlink(fsys, entry.RelPath)
		if resolveErr != nil {
			stats.Problems = append(stats.Problems, SyncProblem{
				RelPath: entry.RelPath, Reason: "unreadable", Detail: resolveErr.Error(),
			})
			if removed, derr := removeIfPresent(ctx, store, previous, entry.RelPath); derr != nil {
				return stats, derr
			} else if removed {
				stats.Removed++
			}
			continue
		}

		if entry.Kind == knowledge.ScanKindAttachment {
			// FR-039a still holds exactly: an attachment's bytes are never
			// opened, so it carries no hash and its CONTENT-derived rows never
			// change once written.
			//
			// FR-136 is what changed here. "Its row never changes once
			// written" was true of a row holding {Path, Kind} and is FALSE of
			// one holding size and mtime: an attachment replaced in place —
			// same name, new picture — would otherwise report its first-ever
			// size forever. The refresh reads the WALK's stat, which
			// knowledge.Scan already took; the file itself is never opened, so
			// nothing about FR-039a is relaxed.
			if prev, had := previous[entry.RelPath]; had && prev.kind == propindex.KindAttachment {
				stats.Unchanged++
				if err := refreshStatIfDrifted(ctx, store, entry, &stats); err != nil {
					return stats, err
				}
				continue
			}
			if err := store.UpsertNote(ctx, propindex.NoteRows{
				Path: entry.RelPath, Kind: propindex.KindAttachment,
				Size: entry.Size, MtimeNanos: entry.ModTimeNanos,
				CtimeNanos: entry.CtimeNanos, HasCtime: entry.HasCtime,
			}); err != nil {
				return stats, fmt.Errorf("vaultprops: indexing attachment %q: %w", entry.RelPath, err)
			}
			stats.Indexed++
			continue
		}

		// A note. Read through the same eviction-aware path lifecycle.go's
		// write/read tools use: a cloud placeholder that stats non-empty and
		// reads back nothing (FR-111) is refused as ErrNoteEvicted, distinct
		// from an ordinary read failure, and MUST NOT be indexed as an empty
		// note — that would be confidently wrong, not merely stale.
		src, readErr := knowledge.ReadNoteContent(fsys, abs)
		if readErr != nil {
			stats.Problems = append(stats.Problems, SyncProblem{
				RelPath: entry.RelPath, Reason: "unreadable", Detail: readErr.Error(),
			})
			// Mirrors knowledge.Index.SyncWith's own rule for the text index:
			// a file that could not be read is reported and left OUT of the
			// index, never indexed as empty and never left at its last-known
			// (possibly stale) content with nothing on record explaining why
			// it stopped moving.
			if removed, derr := removeIfPresent(ctx, store, previous, entry.RelPath); derr != nil {
				return stats, derr
			} else if removed {
				stats.Removed++
			}
			continue
		}

		hash := propindex.SourceHash(src)
		if prev, had := previous[entry.RelPath]; had && prev.kind == propindex.KindNote && prev.hash == hash {
			// CONTENT is unchanged — the note is not re-parsed and its
			// property, relation and task rows are left exactly as they are.
			// STAT is a separate question (FR-136): this is the branch a `git
			// checkout`, an rsync, a `touch` or an iCloud resync lands in, and
			// before this refresh existed it was where file.mtime and
			// file.size went to freeze.
			stats.Unchanged++
			if err := refreshStatIfDrifted(ctx, store, entry, &stats); err != nil {
				return stats, err
			}
			continue
		}

		rec := records.ParseRecord(entry.RelPath, src)
		if rec.ParseError != "" {
			// The frontmatter itself could not be read — distinct from a
			// property inside it failing to conform (rows.go's own
			// nonConformingEvidence path handles that case, per-property,
			// with the row still written). This note still gets a row below
			// (ExtractTasks and the bare note identity do not depend on
			// frontmatter parsing), but the fact that its declared type and
			// properties could not be determined is reported, not silently
			// treated as "this note declares no type".
			stats.Problems = append(stats.Problems, SyncProblem{
				RelPath: entry.RelPath, Reason: "frontmatter_unparseable", Detail: rec.ParseError,
			})
		}

		var schema *records.Schema
		if t := rec.TypeName(); t != "" {
			if sc, ok := schemas.Get(t); ok {
				schema = sc
			}
			// t != "" and no matching schema: FR-005, an ordinary note. Not
			// a problem to report — a note declaring a type nobody has
			// defined yet is the normal state of a vault mid-authoring.
		}

		rows := propindex.BuildNoteRows(rec, schema, src, hash)
		// Stat is set ON THE INSERT, not by a follow-up UPDATE. A row that is
		// briefly wrong is a row a concurrent reader can observe, and a note
		// that reports size 0 for the width of one extra statement is exactly
		// the kind of transient wrong answer this index is built to not have.
		rows.Size, rows.MtimeNanos = entry.Size, entry.ModTimeNanos
		rows.CtimeNanos, rows.HasCtime = entry.CtimeNanos, entry.HasCtime
		if err := store.UpsertNote(ctx, rows); err != nil {
			return stats, fmt.Errorf("vaultprops: indexing %q: %w", entry.RelPath, err)
		}
		stats.Indexed++
	}

	// Deletion pass: every path the store held that this walk did not see —
	// deleted, renamed, moved out of the collection, or trashed (.trash is a
	// scanSkippedDirName, so a note moved there simply stops appearing here).
	// Sorted so the outcome (and a Recorder observing it) is deterministic.
	var toDelete []string
	for path := range previous {
		if _, ok := seen[path]; !ok {
			toDelete = append(toDelete, path)
		}
	}
	sort.Strings(toDelete)
	for _, path := range toDelete {
		if err := store.DeleteNote(ctx, path); err != nil {
			return stats, fmt.Errorf("vaultprops: removing %q: %w", path, err)
		}
		stats.Removed++
	}

	// FR-032, mirroring knowledge.enforceIndexPermissions for the bleve
	// index: the properties index holds the same note content, under the
	// same $OMNIPUS_HOME, and must not be left world- or group-readable under
	// a permissive umask.
	if err := os.Chmod(idxPath, 0o600); err != nil {
		return stats, fmt.Errorf("vaultprops: setting permissions on the properties index: %w", err)
	}
	if err := os.Chmod(filepath.Dir(idxPath), 0o700); err != nil {
		return stats, fmt.Errorf("vaultprops: setting permissions on the index directory: %w", err)
	}

	return stats, nil
}

// removeIfPresent deletes path from store only if this run's own snapshot of
// the store (taken before any write) shows it was actually there — sparing a
// DeleteNote round trip for a path that was never indexed to begin with, and
// reporting accurately whether anything was removed.
func removeIfPresent(ctx context.Context, store propindex.Store, previous map[string]storedNoteState, path string) (bool, error) {
	if _, had := previous[path]; !had {
		return false, nil
	}
	if err := store.DeleteNote(ctx, path); err != nil {
		return false, fmt.Errorf("vaultprops: removing %q: %w", path, err)
	}
	return true, nil
}
