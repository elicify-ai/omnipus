// Omnipus — the importer's human-readable report: schema inference,
// ambiguities refused, per-base three-way outcomes, and validation numbers.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TypeSchemaSummary is one inferred record type's shape, for the report.
type TypeSchemaSummary struct {
	Type          string
	NoteCount     int
	PropertyCount int
	RequiredCount int
	ManyCount     int
	RelationCount int
	EnumCount     int
	DateCount     int
	IntegerCount  int
	DecimalCount  int
	CheckboxCount int
	TextCount     int
}

// ValidationSummary is the vault-wide record-validity count, produced by
// actually running records.Validate over every note against the schemas
// this importer just wrote — not estimated.
type ValidationSummary struct {
	TotalNotes          int
	NotesWithoutType    int // no `type:` at all — never a candidate record
	NotesWithType       int
	RecognisedRecords   int // `type:` matched a schema this run declared
	NotRecordsAtAll     int // NotesWithoutType, named for the report
	ValidRecords        int
	InvalidRecords      int
	ErrorFindingCount   int
	WarningFindingCount int
	InvalidExamples     []string // capped, for the report
	// DryRun records that these numbers came from a --dry-run: the schemas
	// were rendered and loaded from a throwaway staging directory rather than
	// from the vault. The numbers are the same ones a real run would produce;
	// the heading has to say which happened, or the report claims a state of
	// the vault that does not exist yet.
	DryRun bool
}

// Report is the importer's complete, honest account of one run.
type Report struct {
	VaultRoot string
	// DryRun is true when nothing was written to the vault.
	DryRun        bool
	Discriminator TypeDiscriminatorCheck
	LoadProblems  []LoadProblem
	// RejectedTypes is every declared `type:` this run refused to treat as a
	// record type because it is not a legal identifier (and therefore not a
	// safe file name) — reported rather than silently skipped.
	RejectedTypes []RejectedType
	// Provisioned is every record type DECLARED from a `.base` file alone
	// because no note in the vault carries it (FR-018d provisioning).
	Provisioned []ProvisionedType
	// NameEvidenced is every property typed from its NAME because the vault
	// holds no value for it anywhere — a guess, on the record, rather than an
	// observation. Populated from infer.CollectNameEvidencedInferences.
	NameEvidenced  []NameEvidencedInference
	Types          []TypeSchemaSummary
	Ambiguities    []AmbiguousInference
	RelationSplits []RelationSplitReport
	// AritySplits is every property whose notes disagreed about whether it
	// holds one value or a list. The majority decides `many:`; the minority is
	// named here, because those notes WILL fail validation and an operator
	// reading an arity error deserves to know this run predicted it.
	AritySplits []AritySplitReport
	// TypeInference is FR-104b's per-note account of every note that
	// arrived carrying no `type:` key.
	TypeInference TypeInferenceReport
	Bases         []BaseOutcome
	SchemaReload  *records.SchemaLoadReport
	ViewReload    *records.ViewLoadReport
	Validation    ValidationSummary
}

