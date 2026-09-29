// Omnipus — gateway-side helpers for the generated LibraryEntryView type.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ptrStringSlice wraps s in a pointer (or nil for an empty slice) for the
// generated LibraryEntryView.ConflictPaths field.
func ptrStringSlice(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	return &s
}

// rejectionEntryRejection wraps the raw rejection string in the generated
// enum pointer. Returns nil for the empty string so a healthy entry never
// carries a rejection field.
func rejectionEntryRejection(raw string) *gen.LibraryEntryViewRejection {
	if raw == "" {
		return nil
	}
	r := gen.LibraryEntryViewRejection(raw)
	return &r
}

// parseViewBytesShared parses a .view file's bytes (already read) and
// returns the loaded SavedView plus a rejection. It is the gateway's
// shared view-bytes parser so the listing path (D-VIEW-INDEX cache
// rebuild) and any future preview pre-fetch use one implementation, not
// each writing its own JSON-round-trip through generated.ViewDef.
//
// Deduplication is NOT done here: the caller decides whether to feed the
// result into a dedup index, and the same parser serves both the per-path
// stamp and the per-collection dedup pass without either leaking its
// concerns into the other.
func parseViewBytesShared(path string, data []byte) (*records.SavedView, *records.ViewRejection) {
	return records.ParseView(path, data)
}
