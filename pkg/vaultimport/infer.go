// Omnipus — record-type inference from observed frontmatter (importer HALF 1).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"sort"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHAT IT DELIBERATELY DOES NOT DO
//
// It classifies each observed property of each observed record `type:` into
// one of records.PropertyTypes using records.ParseValue itself as the
// oracle — never a second, hand-rolled parser. A candidate type is only
// assigned when EVERY observed value for that property, across every note of
// that type, parses successfully as that type; the moment even one value
// disagrees, the property is either the next-looser candidate down the chain
// or — if no candidate is unanimous AND the disagreement is not
// coincidental — reported as an AMBIGUOUS INFERENCE and defaulted to `text`
// (the one type every value always parses as, per D3: text is prose, never
// validated for shape).
//
// It does NOT decide `required`/`many` by guessing either. `required` is true
// only when EVERY note of the type carries a non-null, non-empty value for it
// (D3.2/FR-007 treat an explicit null exactly like absence). `many` follows the
// MAJORITY of the notes that carried a value, and the minority is reported
// (AritySplitReport) — it used to be true the moment ANY note wrote a
// sequence, which was the one inference here made from a single observation,
// and it manufactured validation errors: one note writing `tags: [a, b]`
// against three writing `tags: a` declared `many: true` and records.Validate
// then reported an arity error against each of the three.
// ---------------------------------------------------------------------------

// ClassifyKind names which inference rule decided a property's type. It is
// exported so the CLI report can render WHY, not just WHAT.
type ClassifyKind string

const (
	ClassifyRelation  ClassifyKind = "relation" // every value is a wikilink
	ClassifyBoolean   ClassifyKind = "boolean"  // every value folds to true/false -> checkbox
	ClassifyDate      ClassifyKind = "date"     // every value parses as records.TypeDate
	ClassifyInteger   ClassifyKind = "integer"  // every value parses as records.TypeInteger
	ClassifyDecimal   ClassifyKind = "decimal"  // every value parses as records.TypeDecimal
	ClassifyEnum      ClassifyKind = "enum"     // small, repeated, closed vocabulary
	ClassifyText      ClassifyKind = "text"     // the fallback every value accepts
	ClassifyAmbiguous ClassifyKind = "ambiguous_defaulted_to_text"
	// ClassifyDateFromName: NOT ONE value was ever observed for this
	// property anywhere in the vault, so there is no data to classify at
	// all, and its NAME is the only evidence that exists. See
	// nameEvidencedDate for the rule and for why this is the one place in
	// this file a name is allowed to decide anything.
	ClassifyDateFromName ClassifyKind = "date_from_name_no_values_observed"
	// ClassifyDateFromFormula: not one value was ever observed for this
	// property either, but this time the vault DID say what it is — a
	// `.base` file's own formula applies `date()` to it, which is the
	// operator's written statement about his own property rather than this
	// package's reading of English. See TypePropertiesFromBaseFormulas.
	ClassifyDateFromFormula ClassifyKind = "date_from_base_formula_no_values_observed"

	// ClassifyNumberFromBaseSummary: not one value was ever observed for this
	// property, and a `.base` view TOTALS it — an operator asking for a sum
	// over his own property is stating that it holds a number. Same evidence
	// class as ClassifyDateFromFormula, different part of the file.
	ClassifyNumberFromBaseSummary ClassifyKind = "number_from_base_summary_no_values_observed"
	// ClassifyDomainAdopted: not one value was observed for this property on
	// THIS record type, and the record types that did observe it agree on one
	// domain, so this declaration yielded to theirs rather than standing on
	// the `text` fallback. See AdoptObservedDomains.
	ClassifyDomainAdopted ClassifyKind = "domain_adopted_from_observing_types"
)

// Tunable inference thresholds, stated here (not buried in a condition) so a
// reviewer can find and question them in one place.
const (
	// enumMaxDistinct: a property with more than this many distinct folded
	// values is never inferred as an enum — past this point "closed
	// vocabulary" stops being a credible read of the data.
	enumMaxDistinct = 15
	// enumSmallEnough: a distinct-value count at or below this is treated as
	// an enum regardless of repetition — a 2- or 3-way controlled vocabulary
	// is recognisable as one even from a handful of notes.
	enumSmallEnough = 6
	// enumMinAvgRepeat: above enumSmallEnough, a property is an enum only if
	// its values repeat on average at least this many times (total
	// observations / distinct values) — the signal that the vocabulary is
	// actually CLOSED rather than merely short so far.
	enumMinAvgRepeat = 2.0
	// adoptedEnumMaxDistinct: the ceiling for a vocabulary ASSEMBLED BY UNION
	// across record types, which is a different question from the one
	// enumMaxDistinct answers and therefore gets a different answer.
	//
	// enumMaxDistinct asks "is `closed vocabulary` a credible reading of the
	// values THIS type's own notes hold" — a judgement about data, where 15 is
	// generous. A union asks something else entirely: nineteen record types
	// each carry a short, credible `status` vocabulary of their own, and the
	// union of nineteen credible sets is not itself an incredible set, it is
	// just a longer one. Holding a union to the single-type bound refuses
	// every union of more than a handful of types on principle.
	//
	// THE COST OF THIS CEILING, RATIFIED BY THE FOUNDER 2026-09-01 AFTER BEING
	// SHOWN THE EXACT SCHEMA IT WRITES. A record type with no notes gets a
	// vocabulary borrowed from types that are not it: `invoice.status` becomes
	// the 26-value union, which admits `ratified-partial` (a decision's status)
	// and REFUSES `sent` (an invoice's). Until that type has one note of its
	// own, a value outside the union is rejected. The alternative was leaving
	// it `text`, which accepts anything and splits the untyped domain, taking
	// the Inbox-Triage view down with it. He chose the working view over the
	// honest fallback, with the rejection quoted to him.
	//
	// The moment ONE note of such a type exists, its own values decide and this
	// never applies again — AdoptObservedDomains' clause 2, the same "data
	// beats a base file" that contains every other rule in this file.
	adoptedEnumMaxDistinct = 64
)

