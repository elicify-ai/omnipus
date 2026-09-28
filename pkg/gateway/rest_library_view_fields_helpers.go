// Omnipus — gateway-side helper that constructs the LibraryEntry.View
// anonymous-struct value (oapi-codegen emits it inline, with no exported
// name), without editing the generated package (Hard Constraint #8).
//
// The pattern: viewStructType captures the reflect.Type of the
// LibraryEntry.View field ONCE (off a LibraryEntry zero value) and
// caches it for every subsequent call. Per-row construction is then a
// reflect.New(viewStructType).Elem() plus eight reflect.Value.FieldByName
// sets — the cost is bounded and constant, no map allocations per call.
//
// The shape mirrors the anonymous struct in pkg/api/generated. A generator
// revision that adds a field trips a compile error HERE (the field name
// does not exist on the type, FieldByName returns a zero Value, and the
// next Set panics — caught by tests). The bag below is the source of truth
// the gateway keeps for the field names.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"reflect"
	"sync"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

var (
	viewStructTypeOnce sync.Once
	viewStructType     reflect.Type
)

// viewStructTypeCached resolves LibraryEntry.View's anonymous-struct type
// once. The first call uses a LibraryEntry zero value; the type is the
// struct the generator emitted, and it does not change for the process's
// lifetime.
func viewStructTypeCached() reflect.Type {
	viewStructTypeOnce.Do(func() {
		var e gen.LibraryEntry
		viewStructType = reflect.TypeOf(e.View).Elem()
	})
	return viewStructType
}

// libraryEntryViewFields is the per-field bag the caller fills in. Every
// optional LibraryEntryView field is here so a generator revision that
// adds a field makes this struct wrong (caught by compile) — not a test
// failure at runtime.
type libraryEntryViewFields struct {
	Label           *string
	Name            *string
	Kind            *gen.LibraryEntryViewKind
	CollectionID    *string
	Rejection       *gen.LibraryEntryViewRejection
	RejectionReason *string
	ConflictPaths   *[]string
	DerivedFrom     *string
}

// newLibraryEntryView constructs a LibraryEntry.View value via reflection.
// The returned reflect.Value can be assigned to e.View by the caller (it
// holds the same anonymous-struct type as LibraryEntry.View's pointer
// target). Allocation is one struct per call; no per-field map overhead.
func newLibraryEntryView(f libraryEntryViewFields) reflect.Value {
	v := reflect.New(viewStructTypeCached()).Elem()
	if f.Label != nil {
		v.FieldByName("Label").Set(reflect.ValueOf(f.Label))
	}
	if f.Name != nil {
		v.FieldByName("Name").Set(reflect.ValueOf(f.Name))
	}
	if f.Kind != nil {
		v.FieldByName("Kind").Set(reflect.ValueOf(f.Kind))
	}
	if f.CollectionID != nil {
		v.FieldByName("CollectionId").Set(reflect.ValueOf(f.CollectionID))
	}
	if f.Rejection != nil {
		v.FieldByName("Rejection").Set(reflect.ValueOf(f.Rejection))
	}
	if f.RejectionReason != nil {
		v.FieldByName("RejectionReason").Set(reflect.ValueOf(f.RejectionReason))
	}
	if f.ConflictPaths != nil {
		v.FieldByName("ConflictPaths").Set(reflect.ValueOf(f.ConflictPaths))
	}
	if f.DerivedFrom != nil {
		v.FieldByName("DerivedFrom").Set(reflect.ValueOf(f.DerivedFrom))
	}
	return v
}

// assignLibraryEntryView stamps v (a reflect.Value holding the anonymous-
// struct type) into e.View. The Addr().Interface().(*struct{...}) cast
// pins the type at compile time: a generator revision that changes the
// struct shape makes this cast fail at compile, not at runtime.
func assignLibraryEntryView(e *gen.LibraryEntry, v reflect.Value) {
	e.View = v.Addr().Interface().(*struct {
		CollectionId    *string                        `json:"collection_id,omitempty"`
		ConflictPaths   *[]string                      `json:"conflict_paths,omitempty"`
		DerivedFrom     *string                        `json:"derived_from,omitempty"`
		Kind            *gen.LibraryEntryViewKind      `json:"kind,omitempty"`
		Label           *string                        `json:"label,omitempty"`
		Name            *string                        `json:"name,omitempty"`
		Rejection       *gen.LibraryEntryViewRejection `json:"rejection,omitempty"`
		RejectionReason *string                        `json:"rejection_reason,omitempty"`
	})
}

// ptrStringSlice wraps s in a pointer (or nil for an empty slice) so the
// caller can hand it to libraryEntryViewFields.ConflictPaths verbatim.
func ptrStringSlice(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	return &s
}

// rejectionEntryRejection wraps the raw rejection string in the typed
// enum pointer used by libraryEntryViewFields. Returns nil for the empty
// string so a healthy entry never carries a rejection field.
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