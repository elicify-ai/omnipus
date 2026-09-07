// Omnipus — the adapter between the derived properties index and the vault
// tools that read it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package vaultprops joins pkg/records/propindex to pkg/knowledge.
//
// ---------------------------------------------------------------------------
// WHY THIS PACKAGE EXISTS AT ALL — it is a cycle break, and the cycle is real
//
// check_integrity (pkg/knowledge) needs to read the derived properties index
// (pkg/records/propindex). The obvious edge — knowledge importing propindex —
// does not compile:
//
//	go test ./pkg/records/propindex/
//	  imports pkg/knowledge from memory_both_test.go
//	  imports pkg/records/propindex from integrity.go
//	  import cycle not allowed in test
//
// propindex's own in-package tests already import pkg/knowledge, and an
// in-package _test.go file's imports count toward the cycle. The PRODUCTION
// graph is acyclic; the TEST build is not, and only the test build says so.
// (pkg/knowledge/fields.go's header reasons about exactly this and stops one
// step short of noticing it.)
//
// So the dependency is inverted: pkg/knowledge declares the two questions it
// asks (knowledge.PropertyIndexReader) and this package answers them over the
// real store. It imports BOTH sides, and nothing imports it except the wiring
// layer that constructs the tools — so it can never be part of a cycle.
//
// It holds no logic of its own beyond the shape change. Anything that looks
// like a decision here belongs in the sweep or in the store.
// ---------------------------------------------------------------------------
package vaultprops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// Reader adapts a propindex.Store to knowledge.PropertyIndexReader.
type Reader struct {
	store propindex.Store
}

// NewReader wraps an already-open store. The caller keeps ownership: Close
// here closes the store it was given, and a caller that wants to keep the
// store should not call it.
func NewReader(store propindex.Store) *Reader { return &Reader{store: store} }

// ScanRecords visits every indexed record of one declared type.
//
// Every candidate is REJECTED on the way out, and that is not a verdict about
// the record — it is what keeps the sweep from counting against the store's
// survivor bound (propindex.BoundSurvivors). The sweep materialises nothing:
// it keeps an identifier and a path per record and drops the rest, so
// "accepted" would be claiming a survivor nobody is holding.
func (r *Reader) ScanRecords(ctx context.Context, recordType string, visit func(knowledge.IndexedRecord) error) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("vaultprops: ScanRecords called with no properties index")
	}
	sel := propindex.Selector{RecordType: recordType}
	return r.store.Candidates(ctx, sel, func(c propindex.Candidate) (propindex.Verdict, error) {
		if err := visit(knowledge.IndexedRecord{
			Path:       c.Path,
			RecordType: c.RecordType,
			RecordID:   c.RecordID,
		}); err != nil {
			return propindex.Rejected, err
		}
		return propindex.Rejected, nil
	})
}

// ScanRelations visits every relation edge owned by records of one type.
func (r *Reader) ScanRelations(ctx context.Context, recordType string, visit func(knowledge.IndexedRelation) error) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("vaultprops: ScanRelations called with no properties index")
	}
	sel := propindex.Selector{RecordType: recordType}
	return r.store.Relations(ctx, sel, func(hit propindex.RelationHit) error {
		return visit(knowledge.IndexedRelation{
			Path:     hit.Path,
			RecordID: hit.RecordID,
			Property: hit.Relation.Prop,
			Target:   hit.Relation.Target,
		})
	})
}

// Close releases the underlying store.
func (r *Reader) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

// Open is the knowledge.OpenPropertyIndexFunc a host wires into the vault
// tools. It resolves the index path for a collection, opens it, and refuses —
// by name — in every case where the typed questions cannot honestly be
// answered.
//
// THREE DISTINCT REFUSALS, and collapsing any two of them would lose
// information the operator needs:
//
//   - No SQLite in this build. propindex.Open returns
//     records.RequirePropertyIndex's error, which names the platform; it is
//     returned UNCHANGED (FR-020h), never wrapped into something generic.
//   - The index has never been built for this collection. NeedsFullIndex is
//     true, and a sweep over it would report zero duplicate identifiers for a
//     vault nobody has indexed — a confidently wrong all-clear.
//   - The path could not be resolved at all.
//
// A refusal here is not a failed call: knowledge_describe renders it as NOT
// CHECKED against the categories it blocks, by name.
func Open(ctx context.Context, home, collectionRoot string) (knowledge.PropertyIndexReader, error) {
	// THE PLATFORM QUESTION IS ASKED FIRST, and the order is load-bearing.
	// On a build with no SQLite there is no index file and never will be, so
	// the existence check below would fire first and answer "this collection
	// has not been indexed yet" — true of the file and false about the cause,
	// and it would send an operator on linux/mipsle to run an index that
	// cannot exist. FR-020h wants the refusal that names the PLATFORM.
	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		return nil, err
	}
	path, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	if err != nil {
		return nil, fmt.Errorf("the properties index could not be located: %w", err)
	}
	// The file is checked for EXISTENCE before propindex.Open is asked to
	// open it, and that is deliberate on two counts.
	//
	// (a) HONESTY. propindex.Open creates what it cannot find, and its failure
	//     when the containing directory does not exist is SQLite's
	//     "unable to open database file: out of memory (14)" — a message that
	//     describes nothing an operator can act on, in place of the true and
	//     actionable "this collection has never been indexed".
	// (b) NO WRITE ON A READ. knowledge_describe is a read tool. Letting it create
	//     a database as a side effect of describing a vault would put a file
	//     in $OMNIPUS_HOME that the caller never asked for and would then have
	//     to be told is empty.
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, errIndexNotBuilt()
		}
		return nil, fmt.Errorf("the properties index at %s could not be read: %w", path, statErr)
	}
	store, err := propindex.Open(ctx, path, propindex.Options{})
	if err != nil {
		// Returned unchanged. On a SQLite-less build this IS the platform
		// refusal, and wrapping it would replace the one message that names
		// the platform and says what still works.
		return nil, err
	}
	if store.NeedsFullIndex() {
		if cerr := store.Close(); cerr != nil {
			return nil, fmt.Errorf("%w (and closing it failed: %v)", errIndexNotBuilt(), cerr)
		}
		return nil, errIndexNotBuilt()
	}
	return NewReader(store), nil
}

// errIndexNotBuilt is the one wording for "there is nothing indexed here", so
// the two paths that reach it cannot drift into two different explanations of
// the same state.
func errIndexNotBuilt() error {
	return errors.New(
		"the properties index for this collection has not been built yet, so nothing typed can be checked; " +
			"index the collection and re-run check_integrity")
}