// PropertyObservation is everything this package saw about one property on
// one record type, across every note of that type.
type PropertyObservation struct {
	Name string
	// DeclaredCount is how many notes of the type WROTE THE KEY AT ALL,
	// whatever they wrote after it — a real value, an explicit null, an
	// empty string, an empty list, a nested mapping. It is deliberately a
	// wider count than PresentNonEmptyCount, and the gap between the two is
	// the evidence that matters for a property nobody ever filled in: 12 of
	// 12 project notes declare `deadline` and every one of them left it
	// blank is a fact about the vault, whereas "0 values observed" alone
	// cannot tell that apart from a property one stray note mentioned once.
	DeclaredCount int
	// PresentNonEmptyCount is how many notes of the type carried a genuine
	// (non-null, non-empty) value — the numerator for `required`.
	PresentNonEmptyCount int
	// ListNotes and ScalarNotes partition the notes that carried a genuine
	// value for this property by the SHAPE they wrote it in — a YAML sequence
	// or a single scalar. Their lengths sum to PresentNonEmptyCount.
	//
	// They replaced a single `Many bool` that was set the moment ANY note
	// wrote a list. That was the one inference in this file made from a single
	// observation, and it manufactured errors: three notes writing
	// `tags: urgent` and one writing `tags: [urgent, legal]` produced
	// `many: true`, and records.Validate then reported an arity error against
	// each of the three — errors created by this importer's own guess, about
	// notes it had read. The majority decides now, and the minority is
	// REPORTED (AritySplitReport) rather than left for the operator to
	// discover as three findings with no stated cause.
	ListNotes   []string
	ScalarNotes []string
	// Values is every individual scalar value observed (a many-valued
	// property contributes each element), each tagged with the note it came
	// from so an ambiguity report can name real examples.
	Values []observedValue
	// TemplateNotes are the `type: template` notes that declared this key
	// for this record type without any note of the type carrying it — see
	// applyTemplateDeclarations. It is PROVENANCE, not a count: a property
	// resting on a template rather than on notes is a different claim, and
	// the report has to be able to say which one it is making. Empty for
	// every property real notes declare, which is almost all of them.
	TemplateNotes []string
}

// AritySplitReport is one property whose notes disagreed about whether it
// holds a single value or a list.
type AritySplitReport struct {
	RecordType string
	Property   string
	// Many is what this run declared — the majority shape.
	Many bool
	// ListCount and ScalarCount are how many notes wrote each shape.
	ListCount   int
	ScalarCount int
	// Examples names up to three notes in the MINORITY: the ones that will be
	// reported as an arity error against the schema this run wrote.
	Examples []string
}

type observedValue struct {
	Text string
	// Block reports that this value was written as a YAML BLOCK scalar (`|`
	// or `>`). It is carried because records.parseLinkValue REFUSES a block
	// scalar as a wikilink by name under FR-030a — its raw source is a block
	// indicator and an indented body, which is not wikilink syntax — and a
	// value's Text alone cannot tell. Without it isWikilink was a SECOND
	// ORACLE that disagreed with the validator: `company: |` + `[[Acme]]`
	// inferred `type: relation` and then failed validation on every such
	// note, an error the importer manufactured about its own inference.
	Block    bool
	NotePath string // vault-relative, for reporting
}

// TypeGroup is every note this package saw with one declared record `type:`.
type TypeGroup struct {
	Type      string
	NoteCount int
	NotePaths []string // vault-relative
	PropOrder []string
	Props     map[string]*PropertyObservation
}

// NoteRecord pairs a parsed records.Record with its provenance.
type NoteRecord struct {
	AbsPath string
	RelPath string
	Rec     records.Record
}

// NameIndex resolves a wikilink TARGET (the note title Obsidian links write,
// e.g. `[[Acme Corp]]`) to the record type(s) of the note(s) that title
// could plausibly mean — built once, from every scanned note, so relation
// `to:` inference has real link targets to look up rather than the type
// group's own notes only (a relation typically points OUT of its group).
type NameIndex struct {
	// byStem maps a folded note title (filename without extension) to every
	// type it was observed with. More than one entry means two notes share a
	// title in different folders — Obsidian itself is ambiguous there too.
	byStem map[string][]string
}

// InferredProperty is one property's inferred schema declaration, plus the
// evidence behind it — the evidence is what makes an ambiguous case
// reportable instead of silent.
type InferredProperty struct {
	Name     string
	Type     records.PropertyType
	Many     bool
	Required bool
	// To is the relation/person target type, when a majority was found.
	To string
	// EnumValues is the closed set, folded-sorted, for TypeEnum.
	EnumValues []string
	Kind       ClassifyKind

	// ObservedCount is how many notes of this type carried a genuine
	// (non-null, non-empty) value for this property — PropertyObservation's
	// PresentNonEmptyCount, carried forward rather than recomputed.
	//
	// It is NOT part of the declaration and never reaches the written
	// schema file (schema_write.go's renderPropertyDecl names the fields it
	// emits, and this is not one of them). It is EVIDENCE, and it exists
	// for exactly one consumer: FR-104b's best-fit tie-break, which needs
	// to know how TYPICAL a property is for its type. `required` cannot
	// answer that — it is a single bit, and the difference between a
	// property 94% of a type's notes fill in and one 1% of them fill in is
	// the whole signal that separates two types a note's key set alone
	// cannot.
	//
	// A caller that builds InferredProperty values by hand leaves this
	// zero, and the tie-break then scores every candidate 0 and declines to
	// break the tie. That is the safe direction — an unweighted schema set
	// produces the old "left as is, it is a coin toss" outcome, never a
	// guess made on absent evidence.
	ObservedCount int

	// Ambiguity is set when this property's type was NOT a unanimous match
	// and had to be defaulted to text — the honesty-contract payload.
	Ambiguity *AmbiguousInference
	// NameEvidenced is set when the vault held NO value for this property
	// anywhere and its type was read from its NAME instead. It is the
	// honesty payload for the one decision in this package made without a
	// single observation behind it; see classifyWithNoValues.
	NameEvidenced *NameEvidencedInference
	// RelationSplit is set when a relation's targets did not converge
	// unanimously on one record type (or converged on none at all) —
	// reported rather than left silent, whatever `to:` ended up being.
	RelationSplit *RelationSplitReport
	// AritySplit is set when the notes of this type disagreed about whether
	// this property holds one value or a list. `many:` follows the majority
	// and the minority is named here.
	AritySplit *AritySplitReport
	// FormulaEvidenced is set when the vault held NO value for this property
	// anywhere and its type was read from a `.base` file's own FORMULA
	// instead — a stronger evidence class than NameEvidenced, and reported
	// separately for that reason. See TypePropertiesFromBaseFormulas.
	FormulaEvidenced *FormulaEvidencedType
	// EnumWidened is set when this property's closed set met a literal the
	// operator's own `.base` files filter on and no note carries — whether
	// the set grew or the growth was refused at enumMaxDistinct. See
	// WidenEnumsFromBases.
	EnumWidened *EnumWidening
	// DomainAdopted is set when no note of THIS record type carried a value
	// for this property and its type was taken from the record types that DID
	// observe it. It is the honesty payload for the one decision in this
	// package made on another record type's evidence; see AdoptObservedDomains.
	DomainAdopted *AdoptedDomain
	// DomainAdoptionDeclined is set when that same rule found an agreed
	// observed domain and REFUSED to adopt it, because the enum vocabulary it
	// would have declared is past enumMaxDistinct. The property keeps a type
	// nothing observed, which is precisely why the refusal is reported.
	DomainAdoptionDeclined *DeclinedAdoption
}

