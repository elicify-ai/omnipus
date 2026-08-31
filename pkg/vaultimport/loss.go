// Omnipus — FR-105's structural partition: WHERE a named loss came from,
// and therefore what it can do to the set of rows a view returns.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import "fmt"

// ---------------------------------------------------------------------------
// THE BROADENING PROHIBITION, MADE STRUCTURAL
//
// FR-105 is the one import rule that admits no exception: an imported view
// MUST NEVER return MORE rows than its original while looking correct. The
// standing example is Decisions.base — `type == "decision"` AND NOT
// inFolder("99-Temp") AND NOT inFolder("00-Inbox"). Drop the two folder
// clauses (which is exactly what this importer did before this file existed)
// and the view still runs, still looks right, and now silently includes
// every scratch note in the vault.
//
// The ORACLE is structural, not arithmetic. Nothing counts rows at import
// time. Instead every loss is emitted with its POSITION, positions are
// classified once — here — by what they can do to the row set, and a view
// carrying a loss that could ADD rows is DISABLED. "Never more rows" then
// follows by induction over clauses rather than by measurement.
//
// WHY THIS IS AN ENUM AND NOT A LIST OF STRINGS TO GREP FOR. A flat "these
// substrings mean a filter" list cannot detect its own incompleteness: add a
// new loss position tomorrow, forget to list it, and it silently classifies
// as harmless — the view stays ENABLED and broadens, which is the precise
// failure this requirement exists to stop, reintroduced by an omission
// nothing tests. So positions are a closed enum, the classification is a map
// over that enum, and TestLossPositions_ArePartitioned (loss_test.go) fails
// by name if a constant is added without classifying it, or classified
// without being a constant. That test is the guard; this comment is not.
//
// ---------------------------------------------------------------------------
// WHY THE CLASSIFICATION IS NO LONGER A BOOLEAN (the narrowing position)
//
// Until FR-140 carried a base's `formulas:` block, every loss this importer
// could emit meant the same thing: something was DROPPED. Dropping a conjunct
// removes a restriction, so every filter-position loss BROADENED and every
// display-position loss changed nothing. Two outcomes, one boolean.
//
// A translated formula breaks that symmetry. `Contracts.base` → "Expiring
// Soon" filters on `formula.days_to_expiry <= 60 && >= 0`, where
// `days_to_expiry: if(end_date, (date(end_date) - today()).days, "")`. The
// clause is not dropped — it is TRANSLATED, faithfully. But Obsidian's
// else-branch is the empty string and JavaScript reads `"" <= 60` as
// `0 <= 60`, i.e. TRUE, while this product's W1 rewrite makes the else-branch
// ABSENT and §8 R-2 makes every comparison over an absent operand FALSE. On
// the founder's vault `contract.end_date` has zero values across all 757
// notes, so Obsidian returns EVERY contract and we return NONE.
//
// That is FR-105's SECOND sentence, and the boolean had no way to say it:
// returning FEWER rows, with the loss named, is permitted. Forced into the
// old partition the position had to be either `true` (disable — and the
// entire formula effort moves zero views) or `false` (an "annotation" — and
// a report describes a total row-set collapse in the same voice as a dropped
// column heading). Both branches lose, so the classification became a
// TRI-STATE: broadens / narrows / annotates.
//
// ---------------------------------------------------------------------------
// WHY A NARROWING CLAIM NEEDS EVIDENCE, AND WHAT ACTUALLY PROVES IT
//
// "This translation is narrower" is a CLAIM about two engines' semantics, and
// an unproven one is precisely how a broadened view ships. So the narrowing
// positions are not reachable by writing a constant at a call site. They are
// reachable only through narrowingLossf, which takes a narrowingProof and
// decides the position ITSELF. An incomplete proof renders the UNPROVEN
// position, which is classified `broadens` and disables the view. The safe
// default is structural, not a judgement made again at each site.
//
// The proof has two obligations, and BOTH are required.
//
// OBLIGATION 1 — GROUND. Why is the translated clause's match set a subset of
// the original's? Only grounds in knownNarrowingGrounds count, so a caller
// cannot invent one by passing a new string. Today there is exactly one, and
// it rests on facts verified in the product's own code rather than on a
// comment: the W1 rewrite yields ABSENT where Obsidian's `""` else-branch
// yielded a coercible value (records.evalDateDifference returns absent if
// EITHER side is absent, and it is dispatched before evalArithmetic's own
// absence check, so `(date(due) - today()).days` over a note with no `due` is
// genuinely absent rather than zero), and §8 R-2 then makes the comparison
// FALSE (records.Comparator.Evaluate). A row can be dropped; none is added.
// Note what makes that argument strong: it never needs to know what
// JavaScript does with `"" <= 60`.
//
// OBLIGATION 2 — POLARITY, and it is the one a leaf-level argument misses.
// R-2 decides a LEAF. It does not decide the TREE the leaf sits in, and this
// importer builds trees: translateNot renders `not: [...]` as
// `{not: {all: [...]}}` and loses nothing (translate.go), while
// knowledgefind's nodeNot evaluates `matched: !inner.matched` (tree.go) with
// no absence rule of its own. FR-008's rescue — which DOES re-include an
// absent record for a negative OPERATOR like `!=` — lives in
// records.PreparedFilter.MatchValue and never reaches a `not:` COMBINATOR.
//
// So put the Contracts clause under a `not:`:
//
//	Obsidian: `"" <= 60` -> true  -> not -> row EXCLUDED
//	Ours:     absent      -> false -> not -> row INCLUDED
//
// A row Obsidian excluded, we include. The clause is narrower and the VIEW is
// broader. A narrowing proof that ignores polarity is therefore unsound, and
// classifying a bare "filter narrowing" position as never-broadening would
// ship exactly that. Hence: a proof must show the clause sits under NO `not:`
// ancestor, all the way to the filter root.
//
// GRADE. Polarity also decides how loud the report must be, because "narrowed"
// and "narrowed to nothing" are not the same news. Under `all:` ancestors only,
// an always-false clause makes the whole conjunction empty — the view returns
// ZERO rows. Under an `any:` ancestor it merely contributes nothing to a
// disjunction, so the view narrows without collapsing. Two positions, so the
// report can say which happened from the `[prefix]` alone.
// ---------------------------------------------------------------------------

