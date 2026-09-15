// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_trash_folder.go - trashing and restoring a whole
// FOLDER inside a knowledge base (round-4 attachment cascade; UAT 2026-09-13
// D-123 / #701). Before this, deleting a folder through the Library removed
// it and everything in it for good.
//
// The trash convention is the file engine's, applied to a directory: the
// folder moves, bytes untouched, to .omnipus-vault/trash/<timestamp>/<original
// path>, with an entry.json receipt beside it. Links INTO the folder from
// notes outside it are counted and named, never repaired (FR-048). Links
// between files inside the folder travel with it.
//
// Two decisions differ from the single-file engine, and both are deliberate.
//
// The receipt is written BEFORE the move, not after. A folder is only
// recognised as a trashed folder through its receipt (see
// findTrashFolderCopies), so a folder that reached the trash without one could
// not be restored by name. If the move then fails, the receipt is removed
// again and nothing else has changed.
//
// Restore consults the receipt to decide WHETHER a trash entry holds a folder.
// It never lets the receipt decide WHERE to write: the destination is always
// ResolveContainedNoSymlink(original path), exactly as for a file.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// trashKindFolder is trashReceipt.Kind for a whole-folder trash.
const trashKindFolder = "folder"

// trashFolder moves the folder `from` and everything under it into the trash.
func (tr *Trasher) trashFolder(fsys LinkFS, from string) (*TrashResult, error) {
	refuse := func(err error) (*TrashResult, error) {
		tr.emit(trashOpTrash, "refused", []string{from}, err.Error())
		return nil, err
	}

	if rerr := authorRefuseReserved(from); rerr != nil {
		return refuse(rerr)
	}
	abs, err := tr.Root.ResolveContainedNoSymlink(fsys, from)
	if err != nil {
		return refuse(err)
	}
	info, err := fsys.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return refuse(fmt.Errorf("%w: no folder at %q", ErrTrashSourceMissing, from))
	case err != nil:
		return refuse(fmt.Errorf("knowledge: stat %q: %w", from, err))
	case !info.IsDir():
		return refuse(fmt.Errorf("%w: %q is not a folder", ErrTrashSourceMissing, from))
	}
	// The same reason checkFolderRenameSource gives: a differently-spelled
	// path would match no file in the graph, so the dangling-link report and
	// Members would both come back empty for a folder that is not empty.
	if serr := requireExactSpelling(tr.Root, from); serr != nil {
		return refuse(fmt.Errorf("%w: %w", ErrTrashSourceMissing, serr))
	}

	// Computed BEFORE the move, while the files still resolve (AC-X1).
	graph, gerr := BuildLinkGraph(fsys, tr.Root)
	if gerr != nil {
		return refuse(gerr)
	}
	members := filesUnderFolder(graph.Files(), from)
	backlinks := inboundFromOutsideFolder(graph, members, from)
	danglingNotes, truncated := dedupeAndCapNotePaths(backlinks)

	var result TrashResult
	lockErr := WithNoteWriteLock(tr.lockConfig(), from, func() error {
		now := tr.now()
		trashID, trashDirRel, aerr := tr.allocateTrashDir(fsys, now)
		if aerr != nil {
			return aerr
		}
		entryAbs := filepath.Join(tr.Root.Path(), filepath.FromSlash(trashDirRel))
		trashFolderRel := path.Join(trashDirRel, from)
		trashFolderAbs := filepath.Join(tr.Root.Path(), filepath.FromSlash(trashFolderRel))
		if mkErr := os.MkdirAll(filepath.Dir(trashFolderAbs), markerDirPerm); mkErr != nil {
			return fmt.Errorf("knowledge: create trash directory: %w", mkErr)
		}

		receipt := trashReceipt{
			OriginalPath: from, Collection: tr.Root.Path(), TrashedAt: now.UTC().Format(time.RFC3339),
			AgentID: tr.AgentID, Kind: trashKindFolder,
			DanglingLinks: len(backlinks), DanglingNotes: danglingNotes,
		}
		receiptBytes, jerr := json.MarshalIndent(receipt, "", "  ")
		if jerr != nil {
			return fmt.Errorf("knowledge: encode trash receipt: %w", jerr)
		}
		receiptAbs := filepath.Join(entryAbs, trashReceiptFileName)
		if werr := fileutil.WriteFileAtomic(receiptAbs, receiptBytes, markerFilePerm); werr != nil {
			discardTrashEntry(entryAbs, receiptAbs, trashID)
			return fmt.Errorf("knowledge: write trash receipt: %w", werr)
		}
		if mvErr := os.Rename(abs, trashFolderAbs); mvErr != nil {
			discardTrashEntry(entryAbs, receiptAbs, trashID)
			return fmt.Errorf("knowledge: move folder %q to trash: %w", from, mvErr)
		}

		priors, perr := tr.priorTrashings(fsys, from, trashID)
		if perr != nil {
			slog.Warn("knowledge: could not enumerate prior trashings", "path", from, "error", perr)
		}
		result = TrashResult{
			OriginalPath: from, TrashID: trashID, TrashPath: trashFolderRel,
			DanglingLinkCount: len(backlinks), DanglingNotes: danglingNotes, DanglingNotesTruncated: truncated,
			PriorTrashings: priors, Folder: true, Members: members,
		}
		return nil
	})
	if lockErr != nil {
		return refuse(lockErr)
	}
	tr.emit(trashOpTrash, "applied", []string{from, result.TrashPath}, "")
	return &result, nil
}