// AmbiguousInference is one property this package refused to classify
// silently.
type AmbiguousInference struct {
	RecordType   string
	Property     string
	BestType     records.PropertyType
	MatchFrac    float64
	TotalValues  int
	MatchedCount int
	// Examples names up to 3 (path, value) pairs that did NOT match
	// BestType — the concrete evidence a human reviews.
	Examples []AmbiguousExample
}

type AmbiguousExample struct {
	NotePath string
	Value    string
}

// RelationSplitReport is one relation property whose link targets did not
// converge UNANIMOUSLY on one record type — a supermajority with a named
// minority, a genuine mix with no majority at all, or total non-resolution.
//
// FR-104a (founder ruling, ADR-068 revision 13 D24.6 ruling 3) is what this
// type reports on, and the shape of the report is the requirement: a
// supermajority declares `to:` AND names the minority, because those
// minority links ARE type mismatches and D5/FR-034's
// `relation_type_mismatch` finding is the right place for them to surface.
type RelationSplitReport struct {
	RecordType string
	Property   string
	// ByType is target-type -> count of resolved links, sorted for display
	// by the caller. Empty when nothing resolved at all.
	ByType map[string]int
	// ResolvedTotal is every link that resolved to SOME record type.
	ResolvedTotal int
	// LinkTotal is every wikilink value observed for this property —
	// resolved, resolved-to-a-non-record, and dangling alike. IT IS THE
	// DENOMINATOR THE 2/3 TEST USES, and the choice is deliberate; see
	// inferRelationTarget's header for the reasoning and for the reading of
	// FR-104a it turns on.
	LinkTotal int
	// Unresolved is how many link targets matched no known note at all.
	Unresolved int
	// AmbiguousLinks is how many links resolved to MORE THAN ONE record type,
	// because two notes in different folders share the linked title. Such a
	// link is evidence for no type at all — which target the operator meant is
	// the one thing this package cannot recover — so it is excluded from every
	// numerator and reported here instead of silently voting for whichever
	// type sorts first.
	AmbiguousLinks int
	// MajorityType and MajorityCount are the winning target type and its
	// count. MajorityType is empty when no type reached the threshold.
	MajorityType  string
	MajorityCount int
	// StrictFrac is what the majority's share WOULD have been counting only
	// resolved targets — the narrower reading of FR-104a's wording. It is
	// carried so the report can show both numbers and nobody has to take
	// this package's word for which reading was applied.
	StrictNumerator   int
	StrictDenominator int
	// Minority names every resolved target type OTHER than MajorityType
	// with its count, sorted — FR-104a's "minority reported by name". When
	// no majority was reached this holds every resolved type, which is the
	// whole evidence set the operator needs to choose from.
	Minority []string
	// Rule names WHICH of FR-104a's branches decided this property, so a
	// reader is never left inferring the rule from the numbers.
	Rule RelationRule
	// Declared is what this property ended up declared as: "relation" (a
	// unanimous or supermajority target existed, so FR-034's mandatory
	// `to:` has real evidence behind it) or "text" (the evidence was
	// genuinely mixed or absent — schema.go's finalize() REJECTS a relation
	// with `to: ""` outright, so a relation with no majority cannot be
	// declared a relation at all).
	Declared string
	// Remedy is the one-line knowledge_configure edit that settles the
	// question once the operator decides. Set only when Declared == "text";
	// FR-104a requires the report to NAME the fix, not merely the problem.
	Remedy string
}

// RelationRule names which FR-104a branch decided a relation's `to:`.
type RelationRule string

// NameEvidencedInference is one property this package typed from its NAME
// because the vault held no value for it anywhere — the honesty payload for
// classifyWithNoValues, and the thing that keeps a name-based guess from
// being a silent one.
type NameEvidencedInference struct {
	RecordType string
	Property   string
	// Type is what the name was read as. Only records.TypeDate is ever
	// produced today; the field is here so a reader of the report does not
	// have to know that.
	Type records.PropertyType
	// DeclaringNotes is how many notes of the record type wrote the key at
	// all — every one of them leaving it blank, which is the whole reason
	// this branch ran.
	DeclaringNotes int
	// DeclaringTemplates are the `type: template` notes that named this
	// property for this record type when NO note of the type wrote the key
	// at all (applyTemplateDeclarations). It is the second legitimate
	// history a name-evidenced guess can have, and it exists because the
	// first one — "notes declared it and left it blank" — became false for
	// some entries the moment templates were read, and a report that keeps
	// asserting it is a report that lies about its own evidence.
	DeclaringTemplates []string
}

