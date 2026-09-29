// Omnipus — re-derive .base views using record-backed, locked writes.
// The editable derived_from marker is never authority to overwrite/delete.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// RederiveBaseResult is the account of one re-derivation: what the edited
// `.base` produced on disk, so the calling door can log it and a test can
// assert it, without either re-reading the views directory to infer what
// happened.
type RederiveBaseResult struct {
	// BaseRelPath is the vault-relative `.base` path that was re-derived.
	BaseRelPath string
	// Status is the rolled-up BaseOutcome status. OutcomeRefused means
	// NOTHING was written or deleted — see this file's header for why a
	// refusal is non-destructive.
	Status Outcome
	// RefusedReason is set only when Status == OutcomeRefused.
	RefusedReason string
	// Views carries the per-view outcomes (losses, refusals, produced paths)
	// for the caller that wants to report more than the rolled-up status.
	Views []ViewOutcome
	// Written lists the view slugs whose YAML was written (new or changed).
	Written []string
	// Unchanged lists the view slugs whose re-derived YAML was byte-identical
	// to the file already on disk, which was therefore left alone.
	Unchanged []string
	// Deleted lists the view slugs whose files were removed because this
	// source no longer declares them.
	Deleted []string
	// KeptExisting lists the view slugs from this source that were left in
	// place because the translation refused (nothing is deleted on a refusal).
	KeptExisting []string
	// MembershipWarning reports an unreadable/corrupt membership record. The
	// record is treated as empty, never reconstructed from editable view YAML.
	MembershipWarning string
}

// RederiveBase keeps the direct-call API for callers outside the gateway.
// Its sibling index stays outside this collection; production callers pass
// their actual Omnipus home through RederiveBaseWithHome instead.
func RederiveBase(vaultRoot, baseRelPath string) (*RederiveBaseResult, error) {
	return RederiveBaseWithHome(defaultViewMembershipHome(vaultRoot), vaultRoot, baseRelPath)
}

func defaultViewMembershipHome(vaultRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(vaultRoot)), ".omnipus-view-index")
}

// RederiveBaseWithHome serializes membership reconciliation, scans ALL views,
// and verifies every recorded member before any destructive write.
func RederiveBaseWithHome(home, vaultRoot, baseRelPath string) (*RederiveBaseResult, error) {
	root, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), vaultRoot)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Ext(baseRelPath), ".base") {
		return nil, fmt.Errorf("vaultimport: %q is not a .base file", baseRelPath)
	}
	abs, err := root.ResolveContainedNoSymlink(knowledge.OSLinkFS(), baseRelPath)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: unsafe base path %q: %w", baseRelPath, err)
	}
	data, err := knowledge.ReadNoteContent(nil, abs)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: reading the base file %q: %w", baseRelPath, err)
	}
	schemaSet, _, err := records.LoadSchemas(root.Path())
	if err != nil {
		return nil, fmt.Errorf("vaultimport: loading schemas for %q: %w", vaultRoot, err)
	}
	schemaIdx := schemaIndexFromSchemas(schemaSet)
	var result *RederiveBaseResult
	err = knowledge.WithViewMembershipLock(home, root.Path(), func() error {
		record, loadErr := knowledge.LoadViewMembership(home, root.Path())
		warning := ""
		if loadErr != nil {
			warning = loadErr.Error()
		}
		existing, report, viewErr := knowledge.LoadViewsForCollection(knowledge.OSLinkFS(), root, schemaSet)
		if viewErr != nil {
			return fmt.Errorf("vaultimport: discovering current views: %w", viewErr)
		}
		if reconcileErr := record.ReconcileManagedViewPaths(existing, report); reconcileErr != nil {
			return fmt.Errorf("vaultimport: reconcile moved managed views: %w", reconcileErr)
		}
		slugs := NewSlugRegistry()
		for _, v := range existing.Views() {
			slugs.Reserve(v.Name())
		}
		for _, name := range report.RejectedNames() {
			slugs.Reserve(name)
			if _, managed := record.Bases[baseRelPath][name]; managed {
				return fmt.Errorf("vaultimport: managed view %q has a duplicate or rejected definition", name)
			}
		}
		mine := make([]string, 0, len(record.Bases[baseRelPath]))
		for name := range record.Bases[baseRelPath] {
			v, _, verifyErr := record.VerifiedManagedView(baseRelPath, name)
			if verifyErr != nil {
				return fmt.Errorf("vaultimport: cannot verify managed view %q: %w", name, verifyErr)
			}
			slugs.Pin(baseRelPath, v.DisplayLabel(), name)
			mine = append(mine, name)
		}
		sort.Strings(mine)
		pb, parseErr := ParseBaseFile(data)
		if parseErr != nil {
			result = &RederiveBaseResult{BaseRelPath: baseRelPath, Status: OutcomeRefused,
				RefusedReason: parseErr.Error(), KeptExisting: mine}
		} else {
			result, viewErr = fileTranslatedBaseWithRecord(root.Path(), baseRelPath, pb, schemaIdx, slugs, record, mine)
			if viewErr != nil {
				return viewErr
			}
		}
		result.MembershipWarning = warning
		return nil
	})
	return result, err
}