// discardTrashEntry removes a trash entry that a failed folder trash left
// behind (its receipt and the empty directories), so a refusal leaves no
// trace in the trash. Failures are logged: the folder itself was never moved.
func discardTrashEntry(entryAbs, receiptAbs, trashID string) {
	if remErr := os.Remove(receiptAbs); remErr != nil && !errors.Is(remErr, fs.ErrNotExist) {
		slog.Warn("knowledge: could not remove the receipt of a failed folder trash", "trash_id", trashID, "error", remErr)
	}
	if pruneErr := removeEmptyDirsRecursively(entryAbs); pruneErr != nil {
		slog.Warn("knowledge: could not prune the entry of a failed folder trash", "trash_id", trashID, "error", pruneErr)
	}
}

// findTrashFolderCopies enumerates every trashed copy of the FOLDER
// originalPath, most recently trashed first.
//
// Unlike findTrashCopies this has to read the receipt. Trashing the note
// "Deals/Acme.md" also creates "<trash id>/Deals/" as that note's parent
// directory, which on disk looks exactly like a trashed folder "Deals"; only
// the receipt says which operation made it. A folder whose receipt is missing
// or unreadable therefore cannot be restored as a folder, but every file
// inside it can still be restored by its own path, because findTrashCopies
// needs no receipt (design note section 7: degrade, never fail).
func (tr *Trasher) findTrashFolderCopies(fsys LinkFS, originalPath string) ([]trashCopy, error) {
	ids, err := tr.listTrashDirs(fsys)
	if err != nil {
		return nil, err
	}
	var out []trashCopy
	for _, id := range ids {
		receipt, ok := tr.readReceipt(id)
		if !ok || receipt.Kind != trashKindFolder || receipt.OriginalPath != originalPath {
			continue
		}
		dirAbs := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, id, filepath.FromSlash(originalPath))
		info, statErr := fsys.Lstat(dirAbs)
		if statErr != nil || !info.IsDir() {
			continue
		}
		out = append(out, trashCopy{TrashID: id, FileAbs: dirAbs, HasReceipt: ok, Receipt: receipt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrashID > out[j].TrashID })
	return out, nil
}

