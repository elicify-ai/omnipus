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
func schemaSetFromRendered(inferred map[string][]InferredProperty, provisioned map[string]ProvisionedType) (*records.SchemaSet, *records.SchemaLoadReport, error) {
	stage, err := os.MkdirTemp("", "omnipus-import-dryrun-")
	if err != nil {
		return nil, nil, fmt.Errorf("vaultimport: staging a dry-run schema directory: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := writeSchemas(stage, inferred, provisioned); err != nil {
		return nil, nil, err
	}
	return records.LoadSchemas(stage)
}

// sortedBaseRelPaths lists every `.base` file's vault-relative path in a
// stable order, with the absolute path each one resolves to.
func sortedBaseRelPaths(inv *Inventory) (relPaths []string, byRel map[string]string) {
	relPaths = make([]string, 0, len(inv.Bases))
	byRel = map[string]string{}
	for _, abs := range inv.Bases {
		rel := inv.BaseRel[abs]
		relPaths = append(relPaths, rel)
		byRel[rel] = abs
	}
	sort.Strings(relPaths)
	return relPaths, byRel
}

// parseAllBases reads and parses every `.base` file ONCE, returning the parsed
// files and, separately, the refusal outcome for each one that could not be
// read or parsed. Splitting the failures out keeps the translation loop below
// a pure walk over already-parsed input, and keeps a file that cannot be
// parsed from being opened a second time just to fail again.
func parseAllBases(relPaths []string, byRel map[string]string) (map[string]*ParsedBase, map[string]BaseOutcome) {
	parsed := make(map[string]*ParsedBase, len(relPaths))
	failed := map[string]BaseOutcome{}
	for _, rel := range relPaths {
		data, readErr := knowledge.ReadNoteContent(nil, byRel[rel])
		if readErr != nil {
			failed[rel] = BaseOutcome{
				BaseRelPath: rel, Status: OutcomeRefused,
				RefusedReason: fmt.Sprintf("could not read the file: %v", readErr),
			}
			continue
		}
		pb, parseErr := ParseBaseFile(data)
		if parseErr != nil {
			failed[rel] = BaseOutcome{
				BaseRelPath: rel, Status: OutcomeRefused,
				RefusedReason: parseErr.Error(),
			}
			continue
		}
		parsed[rel] = pb
	}
	return parsed, failed
}

// writeSchemas renders and writes every inferred schema under root's marker
// directory. It is the ONE place a schema file is created, so the type-name
// guard above cannot be bypassed by a second writer.
func writeSchemas(root string, inferred map[string][]InferredProperty, provisioned map[string]ProvisionedType) error {
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
		var data []byte
		var err error
		if p, isProvisioned := provisioned[t]; isProvisioned {
			// A schema declared from a `.base` file and no note carries its
			// own account IN THE FILE — the operator reads it exactly where
			// they would go to change it.
			data, err = RenderProvisionedSchemaYAML(name, inferred[t], p)
		} else {
			data, err = RenderSchemaYAML(name, inferred[t])
		}
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

	// Every property typed from its NAME because the vault held no value for
	// it anywhere. It is asked for HERE, straight after inference and before
	// provisioning adds any type of its own, so the list describes exactly the
	// decisions the inference pass made. A guess this run made and did not
	// print is a guess the operator cannot correct, which is the whole reason
	// the inference pass records it.
	nameEvidenced := CollectNameEvidencedInferences(inferred)

	// FR-104b (founder ruling): untyped notes are not left stranded. This
	// runs AFTER every schema is inferred — the shapes it matches against
	// are the schemas this run just produced — and BEFORE validation, so a
	// note whose `type:` was written this run is validated as the record it
	// has just become rather than reported as "not a record at all" by the
	// same run that typed it.
	typeInference := InferTypesForUntypedNotes(notes, inferred, write)

	// The `.base` files are read and parsed HERE, before a single schema is
	// written, and the parse results are carried into the translation loop
	// below rather than the files being opened twice. The ordering is the
	// point: FR-018d provisioning needs to see what the bases declare while
	// the schema set is still being decided, because a type declared by a
	// base and by no note has to be IN that set before writeSchemas runs and
	// before records.LoadSchemas reads it back.
	baseRelPaths, baseByRel := sortedBaseRelPaths(inv)
	parsedBases, baseReadOutcomes := parseAllBases(baseRelPaths, baseByRel)

	provisioned := provisionTypesFromBases(baseRelPaths, parsedBases, inferred, notes)
	provisionedByType := map[string]ProvisionedType{}
	for _, p := range provisioned {
		provisionedByType[p.Type] = p
		inferred[p.Type] = provisionedProperties(p)
	}

	// An inferred enum's closed set is what the NOTES happen to hold, and the
	// operator never declared it closed — this package did, by sampling. A
	// base filtering on a value no note carries is the operator saying the
	// value is legal, so it is admitted here.
	//
	// The position is the point, and it is the same one provisioning occupies:
	// BEFORE writeSchemas and before NewSchemaIndex. Widening only the index
	// would admit the clause at translation time and leave the written schema
	// refusing the same value at query time — the view would translate
	// cleanly and then match nothing, which is worse than the loss it
	// replaced. See infer.go's header for the argument and its three
	// containment clauses.
	// The account of each one is stored ON the widened property
	// (InferredProperty.EnumWidened), so the report asks this package for its
	// own decisions with CollectEnumWidenings(inferred) — the same shape
	// CollectNameEvidencedInferences already has — rather than this function
	// threading a second list through.
	// A `.base` formula wrapping a bare property name in `date()` is the
	// operator saying that property IS a date — the same base-file-as-evidence
	// move as FR-018d provisioning and the enum widening below, one level
	// down. It runs BEFORE the widening because both read `inferred`, and a
	// property's TYPE has to settle before a literal is judged against it.
	//
	// It takes `notes` rather than a count captured earlier ON PURPOSE:
	// InferredProperty.ObservedCount is frozen by CollectTypeGroups above,
	// BEFORE InferTypesForUntypedNotes writes `type:` into untyped notes. A
	// note that JOINS a record type mid-run is invisible to that count, and
	// promoting its property text->date on stale evidence could invalidate the
	// very note this run just typed — the one bar this package admits no
	// exception to.
	TypePropertiesFromBaseFormulas(inferred, notes, baseRelPaths, parsedBases)

	// The number half of the same evidence class: a view that totals a
	// property is its operator stating the property holds a number. Runs
	// AFTER the formula rule and before enum widening, in the same window and
	// for the same reason — both read a `.base` file as a statement about a
	// schema, and both are contained by "data beats a base file", so neither
	// can speak about a property the notes have already decided.
	TypePropertiesFromBaseSummaries(inferred, notes, baseRelPaths, parsedBases)
	// A record type that observed NOTHING for a property is standing on this
	// package's `text` fallback, not on a reading of its own data — and that
	// fallback used to stand as an equal partner to seven hundred observations
	// of the same property name on other types, splitting the untyped-query
	// domain and costing the founder's Inbox-Triage base its `created` column
	// and its `formula.age`. Absence of evidence yields to evidence here, on
	// the same sentence the two rules above already turn on: data beats a base
	// file, as data beats a name.
	//
	// The position is between the two for a reason. AFTER the formula rule,
	// because a property whose type the operator's own formula states is not
	// an unobserved fallback and must settle first. BEFORE the widening,
	// because an adopted enum is an enum like any other and its closed set has
	// to be offered the operator's own base literals in the same pass every
	// other enum's is.
	//
	// The declined half is not thrown away: `status` is refused here on this
	// vault, and both halves are reported in the schema files the operator
	// would open to correct them (schema_write.go).
	// The two accounts are stored ON the properties themselves
	// (InferredProperty.DomainAdopted / .DomainAdoptionDeclined) and rendered
	// into the very schema file each one would be corrected in — see
	// schema_write.go's propertyAccountComment. The returned slices are the same
	// decisions in list form, for the tests that grade this rule; a REPORT row
	// for them is owed and belongs in report.go, which this change does not own.
	_, _ = AdoptObservedDomains(inferred, notes)

	WidenEnumsFromBases(inferred, baseRelPaths, parsedBases)

	if write {
		if writeErr := writeSchemas(inv.Root, inferred, provisionedByType); writeErr != nil {
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
		schemaSet, schemaReload, err = schemaSetFromRendered(inferred, provisionedByType)
	}
	if err != nil {
		return nil, fmt.Errorf("vaultimport: reloading schemas: %w", err)
	}

	schemaIdx := NewSchemaIndex(inferred)
	slugs := NewSlugRegistry()

	var baseOutcomes []BaseOutcome
	var allProduced []ProducedView
	for _, rel := range baseRelPaths {
		if bad, failed := baseReadOutcomes[rel]; failed {
			baseOutcomes = append(baseOutcomes, bad)
			continue
		}
		outcome, produced := TranslateBase(parsedBases[rel], rel, schemaIdx, slugs)
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
		VaultRoot:     inv.Root,
		DryRun:        !write,
		Discriminator: disc,
		LoadProblems:  loadProblems,
		RejectedTypes: rejectedTypes,
		NameEvidenced: nameEvidenced,
		// Asked for HERE rather than captured from WidenEnumsFromBases's
		// return value, deliberately. Every account is stored on the property
		// it belongs to, so this reads the same source the written schema was
		// built from — a widening that reached the schema and not this list
		// is not expressible.
		EnumWidenings:    CollectEnumWidenings(inferred),
		FormulaEvidenced: CollectFormulaEvidencedTypes(inferred),
		Provisioned:      provisioned,
		Types:            typeSummaries,
		Ambiguities:      ambiguities,
		RelationSplits:   relationSplits,
		AritySplits:      aritySplits,
		Bases:            baseOutcomes,
		TypeInference:    typeInference,
		SchemaReload:     schemaReload,
		ViewReload:       viewReload,
		Validation:       vs,
	}, nil
}

// ---------------------------------------------------------------------------
// FR-018d PROVISIONING — DECLARING A RECORD TYPE FROM ITS `.base` FILE ALONE
//
// The importer used to REFUSE, outright, every view naming a record type no
// note in the vault carried. On the founder's own vault that was four `.base`
// files dead and twelve views gone, and the reason given was true but useless:
// "no note carries `type: compliance`". The operator's answer to that is "yes,
// I know — I wrote the Compliance base FIRST, the notes come after", and there
// was nothing in the report telling them what to do about it.
//
// The reasoning that produced the refusal stopped one step short. A schema was
// missing because schema inference reads NOTES, and there were none. But the
// `.base` file is itself a statement by the operator, in their own hand, that
// this record type exists and that these are the properties they file, filter
// and display it by. It is EVIDENCE — just evidence of a different kind from a
// note's frontmatter. The view loader's own comment already draws the line
// this code now moves the vault across (pkg/records/view.go, above
// RejectViewUnknownType):
//
//	"A declared type holding ZERO records is a valid, empty view (FR-018d) ...
//	 RejectViewUnknownType below still fires for a type NO schema declares:
//	 that is drift, not provisioning."
//
// It was drift only because nothing had declared the type. This declares it,
// so it is provisioning, and the loader's own rule then admits it.
//
// WHAT IS ASSUMED, AND WHY EACH ASSUMPTION IS SAFE IN THE FR-105 DIRECTION
//
// A property's TYPE cannot be inferred from a base file — there are no values
// to look at. So none is inferred. Every provisioned property is declared
// `text`, not required, not many, which is the type system's own unconstrained
// floor: `TypeText` is "prose. Never validated for shape" (pkg/records/
// schema.go), so a text declaration cannot REJECT a value the operator later
// writes. That is the answer to the strongest objection against doing this at
// all — that a wrong type guess produces a schema which rejects the first real
// note. A text declaration rejects nothing.
//
// It is NOT, however, safe in every filter position, and the unsafe ones are
// left UNDECLARED rather than declared and hoped for:
//
//   - AN ORDERING COMPARISON (`<`, `<=`, `>`, `>=`). `operatorDefinedForType`
//     permits all four on text, LEXICALLY. `amount > 100` over text answers
//     TRUE for "50". That is BROADENING — the one thing FR-105 forbids — and
//     it is broadening in the TRANSLATION, deterministic and independent of
//     any future note. So the property is not declared, the clause becomes a
//     named `[filter]` loss, and FR-105 disables the view. Fewer rows, named.
//
//   - `prop.contains("x")`. On a text property this importer emits
//     `LIKE '%x%'`, which is substring matching; if the operator's property is
//     really a list, Obsidian's `.contains` is whole-ELEMENT membership, and
//     substring matching is strictly broader (view_find_bridge.go's header
//     spells out this exact widening as the reason the flat view format was
//     withdrawn). Same treatment.
//
// Everything else the base does with a property — equality, inequality, the
// `!= ""` / `== ""` idioms, a bare truthy/falsy test, and the purely display
// positions (`order:`, `groupBy:`, `sort:`, `summaries:`) — either translates
// faithfully under a text declaration or is already refused by
// buildV2LeafNode on its own terms, with a named loss and, where it decides
// rows, a disabled view. Nothing here can widen a row set.
//
// WHAT IS NEVER INVENTED
//
// Only property names the `.base` file itself writes. No name from a sibling
// type, no name guessed from the type's name, no `created`/`updated`/`tags`
// scaffolding. If the base names no usable property at all the type is NOT
// provisioned — a zero-property schema is refused by the loader
// (RejectNoProperties) and would take the whole import down with it.
//
// A type that real notes DO carry is never provisioned: observed frontmatter
// always wins over a base file's word for it.
// ---------------------------------------------------------------------------

// ProvisionedType is one record type this run DECLARED from a `.base` file
// because no note in the vault carries it. It is the honesty payload for a
// declaration made with no note behind it: what was assumed, what was
// deliberately not assumed, and the one edit that corrects each.
type ProvisionedType struct {
	// Type is the record type name, as the base's `type == "..."` wrote it.
	Type string
	// Bases are the vault-relative `.base` files that named it, sorted.
	Bases []string
	// Properties are the property names declared, sorted. Every one is
	// declared `text`, not required, not many.
	Properties []string
	// Omitted is every property the base referenced that was deliberately
	// NOT declared, with the reason. Each of these becomes a named loss in
	// the translated view.
	Omitted []ProvisionedOmission
	// Templates are the `type: template` notes that named this record type,
	// vault-relative and sorted. They are the reason some of Properties is
	// declared at all, so the report can credit the operator's own template
	// rather than letting a `.base` file take the credit for it.
	Templates []string
}

// ProvisionedOmission is one property a base referenced that provisioning
// refused to declare, and why.
type ProvisionedOmission struct {
	Property string
	Reason   string
}

// ReportLines renders the whole account of one provisioned type, first line
// first. It lives here rather than in the report renderer because the same
// text is also written into the generated schema file's own header, and two
// spellings of one account is how they drift apart.
func (p ProvisionedType) ReportLines() []string {
	lines := make([]string, 0, 3+len(p.Omitted))
	lines = append(lines, fmt.Sprintf(
		"%s: DECLARED FROM %s — no note in the vault carries `type: %s`, so its %d propert%s below %s assumed `text` (not required, not a list). Nothing here was observed; it is the base file's own word for what this type is.",
		p.Type, strings.Join(p.Bases, ", "), p.Type,
		len(p.Properties), plural(len(p.Properties), "y", "ies"), plural(len(p.Properties), "is", "are")))
	lines = append(lines, "assumed `text`: "+strings.Join(p.Properties, ", "))
	lines = append(lines,
		fmt.Sprintf("correct any one of them in a single edit: knowledge_configure set schema %s property <name> type=<date|integer|decimal|enum|checkbox|relation|person> [many=true] [required=true]", p.Type))
	if len(p.Templates) > 0 {
		lines = append(lines, fmt.Sprintf(
			"some of those names come from the operator's own template, not from a base file: %s declare%s properties of `%s` directly, and a template is a statement that the property EXISTS — which is a different claim from anything a base file's use of it can make.",
			strings.Join(p.Templates, ", "), plural(len(p.Templates), "s", ""), p.Type))
	}
	for _, o := range p.Omitted {
		lines = append(lines, fmt.Sprintf("NOT declared — %s: %s", o.Property, o.Reason))
	}
	return lines
}

// provisionedPropertyType is the declaration every provisioned property gets.
// It is a named constant because the whole safety argument above rests on it
// being the type the validator never checks a value against.
const provisionedPropertyType = records.TypeText

// reProvisionablePropertyName is the property-name grammar provisioning will
// take from a base file. It is deliberately the same shape leaf.go's own
// patterns already enforce for a filter's left-hand side, so a name that
// reaches here from a display position (`order:`, where the base file is free
// to write anything at all) is held to the same standard as one that reached
// here from a filter.
var reProvisionablePropertyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// provisionUse is one place a base file referenced a property, reduced to the
// only question provisioning asks: may a `text` declaration carry it?
type provisionUse struct {
	property string
	// unsafeReason is non-empty when a text declaration would translate this
	// use into something that can return MORE rows than the Obsidian
	// original. The property is then not declared at all.
	unsafeReason string
}

// collectV2Leaves walks one translated filter tree and returns every leaf that
// names a property. A lost subtree carries no leaf (it is reported verbatim as
// a loss by the translator, which is the right place for it) and a prebuilt
// file-method node names no record property, so both are skipped.
func collectV2Leaves(n *rawNode) []v2Leaf {
	if n == nil {
		return nil
	}
	var out []v2Leaf
	if n.Kind == rawKindLeaf {
		out = append(out, n.Leaf)
	}
	for _, k := range n.Kids {
		out = append(out, collectV2Leaves(k)...)
	}
	return out
}

// provisionUsesFromLeaves classifies every filter leaf of one subtree.
func provisionUsesFromLeaves(leaves []v2Leaf) []provisionUse {
	uses := make([]provisionUse, 0, len(leaves))
	for _, l := range leaves {
		u := provisionUse{property: l.Property}
		switch {
		case l.Shape == shapeContains:
			u.unsafeReason = fmt.Sprintf(
				"the base matches it with `%s.contains(...)`. On a list that is whole-element membership; on text this importer must emit `LIKE '%%...%%'`, which is substring matching and returns MORE rows. With no note to say which it is, declaring it `text` would broaden the view (FR-105), so the clause is dropped and named instead",
				l.Property)
		case l.Shape == shapeCompare && isOrderingOp(l.Op):
			u.unsafeReason = fmt.Sprintf(
				"the base compares it with `%s`. Text compares lexically, so `50 > 100` would answer TRUE and the view would return MORE rows than Obsidian's (FR-105). Declaring a type here would be a guess with nothing behind it, so the clause is dropped and named instead",
				string(l.Op))
		}
		uses = append(uses, u)
	}
	return uses
}

// provisionUsesFromDisplay collects the property names a view's display
// positions name. A display position cannot decide a row set, so no use found
// here is ever unsafe.
func provisionUsesFromDisplay(vraw map[string]any) []provisionUse {
	var uses []provisionUse
	add := func(name string) {
		if s := strings.TrimSpace(name); s != "" {
			uses = append(uses, provisionUse{property: s})
		}
	}
	if ord, ok := vraw["order"].([]any); ok {
		for _, o := range ord {
			add(stringOf(o))
		}
	}
	if gb, ok := vraw["groupBy"].(map[string]any); ok {
		add(stringOf(gb["property"]))
	}
	if srt, ok := vraw["sort"].([]any); ok {
		for _, s := range srt {
			if sm, ok := s.(map[string]any); ok {
				add(stringOf(sm["property"]))
			}
		}
	}
	if summ, ok := vraw["summaries"].(map[string]any); ok {
		for _, k := range sortedKeys(summ) {
			op, known := aggregateOpFor(strings.TrimSpace(stringOf(summ[k])))
			if known && nonProvisionableAggregates[string(op)] {
				// A NUMERIC summary is the one display position that is
				// evidence about a property's TYPE rather than just its name:
				// the operator asking for a sum is the operator saying this is
				// not prose.
				//
				// THE STATED REASON HERE USED TO BE WRONG ABOUT THIS ENGINE,
				// and the wrong version was the more frightening one, which is
				// why it went unchallenged. It said a `text` declaration
				// "would carry a summary that silently computes nonsense".
				// Nothing in this system computes silently: knowledgefind
				// refuses an op its property's type does not define, loudly and
				// by name (FR-155). The real consequence is worse in a
				// different direction — that refusal aborts the WHOLE find
				// request rather than just the total, so one undefined summary
				// makes every row of the view unreachable.
				//
				// That is now handled where it belongs. view_write.go's
				// summaryDefinedForType asks knowledgefind's own table before
				// writing a summary and drops the ones it would refuse, so this
				// omission no longer has to protect against it. What remains
				// here is the narrower, honest claim: a numeric summary is
				// TYPE evidence, and this package will not declare a type on
				// the strength of it. (Either way the view keeps every row —
				// loss.go classifies `aggregates` as an annotation.)
				uses = append(uses, provisionUse{property: k, unsafeReason: fmt.Sprintf(
					"the base asks for %s(%s), which %s — the request is itself the operator's statement that this property is not text, and this package will not pick a numeric type on the strength of a base file alone",
					string(op), k, aggregateOmissionSentinel)})
				continue
			}
			add(k)
		}
	}
	return uses
}

// nonProvisionableAggregates are the summary functions that cannot be computed
// over prose. `count`, `min`, `max`, `empty`, `filled` and `unique` are absent
// DELIBERATELY: each is well defined over text, so none of them says anything
// that contradicts a `text` declaration.
var nonProvisionableAggregates = map[string]bool{
	"sum": true, "avg": true, "median": true, "stddev": true, "range": true,
	"earliest": true, "latest": true, "checked": true, "unchecked": true,
}

// provisionAccumulator gathers one prospective type's evidence across every
// base and view that named it.
type provisionAccumulator struct {
	bases    map[string]struct{}
	declare  map[string]struct{}
	omit     map[string]string
	viewSeen bool
	// templates are the `type: template` notes that named this record type.
	// Their evidence is about EXISTENCE and it is filed by its own door —
	// see recordTemplate.
	templates map[string]struct{}
}

// templateEvidence is what the operator's own `type: template` notes say about
// one record type: which properties a note of that type carries, and which
// template files said so.
type templateEvidence struct {
	// Properties are the donated property names, sorted.
	Properties []string
	// Notes are the vault-relative template files, sorted. Reported, never
	// inferred from.
	Notes []string
}

// templateEvidenceByType indexes the founder's `type: template` notes by the
// record type each one templates.
//
// It is the same reading applyTemplateDeclarations (infer.go) already does, and
// it deliberately calls that file's own helpers rather than re-deriving the
// rules — `templateTargetType` for the target and `templateDonatesKey` for the
// scaffolding-key exclusion. Two spellings of "what a template donates" is how
// the schema pass and the provisioning pass would come to disagree about the
// same note.
//
// WHY THIS INDEX HAS TO EXIST SEPARATELY AT ALL. infer.go's pass runs over
// `groups`, which holds only record types some note carries, and it declines —
// correctly, and by explicit design — to invent a type. So a template for a
// type that only a `.base` file declares is skipped there, and until now was
// read by nobody: `Template — invoice.md` listed `amount:` in the founder's own
// hand and the importer dropped the `amount` column anyway.
func templateEvidenceByType(notes []NoteRecord) map[string]templateEvidence {
	props := map[string]map[string]struct{}{}
	files := map[string]map[string]struct{}{}
	for i := range notes {
		n := &notes[i]
		if n.Rec.TypeName() != templateRecordType {
			continue
		}
		target := templateTargetType(n.Rec)
		if target == "" || target == templateRecordType {
			continue
		}
		if props[target] == nil {
			props[target] = map[string]struct{}{}
			files[target] = map[string]struct{}{}
		}
		files[target][n.RelPath] = struct{}{}
		for _, key := range n.Rec.Frontmatter.Keys {
			if templateDonatesKey(key) {
				props[target][key] = struct{}{}
			}
		}
	}
	out := make(map[string]templateEvidence, len(props))
	for t := range props {
		out[t] = templateEvidence{Properties: sortedSetKeys(props[t]), Notes: sortedSetKeys(files[t])}
	}
	return out
}

// provisionTypesFromBases is the whole decision: which record types named by a
// `.base` file, and declared by no note, this run will declare anyway.
//
// It resolves each view's record type with the SAME functions the translator
// uses a few lines later (TranslateFilterTree + resolveViewType) rather than a
// second reading of the base's shape — a divergence between the two would
// declare a type nothing queries, or leave one declared nowhere.
func provisionTypesFromBases(relPaths []string, parsed map[string]*ParsedBase, inferred map[string][]InferredProperty, notes []NoteRecord) []ProvisionedType {
	acc := map[string]*provisionAccumulator{}

	for _, rel := range relPaths {
		pb := parsed[rel]
		if pb == nil {
			continue
		}
		outer := TranslateFilterTree(pb.Filters)
		outerUses := provisionUsesFromLeaves(collectV2Leaves(outer.Root))

		for _, vraw := range pb.Views {
			viewTrans := TranslateFilterTree(vraw["filters"])
			rt, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
			if conflict != "" || rt == "" {
				// No single type, or an untyped (folder-scoped) view: there is
				// nothing to declare and the translator handles both.
				continue
			}
			if _, observed := inferred[rt]; observed {
				// Real notes carry it. Observation always wins.
				continue
			}
			if _, ok := validRecordTypeName(rt); !ok {
				// A type name that cannot become a file name is refused for
				// exactly the reason partitionTypeGroups refuses it above; it
				// must not sneak in through a base file instead of a note.
				continue
			}
			a := acc[rt]
			if a == nil {
				a = &provisionAccumulator{
					bases:     map[string]struct{}{},
					declare:   map[string]struct{}{},
					omit:      map[string]string{},
					templates: map[string]struct{}{},
				}
				acc[rt] = a
			}
			a.viewSeen = true
			a.bases[rel] = struct{}{}

			uses := append([]provisionUse{}, outerUses...)
			uses = append(uses, provisionUsesFromLeaves(collectV2Leaves(viewTrans.Root))...)
			uses = append(uses, provisionUsesFromDisplay(vraw)...)
			for _, u := range uses {
				a.record(u)
			}
		}
	}

	// TEMPLATE EVIDENCE GOES IN LAST, AND ONCE — after every base and every
	// view has been read.
	//
	// The ordering is load-bearing, not tidiness. A template can LIFT an
	// omission (recordTemplate), and `record` files an omission the first time
	// it sees one. Applying the lift inside the loop would let a template
	// donation land between two views and rescue a property that a LATER
	// view's ordering comparison would have refused — the exact FR-105
	// broadening this package exists to prevent, arriving through the door
	// meant to restore a column. Applied here, no use can arrive afterwards.
	templates := templateEvidenceByType(notes)
	for rt, a := range acc {
		ev, ok := templates[rt]
		if !ok {
			continue
		}
		for _, name := range ev.Properties {
			a.recordTemplate(name)
		}
		for _, rel := range ev.Notes {
			a.templates[rel] = struct{}{}
		}
	}

	out := make([]ProvisionedType, 0, len(acc))
	for _, rt := range sortedAccKeys(acc) {
		a := acc[rt]
		if !a.viewSeen {
			continue
		}
		props := sortedSetKeys(a.declare)
		if len(props) == 0 {
			// A schema with no properties is REJECTED by records.LoadSchemas
			// (RejectNoProperties), which would take the whole reload down.
			// There is also nothing to gain: every one of the view's own
			// property references would be a loss against an empty schema.
			continue
		}
		pt := ProvisionedType{Type: rt, Bases: sortedSetKeys(a.bases), Properties: props, Templates: sortedSetKeys(a.templates)}
		for _, name := range sortedMapKeys(a.omit) {
			pt.Omitted = append(pt.Omitted, ProvisionedOmission{Property: name, Reason: a.omit[name]})
		}
		out = append(out, pt)
	}
	return out
}

// record files one use against a prospective type. An UNSAFE use wins over a
// safe one and is never overwritten by a later safe one: the unsafe clause is
// in the base file whatever else the base also does with the property, and
// declaring the property would broaden that clause.
func (a *provisionAccumulator) record(u provisionUse) {
	name := u.property
	if name == "" || name == "type" {
		return
	}
	if strings.HasPrefix(name, "formula.") || records.IsFileNamespace(name) {
		return
	}
	if !reProvisionablePropertyName.MatchString(name) {
		return
	}
	if u.unsafeReason != "" {
		// PRECEDENCE AMONG OMISSIONS, and it is not first-writer-wins any
		// more. Since template evidence can LIFT one kind of omission (the
		// numeric-summary kind) and must never lift the other two, "which
		// reason got recorded" decides whether a filter clause that would
		// broaden the view stays refused. First-writer-wins made that turn on
		// the order the base files happen to sort in: a `summaries: {amount:
		// Sum}` in one view would claim the slot, and a later view's `amount >
		// 100` — the clause that actually returns MORE rows on a text
		// declaration — would find the property already omitted and say
		// nothing. The template would then lift the summary's reason and
		// declare the property, and the ordering comparison would translate.
		//
		// So an omission a template CANNOT lift outranks one it can, whichever
		// arrives first. Between two of the same rank the first still wins;
		// they are equally binding and the message is the operator's, not a
		// decision.
		if existing, already := a.omit[name]; !already ||
			(omissionLiftedByTemplateEvidence(existing) && !omissionLiftedByTemplateEvidence(u.unsafeReason)) {
			a.omit[name] = u.unsafeReason
		}
		delete(a.declare, name)
		return
	}
	if _, refused := a.omit[name]; refused {
		return
	}
	a.declare[name] = struct{}{}
}

// recordTemplate files one property name donated by a `type: template` note.
//
// WHY THIS IS NOT JUST `record(provisionUse{property: name})`, AND WHY THAT
// DIFFERENCE IS THE WHOLE OF THE `invoice.amount` DECISION
//
// A base file's uses answer "how is this property USED". A template answers a
// different question — "does this property EXIST on this record type" — and it
// is the operator answering the schema question directly, in his own hand.
// Those are separate claims, and conflating them is what cost the column.
//
// The base says `summaries: {amount: Sum}`. That is real evidence, and it is
// evidence about the property's TYPE: nobody asks for the sum of prose. The
// provisioner read it correctly and then drew one conclusion too many — it
// treated "I cannot tell you this property's type" as "I cannot tell you this
// property exists", and withheld the DECLARATION. Withholding the declaration
// costs the COLUMN too, and the column was never in doubt: `order:` names
// `amount`, and the founder's `Template — invoice.md` lists `amount:` among the
// properties an invoice note carries.
//
// So a template's donation LIFTS an omission whose only source was a numeric
// summary. It lifts neither of the other two, and that asymmetry is the safety
// argument rather than a detail of it:
//
//   - AN ORDERING COMPARISON (`amount > 100`) and `.contains(...)` stay
//     omitted even when a template names the property, because those omissions
//     were never about existence either — they are about a clause that, under
//     a `text` declaration, matches MORE rows than Obsidian (FR-105). The
//     template settles existence and says nothing about that, so the clause
//     stays a named loss and the view stays disabled. Restoring a column must
//     never buy a row.
//   - A numeric summary decides no rows at all, so lifting it cannot broaden
//     anything. The summary itself is then dropped downstream by
//     view_write.go's summaryDefinedForType, which asks the query engine's own
//     op/type table — so the restored column does not smuggle in a view the
//     engine would refuse.
//
// WHY `text` AND NOT `decimal`, WHICH WOULD ALSO RESTORE THE COLUMN AND WOULD
// MAKE THE SUM WORK. Because a declaration made with no note behind it must not
// be able to REJECT the first note the operator writes, and only one of the two
// candidates has that property. `TypeText` is prose and is never validated for
// shape (pkg/records/schema.go), so no value can fail against it. `decimal`
// rejects anything that does not parse as a number — and THIS vault already
// shows what the founder writes in a money field: `subscription.cost` holds 42
// parseable values out of 63 and was defaulted to text over counter-examples
// like `PLACEHOLDER — amount unknown`. An `invoice.amount` declared `decimal`
// on the strength of a `.base` summary would reject his first invoice note
// written in his own demonstrated house style, and it would do it on the
// authority of a guess this package made. Text cannot reject; decimal can.
// That asymmetry decides it, and it decides it AGAINST the reading that would
// have closed more losses.
//
// The cost of choosing text is stated rather than hidden: `sum(amount)` is not
// defined over text (FR-155), so the summary is dropped as a named loss. It is
// the operator's one-line edit — `knowledge_configure set schema invoice
// property amount type=decimal`, which ReportLines already prints — that turns
// the column into a number and the sum back on, and by then a real note exists
// to justify the type.
func (a *provisionAccumulator) recordTemplate(name string) {
	if name == "" || name == "type" {
		return
	}
	if strings.HasPrefix(name, "formula.") || records.IsFileNamespace(name) {
		return
	}
	if !reProvisionablePropertyName.MatchString(name) {
		return
	}
	if reason, refused := a.omit[name]; refused {
		if !omissionLiftedByTemplateEvidence(reason) {
			return
		}
		delete(a.omit, name)
	}
	a.declare[name] = struct{}{}
}

// aggregateOmissionSentinel marks an omission whose only cause is a numeric
// summary — the one kind template evidence overrides. It is embedded IN the
// reason text the operator reads, rather than kept as a separate flag, so that
// the sentence and the classification cannot drift apart silently; a test pins
// that the aggregate branch still writes it.
const aggregateOmissionSentinel = "cannot be computed over prose"

// omissionLiftedByTemplateEvidence reports whether a recorded omission is one
// that a template's existence claim answers.
func omissionLiftedByTemplateEvidence(reason string) bool {
	return strings.Contains(reason, aggregateOmissionSentinel)
}

func sortedAccKeys(m map[string]*provisionAccumulator) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// provisionedProperties turns one provisioned type's declaration into the
// InferredProperty slice the rest of the pipeline already speaks.
func provisionedProperties(p ProvisionedType) []InferredProperty {
	props := make([]InferredProperty, 0, len(p.Properties))
	for _, name := range p.Properties {
		props = append(props, InferredProperty{Name: name, Type: provisionedPropertyType})
	}
	return props
}