// ---------------------------------------------------------------------------
// WIDENING AN INFERRED ENUM FROM A `.base` FILE
//
// An inferred enum's closed set is the DISTINCT VALUES OBSERVED. That makes
// it a statement about what the vault currently holds, and the operator's
// `.base` files routinely make a different one: `status == "doing"` against a
// `task.status` inferred as (blocked, done, open, todo). No note carries
// `doing`, so buildV2LeafNode refuses the literal, the clause is DROPPED, and
// a dropped conjunct matches more rows than the original — so FR-105 disables
// the whole view. On the founder's vault that cost four views across four
// bases, none of them for a reason he would recognise as a reason.
//
// THIS IS THE SAME MOVE FR-018d MADE, AND THE SAME MOVE THE TEMPLATE RULE
// ABOVE MAKES. A `.base` file naming a record type no note carries declares
// that type. A template naming a property no note carries declares that
// property. A base FILTERING on a value no note carries declares that value.
// In each case the operator wrote the file, so the file is evidence.
//
// THE AMBIGUITY, STATED PLAINLY. A base filtering on a value no note carries
// is genuinely two things at once: the operator filtering for a state that
// has not occurred yet, or the operator's typo. This package cannot tell them
// apart and does not pretend to.
//
// It does not have to, and that is the whole argument for widening rather
// than refusing: IF THE LITERAL IS A TYPO, OBSIDIAN'S OWN VIEW ALSO RETURNS
// NOTHING. Widening reproduces the original exactly under BOTH readings — a
// real unoccurred state and a slip alike — where refusing reproduces it under
// neither. So this is not a coin toss between two translations. It is the
// faithful translation, and the only open question is whether the operator is
// TOLD, which is what EnumWidening and its ReportLines exist for.
//
// Refusing is also not the cautious option it looks like. It costs a working
// view for a state that occurs next week, and it tells the operator less: a
// disabled view names the clause, but a widened one names the clause, the
// base file, the observed values, and the nearest declared spelling.
//
// WHY IT CANNOT REJECT A NOTE. Every admitted value is by construction one
// that canonicalEnumValue could not already resolve, i.e. one NO note of the
// type carries. Validation of the current vault is therefore bit-identical
// before and after: the acceptance bar (a note this run typed is never
// reported invalid by the same run) is preserved by monotonicity, not by
// luck. A larger `values:` list can only ever accept more.
//
// WHY IT CANNOT REORDER ANYTHING. records.comparisonDomain maps TypeEnum onto
// TypeText — "an enum compares as the text it is" — so enum ordering is
// LEXICAL on the value, never ordinal by position in `values:`. Appending to
// the list cannot change the result of any comparison over any note's stored
// value. Had enums compared by declaration order, this rule would be unsafe
// and would have to be abandoned; it is worth knowing which fact it rests on.
//
// WHY IT CANNOT BROADEN A VIEW (FR-105), INCLUDING UNDER A NEGATION. This is
// the obligation a leaf-level argument misses, and one was got wrong on this
// branch today: a rewrite that NARROWS a leaf BROADENS the view when the leaf
// sits under a `not:`, because a subset's complement is a superset.
//
// Widening survives that for a reason a narrowing rewrite cannot borrow: the
// translated clause is EQUIVALENT to Obsidian's, not a subset of it.
// `status == "doing"` in Obsidian matches the records whose status is
// `doing`; the widened enum emits `status = doing`, which matches the same
// records. Equivalence is preserved by complement — if A = B then ¬A = ¬B —
// so the clause is faithful in BOTH polarities and needs no polarity proof.
// Contrast `prop != ""` -> `IS NOT NULL`, which is a genuine subset relation
// (`IS NOT NULL` does not have Obsidian's present-but-empty case) and is
// therefore refused under a `not:`. The difference is exactly equivalence
// versus containment, and it is why this rule needs no narrowingProof.
//
// Note also what does NOT happen here: no new translation path is created. An
// admitted literal takes the identical buildV2LeafNode branch its already-
// declared siblings take — after this rule, `status == "doing"` is emitted
// exactly as `status == "blocked"` next door in the same base already was.
// The rule moves a literal from "refused" into a class whose semantics this
// importer already stands on.
//
// TWO HONEST LIMITS ON THAT EQUIVALENCE, NEITHER INTRODUCED HERE.
//
//   - canonicalEnumValue matches with records.FoldKey, so the emitted clause
//     is case-insensitive where Obsidian's `==` is not. That is PRE-EXISTING
//     for every enum literal this importer already translates, and it is not
//     caused by widening — it is stated here only so a later reader does not
//     mistake this rule for its source.
//   - Widening never rescues a clause refused for some OTHER reason. A
//     `.contains` on a scalar enum, or an ordering operator on a many
//     property, is refused whatever the closed set holds; admitting its
//     literal would change a schema the operator reads without changing one
//     row of one view. leafAssertsEnumMembership is aligned with
//     buildV2LeafNode precisely so this cannot drift apart.
//
// WHERE IT STOPS. enumMaxDistinct is the count above which classifyProperty
// declines to call a property an enum at all. A base naming enough unknown
// literals to push the set past that bound is not evidence for a wider enum —
// it is evidence the INFERENCE was wrong about the property. So the widening
// is refused wholesale, the observed set is left EXACTLY as the notes made it,
// and the refusal is reported. It is refused wholesale rather than partially
// because a partial widening would admit an arbitrary subset of the
// operator's own literals, which is a worse answer than either extreme.
// ---------------------------------------------------------------------------

