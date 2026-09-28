// Omnipus — orchestration between pkg/knowledge.DiscoverViewFiles (walk +
// no-follow read + size cap) and pkg/records.LoadViewPaths (parse +
// dedup). Per library-views-anywhere-spec §4 steps 1 and 3, these two
// halves are separate so the import cycle MAJ-008 forbids does not
// reverse; this function is the ONE place both halves are wired.
//
// Every caller that needs the loaded view set for a collection — the
// knowledge tools, the gateway view annotation, the re-derivation
// pipeline, the importer — goes through here. The signature is a
// small change from the legacy `records.LoadViews(root, schemas)`:
// callers now hand the LinkFS and CollectionRoot they already hold
// (agent tools and the gateway have one from their own doors; the
// importer and re-derivation build one from their KB).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// LoadViewsForCollection is the public orchestration entry point. It
// runs the discovery walk, then hands the bytes to records.LoadViewPaths.
//
// schemas may be nil (the orphan-keyword distinction is documented on
// records.LoadViews's old signature). Skipped directories from the
// walk are surfaced on the report as view_unreadable rejections with
// the subfolder's collection-relative path — every view-reporting
// surface (knowledge_describe, knowledge_find, the gateway listing,
// the two REST view-list handlers) gets the same signal, so a
// subfolder can never silently disappear from the view count.
//
// fsys and root must already be validated (LinkFS.EvalSymlinks + the
// root's own constructor run first). The helper does not re-validate
// them — callers build them once per request and pass them down.
func LoadViewsForCollection(fsys LinkFS, root CollectionRoot, schemas *records.SchemaSet) (*records.ViewSet, *records.ViewLoadReport, error) {
	if !root.Valid() {
		return nil, nil, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	disc, err := DiscoverViewFiles(fsys, root)
	if err != nil {
		return nil, nil, fmt.Errorf("view discovery: %w", err)
	}

	// Translate the discovery report into the records loader's input
	// shape. Bytes already read by the walker flow through unchanged;
	// walker-level rejections propagate as their own report entries.
	files := make([]records.ViewFileBytes, 0, len(disc.Files))
	for _, vf := range disc.Files {
		files = append(files, records.ViewFileBytes{
			Path:      vf.Path,
			Bytes:     vf.Bytes,
			Rejection: vf.Rejection,
			Err:       vf.Err,
		})
	}

	set, report, err := records.LoadViewPaths(files, schemas)
	if err != nil {
		return nil, nil, fmt.Errorf("view parse: %w", err)
	}
	// Surface skipped subfolders on the report, every surface (FR-VA-025,
	// R2-MIN-007). Each skipped directory is reported as ONE rejection
	// naming the directory's collection-relative path, with the
	// view_unreadable code, so a reader learns the count may be
	// incomplete without the listing having to fail.
	for _, sk := range disc.Skipped {
		if sk == "" {
			continue
		}
		report.Rejections = append(report.Rejections, records.ViewRejection{
			Paths:  []string{sk},
			Code:   records.RejectViewUnreadable,
			Reason: fmt.Sprintf("could not read subfolder %q during view discovery; views under it may have been missed", sk),
		})
	}
	return set, report, nil
}
