// Omnipus — public surface of pkg/records used by pkg/knowledge's
// view-discovery orchestration. Per library-views-anywhere-spec §4
// step 3 (round-2 corrected): pkg/knowledge owns the walk + read
// (DiscoverViewFiles), pkg/records owns the parse + dedup
// (LoadViewPaths / loadViewBytes), and the input between them is the
// PUBLIC `ViewFileBytes` shape this file defines.
//
// pkg/records cannot import pkg/knowledge (MAJ-008's import cycle:
// pkg/knowledge already imports pkg/records). The orchestration
// function pkg/knowledge.LoadViewsForCollection closes the loop on
// the knowledge side, and the records side stays leaf — its callers
// pass []ViewFileBytes in and get a *ViewSet + *ViewLoadReport back.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

// ViewFileBytes is the in-memory shape of one .view file after the
// walker (pkg/knowledge.DiscoverViewFiles) has already opened and
// read it. The walker owns the no-follow open and the size cap
// (D-SYMLINK-READ / D-SIZECAP); the records loader only parses.
//
// PUBLIC because pkg/knowledge has to hand these across the package
// boundary, and pkg/records cannot import pkg/knowledge.
type ViewFileBytes struct {
	// Path is the absolute, real (post-symlink) path of the file —
	// matches what ParseView's `path` argument historically produced
	// (SavedView.SourcePath is set from it). Callers that need the
	// collection-relative form can compute it from the CollectionRoot.
	Path string
	// Bytes is the file's contents, capped at MaxViewFileBytes by the
	// walker. Empty when Rejection is set.
	Bytes []byte
	// Rejection is a view_* code (view_unreadable, view_too_large, ...)
	// when the walker could not deliver clean bytes. Empty for a
	// successful read.
	Rejection string
	// Err is the underlying walker error for a rejected file; empty for
	// a clean read.
	Err error
}

// MaxViewFileBytes is the .view per-file read cap (D-SIZECAP). 256 KiB
// is spec's floor, deliberately round and well above any real view's
// expected size (a filter tree plus metadata). The walker enforces it
// at the read via io.LimitReader(MaxViewFileBytes+1); the parser uses
// it only to label a file that grew past the cap during the walk as
// view_too_large.
//
// Public because pkg/knowledge has to honour the SAME constant in
// its own walker — two definitions would let one drift.
const MaxViewFileBytes = 256 * 1024

// LoadViewPaths parses a slice of already-discovered, already-read
// view files into a ViewSet plus a rejection report. The walker that
// produced files (pkg/knowledge.DiscoverViewFiles in production) is
// the ONLY caller that knows how the read happened — this function
// does ParseView per file (the JSON-round-trip through generated.ViewDef
// with DisallowUnknownFields), then the dedup step, and reports
// walker-level rejections verbatim.
//
// schemas may be nil: with a schema set, a view is additionally
// checked against it and a view naming a vanished type or property
// is REJECTED and reported; without one, only the view's own format
// is checked.
func LoadViewPaths(files []ViewFileBytes, schemas *SchemaSet) (*ViewSet, *ViewLoadReport, error) {
	report := &ViewLoadReport{}
	if len(files) == 0 {
		return NewViewSet(), report, nil
	}
	scanned := make([]string, 0, len(files))
	for _, f := range files {
		if f.Rejection != "" {
			report.Rejections = append(report.Rejections, ViewRejection{
				Paths:  []string{f.Path},
				Code:   ViewRejectionCode(f.Rejection),
				Reason: errReason(f.Err),
			})
			continue
		}
		scanned = append(scanned, f.Path)
	}
	report.ScannedFiles = append(report.ScannedFiles, scanned...)
	sortStrings(report.ScannedFiles)
	return loadViewBytes(files, schemas, report)
}

func errReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// sortStrings is a tiny adapter so the public LoadViewPaths doesn't
// need to import "sort" twice across this file and the loadViewBytes
// helper above.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