// EnumWidening is the account of one property's closed set meeting the
// literals the operator's `.base` files filter it against — whether the set
// grew or the growth was refused. It is the honesty payload for this rule, on
// the same contract as AmbiguousInference and NameEvidencedInference: a guess
// is acceptable when it is REPORTED and correctable in one edit.
type EnumWidening struct {
	RecordType string
	// Property is the enum property the literals were compared against.
	Property string
	// Observed is the closed set as inference left it, before this rule.
	Observed []string
	// Requested is every literal the bases named that the observed set did
	// not already contain, folded-deduplicated and sorted.
	Requested []string
	// Added is what actually joined the set. It equals Requested on a
	// widening and is EMPTY on a refusal, so a reader never has to consult
	// Refused to know what changed.
	Added []string
	// Refused is set when admitting Requested would have pushed the set past
	// Bound, so nothing was added at all.
	Refused bool
	// Bound is the ceiling this widening was judged against — enumMaxDistinct
	// for a vocabulary this type's own notes produced, adoptedEnumMaxDistinct
	// for one it adopted from other types. Carried rather than recomputed at
	// render time because the message quotes it to the operator, and a refusal
	// naming the wrong ceiling sends him to correct a limit never applied.
	Bound int
	// Bases names every `.base` file that filtered on one of these values,
	// sorted — the operator has to be told which file to go and look at.
	Bases []string
	// Nearest maps a requested literal to the OBSERVED value within one edit
	// of it, when there is one. It is the typo check, handed to the operator
	// rather than guessed at here.
	Nearest map[string]string
}

func findInferredProperty(props []InferredProperty, name string) (InferredProperty, bool) {
	if i := indexOfProperty(props, name); i >= 0 {
		return props[i], true
	}
	return InferredProperty{}, false
}

func indexOfProperty(props []InferredProperty, name string) int {
	for i := range props {
		if props[i].Name == name {
			return i
		}
	}
	return -1
}