// LossPosition names where in a `.base` file a named loss originated. It is
// rendered as the `[position]` prefix of every loss line, so the report a
// human reads and the classification the code makes are the same fact.
type LossPosition string

const (
	// LossBaseOuterFilter — the base file's own top-level `filters:` tree,
	// which applies to every view in the file.
	LossBaseOuterFilter LossPosition = "base outer filter"
	// LossViewFilter — a subtree of one view's own `filters:` that could not
	// be translated as a unit (an `or:` group mixing record types).
	LossViewFilter LossPosition = "view filter"
	// LossFilterLeaf — a single filter clause that parsed but could not be
	// built against the resolved schema (an undeclared property, an enum
	// literal the inferred schema does not declare, an operator not defined
	// for the property's type).
	LossFilterLeaf LossPosition = "filter"
	// LossGroupBy — a `groupBy` property or direction.
	LossGroupBy LossPosition = "group_by"
	// LossProperties — a display column from `order:`.
	LossProperties LossPosition = "properties"
	// LossSort — a sort key.
	LossSort LossPosition = "sort"
	// LossAggregates — a `summaries` entry.
	LossAggregates LossPosition = "aggregates"
	// LossLayout — the view's requested rendering (FR-109).
	LossLayout LossPosition = "layout"
	// LossLimit — the view's own row-count bound (`limit:`). A limit DECIDES
	// the row set: dropping one lets the view return more rows than the base
	// asked for, which is the FR-105 direction, so it is classified with the
	// filters and not with the annotations.
	LossLimit LossPosition = "limit"

	// LossNarrowedToNothing — a clause was translated faithfully, is provably
	// no wider than the original, and sits under `all:` ancestors only, so the
	// conjunction it belongs to matches NOTHING and the view returns ZERO
	// rows. FR-105-legal and the loudest thing this taxonomy can say: the
	// position name is the headline, so a report can single the view out
	// without parsing the detail text.
	LossNarrowedToNothing LossPosition = "narrowed to nothing"
	// LossNarrowedRowSet — the same proof, but an `any:` ancestor stands
	// between the clause and the root, so the clause contributes nothing to a
	// disjunction rather than emptying a conjunction. Fewer rows, not zero.
	LossNarrowedRowSet LossPosition = "narrowed"
	// LossUnprovenNarrowing — narrowingLossf was called and the proof did NOT
	// close: no recognised ground, or a `not:` ancestor, or an ancestry the
	// caller could not describe. The translation may well be narrower; this
	// importer cannot show it, and FR-105 has no tolerance for "probably". It
	// is classified `broadens` and disables the view, which keeps the failure
	// mode of an unthreaded call site VISIBLE and safe rather than silent.
	LossUnprovenNarrowing LossPosition = "unproven narrowing"
)

