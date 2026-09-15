// Omnipus — assembling one translated Base view into a
// records.ParseView-shaped VERSION-2 YAML file, and the three-way
// per-base/per-view outcome this whole importer exists to report honestly
// (see doc.go).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"github.com/elicify-ai/omnipus/pkg/records"
)

// Outcome is the three-way honesty contract SC-010/US-7 require of every
// imported Base view (and, rolled up, of every Base file).
type Outcome string

const (
	OutcomeConverted           Outcome = "CONVERTED"
	OutcomeConvertedWithLosses Outcome = "CONVERTED WITH NAMED LOSSES"
	OutcomeRefused             Outcome = "REFUSED"
)

// ViewOutcome is one produced (or refused) view.
type ViewOutcome struct {
	BaseRelPath  string
	DisplayName  string
	Status       Outcome
	ResolvedType string
	// Losses names every dropped clause/column/aggregate, in the order
	// found, prefixed with WHERE it came from — see loss.go's LossPosition
	// for the closed set of prefixes and what each one means for FR-105.
	// SC-010's "reported verbatim".
	Losses []string
	// Disabled is FR-105's broadening prohibition, on the record: the view
	// was STORED but MUST NOT be applied, because at least one of its
	// losses sits in a row-set-affecting position and applying it would
	// return MORE rows than the Obsidian original while looking correct.
	//
	// A disabled view is not a refusal. The file is written, the filters
	// that DID translate are in it, and the expressions that did not are in
	// `untranslated` verbatim — so an operator can see exactly what is
	// missing and decide. What it may not do is silently answer a query.
	Disabled bool
	// DisablingLosses is the subset of Losses that caused Disabled, so a
	// reader is never left diffing two lists to find out why.
	DisablingLosses []string
	// Layout is the rendering the Obsidian view asked for (FR-109), as
	// written in the `.base` file. Empty when the view declared none.
	Layout string
	// RefusedReason is set only when Status == OutcomeRefused.
	RefusedReason string
	// OutputRelPath is the vault-relative view file path, set only when a
	// file was produced (Converted or ConvertedWithLosses).
	OutputRelPath string
	// AuthoredFormulas names every formula this importer WROTE INTO THE
	// OPERATOR'S FILE that the operator did not write — one per filter clause
	// that was a whole expression rather than a property/operator/literal leaf
	// — together with the clause it stands for and why a leaf could not hold
	// that clause directly.
	//
	// IT IS REPORTED, NOT MERELY RECORDED, and that is a condition of the
	// mechanism existing at all. A file the operator owns gaining a definition
	// he never authored is a new surface, and a new surface nobody is told
	// about is indistinguishable from a bug. The same text is written into the
	// produced view file's own header comment (authoredFormulaHeader), which
	// is the surface that survives: a console report scrolls away, and an
	// operator comparing this view against the `.base` file it came from is
	// holding the file.
	//
	// Like FormulaRewrites it is deliberately NOT a loss — nothing was
	// dropped, so recording it as one would disable a view whose translation
	// is complete.
	AuthoredFormulas []string
	// FormulaRewrites names every carried formula whose SOURCE this importer
	// changed on the way in, and what changed.
	//
	// IT IS DELIBERATELY NOT A LOSS, and the distinction is the whole reason
	// the field exists rather than another `lossf` call. A loss is something
	// DROPPED, and every loss position that could hold this one is classified
	// row-set-affecting, so recording a rewrite as a loss would DISABLE the
	// view. FR-105 forbids returning MORE rows; both rewrites here return the
	// same rows or fewer (see translate.go's W1/W2 proofs), so disabling the
	// view would be the importer refusing its own faithful translation.
	//
	// It is also not nothing. A view file whose `formulas:` no longer reads
	// character-for-character like the `.base` file it came from is a
	// difference an operator must be able to find without diffing two files,
	// so the same text is written into the view file's own header comment —
	// see formulaRewriteHeader.
	FormulaRewrites []string
}

// ProducedView is one view file this importer is about to write.
type ProducedView struct {
	RelPath string // vault-relative, under .omnipus-vault/views/
	Bytes   []byte
}