// sortedStringKeys returns a map's keys in lexical order, so every list this
// rule produces is identical between two runs over the same vault.
func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// TYPING A PROPERTY FROM A `.base` FILE'S OWN FORMULA
//
// THE EVIDENCE, in the founder's vault, verbatim. `CRM.base` declares:
//
//	days_since_refresh: if(last_refreshed, (today() - date(last_refreshed)).days, "")
//
// and `last_refreshed` is inferred `text`, because ONE note of the whole vault
// declares the key and that note leaves it blank. Two of the view "Entities —
// Refresh Due"'s three clauses are then lost and the view is DISABLED:
// `last_refreshed != ""` has no faithful translation on a text property
// (FR-007a keeps `""` a PRESENT value there, so `IS NOT NULL` would BROADEN),
// and `formula.days_since_refresh > 365` fails to type because translate.go's
// W2 guard-dropping rewrite is proved for a scalar `date` and for nothing else.
//
// BOTH LOSSES ARE THE SAME ROOT CAUSE, and the operator already answered the
// question the run is guessing at: he wrapped the property in `date()`. No
// note carries a value, so the notes CANNOT say what the type is — the base
// can, and it does.
//
// WHY `date(x)` IS EVIDENCE AND MOST FUNCTIONS ARE NOT. The bar is the one
// dateNameExact states for names and it is the same bar: a wrong declaration
// REJECTS the operator's first real note, which is worse than the lost filter
// the rule exists to recover. So the shape has to be one under which the
// property has exactly ONE reading — for a call, one whose every accepted
// operand type denotes the SAME concept; for an operator, one the pinned
// grammar types over a single domain. Both kinds appear below, marked
// ADMITTED, and every other shape considered is marked REJECTED with why:
//
//	date(x)    ADMITTED. It is a date CONSTRUCTOR. inferCall accepts exactly
//	           `text` and `date` for it, and `text` is accepted there as the
//	           SERIALISED SPELLING of a date, not as a second meaning — there
//	           is no reading of `date(x)` under which x is a quantity, a state
//	           or a piece of prose. Calling it is the operator saying "this
//	           holds a calendar date".
//	time(x)    ADMITTED, on strictly stronger ground than its sibling.
//	           inferCall accepts ONLY `date` for it, so a text receiver is a
//	           type error the operator would already have been shown. It fires
//	           on nothing in the founder's vault, and that is deliberately not
//	           the test applied here: `date` and `time` are the same
//	           conversion written two ways, and admitting the LOOSER of the
//	           pair while refusing the STRICTER would be a rule about which
//	           spelling the operator happened to pick. Which one carried a
//	           declaration is recorded on FormulaEvidence.Function, so the
//	           report never has to say `date()` about a `time()`.
//
// REJECTED, each with the reason:
//
//	format(x), link(x), icon(x)
//	           accept ANYTHING (inferCall returns `presentation` without
//	           inspecting the operand). Evidence of nothing.
//	contains(x, y), file.hasLink(x)
//	           defined over more than one operand type, and those types mean
//	           genuinely different things. Not evidence.
//	if(P, …)   THE ONE THAT LOOKS LIKE EVIDENCE AND IS NOT. It is the whole
//	           reason these views are disabled, so it is tempting to read the
//	           guard as a declaration. It is not: Obsidian reads a bare `P`
//	           there as a JavaScript truthiness test, which is exactly as
//	           meaningful over a string, a number or a checkbox. Typing P from
//	           its appearance in a guard would be typing it from the fact that
//	           the operator tested whether it was filled in.
//	today() - x, x - today(), and the same two with now()
//	           ADMITTED, and this entry USED TO SAY "deliberately NOT
//	           implemented". What changed is a measurement, not a judgement,
//	           so both are recorded here.
//
//	           THE READING. `today()` and `now()` are ZERO-ARGUMENT date
//	           constructors: inferCall returns `date` for them with no operand
//	           to look at. And inferBinary defines `-` over exactly two
//	           domains — `date - date`, the only producer of a duration, and
//	           number-minus-number. There is no `date - number`. So an
//	           expression in which a bare property is subtracted from
//	           `today()`, or `today()` from it, TYPE-CHECKS IF AND ONLY IF
//	           that property is a date. The operator wrote an expression with
//	           exactly one reading, which is the same thing `date(x)` is and
//	           the bar this section sets.
//
//	           WHY THE OLD (a) NO LONGER APPLIES. It said `-` "is defined over
//	           numbers as well as dates — so the rule only exists once the
//	           OTHER operand has been proved a date, which is a second type
//	           inference carrying its own failure modes". That is true of `-`
//	           in general and false of the shape actually admitted here: the
//	           other operand is not inferred, it is REQUIRED TO BE THE
//	           SYNTACTIC TOKEN `today()` or `now()` — a call with zero
//	           arguments, whose type is a constant of the pinned grammar.
//	           Nothing is proved about it because there is nothing in it to
//	           prove. A property subtracted from another PROPERTY carries no
//	           evidence and is not matched, exactly as (a) demanded.
//
//	           WHY THE OLD (b) NO LONGER HOLDS. It said the rule "would
//	           recover nothing here", because the properties these formulas
//	           subtract "are either already typed `date` from hundreds of real
//	           ISO values or not declared on the record type at all". The
//	           second half stopped being true: `candidate` is now DECLARED
//	           FROM `Hiring.base` with its properties taken from the
//	           operator's own `03-Reference/Ops-Templates/Template —
//	           candidate.md`, so `candidate.created` is a declared, value-less
//	           `text` property — through clause 1, not refused by it. The
//	           measurement today, over all 18 base files: three formulas
//	           subtract a bare property from `today()` (`Deals.age`,
//	           `Inbox-Triage.age`, `Hiring.age_in_stage`), and the rule fires
//	           on exactly ONE of them. `Deals.age` names `deal.created`, which
//	           hundreds of real ISO values already typed `date` — clauses 2
//	           and 3 both refuse it. `Inbox-Triage.age` sits in an UNTYPED
//	           view, so no record type owns the name and no evidence is
//	           attributed. `Hiring.age_in_stage` names `candidate.created` and
//	           that one is recovered, which is what returns the
//	           `formula.age_in_stage` column to the Hiring view.
//
//	           BOTH OPERAND ORDERS, on the precedent `time()` set one entry
//	           up. `today() - P` and `P - today()` are one declaration written
//	           two ways; only the first spelling appears in this vault. The
//	           second is admitted anyway for the reason `time()` is: refusing
//	           it would make the rule about which order the operator happened
//	           to write, which is not a fact about his data. `now()` joins
//	           `today()` on the same terms — inferCall types the pair
//	           identically, in one `case` arm.
//
//	           WHAT IS STILL REFUSED, so the widening is bounded: the property
//	           must be BARE on its side (`today() - date(x)` is already the
//	           `date()` rule, and `today() - (a + b)` is about an expression),
//	           the constructor must take zero arguments as written, and every
//	           one of the four containment clauses below applies to this shape
//	           unchanged. `-` between two properties, and every other
//	           arithmetic operator, carry nothing.
//	(a - b).days, x.hours, …
//	           REFUSED one level further down, for the same reason. A duration
//	           accessor proves its RECEIVER is a duration, and in every real
//	           formula that receiver is a subtraction rather than a bare
//	           property — so the accessor adds nothing the `-` rule above did
//	           not already have to prove. `P.days` on a bare property appears
//	           nowhere in this vault.
//	date(P) INSIDE A VIEW FILTER, e.g. `date(close_date).year == today().year`
//	           OUT OF SCOPE, deliberately. This rule reads a base's
//	           `formulas:` block and nothing else. The one clause of that
//	           shape in this vault is refused by the grammar anyway (`.year`
//	           is outside FR-143's pinned snapshot), so admitting it would
//	           change no view; and a filter clause is somewhere the translator
//	           already forms its own judgement, which is exactly where a
//	           second reader of the same text starts to disagree with it.
//
// THE FOUR CONTAINMENT CLAUSES, which are what make this safe rather than
// merely plausible. Every one is checked in typeEligibleForFormulaEvidence:
//
//  1. THE PROPERTY MUST ALREADY BE DECLARED for the record type. This rule
//     never invents a property — creating a vocabulary and re-reading one are
//     different questions, and the second belongs to FR-018d provisioning and
//     to the template rule. (Same clause WidenEnumsFromBases states.)
//  2. NO NOTE THIS RUN WILL VALIDATE AS THE RECORD TYPE MAY CARRY A VALUE FOR
//     IT. This is the clause that makes the invariant hold rather than merely
//     be hoped for: `date` is a STRICTER declaration than `text`
//     (records.TypeDate accepts six ISO layouts; `text` accepts every string),
//     so it can only invalidate a note through a value — and there are none.
//     A property with values was typed FROM those values, and data beats a
//     base file exactly as data beats a name.
//
//     IT IS ASKED OF THE NOTES, NOT OF `InferredProperty.ObservedCount`, AND
//     THE DIFFERENCE IS THE WHOLE CLAUSE. ObservedCount is frozen by
//     CollectTypeGroups at the top of Run; FR-104b's InferTypesForUntypedNotes
//     then writes `type:` into untyped notes AFTER inference and BEFORE
//     validation, so a note can JOIN a record type carrying values inference
//     never counted. Gating on the frozen count reads "no values" off a
//     population that is not final yet, and this vault has 27 untyped notes
//     feeding that path. The promotion would then make a note invalid against
//     the schema the SAME run wrote — the one outcome this package admits no
//     exception to. noValueJoinsRecordType asks the final population instead;
//     TestFormulaEvidence_ANoteThatJOINSTheTypeIsSeenByTheGate forces the
//     frozen-count version to fire so its zero is measured, not assumed.
//  3. IT MUST CURRENTLY BE `text`. The rule only ever strengthens the
//     fallback. It never overrules `enum`, `relation`, `checkbox` or an
//     already-inferred `date`, and it never fires twice.
//  4. IT MUST BE SINGLE-VALUED. `many` is decided by the arity of what the
//     notes wrote, and translate.go's W2 excludes a `many` date on its own
//     argument (an empty list is present-and-falsy in JavaScript). A rule that
//     produced a `many` date would be handing W2 a case it refuses anyway.
//
// AND THE DIRECTION THAT IS NOT ABOUT NOTES: can a filter that was REFUSED now
// translate to something WIDER than Obsidian? The only clause shape whose
// translation changes is `P != ""` / `P == ""`, which FR-007a re-reads on a
// date as `IS NOT NULL` / `IS NULL`. Clause 2 settles it: with zero values,
// `IS NOT NULL` matches NO record of the type, so the imported view returns
// zero rows there — at or below the Obsidian original on every reading of it,
// which is the FR-105 direction that matters. It is checked on the real vault
// against the independent expected-row-set oracle, not argued here.
//
// WHERE THIS RUNS: after every schema is inferred and provisioned, and BEFORE
// writeSchemas and NewSchemaIndex — the same position, for the same reason,
// that WidenEnumsFromBases occupies. A type that reached the index but not the
// written schema would let the clause translate and then be refused by
// `records` at query time: a view that imports clean and returns an error.
// ---------------------------------------------------------------------------