// allLossPositions is every position this importer can emit. It exists so a
// test can assert the classification below covers exactly it — see this
// file's header on why a list of substrings would be vacuous.
var allLossPositions = []LossPosition{
	LossBaseOuterFilter,
	LossViewFilter,
	LossFilterLeaf,
	LossGroupBy,
	LossProperties,
	LossSort,
	LossAggregates,
	LossLayout,
	LossLimit,
	LossNarrowedToNothing,
	LossNarrowedRowSet,
	LossUnprovenNarrowing,
}

// lossEffect is what a loss at some position can do to the set of rows the
// imported view returns, relative to what the Obsidian original returned.
//
// THE ZERO VALUE IS THE DANGEROUS ONE, deliberately. The predecessor of this
// type was a bool, whose zero value is `false` = "annotation" = ship the view
// ENABLED. So a position that fell out of the map — the exact omission this
// file's header warns about — failed OPEN: it read as harmless and let the
// view broaden, and only a passing test stood between that and a release.
// With lossEffectUnclassified at 0 the same omission fails CLOSED: the view
// disables. The guard test still fires (and must — see
// TestLossPositions_ArePartitioned), but it is no longer the only thing
// standing there.
type lossEffect uint8

const (
	// lossEffectUnclassified — no opinion recorded. Treated as broadening.
	lossEffectUnclassified lossEffect = iota
	// lossEffectBroadens — the loss can ADD rows the original did not return.
	// FR-105 forbids shipping this: the view imports DISABLED.
	lossEffectBroadens
	// lossEffectNarrows — the row set changed, provably in the permitted
	// direction (a subset of the original). The view imports ENABLED and the
	// loss is reported AS A NARROWING, not as an annotation.
	lossEffectNarrows
	// lossEffectAnnotates — display config, ordering, a summary, a rendering.
	// Cannot change the row set at all. The view imports ENABLED.
	lossEffectAnnotates
)

// allLossEffects lets a test assert the classification uses every effect, so
// the map cannot quietly collapse into answering a constant.
var allLossEffects = []lossEffect{
	lossEffectUnclassified,
	lossEffectBroadens,
	lossEffectNarrows,
	lossEffectAnnotates,
}

func (e lossEffect) String() string {
	switch e {
	case lossEffectBroadens:
		return "broadens"
	case lossEffectNarrows:
		return "narrows"
	case lossEffectAnnotates:
		return "annotates"
	default:
		return "unclassified"
	}
}

// disablesView is FR-105's decision, written as an ALLOW-LIST on purpose.
// Only the two effects known to be safe to ship let a view through; anything
// else — the zero value, or an effect somebody adds tomorrow and has not
// thought through — disables. A deny-list here (`e == lossEffectBroadens`)
// would make every future addition default to shipping.
func (e lossEffect) disablesView() bool {
	switch e {
	case lossEffectNarrows, lossEffectAnnotates:
		return false
	default:
		return true
	}
}