// restoreFolder puts a trashed folder back at its original path, with the
// same refusals a file restore makes: a reserved location, an occupied
// destination (never overwritten), and a record identifier a live note already
// holds (FR-038a), checked for every note inside the folder.
func (tr *Trasher) restoreFolder(fsys LinkFS, orig string, copies []trashCopy, trashedAt string) (*RestoreResult, error) {
	refuse := func(paths []string, err error) (*RestoreResult, error) {
		tr.emit(trashOpRestore, "refused", paths, err.Error())
		return nil, err
	}

	if rerr := authorRefuseReserved(orig); rerr != nil {
		return refuse([]string{orig}, rerr)
	}
	chosen, cerr := chooseTrashCopy(copies, trashedAt, "folder", orig)
	if cerr != nil {
		return refuse([]string{orig}, cerr)
	}
	destAbs, err := tr.Root.ResolveContainedNoSymlink(fsys, orig)
	if err != nil {
		return refuse([]string{orig}, err)
	}
	if _, statErr := fsys.Lstat(destAbs); statErr == nil {
		rerr := fmt.Errorf("%w: something already occupies %s, so the trashed folder (trashed_at %s, kept at %s) cannot be restored onto it; move or rename what is there first, then restore again",
			ErrRestoreDestinationExists, orig, chosen.TrashID, trashRelPath(tr.Root, chosen.FileAbs))
		return refuse([]string{orig}, rerr)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return refuse([]string{orig}, statErr)
	}

	if ids := trashedFolderRecordIDs(chosen.FileAbs, orig); len(ids) > 0 {
		want := make(map[string]struct{}, len(ids))
		for id := range ids {
			want[id] = struct{}{}
		}
		collidingPath, id, found, ferr := tr.findLiveRecordByAnyID(fsys, want)
		if ferr != nil {
			return refuse([]string{orig}, ferr)
		}
		if found {
			crerr := fmt.Errorf("%w: %s already holds identifier %q, the identifier the trashed copy of %s carries",
				ErrRestoreIdentifierCollision, collidingPath, id, ids[id])
			return refuse([]string{orig, collidingPath}, crerr)
		}
	}

	var result RestoreResult
	lockErr := WithNoteWriteLock(tr.lockConfig(), orig, func() error {
		if mkErr := os.MkdirAll(filepath.Dir(destAbs), markerDirPerm); mkErr != nil {
			return fmt.Errorf("knowledge: create %q: %w", path.Dir(orig), mkErr)
		}
		if mvErr := os.Rename(chosen.FileAbs, destAbs); mvErr != nil {
			return fmt.Errorf("knowledge: restore folder %q: %w", orig, mvErr)
		}
		entryAbs := filepath.Join(tr.Root.Path(), MarkerDirName, trashDirName, chosen.TrashID)
		if remErr := os.Remove(filepath.Join(entryAbs, trashReceiptFileName)); remErr != nil && !errors.Is(remErr, fs.ErrNotExist) {
			slog.Warn("knowledge: could not remove trash receipt after restore", "trash_id", chosen.TrashID, "error", remErr)
		}
		if pruneErr := removeEmptyDirsRecursively(entryAbs); pruneErr != nil {
			slog.Warn("knowledge: could not prune empty trash directory", "trash_id", chosen.TrashID, "error", pruneErr)
		}

		resolvedLinks := 0
		if graph, gerr := BuildLinkGraph(fsys, tr.Root); gerr == nil {
			resolvedLinks = len(inboundFromOutsideFolder(graph, filesUnderFolder(graph.Files(), orig), orig))
		} else {
			slog.Warn("knowledge: could not recompute backlinks after restore", "path", orig, "error", gerr)
		}

		other := make([]string, 0, len(copies)-1)
		for _, c := range copies {
			if c.TrashID != chosen.TrashID {
				other = append(other, c.TrashID)
			}
		}
		result = RestoreResult{
			OriginalPath: orig, RestoredFrom: chosen.TrashID, OtherAvailable: other,
			ResolvedLinksCount: resolvedLinks, Folder: true,
		}
		return nil
	})
	if lockErr != nil {
		return refuse([]string{orig}, lockErr)
	}
	tr.emit(trashOpRestore, "applied", []string{orig, path.Join(MarkerDirName, trashDirName, chosen.TrashID)}, "")
	return &result, nil
}

// trashedFolderRecordIDs reads the record identifier of every markdown note
// inside a trashed folder, mapping identifier to the note's original
// collection-relative path. It skips what the live walk skips (symbolic links
// and the tool-state directories in scanSkippedDirNames), because a note there
// is not a live record after the restore either. An unreadable directory or
// note is skipped for the reason findLiveRecordByAnyID gives: it cannot be
// proven to collide.
func trashedFolderRecordIDs(dirAbs, orig string) map[string]string {
	ids := make(map[string]string)
	type queued struct{ abs, rel string }
	stack := []queued{{abs: dirAbs, rel: orig}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(cur.abs)
		if err != nil {
			slog.Warn("knowledge: could not read a trashed folder while checking record identifiers", "path", cur.rel, "error", err)
			continue
		}
		for _, e := range entries {
			childAbs := filepath.Join(cur.abs, e.Name())
			childRel := cur.rel + "/" + e.Name()
			mode := e.Type()
			switch {
			case mode&fs.ModeSymlink != 0:
				continue
			case mode.IsDir():
				if _, skip := scanSkippedDirNames[e.Name()]; !skip {
					stack = append(stack, queued{abs: childAbs, rel: childRel})
				}
			case mode.IsRegular() && IsMarkdownPath(childRel):
				data, rerr := os.ReadFile(childAbs)
				if rerr != nil {
					slog.Warn("knowledge: could not read a trashed note while checking record identifiers", "path", childRel, "error", rerr)
					continue
				}
				if id := records.ParseRecord(childRel, data).ID(); id != "" {
					if _, seen := ids[id]; !seen {
						ids[id] = childRel
					}
				}
			}
		}
	}
	return ids
}

// trashRelPath spells an absolute path inside the collection relative to it,
// for messages.
func trashRelPath(root CollectionRoot, abs string) string {
	if rel, err := filepath.Rel(root.Path(), abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return abs
}

// RemoveFromIndexesForFolderTrash is RemoveFromIndexesForNote for a trashed
// folder: every file that left the live collection with it leaves both
// indexes. It returns a warning, never an error: the trash has already
// happened.
func RemoveFromIndexesForFolderTrash(ctx context.Context, home, collectionRoot string, res *TrashResult) string {
	if res == nil {
		return ""
	}
	return refreshIndexesForPaths(ctx, home, collectionRoot, res.Members, nil)
}