// FormulaEvidence is one `.base` formula that carried a type declaration.
type FormulaEvidence struct {
	// Op is set ONLY when this evidence is a view's `summaries:` entry rather
	// than a `formulas:` expression. It is what makes ReportLines able to
	// describe the two in the operator's own vocabulary — he wrote `Sum`
	// under `summaries:`, not a formula, and being told "reads `amount`
	// through sum()" would send him looking for a call he never wrote.
	Op string
	// Base is the `.base` file's path, relative to the vault root.
	Base string
	// Formula is the key in that file's `formulas:` block.
	Formula string
	// Source is the expression EXACTLY as the operator wrote it, so the
	// report quotes his text rather than this package's rewrite of it.
	Source string
	// Function is the grammar term that carried the declaration: `date` or
	// `time` when a CONVERSION was applied to the property, `today` or `now`
	// when the property was SUBTRACTED from (or had subtracted from it) a
	// zero-argument date constructor. The four spellings are the union of
	// dateEvidencingFunctions and dateSubtrahendFunctions, and the two maps
	// are disjoint, so this one field says which of the two shapes fired
	// without a second flag that could disagree with it.
	//
	// It is recorded because the report names the text the founder is being
	// asked to check, and a report that says `date()` about a `time()` — or
	// about a subtraction, which is not a call at all — sends him looking for
	// text that is not in his file. FormulaEvidencedType.ReportLines branches
	// on it for exactly that reason.
	Function string
}

// FormulaEvidencedType is one property this package typed from a `.base`
// file's own formula because the vault held no value for it anywhere.
//
// It is the honesty payload for this rule, on the same contract as
// AmbiguousInference, NameEvidencedInference and EnumWidening: a guess is
// acceptable when it is REPORTED and correctable in one edit. It is a
// DIFFERENT and stronger evidence class than NameEvidencedInference — a name
// is this package's reading of English, a formula is the operator's own
// written statement about his own property — and it is reported separately so
// the founder can see which of the two decided each type.
type FormulaEvidencedType struct {
	RecordType string
	Property   string
	// Type is what the base file was read as saying: records.TypeDate from a
	// `formulas:` expression, records.TypeDecimal from a `summaries:` total.
	Type records.PropertyType
	// Was is the declaration this replaced — always records.TypeText, by
	// clause 3, and carried so the report states the change rather than only
	// the outcome.
	Was records.PropertyType
	// Evidence is every statement in a `.base` file that typed this property
	// under a view resolving to this record type — a formula applying `date()`
	// to it, or a view totalling it under `summaries:` — sorted by base then
	// formula. All of them are named: the founder overrules the type in one
	// edit, but he fixes a MISTAKEN formula in the base file, and he cannot
	// do that without being told which file and which key.
	Evidence []FormulaEvidence
}