// BaseOutcome is one `.base` file's rolled-up result.
type BaseOutcome struct {
	BaseRelPath   string
	Status        Outcome
	RefusedReason string // set only when Status == OutcomeRefused
	Views         []ViewOutcome
}

// TranslateBase translates every view in one parsed Base file.
func TranslateBase(pb *ParsedBase, baseRelPath string, schemas *SchemaIndex, slugs *SlugRegistry) (BaseOutcome, []ProducedView) {
	outcome := BaseOutcome{BaseRelPath: baseRelPath}
	if len(pb.Views) == 0 {
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "the base file declares no views at all"
		return outcome, nil
	}

	outerTrans := TranslateFilterTree(pb.Filters)

	// The base's `formulas:` block is translated PER RECORD TYPE, because a
	// formula is typed against the schema of the type its view queries and two
	// views in one base can resolve to two different types. It is cached
	// because the translation is the same work every time for the same type,
	// and because a base's formulas would otherwise be re-parsed and
	// re-validated once per view.
	formulaCache := map[string]FormulaTranslation{}
	formulasFor := func(recordType string) FormulaTranslation {
		if ft, done := formulaCache[recordType]; done {
			return ft
		}
		ft := TranslateFormulas(pb, schemaForType(schemas, recordType))
		formulaCache[recordType] = ft
		return ft
	}

	var produced []ProducedView
	anyNonRefused := false
	anyLossy := false
	for _, vraw := range pb.Views {
		name, _ := vraw["name"].(string)
		slug := slugs.Slug(baseRelPath, name)
		vo, pv := translateOneView(vraw, outerTrans, pb, baseRelPath, slug, schemas, formulasFor)
		outcome.Views = append(outcome.Views, vo)
		if vo.Status != OutcomeRefused {
			anyNonRefused = true
		}
		if vo.Status == OutcomeConvertedWithLosses {
			anyLossy = true
		}
		if pv != nil {
			produced = append(produced, *pv)
		}
	}

	switch {
	case !anyNonRefused:
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "every view in this base failed to translate — see each view's own reason below"
	case anyLossy || hasRefusedView(outcome.Views):
		outcome.Status = OutcomeConvertedWithLosses
	default:
		outcome.Status = OutcomeConverted
	}
	return outcome, produced
}

func hasRefusedView(views []ViewOutcome) bool {
	for _, v := range views {
		if v.Status == OutcomeRefused {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Resolving the intermediate tree against the view's record type
// ---------------------------------------------------------------------------

// leafResolver carries the two things a leaf needs to become a real filter
// node: which record type the view queries (EMPTY for an untyped view) and the
// inferred schemas to check it against.
type leafResolver struct {
	recordType string
	schemas    *SchemaIndex
	// formulas is the base's `formulas:` block, translated against THIS view's
	// record type. A `formula.<name>` in any property position resolves against
	// it; a name it does not carry becomes a named loss quoting why the formula
	// itself could not be translated, never a bare "no such formula".
	formulas FormulaTranslation
	// schema is the view's record type rendered as the REAL *records.Schema
	// this import is about to write — the same value schemaForType produces for
	// the formula translator, carried rather than recomputed.
	//
	// It exists so a literal can be checked by the ENGINE'S OWN rule rather
	// than by a second opinion restated here: records.ParseValue is the
	// function a note's own value goes through AND the one Filter.Validate
	// runs a filter literal through at serve time, so a literal it refuses is
	// exactly a literal knowledge_find will refuse.
	schema *records.Schema
}

// formulaNamespace is FR-140's reserved prefix for a computed property.
//
// It is spelled here rather than imported because pkg/records keeps its own
// copy unexported (view.go's viewFormulaNamespace) and pkg/records/knowledgefind
// keeps a third (FormulaNamespace). TestFormulaNamespace_MatchesTheLoaders
// asserts this constant against the loader's behaviour rather than against
// either spelling, so a divergence fails by name instead of producing a view
// whose references the loader refuses.
const formulaNamespace = "formula."
