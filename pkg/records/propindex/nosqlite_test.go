// Omnipus — FR-020h: on a build without SQLite the record layer REFUSES BY NAME.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build records_no_sqlite || mipsle || netbsd || (freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestOpen_RefusesByNameWithoutSQLite.
//
// The failure this guards against is not a crash — it is an EMPTY RESULT that
// looks complete. D13's headline failure and §1.3's whole subject is a system
// that answers confidently over data it could not see, so a build that cannot
// hold typed properties must say so rather than return zero records.
//
// It runs on this developer machine because `records_no_sqlite` is a forcing
// tag with no product build behind it:
//
//	go test -tags goolm,stdjson,records_no_sqlite ./pkg/records/...
func TestOpen_RefusesByNameWithoutSQLite(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "properties.db"), Options{})
	if err == nil {
		t.Fatal("FR-020h: Open must refuse on a build without SQLite; " +
			"it returned a usable store, which would answer every typed query with a confident nothing")
	}
	if store != nil {
		t.Errorf("a store was returned alongside the refusal: %#v", store)
	}
	if !errors.Is(err, records.ErrPropertyIndexUnavailable) {
		t.Errorf("the refusal does not unwrap to the platform sentinel, so no caller can recognise it: %v", err)
	}
	// The refusal string belongs to pkg/records and is pinned by its own test.
	// What is asserted here is that this package returns it UNCHANGED: wrapping
	// it with a second sentence is how two refusals for one condition appear.
	want := records.RequirePropertyIndex(records.CapabilityOpenIndex)
	if want == nil {
		t.Fatal("the platform gate reports the index is available on a build that cannot compile it")
	}
	if err.Error() != want.Error() {
		t.Errorf("the refusal was reworded on the way out:\n  got:  %q\n  want: %q", err, want)
	}
}
