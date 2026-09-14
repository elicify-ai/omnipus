// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// rename_folder.go - renaming or moving a FOLDER without breaking the
// collection (round-4 attachment cascade; UAT 2026-09-13 D-123 / #701).
//
// A folder rename is a note rename repeated for every file under the folder,
// with one physical move. So it is built from the same parts rather than
// beside them. Renamer.Plan expands the folder into one PathMove per note and
// attachment under it, and the single link-rewrite loop in rename.go rewrites
// every resolved link whose target is any of those files, exactly as it does
// for the one file of a note rename.
//
// The journal is unchanged as well. Its From/To name the folder, performMove
// renames the folder in one syscall, and Recover always moves before it
// rewrites. So each step for a file INSIDE the folder is recorded at the path
// that file will have after the move. A crash at any point recovers forward
// through the existing machinery, and a journal written by this code is read
// and applied correctly by a binary that predates it.
//
// A folder rename must be asked for explicitly (RenameRequest.Folder). Every
// agent door adds ".md" to the path it is given, and the flag keeps it that
// way by construction: a caller that only ever meant a note can never move a
// whole folder.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PathMove is one file a rename relocates: the single subject of a note or
// attachment rename, or each file under a renamed folder.
type PathMove struct {
	From string
	To   string
}

// checkFolderRenameSource refuses the two folder renames that a file rename
// cannot express.
//
// Into itself: os.Rename refuses it, but only AFTER the journal has been
// written, which would leave a pending journal that blocks every later rename
// in the collection.
//
// A source spelled differently from the folder on disk: on a case-insensitive
// filesystem "projects/atlas" opens "Projects/Atlas", but the link graph lists
// the files under their on-disk spelling, so no file would match the folder
// and the folder would move with every link into it left dangling. A note
// rename is protected from the same thing by its graph-membership check; a
// folder has no entry in the graph to check.
func checkFolderRenameSource(root CollectionRoot, from, to string) error {
	if strings.HasPrefix(strings.ToLower(to), strings.ToLower(from)+"/") {
		return fmt.Errorf("%w: cannot move folder %q inside itself (%q)", ErrRenameInvalidPath, from, to)
	}
	if err := requireExactSpelling(root, from); err != nil {
		return fmt.Errorf("%w: %w", ErrRenameSourceNotAddressable, err)
	}
	return nil
}

// requireExactSpelling confirms that every segment of rel names a directory
// entry spelled exactly that way on disk. It reads directory listings rather
// than stat results, because a stat on a case-insensitive filesystem succeeds
// for any spelling and so cannot tell the two apart.
func requireExactSpelling(root CollectionRoot, rel string) error {
	dir := root.Path()
	for _, seg := range strings.Split(rel, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("knowledge: read %q: %w", dir, err)
		}
		found := false
		for _, e := range entries {
			if e.Name() == seg {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%q is not spelled the way it is on disk", rel)
		}
		dir = filepath.Join(dir, seg)
	}
	return nil
}

// filesUnderFolder returns the files (notes and attachments, as the link
// graph lists them) that sit anywhere under folder, in the input's order.
func filesUnderFolder(files []string, folder string) []string {
	prefix := folder + "/"
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// folderMoves is the plan of a folder rename, one PathMove per file under it.
func folderMoves(files []string, from, to string) []PathMove {
	members := filesUnderFolder(files, from)
	out := make([]PathMove, 0, len(members))
	for _, m := range members {
		out = append(out, PathMove{From: m, To: to + "/" + strings.TrimPrefix(m, from+"/")})
	}
	return out
}

// inboundFromOutsideFolder is every resolved link to any of members from a
// note that is NOT itself under folder. A link between two files inside the
// folder travels with the folder, so it is neither broken by a trash nor
// repaired by a restore.
func inboundFromOutsideFolder(graph *LinkGraph, members []string, folder string) []ResolvedLink {
	prefix := folder + "/"
	var out []ResolvedLink
	for _, m := range members {
		for _, bl := range graph.Backlinks(m) {
			if !strings.HasPrefix(bl.From, prefix) {
				out = append(out, bl)
			}
		}
	}
	return out
}

// RefreshIndexesForFolderRename is RefreshIndexesForRename for a renamed
// folder: every file that moved leaves both indexes under its old path and is
// re-derived under its new one, and every note whose links were rewritten is
// re-derived too. The folder's own paths (res.From, res.To) are skipped
// because an index holds files, not folders. Like RefreshIndexesForRename it
// returns a warning, never an error: the rename has already fully applied.
func RefreshIndexesForFolderRename(ctx context.Context, home, collectionRoot string, res *RenameResult) string {
	if res == nil || res.NoOp {
		return ""
	}
	removed := make([]string, 0, len(res.Moves))
	updated := make([]string, 0, len(res.Moves)+len(res.Touched))
	for _, m := range res.Moves {
		removed = append(removed, m.From)
		updated = append(updated, m.To)
	}
	for _, p := range res.Touched {
		if p == res.From || p == res.To {
			continue
		}
		updated = append(updated, p)
	}
	return refreshIndexesForPaths(ctx, home, collectionRoot, removed, updated)
}

// refreshIndexesForPaths drops every removed path from both indexes and
// re-derives every updated path, opening each index once for the whole set.
// It is refreshIndexesForRename (author.go) generalised from one removed path
// to many, for the two whole-folder operations.
func refreshIndexesForPaths(ctx context.Context, home, collectionRoot string, removed, updated []string) string {
	if len(removed) == 0 && len(updated) == 0 {
		return ""
	}
	// The path an index-open failure is reported against: any one path of
	// the set, since the failure applies to all of them.
	var label string
	if len(removed) > 0 {
		label = removed[0]
	} else {
		label = updated[0]
	}

	var out indexRefreshOutcome
	ix, ixErr := OpenIndex(home, collectionRoot)
	if ixErr != nil {
		out.textProblem(label, ixErr)
	}
	store, stErr := openPropertiesIndexStore(ctx, home, collectionRoot)
	if stErr != nil {
		out.propsProblem(label, stErr)
	}

	for _, p := range removed {
		if ix != nil {
			out.textProblem(p, ix.RemovePath(ctx, p))
		}
		if store != nil {
			out.propsProblem(p, store.DeleteNote(ctx, p))
		}
	}
	seen := make(map[string]struct{}, len(updated))
	for _, p := range updated {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if ix != nil {
			out.textProblem(p, ix.UpdatePath(ctx, p))
		}
		if store != nil {
			out.propsProblem(p, upsertPropertiesNote(ctx, store, collectionRoot, p))
		}
	}

	if ix != nil {
		if cerr := ix.Close(); cerr != nil {
			slog.Warn("knowledge: closing the text index after a folder's index refresh",
				"root", collectionRoot, "error", cerr)
		}
	}
	if store != nil {
		if cerr := store.Close(); cerr != nil {
			slog.Warn("knowledge: closing the properties index after a folder's index refresh",
				"root", collectionRoot, "error", cerr)
		}
	}
	return out.summary()
}
