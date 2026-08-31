// Omnipus — the importer's orchestrator: scan, infer, write, reload through
// the REAL loaders, validate, report. This is the one entry point
// cmd/omnipus/internal/records calls (FR-100: operator/CLI one-shot).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// schemaFilePerm/viewFilePerm match the marker directory's own owner-only
// posture (pkg/knowledge/marker.go's markerFilePerm) — this is Omnipus
// control-plane state living inside the operator's own vault folder.
const generatedFilePerm = 0o600

// ---------------------------------------------------------------------------
// A NOTE'S `type:` BECOMES A FILE NAME, SO IT IS VALIDATED FIRST
//
// records.Record.TypeName() returns the trimmed raw frontmatter scalar with no
// grammar check of any kind — the discriminator is whatever the operator wrote.
// This package then builds `<schemaDir>/<type>.yaml` out of it, and
// fileutil.WriteFileAtomic creates intermediate directories, so a note carrying
//
//	type: "../../../pwned"
//
// wrote a `.yaml` file OUTSIDE the vault whose content was partly derived from
// that same note's frontmatter. The view writer was safe only by accident (its
// slug function strips everything outside [a-z0-9]); the schema writer had no
// such guard.
//
// The fix is not sanitisation. A type name is an IDENTIFIER — it is compared,
// grouped and written as a file name — so a value that is not one is REFUSED by
// name and reported, never quietly rewritten into a different type than the
// note declares. Rewriting would silently merge two distinct types, or invent a
// type no note actually carries.
// ---------------------------------------------------------------------------

// maxRecordTypeNameLen bounds the name so it stays a usable file name on every
// filesystem this product runs on (the strictest common limit is 255 bytes for
// one path component, and `.yaml` costs five of them).
const maxRecordTypeNameLen = 100

// reRecordTypeName is the grammar a record type must match to become a file
// name: ASCII letters, digits, `-` and `_`, starting with a letter or digit.
// It admits every type the founder's vault actually uses (`task`,
// `legal-entity`, `brand-kit`) and no path component that could escape a
// directory, hide as a dotfile, or collide with a shell glob.
var reRecordTypeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// validRecordTypeName reports whether a note's declared `type:` may be used as
// a record type at all, returning the trimmed name when it may. The second
// return is FALSE for every value that is not a legal identifier — including
// the empty string, anything containing a path separator, `.`/`..`, and
// anything long enough to be a filesystem problem.
func validRecordTypeName(raw string) (string, bool) {
	t := strings.TrimSpace(raw)
	if t == "" || len(t) > maxRecordTypeNameLen {
		return "", false
	}
	if !reRecordTypeName.MatchString(t) {
		return "", false
	}
	return t, true
}

// RejectedType is one declared `type:` this importer refused to treat as a
// record type, with the notes that carry it named so an operator can find and
// fix them. It is REPORTED rather than silently skipped: a note dropped without
// a word is exactly the silence this package exists to remove.
type RejectedType struct {
	// Type is the offending value, verbatim as the note wrote it.
	Type string
	// Reason says what is wrong with it.
	Reason string
	// NotePaths are the vault-relative notes carrying it, capped for the
	// report.
	NotePaths []string
}

// maxRejectedTypeExamples caps how many note paths one rejection names.
const maxRejectedTypeExamples = 10

// partitionTypeGroups splits the collected type groups into the ones this
// importer may write a schema for and the ones it refuses by name.
func partitionTypeGroups(groups map[string]*TypeGroup) (kept map[string]*TypeGroup, rejected []RejectedType) {
	kept = make(map[string]*TypeGroup, len(groups))
	for t, g := range groups {
		if _, ok := validRecordTypeName(t); ok {
			kept[t] = g
			continue
		}
		paths := g.NotePaths
		if len(paths) > maxRejectedTypeExamples {
			paths = paths[:maxRejectedTypeExamples]
		}
		rejected = append(rejected, RejectedType{
			Type: t,
			Reason: fmt.Sprintf(
				"a record type becomes a file name (%s/<type>.yaml), so it must be letters, digits, `-` and `_`, starting with a letter or digit, and at most %d characters. No schema was written and these notes are left untyped as far as this run is concerned — rename the `type:` value in each note, then re-run the import",
				records.VaultMarkerDirName+"/"+records.RecordsDirName, maxRecordTypeNameLen),
			NotePaths: paths,
		})
	}
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Type < rejected[j].Type })
	return kept, rejected
}