// Render writes the full, human-readable report.
func (r *Report) Render(w io.Writer) {
	fmt.Fprintf(w, "Obsidian vault import — %s\n", r.VaultRoot)
	if r.DryRun {
		fmt.Fprintln(w, "DRY RUN — nothing was written to the vault. Every number below was produced by rendering the schemas and views this run WOULD write into a throwaway directory and loading them back through the real loaders, so they are the numbers a real run produces.")
	}
	fmt.Fprintln(w, strings.Repeat("=", 78))

	fmt.Fprintln(w, "\n-- Type discriminator check --")
	fmt.Fprintf(w, "%d notes total; %d carry a `type:` key (%d distinct types); %d carry none.\n",
		r.Discriminator.TotalNotes, r.Discriminator.WithType, r.Discriminator.DistinctTypes, r.Discriminator.WithoutType)

	if len(r.LoadProblems) > 0 {
		fmt.Fprintf(w, "\n-- %d notes could not be read or parsed --\n", len(r.LoadProblems))
		for _, p := range r.LoadProblems {
			fmt.Fprintf(w, "  %s: %s\n", p.RelPath, p.Reason)
		}
	}

	if len(r.RejectedTypes) > 0 {
		fmt.Fprintf(w, "\n-- %d declared `type:` values REFUSED as record types --\n", len(r.RejectedTypes))
		for _, rt := range r.RejectedTypes {
			fmt.Fprintf(w, "  %q: %s\n", rt.Type, rt.Reason)
			for _, p := range rt.NotePaths {
				fmt.Fprintf(w, "      %s\n", p)
			}
		}
	}

	r.renderProvisioned(w)
	r.renderNameEvidenced(w)

	fmt.Fprintf(w, "\n-- %d record types inferred --\n", len(r.Types))
	for _, t := range r.Types {
		fmt.Fprintf(w, "  %-20s %4d notes, %2d properties (required=%d many=%d relation=%d enum=%d date=%d integer=%d decimal=%d checkbox=%d text=%d)\n",
			t.Type, t.NoteCount, t.PropertyCount, t.RequiredCount, t.ManyCount,
			t.RelationCount, t.EnumCount, t.DateCount, t.IntegerCount, t.DecimalCount, t.CheckboxCount, t.TextCount)
	}

	fmt.Fprintf(w, "\n-- %d ambiguous property inferences (refused to guess; defaulted to text) --\n", len(r.Ambiguities))
	for _, a := range r.Ambiguities {
		fmt.Fprintf(w, "  %s.%s: %d/%d values (%.0f%%) parse as %s; defaulted to text.\n",
			a.RecordType, a.Property, a.MatchedCount, a.TotalValues, a.MatchFrac*100, a.BestType)
		for _, ex := range a.Examples {
			fmt.Fprintf(w, "      counter-example: %s = %q\n", ex.NotePath, ex.Value)
		}
	}

	fmt.Fprintf(w, "\n-- %d relation properties whose link targets were not unanimous (FR-104a) --\n", len(r.RelationSplits))
	for _, s := range r.RelationSplits {
		switch s.Rule {
		case RelationUnresolved:
			fmt.Fprintf(w, "  %s.%s: NOT TYPED — no link resolved to exactly one known record type (unresolved=%d, ambiguous=%d); declared text.\n",
				s.RecordType, s.Property, s.Unresolved, s.AmbiguousLinks)
			fmt.Fprintf(w, "      fix: %s\n", s.Remedy)
		case RelationSupermajority:
			fmt.Fprintf(w, "  %s.%s: declared relation, to=%s — %d of %d links (>= 2/3); %d of those links resolved to exactly one type, %d dangled, %d were ambiguous (the linked title exists as more than one record type).\n",
				s.RecordType, s.Property, s.MajorityType, s.MajorityCount, s.LinkTotal, s.ResolvedTotal, s.Unresolved, s.AmbiguousLinks)
			if len(s.Minority) > 0 {
				fmt.Fprintf(w, "      minority (these links WILL be reported as relation_type_mismatch, which is correct): %s\n",
					strings.Join(s.Minority, ", "))
			} else {
				fmt.Fprintf(w, "      no conflicting target type; the shortfall is %d link(s) that resolved to nothing.\n", s.Unresolved)
			}
		case RelationNoMajority:
			fmt.Fprintf(w, "  %s.%s: NOT TYPED — no target type reached 2/3 of this property's %d links (%d resolved to exactly one type, %d dangled, %d ambiguous); declared text.\n",
				s.RecordType, s.Property, s.LinkTotal, s.ResolvedTotal, s.Unresolved, s.AmbiguousLinks)
			fmt.Fprintf(w, "      evidence: %s\n", strings.Join(s.Minority, ", "))
			fmt.Fprintf(w, "      counting only RESOLVED targets it would have been %d of %d — the narrower reading of FR-104a; see inferRelationTarget for why the wider one is applied.\n",
				s.StrictNumerator, s.StrictDenominator)
			fmt.Fprintf(w, "      fix: %s\n", s.Remedy)
		default:
			fmt.Fprintf(w, "  %s.%s: %v (unresolved=%d) — declared %s\n",
				s.RecordType, s.Property, s.ByType, s.Unresolved, s.Declared)
		}
	}

	fmt.Fprintf(w, "\n-- %d properties whose notes disagree about one-value vs list --\n", len(r.AritySplits))
	for _, a := range r.AritySplits {
		fmt.Fprintf(w, "  %s.%s: declared many=%v — %d note(s) write a list, %d write a single value.\n",
			a.RecordType, a.Property, a.Many, a.ListCount, a.ScalarCount)
		fmt.Fprintf(w, "      the %d note(s) in the minority WILL be reported as an arity error against this schema; that is the disagreement, not a defect this run introduced.\n", a.MinorityCount())
		for _, ex := range a.Examples {
			fmt.Fprintf(w, "      minority example: %s\n", ex)
		}
	}

	fmt.Fprintf(w, "\n-- %d `.base` files: three-way outcome --\n", len(r.Bases))
	for _, b := range r.Bases {
		fmt.Fprintf(w, "  %s: %s\n", b.BaseRelPath, b.Status)
		if b.Status == OutcomeRefused && b.RefusedReason != "" {
			fmt.Fprintf(w, "      %s\n", b.RefusedReason)
		}
		for _, v := range b.Views {
			fmt.Fprintf(w, "    - %-30s %s", v.DisplayName, v.Status)
			if v.ResolvedType != "" {
				fmt.Fprintf(w, " (type=%s)", v.ResolvedType)
			}
			if v.Layout != "" {
				fmt.Fprintf(w, " (layout=%s)", v.Layout)
			}
			if v.Disabled {
				fmt.Fprint(w, "  [DISABLED — stored, never applied]")
			}
			fmt.Fprintln(w)
			if v.Status == OutcomeRefused {
				fmt.Fprintf(w, "        reason: %s\n", v.RefusedReason)
			}
			if v.Disabled {
				fmt.Fprintf(w, "        FR-105: %d loss(es) sit where the ROW SET is decided, so applying this view would return MORE rows than the Obsidian original. It is stored disabled rather than imported broadened:\n", len(v.DisablingLosses))
				for _, l := range v.DisablingLosses {
					fmt.Fprintf(w, "          -> %s\n", l)
				}
			}
			for _, l := range v.Losses {
				fmt.Fprintf(w, "        lost: %s\n", l)
			}
		}
	}

	if r.SchemaReload != nil && !r.SchemaReload.OK() {
		fmt.Fprintf(w, "\n-- %d schema files rejected on reload (should be zero) --\n", len(r.SchemaReload.Rejections))
		for _, rej := range r.SchemaReload.Rejections {
			fmt.Fprintf(w, "  %s\n", rej.String())
		}
	}
	if r.ViewReload != nil && !r.ViewReload.OK() {
		fmt.Fprintf(w, "\n-- %d view files rejected on reload (should be zero) --\n", len(r.ViewReload.Rejections))
		for _, rej := range r.ViewReload.Rejections {
			fmt.Fprintf(w, "  %s\n", rej.String())
		}
	}

	ti := r.TypeInference
	fmt.Fprintf(w, "\n-- %d notes arrived with no `type:` — every one has a recorded outcome (FR-104b) --\n", len(ti.Notes))
	fmt.Fprintf(w, "  %d typed by this run, %d left as is because the shape matched several types, %d left as is because it matched none",
		ti.Written, ti.Ambiguous, ti.NoMatch)
	if ti.WriteErrors > 0 {
		fmt.Fprintf(w, ", %d could not be written", ti.WriteErrors)
	}
	fmt.Fprintln(w, ".")
	for _, n := range ti.Notes {
		fmt.Fprintf(w, "  %s: %s\n", n.RelPath, n.Reason)
	}

	fmt.Fprintln(w, "\n-- FR-105: the broadening prohibition, applied --")
	produced, disabled := 0, 0
	for _, b := range r.Bases {
		for _, vv := range b.Views {
			if vv.Status == OutcomeRefused {
				continue
			}
			produced++
			if vv.Disabled {
				disabled++
			}
		}
	}
	fmt.Fprintf(w, "  %d view files written, of which %d are DISABLED because a loss sits where the row set is decided.\n", produced, disabled)
	fmt.Fprintf(w, "  %d are enabled and every clause they carry has a faithful mapping — an enabled imported view NEVER returns more rows than its Obsidian original.\n", produced-disabled)
	if disabled > 0 {
		fmt.Fprintln(w, "  What is actually blocking them, counted (this is the work list, not a list of mistakes):")
		for _, line := range r.disablingCausesByShape() {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}

	v := r.Validation
	if v.DryRun {
		fmt.Fprintln(w, "\n-- Validation over every note, against the schemas this run WOULD write (dry run: staged, not written to the vault) --")
	} else {
		fmt.Fprintln(w, "\n-- Validation over every note, against the schemas just written --")
	}
	fmt.Fprintf(w, "  %d notes total\n", v.TotalNotes)
	fmt.Fprintf(w, "  %d notes carry no `type:` — not records at all\n", v.NotRecordsAtAll)
	fmt.Fprintf(w, "  %d notes carry a `type:` this run recognised as a schema\n", v.RecognisedRecords)
	fmt.Fprintf(w, "  %d valid, %d invalid (%d error findings, %d warning findings)\n",
		v.ValidRecords, v.InvalidRecords, v.ErrorFindingCount, v.WarningFindingCount)
	if len(v.InvalidExamples) > 0 {
		fmt.Fprintln(w, "  invalid record examples:")
		for _, ex := range v.InvalidExamples {
			fmt.Fprintf(w, "    %s\n", ex)
		}
	}

	fmt.Fprintln(w, "\n-- Systemic gaps found in this vault's `.base` files (the point of the exercise) --")
	for _, g := range r.systemicGaps() {
		fmt.Fprintf(w, "  * %s\n", g)
	}
}

// ---------------------------------------------------------------------------
// WHY A LOSS HAPPENED — DERIVED, NOT ASSERTED
//
// This section replaces two hand-written English paragraphs that both went
// stale, in the two ways prose about a limitation always goes stale:
//
//  1. The LIMITATION was lifted and the sentence stayed. The report told the
//     founder that "ViewDef's `filters:` is a flat AND-only list (view.go:
//     \"Filter clauses, combined with AND\") with no boolean tree at all"
//     for weeks after commit 37bfb062 deleted the flat format and gave every
//     view a real `all`/`any`/`not` tree. It quoted a line of `view.go` that
//     no longer existed. The count beside it was right; the reason was not.
//  2. The COUNT was assembled by grepping the loss line for a substring that
//     also occurs in unrelated losses, so one number hid two causes. Six
//     "empty-string comparisons" were four empty-string comparisons and two
//     undeclared properties whose EXPRESSION happened to contain `!= ""`.
//
// Both failures have the same root: the sentence and the count were derived
// from something other than the thing that decides the behaviour. So:
//
//   * A loss is classified on the REASON the importer wrote, never on the
//     user's expression text — user data cannot decide which bucket a loss
//     lands in. A loss carrying no reason is its own bucket, printed, rather
//     than being quietly folded into whichever bucket its text resembles.
//   * The claim each paragraph makes ABOUT THE MODEL is computed by reflection
//     over the generated wire types (`wireCap`), not typed into a string. When
//     a field appears on `ViewDef`, every paragraph that said "the model
//     cannot express this" starts saying "the model can; this importer does
//     not" on the next run, with no edit.
//   * Both summary sections — the FR-105 work list and the systemic-gaps
//     section — read from ONE ordered table, so they cannot disagree about
//     the same loss the way two parallel lists did.
//
// WHAT THIS DOES NOT PROTECT AGAINST, stated so nobody trusts it further than
// it goes:
//
//   * Reflection proves a FIELD EXISTS, never that the engine reads it or
//     reads it correctly. "The wire can carry this" is a claim about shape.
//   * Classification still matches substrings of importer-authored reason
//     text. If a peer rewords a reason, the bucket goes empty and the loss
//     lands in UNCLASSIFIED — visible and counted, but not automatically
//     re-explained. `TestGapTokens_StillExistInTheEmittingSource` narrows
//     that window by failing when a token this file matches on no longer
//     appears as a string literal in the code that emits it. It cannot catch
//     a reason whose wording is unchanged but whose MEANING moved.
//   * Nothing here verifies the drop itself was correct. It explains what the
//     importer did; FR-105's oracle (loss.go) is what keeps it safe.
// ---------------------------------------------------------------------------

// wireCap is one capability question answered by reflection over a generated
// wire type: does the field that would have to exist for this construct to be
// expressible, exist? Every sentence in this report that makes a claim about
// what the MODEL can hold is composed from one of these, so the claim and the
// contract cannot drift apart.
type wireCap struct {
	// Path is the wire spelling an operator would look for, e.g.
	// "ViewDef.filter" — named in the report so the claim is checkable.
	Path string
	// Present is derived, never written down.
	Present bool
}

// verdict is the half-sentence a gap paragraph appends. It is the whole point
// of this type: an operator reading "the model cannot express X" is being told
// to stop looking, and that sentence must never outlive the field's absence.
func (c wireCap) verdict() string {
	if c.Present {
		return fmt.Sprintf("The wire format CAN carry this — `%s` exists on the generated view types — so what is missing is THIS IMPORTER, not the model; under FR-107 every occurrence is a defect to file, not an accepted loss.", c.Path)
	}
	return fmt.Sprintf("The wire format cannot carry this at all — there is no `%s` on the generated view types — so it is genuinely unrepresentable, not merely unimplemented.", c.Path)
}

// jsonFieldName reads a struct field's wire name, which is the spelling an
// operator sees in a view file and therefore the one worth probing for.
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// wireField finds one field of a generated struct by its wire name.
func wireField(sample any, jsonName string) (reflect.StructField, bool) {
	t := reflect.TypeOf(sample)
	if t == nil || t.Kind() != reflect.Struct {
		return reflect.StructField{}, false
	}
	for i := 0; i < t.NumField(); i++ {
		if jsonFieldName(t.Field(i)) == jsonName {
			return t.Field(i), true
		}
	}
	return reflect.StructField{}, false
}

// probeField answers "does this wire type carry this field at all".
func probeField(path string, sample any, jsonName string) wireCap {
	_, ok := wireField(sample, jsonName)
	return wireCap{Path: path, Present: ok}
}

// The probes. Each one is the field whose EXISTENCE decides whether the
// matching gap paragraph is a statement about the model or about this
// importer. Add a gap, add its probe; there is no way to write the sentence
// without one.
var (
	// capAnyCombinator — disjunction in a saved view's filter tree.
	capAnyCombinator = probeField("VaultFilterNode.any", generated.VaultFilterNode{}, "any")
	// capMultiType — a view spanning SEVERAL declared record types. There is
	// no such field, which is exactly why a mixed-type `or:` cannot be
	// carried even though `any:` can. If a `types` list ever lands, this
	// paragraph flips itself.
	capMultiType = probeField("ViewDef.types (a list of record types)", generated.ViewDef{}, "types")
	// capFormulas — computed properties on a saved view.
	capFormulas = probeField("ViewDef.formulas", generated.ViewDef{}, "formulas")
	// capOptionalType — an UNTYPED view, i.e. one spanning every note in
	// scope. Present AND optional is the question; a required `type` would
	// mean untyped views are inexpressible.
	capOptionalType = probeOptionalType()
)

// probeOptionalType is capOptionalType's derivation: `type` must exist AND be
// a pointer, because an untyped view is spelled by OMITTING the key.
func probeOptionalType() wireCap {
	c := wireCap{Path: "ViewDef.type (optional — omit it for an untyped view)"}
	f, ok := wireField(generated.ViewDef{}, "type")
	c.Present = ok && f.Type.Kind() == reflect.Ptr
	return c
}

// ---------------------------------------------------------------------------
// The closed set of gap shapes
// ---------------------------------------------------------------------------

// gapKind names one reason an expression in a `.base` file did not reach the
// imported view. It is a CLOSED set: anything this table does not recognise
// is counted and printed as UNCLASSIFIED rather than being dropped from the
// summary, because a summary that silently omits what it does not understand
// is how a gap disappears from a work list without being fixed.
type gapKind int

const (
	gapFormula gapKind = iota
	gapMixedTypeDisjunction
	gapOtherCombinator
	gapUndeclaredProperty
	gapEnumLiteral
	gapEmptyStringOnText
	gapBaseFunction
	gapReasonDiscarded
	gapUnclassified
	// gapTypeNotProvisioned is reached from a view's REFUSAL rather than
	// from a loss line: the view never produced losses because it never
	// translated at all.
	gapTypeNotProvisioned
)

// gapShape is one row of the closed table. `tokens` are the importer-authored
// substrings that identify the shape; they are matched against the REASON half
// of a loss line only, and TestGapTokens_StillExistInTheEmittingSource fails
// when one of them is no longer a string literal anywhere in the code that
// emits losses.
type gapShape struct {
	kind gapKind
	// label is the one-line entry in the FR-105 work list.
	label string
	// tokens identify the shape from the importer's own reason text.
	tokens []string
	// matchExpr classifies a loss that carries NO reason, from the shape of
	// the expression itself. Nil means this shape is never reached that way.
	matchExpr func(expr string) bool
}

// gapShapes is the table, in match order. Reason tokens are disjoint by
// construction (they are distinct importer sentences); the expression matchers
// below them are ordered most-specific first.
var gapShapes = []gapShape{
	{
		kind:  gapFormula,
		label: "computed property (`formula.*`) the importer could not TRANSLATE — the base's `formulas:` block IS carried now, so what remains is the individual formula our expression grammar cannot express",
		tokens: []string{
			"the base file declares no formula",
			"only a direct comparison against",
			"whose result is a LIST",
			"and formula",
		},
		matchExpr: func(expr string) bool {
			return strings.Contains(expr, "formula.")
		},
	},
	{
		kind:   gapEmptyStringOnText,
		label:  "empty-string comparison on a TEXT property (`prop != \"\"`) — `\"\"` is a PRESENT value for text, so `IS NOT NULL` would admit rows the base excludes",
		tokens: []string{"has no faithful translation on a TEXT property"},
	},
	{
		kind:   gapUndeclaredProperty,
		label:  "property the inferred schema does not declare — no sampled note of that type carried it",
		tokens: []string{"is not declared in the", "not a declared property of"},
	},
	{
		kind:   gapEnumLiteral,
		label:  "enum literal the inferred schema does not declare — the base filters on a value no sampled note carried",
		tokens: []string{"declared enum values"},
	},
	{
		kind:   gapTypeNotProvisioned,
		label:  "record type provisioned ahead of its data — the base names a type no note in this vault carries",
		tokens: []string{"has no inferred schema"},
	},
	{
		kind:      gapMixedTypeDisjunction,
		label:     "mixed-type disjunction — an `or:` whose branches name DIFFERENT record types, which one view's single `type:` cannot hold",
		matchExpr: func(expr string) bool { return isCombinatorExpr(expr) && len(distinctTypeLiterals(expr)) > 1 },
	},
	{
		kind:      gapOtherCombinator,
		label:     "combinator group dropped whole for some other reason — one of its leaves could not be built and the group reports its own text, not the leaf's",
		matchExpr: isCombinatorExpr,
	},
	{
		kind:      gapBaseFunction,
		label:     "`.base` function expression this importer does not parse (`date(...)`, `today()`, `.year`/`.month`)",
		matchExpr: isFunctionCallExpr,
	},
	{
		kind:      gapReasonDiscarded,
		label:     "dropped with NO stated reason — the leaf's diagnosis was discarded before it reached this report",
		matchExpr: func(string) bool { return true },
	},
}

// reCombinator matches a loss whose text is a whole `and:`/`or:`/`not:` block,
// which is how view_write.go reports a group it lost as a unit.
var reCombinator = regexp.MustCompile(`^(and|or|not)\s*:`)

func isCombinatorExpr(expr string) bool {
	return reCombinator.MatchString(strings.TrimSpace(expr))
}

// reTypeLiteral finds `type == "x"` assertions inside a combinator's verbatim
// text. The COUNT of distinct ones is what separates a disjunction this
// product could hold from one it cannot, so it is measured from the base
// file's own text rather than assumed.
var reTypeLiteral = regexp.MustCompile(`\btype\s*==\s*"([^"]*)"`)

func distinctTypeLiterals(expr string) []string {
	seen := map[string]bool{}
	for _, m := range reTypeLiteral.FindAllStringSubmatch(expr, -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reFunctionCall matches a call this importer's leaf parser does not read.
// `file.inFolder(...)`, `file.hasTag(...)` and `file.hasLink(...)` ARE
// translated (translate.go's reFileMethod, FR-134), so they are excluded — a
// paragraph naming them as a gap would be the same stale claim in a new place.
var reFunctionCall = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.]*\(`)

func isFunctionCallExpr(expr string) bool {
	if strings.HasPrefix(strings.TrimSpace(expr), "file.") {
		return false
	}
	return reFunctionCall.MatchString(expr)
}

// splitLossLine cuts a rendered loss line into the base EXPRESSION it names
// and the REASON this importer attached, if any.
//
// The separator is the first " — ". Everything before it is the operator's own
// text and is therefore UNTRUSTED for classification; everything after it was
// written by this importer and is what a bucket is chosen from. That split is
// the fix for the six-that-were-four defect: `registration_renewal_date != ""`
// contains `!= ""` in its expression and "is not declared in the" in its
// reason, and only the second is evidence of anything.
func splitLossLine(line string) (expr, reason string) {
	body := line
	if _, ok := parseLossPosition(line); ok {
		if end := strings.IndexByte(line, ']'); end >= 0 {
			body = strings.TrimSpace(line[end+1:])
		}
	}
	const sep = " — "
	if i := strings.Index(body, sep); i >= 0 {
		return strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+len(sep):])
	}
	return strings.TrimSpace(body), ""
}

// classifyLoss places one rendered loss line in the closed table.
func classifyLoss(line string) gapKind {
	expr, reason := splitLossLine(line)
	if reason != "" {
		for _, sh := range gapShapes {
			for _, tok := range sh.tokens {
				if strings.Contains(reason, tok) {
					return sh.kind
				}
			}
		}
		// A reason this table does not recognise is NOT silently re-read as
		// an expression: the importer explained itself and this report did
		// not understand the explanation, which is the thing worth printing.
		return gapUnclassified
	}
	for _, sh := range gapShapes {
		if sh.matchExpr != nil && sh.matchExpr(expr) {
			return sh.kind
		}
	}
	return gapUnclassified
}

// classifyRefusal places a view's refusal reason in the same table. A refused
// view produces no losses at all, so its reason is the only evidence there is.
func classifyRefusal(reason string) (gapKind, bool) {
	for _, sh := range gapShapes {
		for _, tok := range sh.tokens {
			if strings.Contains(reason, tok) {
				return sh.kind, true
			}
		}
	}
	return gapUnclassified, reason != ""
}

// gapTally is one kind's evidence: how many, and enough examples to check the
// claim without re-running the import.
type gapTally struct {
	Count    int
	Example  string
	Bases    map[string]bool
	Views    int
	TypeRefs map[string]bool
}

func (t *gapTally) add(example string) {
	t.Count++
	if t.Example == "" {
		t.Example = example
	}
}

// tallyLosses classifies every loss produced by a selector over the report.
func (r *Report) tally(includeLosses func(v ViewOutcome) []string, includeRefusals bool) map[gapKind]*gapTally {
	out := map[gapKind]*gapTally{}
	get := func(k gapKind) *gapTally {
		t, ok := out[k]
		if !ok {
			t = &gapTally{Bases: map[string]bool{}, TypeRefs: map[string]bool{}}
			out[k] = t
		}
		return t
	}
	for _, b := range r.Bases {
		for _, v := range b.Views {
			if v.Status == OutcomeRefused {
				if !includeRefusals || v.RefusedReason == "" {
					continue
				}
				k, ok := classifyRefusal(v.RefusedReason)
				if !ok {
					continue
				}
				t := get(k)
				t.add(v.RefusedReason)
				t.Views++
				t.Bases[b.BaseRelPath] = true
				for _, name := range reQuotedType.FindAllStringSubmatch(v.RefusedReason, -1) {
					t.TypeRefs[name[1]] = true
				}
				continue
			}
			if includeLosses == nil {
				continue
			}
			for _, l := range includeLosses(v) {
				t := get(classifyLoss(l))
				t.add(l)
				t.Bases[b.BaseRelPath] = true
			}
		}
	}
	return out
}

// reQuotedType pulls the record-type name out of a refusal so the report can
// NAME the types that blocked, instead of only counting them.
var reQuotedType = regexp.MustCompile(`record type "([^"]*)"`)

// renderProvisioned prints the account of every record type DECLARED from a
// `.base` file alone (FR-018d provisioning): the type existed in the founder's
// base file and in no note, so its schema is the base file's own word for what
// the type is, not an observation.
//
// TWO THINGS THIS SECTION DOES THAT A PLAIN LOOP WOULD NOT, both for the same
// reason the systemic-gaps section was rewritten — a report that explains a
// limitation must not be able to explain it wrongly:
//
//  1. It CHECKS ITS OWN HEADING. The heading says no note carries these types.
//     That is checkable against the very report it appears in, so it is
//     checked: a provisioned type that ALSO turns up among the inferred types
//     means real notes carry it, provisioning should never have fired, and the
//     contradiction is printed rather than papered over.
//  2. It TOLERATES AN EMPTY ACCOUNT. ReportLines() is another package's
//     function; indexing [0] and [1:] on whatever it returns would panic the
//     whole report on an edge case nothing here controls. The founder's only
//     window into his import must not close because one entry came back empty
//     — the entry is named as unaccounted-for instead.
func (r *Report) renderProvisioned(w io.Writer) {
	if len(r.Provisioned) == 0 {
		return
	}
	fmt.Fprintf(w, "\n-- %d record types DECLARED FROM A `.base` FILE (no note carries them yet) --\n", len(r.Provisioned))

	var names []string
	for _, p := range r.Provisioned {
		names = append(names, p.Type)
		renderProvisionedEntry(w, p.Type, p.ReportLines())
	}

	if observed, _ := r.partitionAgainstInferredTypes(names); len(observed) > 0 {
		fmt.Fprintf(w, "  CONTRADICTION — %s also appear(s) among the %d record types inferred from real notes above, so notes DO carry them and nothing here should have been provisioned. Trust the inferred schema and file this.\n",
			strings.Join(observed, ", "), len(r.Types))
	}
}

// renderNameEvidenced prints every property this run typed from its NAME
// because the vault held no value for it anywhere.
//
// THE SAME MECHANISM AS THE SYSTEMIC-GAPS SECTION, for the same reason. This
// section states a limitation ("nothing was observed, so the name decided"),
// and a hardcoded sentence about a limitation rots the moment the limitation
// moves. Two things keep it honest, and neither is prose:
//
//  1. WHAT THE NAMES WERE READ AS IS COUNTED FROM THE DATA, never named in a
//     string. infer.go's rule produces only `date` today and says so in a
//     comment; the day it produces a second type, a sentence saying "these are
//     dates" becomes false in silence. This section groups by the `Type` the
//     inference actually wrote and prints the set it finds.
//  2. EVERY ENTRY'S OWN PREMISE IS CHECKED against the report it appears in.
//     The premise is "notes DECLARED the key and every one left it blank". An
//     entry claiming zero declaring notes contradicts it — nothing declared the
//     key, so there was no key to type — and an entry for a record type this
//     run did not infer contradicts it too. Both are printed as contradictions
//     rather than narrated as though they were fine.
//
// WHAT IT DOES NOT REACH: nothing here can tell whether the guess was RIGHT.
// `renewal_date` typed as a date from its name is still a guess about a
// property no note in the vault ever filled in, and the report says so in
// those words rather than implying the type was measured.
func (r *Report) renderNameEvidenced(w io.Writer) {
	if len(r.NameEvidenced) == 0 {
		return
	}
	byType := map[string]int{}
	for _, n := range r.NameEvidenced {
		byType[string(n.Type)]++
	}
	kinds := make([]string, 0, len(byType))
	for k := range byType {
		kinds = append(kinds, fmt.Sprintf("%s x%d", k, byType[k]))
	}
	sort.Strings(kinds)

	fmt.Fprintf(w, "\n-- %d properties typed from their NAME, not from a value (%s) --\n", len(r.NameEvidenced), strings.Join(kinds, ", "))
	fmt.Fprintln(w, "  Every property below is a GUESS. Notes of the record type declare the key and every one of them leaves it blank, so this run had no value anywhere to read a type from and used the property's name instead. Correct any one of them with: knowledge_configure set schema <type> property <name> type=<...>")

	inferredTypes := map[string]bool{}
	for _, t := range r.Types {
		inferredTypes[t.Type] = true
	}
	for _, n := range r.NameEvidenced {
		fmt.Fprintf(w, "  %s.%s -> %s (declared by %d note(s), every one blank)\n", n.RecordType, n.Property, n.Type, n.DeclaringNotes)
		if n.DeclaringNotes <= 0 {
			fmt.Fprintf(w, "      CONTRADICTION — no note declares `%s` at all, so there was no key here to type from a name. The premise of this whole section fails for this entry; file it rather than trusting the type.\n", n.Property)
		}
		if len(r.Types) > 0 && !inferredTypes[n.RecordType] {
			fmt.Fprintf(w, "      CONTRADICTION — %q is not among the %d record types this run inferred, so this guess is attached to a type that does not exist here.\n", n.RecordType, len(r.Types))
		}
	}
}

// renderProvisionedEntry prints one provisioned type's account.
//
// It is a FUNCTION OVER THE LINES rather than inline code over a
// ProvisionedType because the only interesting case — an empty account — is
// unreachable through ProvisionedType.ReportLines() as that method is written
// today. Inlined, the length guard could only be tested by mutating another
// package's code, which is to say it could not be tested at all, and an
// untested guard against a panic in the founder's only report is not a guard.
// TestRenderProvisionedEntry_SurvivesAnEmptyAccount calls this directly.
func renderProvisionedEntry(w io.Writer, typeName string, lines []string) {
	if len(lines) == 0 {
		fmt.Fprintf(w, "  %s: DECLARED, but this run produced no account of what was assumed — read the generated schema file directly. An entry with nothing to say is a defect to file, not a type with no story.\n", typeName)
		return
	}
	fmt.Fprintf(w, "  %s\n", lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(w, "      %s\n", l)
	}
}

// disablingCausesByShape counts WHY views were disabled, using the same closed
// table the systemic-gaps section uses — so the two summaries cannot disagree
// about the same loss, which two parallel substring lists previously did.
//
// This is the list somebody prioritises from. A disabled view is not a mistake
// in the base file; it is a capability this release does not have yet, named.
func (r *Report) disablingCausesByShape() []string {
	tallies := r.tally(func(v ViewOutcome) []string { return v.DisablingLosses }, false)
	var out []string
	for _, sh := range gapShapes {
		if t := tallies[sh.kind]; t != nil && t.Count > 0 {
			out = append(out, fmt.Sprintf("%4d x %s", t.Count, sh.label))
		}
	}
	if t := tallies[gapUnclassified]; t != nil && t.Count > 0 {
		out = append(out, fmt.Sprintf("%4d x UNCLASSIFIED — this report's closed table did not recognise these; see the per-view lines above", t.Count))
	}
	return out
}

// systemicGaps groups every named loss and every refusal into the small number
// of recurring shapes this vault actually hit, and says for each one whether
// the block is the MODEL or this IMPORTER — the second half derived from the
// generated wire types rather than asserted in prose.
func (r *Report) systemicGaps() []string {
	tallies := r.tally(func(v ViewOutcome) []string { return v.Losses }, true)

	var out []string
	var quiet []string
	for _, sh := range gapShapes {
		t := tallies[sh.kind]
		if t == nil || t.Count == 0 {
			quiet = append(quiet, sh.label)
			continue
		}
		out = append(out, r.narrate(sh.kind, t))
	}
	if t := tallies[gapUnclassified]; t != nil && t.Count > 0 {
		out = append(out, fmt.Sprintf("UNCLASSIFIED (%d): this report's closed table of gap shapes did not recognise these losses. A non-zero count here means the importer emits a reason this summary does not know about — the per-view lines above are authoritative until the table is extended. Example: %q", t.Count, t.Example))
	}
	if len(out) == 0 {
		return []string{"none found — every `.base` file translated without a systemic gap"}
	}
	if len(quiet) > 0 {
		out = append(out, fmt.Sprintf("NOT A GAP IN THIS VAULT (0 occurrences each, listed so a shape that stops firing is visible rather than simply absent): %s.", strings.Join(quiet, "; ")))
	}
	return out
}

// narrate composes one gap paragraph: the counted evidence from this run, plus
// the model-vs-importer verdict computed from the wire types.
func (r *Report) narrate(k gapKind, t *gapTally) string {
	switch k {
	case gapFormula:
		return fmt.Sprintf("COMPUTED / FORMULA PROPERTY THIS IMPORTER COULD NOT TRANSLATE (%d dropped clause/column/sort/aggregate(s) across %d base(s)): the base's `formulas:` block IS carried now — it is parsed, translated into this product's own expression grammar, validated against the view's record type and written into the view file, and a `formula.*` reference in a filter, a grouping, a column or a summary resolves against it. What is counted here is the residue: an individual formula our grammar cannot express, and every reference to it. The two shapes that remain in the founder's vault are a JavaScript TRUTHINESS test used as an `if` condition (`if(due, ..., false)` — ours needs a boolean, and a bare date is not one) and an expression past FR-146's depth cap. %s Example: %q",
			t.Count, len(t.Bases), capFormulas.verdict(), t.Example)

	case gapMixedTypeDisjunction:
		return fmt.Sprintf("MIXED-TYPE DISJUNCTION (%d group(s) across %d base(s)): an `or:` whose branches name DIFFERENT record types. The filter grammar is NOT the blocker — a disjunction is carried, `or:` becomes `any:`, and `%s` exists on the generated view types. The RECORD TYPE is the blocker. %s The remaining alternative would be to import the view UNTYPED (`%s` permits that), but an untyped view spans every note in scope, which is strictly MORE rows than \"one of these two types\" — the broadening FR-105 forbids — so the group is dropped whole and the view is DISABLED instead. Example: %q",
			t.Count, len(t.Bases), capAnyCombinator.Path, capMultiType.verdict(), capOptionalType.Path, t.Example)

	case gapOtherCombinator:
		return fmt.Sprintf("COMBINATOR GROUP DROPPED WHOLE (%d): an `or:`/`not:` block lost as a unit although its branches do NOT disagree about the record type, so one of its leaves could not be built. The group reports its own verbatim rather than the failing leaf's reason, so this report cannot say which leaf — a reporting gap to file under FR-107, separate from the drop. Example: %q",
			t.Count, t.Example)

	case gapUndeclaredProperty:
		return fmt.Sprintf("PROPERTY THE INFERRED SCHEMA DOES NOT DECLARE (%d across %d base(s)): the base filters on or displays a property that no sampled note of that record type carried, so this run inferred no such property to filter against. This is an INFERENCE gap, not a model gap — the property is real in the founder's base file and simply never appeared on a note. Example: %q",
			t.Count, len(t.Bases), t.Example)

	case gapEnumLiteral:
		return fmt.Sprintf("ENUM LITERAL THE INFERRED SCHEMA DOES NOT DECLARE (%d across %d base(s)): the base filters on a value no sampled note carried, so the inferred enum's closed set does not contain it. Keeping the clause would be refused at query time; dropping it would BROADEN, so the view is DISABLED instead. Example: %q",
			t.Count, len(t.Bases), t.Example)

	case gapEmptyStringOnText:
		return fmt.Sprintf("EMPTY-STRING COMPARISON ON A TEXT PROPERTY (%d across %d base(s)): `prop != \"\"`. FR-007a keeps `\"\"` a PRESENT value for text, so `IS NOT NULL` would also match a record whose value IS the empty string — a record the Obsidian filter excludes. Approximating it would BROADEN, so it is refused. Example: %q",
			t.Count, len(t.Bases), t.Example)

	case gapBaseFunction:
		return fmt.Sprintf("`.base` FUNCTION EXPRESSION (%d across %d base(s)): a call this importer's leaf parser does not read — `date(x).year`, `today()` and the `.year`/`.month` accessors. `file.inFolder`, `file.hasTag` and `file.hasLink` ARE translated (FR-134) and are not part of this count, so folder scoping is not among this vault's gaps. Example: %q",
			t.Count, len(t.Bases), t.Example)

	case gapReasonDiscarded:
		return fmt.Sprintf("DROPPED WITH NO STATED REASON (%d across %d base(s)): the expression is named but this report cannot say WHY it went. `!=` desugars into a `not:` wrapper (translate.go's nodeFromRawLeaf), and a combinator reports its OWN verbatim rather than the failing leaf's reason (view_write.go's `rawKindAny, rawKindNot` branch), so the leaf's diagnosis is discarded before it reaches here. Under FR-107 that is a reporting defect to file, independent of whether the drop was right. Example: %q",
			t.Count, len(t.Bases), t.Example)

	case gapTypeNotProvisioned:
		named := sortedGapTypeNames(t.TypeRefs)
		inferred, absent := r.partitionAgainstInferredTypes(named)
		claim := fmt.Sprintf("Checked against this run's own inference: none of them is among the %d record types above (%s).", len(r.Types), strings.Join(absent, ", "))
		if len(inferred) > 0 {
			claim = fmt.Sprintf("Checked against this run's own inference, and it DISAGREES: %s DID get an inferred schema this run, so those refusals are a different defect and the reason printed on them is wrong.", strings.Join(inferred, ", "))
		}
		return fmt.Sprintf("RECORD TYPE PROVISIONED AHEAD OF ITS DATA (%d view(s) REFUSED across %d base(s); types: %s): the view names a record type no note in this vault carries, so this run inferred no schema for it and refused the view rather than guess its shape. %s The remedy is a schema for the type — derived from the base file's own columns, or hand-written — not a change to the view model, which already permits both a declared type holding zero records (FR-018d) and an untyped view (`%s`).",
			t.Views, len(t.Bases), strings.Join(named, ", "), claim, capOptionalType.Path)
	}
	return fmt.Sprintf("%d occurrence(s). Example: %q", t.Count, t.Example)
}

func sortedGapTypeNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// partitionAgainstInferredTypes splits record-type names by whether THIS run
// actually inferred a schema for them. It exists so the refusal paragraph
// checks its own claim instead of restating the importer's: a type that was
// refused as "no inferred schema" while appearing in the inferred list is a
// contradiction the report should surface, not hide.
func (r *Report) partitionAgainstInferredTypes(names []string) (inferred, absent []string) {
	have := map[string]bool{}
	for _, t := range r.Types {
		have[t.Type] = true
	}
	for _, n := range names {
		if have[n] {
			inferred = append(inferred, n)
		} else {
			absent = append(absent, n)
		}
	}
	return inferred, absent
}

func sortedGroupKeys(m map[string]*TypeGroup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
