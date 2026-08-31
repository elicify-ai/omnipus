// Omnipus — the importer's orchestrator: scan, infer, write, reload through
// the REAL loaders, validate, report. This is the one entry point
// cmd/omnipus/internal/records calls (FR-100: operator/CLI one-shot).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// schemaFilePerm/viewFilePerm match the marker directory's own owner-only
// posture (pkg/knowledge/marker.go's markerFilePerm) — this is Omnipus
// control-plane state living inside the operator's own vault folder.
const generatedFilePerm = 0o600

// Run performs the full import: scans the vault, infers record-type schemas
// from observed frontmatter, translates every `.base` file into saved
// views, writes both under <root>/.omnipus-vault/, then reloads everything
// through records.LoadSchemas/records.LoadViews/records.Validate — the
// SAME primitives the running product uses — to prove the written files are
// not merely well-formed but actually load and validate real notes.
//
// When write is false, nothing is written to disk; the report still reflects
// what WOULD have been written (a dry run).
func Run(vaultRoot string, write bool) (*Report, error) {
	inv, err := ScanVault(vaultRoot)
	if err != nil {
		return nil, err
	}

	notes, loadProblems, err := LoadNotes(inv)
	if err != nil {
		return nil, err
	}

	disc := CheckTypeDiscriminator(notes)
	groups := CollectTypeGroups(notes)
	nameIdx := BuildNameIndex(notes)

	inferred := map[string][]InferredProperty{}
	var typeSummaries []TypeSchemaSummary
	var ambiguities []AmbiguousInference
	var relationSplits []RelationSplitReport

	typeNames := sortedGroupKeys(groups)
	for _, t := range typeNames {
		g := groups[t]
		props := InferSchema(g, nameIdx)
		inferred[t] = props

		summary := TypeSchemaSummary{Type: t, NoteCount: g.NoteCount, PropertyCount: len(props)}
		for _, p := range props {
			if p.Required {
				summary.RequiredCount++
			}
			if p.Many {
				summary.ManyCount++
			}
			switch p.Type {
			case records.TypeRelation, records.TypePerson:
				summary.RelationCount++
			case records.TypeEnum:
				summary.EnumCount++
			case records.TypeDate:
				summary.DateCount++
			case records.TypeInteger:
				summary.IntegerCount++
			case records.TypeDecimal:
				summary.DecimalCount++
			case records.TypeText:
				summary.TextCount++
			}
			if p.Ambiguity != nil {
				ambiguities = append(ambiguities, *p.Ambiguity)
			}
			if p.RelationSplit != nil {
				relationSplits = append(relationSplits, *p.RelationSplit)
			}
		}
		typeSummaries = append(typeSummaries, summary)
	}

	// FR-104b (founder ruling): untyped notes are not left stranded. This
	// runs AFTER every schema is inferred — the shapes it matches against
	// are the schemas this run just produced — and BEFORE validation, so a
	// note whose `type:` was written this run is validated as the record it
	// has just become rather than reported as "not a record at all" by the
	// same run that typed it.
	typeInference := InferTypesForUntypedNotes(notes, inferred, write)

	schemaDir := records.SchemaDir(inv.Root)
	if write {
		for t, props := range inferred {
			data, renderErr := RenderSchemaYAML(t, props)
			if renderErr != nil {
				return nil, fmt.Errorf("vaultimport: rendering schema for type %q: %w", t, renderErr)
			}
			path := filepath.Join(schemaDir, t+".yaml")
			if writeErr := fileutil.WriteFileAtomic(path, data, generatedFilePerm); writeErr != nil {
				return nil, fmt.Errorf("vaultimport: writing schema %q: %w", path, writeErr)
			}
		}
	}

	// Reload through the REAL loader — proves round-trip, and gives us the
	// canonical SchemaSet records.Validate and records.LoadViews need.
	schemaSet, schemaReload, err := records.LoadSchemas(inv.Root)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: reloading schemas: %w", err)
	}

	schemaIdx := NewSchemaIndex(inferred)
	slugs := NewSlugRegistry()

	baseRelPaths := make([]string, 0, len(inv.Bases))
	baseByRel := map[string]string{} // relPath -> absPath
	for _, abs := range inv.Bases {
		rel := inv.BaseRel[abs]
		baseRelPaths = append(baseRelPaths, rel)
		baseByRel[rel] = abs
	}
	sort.Strings(baseRelPaths)

	viewsDir := records.ViewsDir(inv.Root)
	var baseOutcomes []BaseOutcome
	for _, rel := range baseRelPaths {
		abs := baseByRel[rel]
		data, readErr := knowledge.ReadNoteContent(nil, abs)
		if readErr != nil {
			baseOutcomes = append(baseOutcomes, BaseOutcome{
				BaseRelPath: rel, Status: OutcomeRefused,
				RefusedReason: fmt.Sprintf("could not read the file: %v", readErr),
			})
			continue
		}
		pb, parseErr := ParseBaseFile(data)
		if parseErr != nil {
			baseOutcomes = append(baseOutcomes, BaseOutcome{
				BaseRelPath: rel, Status: OutcomeRefused,
				RefusedReason: parseErr.Error(),
			})
			continue
		}
		outcome, produced := TranslateBase(pb, rel, schemaIdx, slugs)
		baseOutcomes = append(baseOutcomes, outcome)
		if write {
			for _, pv := range produced {
				path := filepath.Join(viewsDir, filepath.Base(pv.RelPath))
				if writeErr := fileutil.WriteFileAtomic(path, pv.Bytes, generatedFilePerm); writeErr != nil {
					return nil, fmt.Errorf("vaultimport: writing view %q: %w", path, writeErr)
				}
			}
		}
	}

	var viewReload *records.ViewLoadReport
	if write {
		_, viewReload, err = records.LoadViews(inv.Root, schemaSet)
		if err != nil {
			return nil, fmt.Errorf("vaultimport: reloading views: %w", err)
		}
	}

	recs := make([]records.Record, 0, len(notes))
	for _, n := range notes {
		recs = append(recs, n.Rec)
	}
	valReport := records.Validate(schemaSet, recs, records.ValidateOptions{ReportUndeclaredProperties: true})

	// The discriminator check ran BEFORE FR-104b wrote anything, so its
	// counts describe the vault as it ARRIVED. The validation summary must
	// describe the vault as it now IS, or the report contradicts the files
	// this run wrote — so the typed/untyped split is re-derived here rather
	// than carried over.
	postDisc := CheckTypeDiscriminator(notes)
	vs := ValidationSummary{
		TotalNotes:       postDisc.TotalNotes,
		NotesWithoutType: postDisc.WithoutType,
		NotesWithType:    postDisc.WithType,
		NotRecordsAtAll:  postDisc.WithoutType,
	}
	for _, rr := range valReport.Records {
		if !rr.Recognised {
			continue
		}
		vs.RecognisedRecords++
		if rr.Valid() {
			vs.ValidRecords++
		} else {
			vs.InvalidRecords++
			if len(vs.InvalidExamples) < 10 {
				vs.InvalidExamples = append(vs.InvalidExamples, rr.Path)
			}
		}
	}
	for _, f := range valReport.Findings() {
		if f.Severity == records.SeverityError {
			vs.ErrorFindingCount++
		} else {
			vs.WarningFindingCount++
		}
	}

	return &Report{
		VaultRoot:      inv.Root,
		Discriminator:  disc,
		LoadProblems:   loadProblems,
		Types:          typeSummaries,
		Ambiguities:    ambiguities,
		RelationSplits: relationSplits,
		Bases:          baseOutcomes,
		TypeInference:  typeInference,
		SchemaReload:   schemaReload,
		ViewReload:     viewReload,
		Validation:     vs,
	}, nil
}