// lossPositionEffects is FR-105's classification. Every position MUST appear,
// and none may map to lossEffectUnclassified: a position added to
// allLossPositions and not to this map, or parked at the zero value, fails
// TestLossPositions_ArePartitioned by name.
var lossPositionEffects = map[LossPosition]lossEffect{
	LossBaseOuterFilter:   lossEffectBroadens,
	LossViewFilter:        lossEffectBroadens,
	LossFilterLeaf:        lossEffectBroadens,
	LossLimit:             lossEffectBroadens,
	LossUnprovenNarrowing: lossEffectBroadens,

	LossNarrowedToNothing: lossEffectNarrows,
	LossNarrowedRowSet:    lossEffectNarrows,

	LossGroupBy:    lossEffectAnnotates,
	LossProperties: lossEffectAnnotates,
	LossSort:       lossEffectAnnotates,
	LossAggregates: lossEffectAnnotates,
	LossLayout:     lossEffectAnnotates,
}

// ---------------------------------------------------------------------------
// THE NARROWING PROOF
// ---------------------------------------------------------------------------

// filterCombinator is one node kind on the path from a view's filter root down
// to a translated clause. The zero value is combinatorUnknown so an ancestry a
// caller could not fully describe cannot accidentally read as `all:` and prove
// something.
type filterCombinator uint8

const (
	// combinatorUnknown — a node whose kind the caller could not determine.
	// Blocks the proof.
	combinatorUnknown filterCombinator = iota
	// combinatorAll — an `and:`/`all:` node. An always-false child empties it.
	combinatorAll
	// combinatorAny — an `or:`/`any:` node. An always-false child contributes
	// nothing to it, narrowing the group without emptying the view.
	combinatorAny
	// combinatorNot — a `not:` node. It INVERTS, with no absence rule of its
	// own (knowledgefind tree.go's nodeNot), so a narrower child makes a
	// BROADER view. Any occurrence destroys the proof.
	combinatorNot
)

// narrowingGround names WHY a translated clause's match set is a subset of the
// original's. It is a closed set (knownNarrowingGrounds) rather than free text
// so that a call site cannot prove a narrowing by describing one.
type narrowingGround string

const (
	// narrowingGroundUnstated is the zero value: no ground given, no proof.
	narrowingGroundUnstated narrowingGround = ""
	// narrowingGroundAbsentElseBranch — FR-140's W1 rewrite. Obsidian's
	// formula returns `""` from an else-branch and JavaScript coerces it into
	// a comparison; this product returns ABSENT and §8 R-2 makes the
	// comparison FALSE. Rows can be dropped, never added. See this file's
	// header for the three code facts this rests on.
	narrowingGroundAbsentElseBranch narrowingGround = "the translated formula yields ABSENT where Obsidian's empty-string else-branch coerced to a comparable value, and §8 R-2 makes every comparison over an absent operand FALSE"
)

// knownNarrowingGrounds is the closed set of arguments this importer accepts
// as establishing the subset direction.
var knownNarrowingGrounds = map[narrowingGround]bool{
	// The zero value is listed EXPLICITLY rather than left to fall out of the
	// map, so the closed set states on its own that "no ground given" is not
	// an argument — a reader does not have to infer it from an absence.
	narrowingGroundUnstated:         false,
	narrowingGroundAbsentElseBranch: true,
}

// narrowingProof is the evidence a call site must produce to claim that a
// clause it translated makes the view narrower rather than wider.
//
// Its ZERO VALUE PROVES NOTHING, which is what makes the migration safe: a
// site that calls narrowingLossf before it can thread the filter ancestry gets
// LossUnprovenNarrowing and the conservative disable, identical in effect to
// the LossFilterLeaf posture it is replacing.
type narrowingProof struct {
	// Ground is the argument for the subset direction (obligation 1).
	Ground narrowingGround
	// Ancestry is every combinator between the view's filter ROOT and the
	// clause, outermost first. It must be complete: a partial ancestry that
	// omits the `not:` above it would prove the opposite of the truth
	// (obligation 2).
	Ancestry []filterCombinator
}

// negated reports whether anything on the path inverts the clause's answer.
func (p narrowingProof) negated() bool {
	for _, c := range p.Ancestry {
		if c == combinatorNot {
			return true
		}
	}
	return false
}