// valuedPropertiesByType answers containment clause 2 over the FINAL note set:
// for each record type, which of its properties any note carries a genuine
// value for.
//
// THE POPULATION IS THE POINT. It reads `notes` as they stand when the rule
// runs, which is AFTER FR-104b's InferTypesForUntypedNotes has written `type:`
// into the untyped notes it could decide. Those notes are validated by this
// same run, so they can be invalidated by this rule, so they have to be
// counted by it. `InferredProperty.ObservedCount` cannot answer this: it is
// frozen by CollectTypeGroups before those notes had a type at all.
//
// "A genuine value" is decided by collectNodeValues — the same function
// CollectTypeGroups counts through — rather than re-tested here. The empty
// string, the explicit null, the empty list and the nested mapping all have
// settled dispositions in that function, and a second copy of them is how two
// answers to one question start to disagree.
//
// Property names are FOLDED. The inferred schema keys on the spelling the
// notes used, so a note writing `Last_Refreshed:` against a schema saying
// `last_refreshed` would slip an exact match — and slipping this gate means
// being promoted, which is the direction that can invalidate a note.
func valuedPropertiesByType(notes []NoteRecord) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for i := range notes {
		rec := notes[i].Rec
		rt := rec.TypeName()
		if rt == "" {
			// An untyped note is validated as no record type at all, so no
			// schema this rule can change is applied to it.
			continue
		}
		for _, key := range rec.Frontmatter.Keys {
			var po PropertyObservation
			po.Name = key
			collectNodeValues(&po, rec.Frontmatter.Values[key], notes[i].RelPath)
			if po.PresentNonEmptyCount == 0 {
				continue
			}
			byProp := out[rt]
			if byProp == nil {
				byProp = map[string]bool{}
				out[rt] = byProp
			}
			byProp[records.FoldKey(key)] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// PRECEDENCE BETWEEN EVIDENCE CLASSES: A DECLARATION NOTHING WAS OBSERVED FOR
// YIELDS TO ONE THE DATA MADE
//
// THE DEFECT, MEASURED IN THE FOUNDER'S VAULT RATHER THAN ASSERTED. His
// Inbox-Triage base is UNTYPED, and knowledge_find refuses an untyped query
// whose property name resolves in two comparison domains at once. Three of its
// four clauses were lost to exactly that refusal, and the report named the
// pair `area` declares `created` date / `brand-kit` declares it text. Look at
// what stands on each side of that "conflict":
//
//   - THIRTY-ONE record types declare `created` as a date, on 700-odd notes
//     that actually hold a date.
//   - FIVE declare it `text`: brand-kit, compliance, connected-account,
//     invoice and round. Every one of them is a record type FR-018d
//     provisioned from a `.base` file. Not one has a single note. Not one has
//     a single observed value for `created`, or for anything else.
//
// `text` is not a reading of those five types' data. It is this package's
// FALLBACK, chosen because there was nothing to read — and provisioning has no
// values to read by construction, so it types every property it declares
// `text` and cannot do otherwise. That fallback then stood as an equal partner
// to seven hundred observations and split the domain, and a view the founder
// wrote lost its filter, its column and its formula to a disagreement that had
// no data on one side of it.
//
// THE RULE. Where a record type's declaration of a property rests on NO
// observation at all, and the record types that DID observe that property
// agree on one domain, the unobserved declaration adopts the observed one.
//
// It is the same precedence this package already applies twice, one level
// down. A template may declare a property NAME but "never touches a property
// real notes carry" (applyTemplateDeclarations). A base formula may type a
// property only when no note of the type carries a value for it —
// typeEligibleForFormulaEvidence's clause 2, written down as "data beats a
// base file, as data beats a name". This is that same sentence applied ACROSS
// record types, which is the level the untyped-query domain lives at.
//
// THE FOUR CLAUSES ON THE SIDE BEING CHANGED (adoptionEligibleTarget)
//
//  1. The property must already be declared on that type. This never invents a
//     property; it only ever settles the type of one that is already there.
//  2. NO note of that type may carry a genuine value for it, judged over the
//     FINAL note set by valuedPropertiesByType — the same population and the
//     same collectNodeValues disposition of null / "" / [] / mapping that the
//     formula rule uses. This is the clause that makes the change incapable of
//     invalidating a note: with no value present there is nothing for a
//     tighter type to reject. FR-007a is what makes that airtight rather than
//     merely likely — an empty string is ABSENT on every non-text type, so a
//     note that wrote `status:` or `status: ""` conforms to `enum` exactly as
//     it conformed to `text`.
//  3. The current declaration must be `text`. Adoption only ever strengthens
//     the FALLBACK. It never overrules `enum`, `date`, `relation`, `checkbox`,
//     or a name- or formula-evidenced type — those are answers this package
//     already had a reason for, and two rules overwriting each other is how a
//     schema stops being explicable.
//  4. Neither side may be `many`. Adoption changes the TYPE and never the
//     arity: an empty sequence (`tags: []`) carries no value but is
//     unambiguous arity evidence, and it is the one shape clause 2 cannot see.
//
// THE THREE CLAUSES ON THE SIDE BEING BELIEVED (observedDomainFor)
//
//  5. At least one record type must have OBSERVED VALUES for the property.
//     A second unobserved declaration is not evidence — it is the same absence
//     of evidence wearing a different type name.
//  6. Every observing type must agree, under the ENGINE'S OWN domain test
//     (sameInferredDomain: declared type, arity, and relation target). Where
//     the observations disagree among themselves there is no single answer to
//     adopt, and inventing one would be this package choosing a winner between
//     two things the founder actually wrote.
//  7. An enum domain must still be a CREDIBLE CLOSED SET once unioned. The
//     engine unions every in-scope type's declared values for an untyped name
//     (knowledgefind's untypedProperty), so the union is the set the adopting
//     type would really be declaring — and enumMaxDistinct is already this
//     package's stated bound on when "closed vocabulary" stops being a
//     credible read of the data. WidenEnumsFromBases refuses past that same
//     bound for the same reason, in its own words: past it, a large set "is
//     evidence the INFERENCE was wrong about the property".
//
// WHAT CLAUSE 7 COSTS ON THIS VAULT, STATED RATHER THAN HIDDEN. It DECLINES
// the `status` adoption, which is the one the founder most wanted. Nineteen
// record types observe `status` as an enum — and they observe NINETEEN
// DIFFERENT VOCABULARIES that share a name: task's (blocked, doing, done,
// open, todo), decision's (accepted, compiled, proposed, ratified,
// ratified-partial, superseded), content's (archived, published, scheduled),
// and so on to a union of TWENTY-SIX distinct values. Adopting that union onto
// `invoice` and `compliance` would type a property with no notes behind it
// from a set this package's own threshold says is not a vocabulary — and it
// would be MEASURABLY worse: Finance-AR filters `status != "paid"` and
// `status != "written-off"` and Compliance filters `status != "filed"`, none of
// which the union contains, and widening cannot admit them because 26 + 2 is
// already past the bound. Three views that convert cleanly today would lose
// their row-set filters and be disabled. So the `status` clause of Triage
// Queue stays a named loss, and it stays one for the reason above rather than
// for the alphabetical accident the message used to report.
// ---------------------------------------------------------------------------

// AdoptedDomain is one property whose type this run took from the record types
// that observed it, because the type it was standing on rested on nothing. It
// is the honesty payload for this rule, on the same contract as
// AmbiguousInference, NameEvidencedInference and EnumWidening: a decision made
// without local evidence is acceptable when it is REPORTED and correctable in
// one edit.
type AdoptedDomain struct {
	RecordType string
	Property   string
	// Was is the declaration this replaced. It is always TypeText — clause 3.
	Was records.PropertyType
	// Type, To and EnumValues are the adopted domain. EnumValues is the UNION
	// of the observing types' sets, which is what the engine itself compares an
	// untyped name against.
	Type       records.PropertyType
	To         string
	EnumValues []string
	// Sources are the record types whose observations decided it, in record-
	// type order, each with the number of notes of that type holding a value.
	Sources []AdoptionSource
}

// AdoptionSource is one observing record type behind an adopted domain.
type AdoptionSource struct {
	RecordType string
	Observed   int
}

// DeclinedAdoption is an adoption this rule REFUSED because the observed
// domain was an enum whose unioned vocabulary is past enumMaxDistinct. It is
// recorded rather than dropped because the refusal is the interesting half:
// the property keeps a type nothing observed, and the operator is the only one
// who can say which vocabulary this type really uses.
type DeclinedAdoption struct {
	RecordType string
	Property   string
	// Sources are the observing types, as for AdoptedDomain.
	Sources []AdoptionSource
	// UnionSize is how many distinct values the observing types declare
	// between them, and Bound is the count past which this package stops
	// calling a set a closed vocabulary.
	UnionSize int
	Bound     int
}