// fileTranslatedBase is retained as a direct translation entry used inside
// this package; its supplied mine list is NOT authority for any mutation.
func fileTranslatedBase(vaultRoot, baseRelPath string, pb *ParsedBase, schemaIdx *SchemaIndex, slugs *SlugRegistry, _ []string) (*RederiveBaseResult, error) {
	home := defaultViewMembershipHome(vaultRoot)
	var result *RederiveBaseResult
	err := knowledge.WithViewMembershipLock(home, vaultRoot, func() error {
		record, loadErr := knowledge.LoadViewMembership(home, vaultRoot)
		if reconcileErr := record.ReconcileDiscoveredViewPaths(); reconcileErr != nil {
			return reconcileErr
		}
		mine := make([]string, 0, len(record.Bases[baseRelPath]))
		for name := range record.Bases[baseRelPath] {
			mine = append(mine, name)
		}
		sort.Strings(mine)
		var writeErr error
		result, writeErr = fileTranslatedBaseWithRecord(vaultRoot, baseRelPath, pb, schemaIdx, slugs, record, mine)
		if writeErr == nil && loadErr != nil {
			result.MembershipWarning = loadErr.Error()
		}
		return writeErr
	})
	return result, err
}

// schemaIndexFromSchemas builds the translator's SchemaIndex over the
// CURRENT on-disk schemas — the same declaration surface records and the
// query engine resolve against, flattened into the InferredProperty shape
// the translator consumes.
//
// The honesty-payload fields of InferredProperty (Ambiguity, NameEvidenced,
// …) stay nil: they are accounts of an INFERENCE decision, and this
// constructor reads a DECIDED schema, where no such decision was made. The
// translator never reads them; it reads Name/Type/Many/Required/To/
// EnumValues, all of which a loaded schema carries.
func schemaIndexFromSchemas(set *records.SchemaSet) *SchemaIndex {
	idx := &SchemaIndex{byType: map[string]map[string]InferredProperty{}}
	for _, typeName := range set.Types() {
		sc, ok := set.Get(typeName)
		if !ok {
			continue
		}
		m := map[string]InferredProperty{}
		for _, name := range sc.PropertyOrder {
			p, ok := sc.Property(name)
			if !ok || p == nil {
				continue
			}
			m[name] = InferredProperty{
				Name:       name,
				Type:       p.Type,
				Many:       p.Many,
				Required:   p.Required,
				To:         p.To,
				EnumValues: enumValuesOf(p),
			}
		}
		idx.byType[typeName] = m
	}
	return idx
}

// enumValuesOf renders a declared enum property's closed set in the
// InferredProperty shape, nil for every other type — matching what InferSchema
// leaves on a non-enum property, so the translator's enum-literal checks see
// the same shape from a decided schema as from an inferred one.
func enumValuesOf(p *records.Property) []string {
	if p.Type != records.TypeEnum {
		return nil
	}
	values := make([]string, 0, len(p.Values))
	for _, v := range p.Values {
		values = append(values, v.Name)
	}
	return values
}