// describable reports whether every ancestor's kind is known. An unknown node
// could be a `not:`.
func (p narrowingProof) describable() bool {
	for _, c := range p.Ancestry {
		if c == combinatorUnknown {
			return false
		}
	}
	return true
}

// position is the whole decision, made HERE rather than at the call site.
//
// It returns the position a loss carrying this proof must be rendered with,
// and that is the only way any narrowing position is ever chosen.
func (p narrowingProof) position() LossPosition {
	if !knownNarrowingGrounds[p.Ground] || !p.describable() || p.negated() {
		return LossUnprovenNarrowing
	}
	// The subset direction holds. The GRADE is the remaining question: an
	// always-false clause empties a conjunction, but only thins a disjunction.
	for _, c := range p.Ancestry {
		if c == combinatorAny {
			return LossNarrowedRowSet
		}
	}
	return LossNarrowedToNothing
}

// narrowingLossf renders a loss for a clause that WAS translated and whose
// translation the caller believes cannot widen the view.
//
// The caller does NOT choose the position — the proof does. That is the whole
// point: "which position is this" stops being a judgement remade at every site
// (where one wrong `false` ships a broadened view) and becomes one function
// with one set of rules and one set of tests.
func narrowingLossf(p narrowingProof, format string, args ...any) string {
	pos := p.position()
	detail := fmt.Sprintf(format, args...)
	if pos == LossUnprovenNarrowing {
		return lossf(pos, "%s — the translation may be narrower, but this importer could not prove it does not widen the view, so the view is disabled rather than shipped on a guess", detail)
	}
	return lossf(pos, "%s — %s", detail, p.Ground)
}

// ---------------------------------------------------------------------------

// lossf renders one named loss as `[position] detail`. It is the ONLY way a
// loss line is built, so no loss can reach a report without a position the
// classification above has an opinion about.
func lossf(pos LossPosition, format string, args ...any) string {
	return fmt.Sprintf("[%s] ", pos) + fmt.Sprintf(format, args...)
}

// parseLossPosition reads back the position a loss line was rendered with.
// The second return is FALSE for a line whose prefix is not a known
// position — which a test treats as a defect rather than as an annotation,
// because "unclassified" defaulting to "harmless" is how the prohibition
// would erode.
func parseLossPosition(line string) (LossPosition, bool) {
	if len(line) == 0 || line[0] != '[' {
		return "", false
	}
	end := -1
	for i := 1; i < len(line); i++ {
		if line[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 {
		return "", false
	}
	pos := LossPosition(line[1:end])
	if _, known := lossPositionEffects[pos]; !known {
		return "", false
	}
	return pos, true
}

// lossLineEffect is what one rendered loss line does to the row set. It is the
// accessor the REPORT layer wants: it can tell a narrowing from an annotation,
// which `lossPositionAffectsRowSet` (a bool) cannot.
//
// An UNKNOWN prefix answers lossEffectUnclassified, which disablesView() reads
// as dangerous.
func lossLineEffect(line string) lossEffect {
	pos, ok := parseLossPosition(line)
	if !ok {
		return lossEffectUnclassified
	}
	return lossPositionEffects[pos]
}

// lossDisablesView reports whether one rendered loss line forces FR-105 to
// import the view DISABLED. An unclassifiable loss answers true: the failure
// mode of forgetting to classify something is a view that is disabled when it
// did not need to be — visible, arguable, and safe — rather than a view that
// silently returns more rows than its original.
func lossDisablesView(line string) bool {
	return lossLineEffect(line).disablesView()
}

// lossPositionAffectsRowSet is lossDisablesView under the name the rest of the
// importer already calls.
//
// THE NAME IS NOW NARROWER THAN THE TRUTH and is kept only so view_write.go's
// existing call site keeps compiling: a narrowing loss genuinely DOES affect
// the row set (that is its entire content) yet answers false here, because
// what this function has always actually decided is "must the view be
// disabled". Prefer lossDisablesView in new code; read this one as a synonym
// for it and not as a claim about row sets.
func lossPositionAffectsRowSet(line string) bool {
	return lossDisablesView(line)
}
