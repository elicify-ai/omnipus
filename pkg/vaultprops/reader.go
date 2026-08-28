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
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
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
// A refusal here is not a failed call: vault_describe renders it as NOT
// CHECKED against the categories it blocks, by name.
func Open(ctx context.Context, home, collectionRoot string) (knowledge.PropertyIndexReader, error) {
	path, err := knowledge.PropertiesIndexPath(home, collectionRoot)
	if err != nil {
		return nil, fmt.Errorf("the properties index could not be located: %w", err)
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
			return nil, fmt.Errorf(
				"the properties index for this collection has not been built yet, and closing it failed: %w", cerr)
		}
		return nil, fmt.Errorf(
			"the properties index for this collection has not been built yet, so nothing typed can be checked; " +
				"index the collection and re-run check_integrity")
	}
	return NewReader(store), nil
}