// schemaSetFromRendered renders the inferred schemas into a THROWAWAY
// directory and loads them back through records.LoadSchemas.
//
// It exists for the dry run (FR-100's `--dry-run`), which previously called
// records.LoadSchemas on the vault itself — reading whatever was already on
// disk, which on a first import is nothing at all. The report then printed "0
// notes carry a `type:` this run recognised as a schema" under the heading
// "against the schemas just written", about schemas it had not written. Staging
// them in a temp directory keeps the REAL loader in the loop (this package
// never builds a SchemaSet by hand) while touching nothing in the operator's
// vault.
func schemaSetFromRendered(inferred map[string][]InferredProperty) (*records.SchemaSet, *records.SchemaLoadReport, error) {
	stage, err := os.MkdirTemp("", "omnipus-import-dryrun-")
	if err != nil {
		return nil, nil, fmt.Errorf("vaultimport: staging a dry-run schema directory: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := writeSchemas(stage, inferred); err != nil {
		return nil, nil, err
	}
	return records.LoadSchemas(stage)
}

// writeSchemas renders and writes every inferred schema under root's marker
// directory. It is the ONE place a schema file is created, so the type-name
// guard above cannot be bypassed by a second writer.
func writeSchemas(root string, inferred map[string][]InferredProperty) error {
	schemaDir := records.SchemaDir(root)
	for _, t := range sortedInferredKeys(inferred) {
		name, ok := validRecordTypeName(t)
		if !ok {
			// Unreachable via Run (partitionTypeGroups filters first) and
			// deliberately a hard error rather than a skip: reaching it means a
			// second path into this function grew that did not partition, and
			// the honest answer is to stop rather than to write the file.
			return fmt.Errorf("vaultimport: refusing to write a schema for record type %q — it is not a legal type name", t)
		}
		data, err := RenderSchemaYAML(name, inferred[t])
		if err != nil {
			return fmt.Errorf("vaultimport: rendering schema for type %q: %w", name, err)
		}
		path := filepath.Join(schemaDir, name+".yaml")
		if err := fileutil.WriteFileAtomic(path, data, generatedFilePerm); err != nil {
			return fmt.Errorf("vaultimport: writing schema %q: %w", path, err)
		}
	}
	return nil
}

func sortedInferredKeys(m map[string][]InferredProperty) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Run performs the full import: scans the vault, infers record-type schemas
// from observed frontmatter, translates every `.base` file into saved
// views, writes both under <root>/.omnipus-vault/, then reloads everything
// through records.LoadSchemas/records.LoadViews/records.Validate — the
// SAME primitives the running product uses — to prove the written files are
// not merely well-formed but actually load and validate real notes.
//
// When write is false, nothing is written to the vault; the report still
// reflects what WOULD have been written (a dry run), and the schemas and views
// are staged in a temp directory so the reload and the validation run against
// the files this run produced rather than against whatever the vault already
// held.
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
	groups, rejectedTypes := partitionTypeGroups(CollectTypeGroups(notes))
	nameIdx := BuildNameIndex(notes)

	inferred := map[string][]InferredProperty{}
	var typeSummaries []TypeSchemaSummary
	var ambiguities []AmbiguousInference
	var relationSplits []RelationSplitReport
	var aritySplits []AritySplitReport

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
			case records.TypeCheckbox:
				summary.CheckboxCount++
			case records.TypeText:
				summary.TextCount++
			}
			if p.Ambiguity != nil {
				ambiguities = append(ambiguities, *p.Ambiguity)
			}
			if p.RelationSplit != nil {
				relationSplits = append(relationSplits, *p.RelationSplit)
			}
			if p.AritySplit != nil {
				aritySplits = append(aritySplits, *p.AritySplit)
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

	if write {
		if writeErr := writeSchemas(inv.Root, inferred); writeErr != nil {
			return nil, writeErr
		}
	}

	// Reload through the REAL loader — proves round-trip, and gives us the
	// canonical SchemaSet records.Validate and records.LoadViews need. On a dry
	// run the same loader reads a staged copy, so the validation below is
	// against the schemas this run produced either way (see
	// schemaSetFromRendered).
	var schemaSet *records.SchemaSet
	var schemaReload *records.SchemaLoadReport
	if write {
		schemaSet, schemaReload, err = records.LoadSchemas(inv.Root)
	} else {
		schemaSet, schemaReload, err = schemaSetFromRendered(inferred)
	}
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

	var baseOutcomes []BaseOutcome
	var allProduced []ProducedView
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
		allProduced = append(allProduced, produced...)
	}

	viewRoot := inv.Root
	if !write {
		stage, stageErr := os.MkdirTemp("", "omnipus-import-dryrun-views-")
		if stageErr != nil {
			return nil, fmt.Errorf("vaultimport: staging a dry-run views directory: %w", stageErr)
		}
		defer os.RemoveAll(stage)
		viewRoot = stage
	}
	viewsDir := records.ViewsDir(viewRoot)
	for _, pv := range allProduced {
		path := filepath.Join(viewsDir, filepath.Base(pv.RelPath))
		if writeErr := fileutil.WriteFileAtomic(path, pv.Bytes, generatedFilePerm); writeErr != nil {
			return nil, fmt.Errorf("vaultimport: writing view %q: %w", path, writeErr)
		}
	}
	_, viewReload, err := records.LoadViews(viewRoot, schemaSet)
	if err != nil {
		return nil, fmt.Errorf("vaultimport: reloading views: %w", err)
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
		DryRun:           !write,
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
		DryRun:         !write,
		Discriminator:  disc,
		LoadProblems:   loadProblems,
		RejectedTypes:  rejectedTypes,
		Types:          typeSummaries,
		Ambiguities:    ambiguities,
		RelationSplits: relationSplits,
		AritySplits:    aritySplits,
		Bases:          baseOutcomes,
		TypeInference:  typeInference,
		SchemaReload:   schemaReload,
		ViewReload:     viewReload,
		Validation:     vs,
	}, nil
}
