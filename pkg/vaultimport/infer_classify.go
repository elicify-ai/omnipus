// infer_classify.go: Classify collected signals into record kinds and properties (observed-value classification, relation targets, name evidence, domain adoption)

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// InferSchema classifies every property of one type group. It never returns
// an error: every property gets a declaration, and every ambiguity is
// reported alongside it rather than blocking the run — a vault with one
// confusing property should still get every other property declared
// correctly (the same "one bad thing does not blind the whole answer"
// posture pkg/records itself takes on a bad schema FILE).
func InferSchema(g *TypeGroup, names *NameIndex) []InferredProperty {
	out := make([]InferredProperty, 0, len(g.PropOrder))
	sortedNames := append([]string(nil), g.PropOrder...)
	sort.Strings(sortedNames)
	for _, name := range sortedNames {
		po := g.Props[name]
		out = append(out, classifyProperty(g.Type, po, g.NoteCount, names))
	}
	return out
}

// ambiguousMatchFloorNum/Den: when the single best-matching non-text
// candidate type (date/integer/decimal) matches at least this fraction of a
// property's observed values but NOT all of them, the property is reported
// as an ambiguous inference instead of silently defaulted to text. Below the
// floor the partial match is treated as coincidence (e.g. one free-text
// title that happens to parse as a number) and not reported.
//
// IT IS ONE HALF, AND IT USED TO BE 0.60. Read before raising it back.
//
// The floor's job is to suppress COINCIDENCE, and coincidence is a small
// ABSOLUTE agreement dressed up as a fraction — one title in fifty that
// parses as a number scores 0.02 and is nowhere near any floor worth
// arguing about. 0.60 was not filtering coincidence; it was hiding the one
// shape in the founder's vault most worth reporting.
//
// The case, measured: `subscription.renewal_date` holds 62 values. THIRTY-ONE
// are real ISO dates and thirty-one are hand-written `PLACEHOLDER — renewal
// date unknown` / `PLACEHOLDER — usage-based model, no fixed renewal` strings
// in seventeen distinct spellings. That is exactly 0.50, it sat below 0.60,
// and so the run declared the property `text`, LOST the `renewal_date != ""`
// filter that Subscriptions.base's "Renewing <14d" view is built on, and said
// NOTHING about why. The founder was left with a disabled view and no line
// connecting it to his own placeholder rows. Refusing to type it date is
// correct — typing it would make those 31 notes invalid against the schema
// this same run wrote, which this package admits no exception to — but
// refusing SILENTLY is not.
//
// Stated as an exact integer ratio rather than a float for the same reason
// relationSupermajorityNum/Den and bestFitMarginNum/Den are: the deciding
// case in the real vault is 31 of 62, which lands EXACTLY on the boundary,
// and a decision that turns on which way a float division rounds is not a
// decision anybody can reproduce. `matched*2 >= 1*total` settles it in
// integers on every platform.
//
// Blast radius, measured on the founder's 757-note vault rather than
// asserted: the whole vault contains exactly TWO properties whose best
// non-text candidate matches some-but-not-all of their values —
// `subscription.cost` at 42/63 (already reported at the old floor) and
// `subscription.renewal_date` at 31/62 (silent at the old floor, reported at
// this one). The change adds one report line and removes none.
const (
	ambiguousMatchFloorNum = 1
	ambiguousMatchFloorDen = 2
)

func classifyProperty(recordType string, po *PropertyObservation, noteCount int, names *NameIndex) InferredProperty {
	ip := InferredProperty{
		Name: po.Name,
		// The MAJORITY shape, not "any note wrote a list". See
		// PropertyObservation.Many for why the single-observation rule this
		// replaced manufactured validation errors.
		Many:       po.Many(),
		AritySplit: po.AritySplit(recordType),
		Required:   po.PresentNonEmptyCount == noteCount && noteCount > 0,
		// The same numerator `required` is decided from, kept rather than
		// reduced to the bit. See the field's own doc comment for why the
		// bit is not enough for FR-104b's tie-break.
		ObservedCount: po.PresentNonEmptyCount,
	}
	total := len(po.Values)
	if total == 0 {
		return classifyWithNoValues(recordType, po, ip)
	}

	// --- a block scalar is prose, and only `text` accepts prose ---------
	//
	// A YAML block scalar (`|` or `>`) is a MULTI-LINE STRING. Its Text here
	// is the folded body, newline and all, and every non-text parser compares
	// that body against a shape: an enum against its declared values, a date
	// against a grammar, a relation against wikilink syntax — which
	// records.parseLinkValue refuses for a block scalar BY NAME under FR-030a.
	//
	// The importer used to see only the Text, so `company: |` + `[[Acme]]`
	// inferred `relation` and then failed validation on every such note: an
	// error this package manufactured about its own inference. Refusing the
	// wikilink is only half the fix — falling through to `enum` declares
	// `[[Acme]]` as a value and the note still fails, because the value it
	// actually holds is `[[Acme]]\n`. D3 makes `text` prose that is never
	// validated for shape, so text is the one honest answer for a value the
	// operator deliberately wrote as a multi-line string.
	if anyValue(po.Values, func(v observedValue) bool { return v.Block }) {
		ip.Type = records.TypeText
		ip.Kind = ClassifyText
		return ip
	}

	// --- relation -----------------------------------------------------
	// isWikilinkValue, not isWikilink: the predicate has to see the BLOCK
	// flag, because records.parseLinkValue refuses a block scalar as a
	// wikilink by name (FR-030a). Testing the Text alone made this a second
	// oracle that disagreed with the validator. The block-scalar branch above
	// already returned, so this is belt and braces on the ONE predicate whose
	// disagreement with the validator was measured.
	if allMatchValue(po.Values, isWikilinkValue) {
		to, split := inferRelationTarget(recordType, po, names)
		if to == "" {
			// FR-034 (schema.go's Property.finalize): a relation MUST
			// declare `to:` — a schema with `type: relation` and no `to:`
			// is REJECTED outright at load time, not merely looser. With
			// zero resolvable evidence for ANY target type there is
			// nothing honest to put there, so this property is declared
			// `text` instead of an invalid relation. It is reported via
			// RelationSplit either way — Declared says which happened.
			ip.Type = records.TypeText
			ip.Kind = ClassifyAmbiguous
		} else {
			ip.Type = records.TypeRelation
			ip.Kind = ClassifyRelation
			ip.To = to
		}
		ip.RelationSplit = split
		return ip
	}

	// --- boolean ------------------------------------------------------
	//
	// THIS IS FR-004c'S `checkbox`, AND MODELLING IT AS A 2-VALUE ENUM WAS A
	// BROADENING BUG, NOT A STYLE CHOICE. Read before "simplifying" it back.
	//
	// The schema has had eight property types since FR-004c/ADR-068 D24.5;
	// this branch predated `checkbox` and wrote `{type: enum, values:
	// [false, true]}` instead. That is not merely a less precise declaration:
	// `enum` sits on the SAFE side of view_write.go's truthy partition, so
	// Obsidian's bare `archived` filter translated to `IS NOT NULL` with NO
	// loss recorded and the view shipped ENABLED — and `IS NOT NULL` matches
	// every note that declares `archived` AT ALL, including the ones holding
	// `false`, which Obsidian's truthy test rejects. 200 task notes split
	// true/false: Obsidian returns the true ones, the imported view returned
	// all 200. That is precisely the broadening FR-105 admits no exception to.
	//
	// The partition entry that would have refused it — TypeCheckbox, whose
	// falsy literal is `false` — was UNREACHABLE, because this was the only
	// place a boolean could have become one and it never did.
	if allMatch(po.Values, isBooleanLiteral) {
		ip.Type = records.TypeCheckbox
		ip.Kind = ClassifyBoolean
		return ip
	}

	// --- date / integer / decimal / relation-without-unanimity ---------
	candidates := []struct {
		t    records.PropertyType
		test func(string) bool
	}{
		{records.TypeDate, isDate},
		{records.TypeInteger, isInteger},
		{records.TypeDecimal, isDecimal},
	}
	// Every candidate is scored against the SAME denominator (`total`), so
	// ranking them by matched COUNT is identical to ranking them by
	// fraction — and it is exact, where a float division is not. Ties keep
	// the first candidate in this fixed list, so two identical runs report
	// the same best type.
	var bestType records.PropertyType
	var bestMatched, bestTotal int
	var bestBad []observedValue
	for _, c := range candidates {
		matched, bad := partitionMatch(po.Values, c.test)
		if matched == total {
			ip.Type = c.t
			ip.Kind = classifyKindFor(c.t)
			return ip
		}
		if matched > bestMatched {
			bestType, bestMatched, bestTotal, bestBad = c.t, matched, total, bad
		}
	}

	// --- enum: small, repeated, closed vocabulary ----------------------
	distinct := distinctFolded(po.Values)
	avgRepeat := float64(total) / float64(len(distinct))
	if len(distinct) <= enumMaxDistinct &&
		(len(distinct) <= enumSmallEnough || avgRepeat >= enumMinAvgRepeat) {
		ip.Type = records.TypeEnum
		ip.Kind = ClassifyEnum
		ip.EnumValues = sortedDistinctDisplay(po.Values, distinct)
		return ip
	}

	// --- ambiguous: a real, non-coincidental partial match --------------
	//
	// `bestMatched/bestTotal >= num/den`, cross-multiplied so the deciding
	// case in the real vault (31 of 62 against a floor of one half — dead on
	// the boundary) is settled by integers rather than by which way a float
	// division happens to land. bestMatched > 0 keeps the guard honest when
	// nothing matched at all: 0/n is not a partial match to report.
	if bestMatched > 0 && bestMatched*ambiguousMatchFloorDen >= ambiguousMatchFloorNum*bestTotal {
		ex := make([]AmbiguousExample, 0, 3)
		for i, b := range bestBad {
			if i >= 3 {
				break
			}
			ex = append(ex, AmbiguousExample{NotePath: b.NotePath, Value: b.Text})
		}
		ip.Ambiguity = &AmbiguousInference{
			RecordType:   recordType,
			Property:     po.Name,
			BestType:     bestType,
			MatchFrac:    float64(bestMatched) / float64(bestTotal),
			TotalValues:  bestTotal,
			MatchedCount: bestMatched,
			Examples:     ex,
		}
	}

	ip.Type = records.TypeText
	ip.Kind = ClassifyText
	if ip.Ambiguity != nil {
		ip.Kind = ClassifyAmbiguous
	}
	return ip
}

// ---------------------------------------------------------------------------
// A PROPERTY NOBODY EVER FILLED IN
//
// classifyWithNoValues decides a property for which the vault holds NOT ONE
// value: every note of the type that mentions the key wrote `deadline:` and
// nothing after it, or an explicit null, or an empty string.
//
// THIS IS NOT A RARE CORNER. On the founder's own vault 141 of the inferred
// properties are in this state — most of them on `template`, which is what a
// template IS, and four of them on real record types whose `.base` views were
// DISABLED because of the answer this function used to give.
//
// WHY `text` WAS THE WRONG ANSWER, AND WHY IT IS NOT THE SAFE ONE EITHER.
//
// The old branch defaulted to text and called it the conservative choice.
// It is not conservative; in this engine it is the single most OPINIONATED
// choice available, and it is the only one that costs a filter.
//
// FR-007a gives `text` absence semantics that no other type has: on text the
// empty string is a PRESENT value, and on all seven other types it is
// ABSENT. So declaring text is a positive assertion — "the empty string is
// meaningful data here" — made about a property for which the vault contains
// no data whatsoever. And it is exactly that assertion that makes Obsidian's
// idiomatic `prop != ""` untranslatable: view_write.go's shapeIsSet can map
// it to `IS NOT NULL` on every NON-text type and must refuse it on text,
// because `IS NOT NULL` would match a record holding `""` that Obsidian's
// filter excludes.
//
// The measured bill for that default, on the founder's vault: `contract.
// end_date`, `deal.close_date` and `project.deadline` — all three with ZERO
// observed values — were declared text, their `!= ""` filters were refused
// by name, and Projects.base's "Deadlines" view (whose ONLY row-set loss was
// that one filter) shipped DISABLED.
//
// WHAT DECIDES IT INSTEAD. With no values, all eight property types are
// EQUALLY consistent with the evidence, and — this is the part that makes
// the choice safe rather than merely arbitrary — NONE of them can invalidate
// a note, because there is no value for any of them to reject. The
// acceptance bar this package will not cross (a note this run typed is never
// reported invalid by the same run) is untouched here as a matter of
// arithmetic, not of luck: zero values cannot fail zero, one, or eight
// schemas.
//
// So the choice is free, and the only signal left is the property's NAME.
// This is the ONE place in this file a name decides anything, and the guard
// rails are the reason it is allowed to:
//
//	(1) It fires ONLY at zero values. One observed value anywhere in the
//	    type and the name is ignored entirely and the normal
//	    parse-every-value chain runs. DATA ALWAYS BEATS THE NAME. The case
//	    that proves the guard rail is real: `subscription.renewal_date`
//	    carries the same date-shaped name and 62 values, 31 real ISO dates
//	    and 31 hand-written `PLACEHOLDER — ...` strings. It stays TEXT. A
//	    rule that read the name there would have made 31 of the founder's
//	    own notes invalid against the schema the same run wrote.
//	(2) The name shapes are a CLOSED, literal list (nameEvidencedDate), not
//	    a fuzzy match, and it covers only names that cannot plausibly mean
//	    anything but a calendar date.
//	(3) Only DATE is inferred this way. There is no `_count -> integer`, no
//	    `is_* -> checkbox`, no `email -> text`. Those would be guesses with
//	    no measured benefit, and this package does not buy tidiness with
//	    guesses.
//	(4) It is EVIDENCE-CARRYING: NameEvidenced is populated so the run can
//	    report the guess by name, with the counts behind it. An unreported
//	    guess is the thing this file exists to refuse.
//
// AND IT CANNOT BROADEN A VIEW (FR-105). Going from text to date changes
// three translations in view_write.go's buildV2LeafNode, and each moves in
// the narrowing direction or not at all:
//
//	`prop != ""`   text: REFUSED (a loss).  date: `IS NOT NULL`, which
//	               matches only records holding a parseable date. Obsidian
//	               matches records holding a non-empty string, plus — on the
//	               reading where `undefined != ""` is true — the absent ones
//	               as well. Ours is a subset under both readings.
//	bare `prop`    text: REFUSED (`""` is present-and-falsy).  date: `IS NOT
//	               NULL`; a date has no falsy literal, so again a subset.
//	`prop == ""`   REFUSED on both. Unchanged.
//
// A FUTURE NOTE CAN STILL BE WRONG, and that is the honest cost. If somebody
// later writes `end_date: on signature` the schema will report it, where a
// text declaration would have let it through. That is what inferring a
// schema MEANS; every other branch in this file carries the same exposure,
// and the report names this property so the operator can overrule it with
// one knowledge_configure edit.
func classifyWithNoValues(recordType string, po *PropertyObservation, ip InferredProperty) InferredProperty {
	if nameEvidencedDate(po.Name) {
		ip.Type = records.TypeDate
		ip.Kind = ClassifyDateFromName
		ip.NameEvidenced = &NameEvidencedInference{
			RecordType:         recordType,
			Property:           po.Name,
			Type:               records.TypeDate,
			DeclaringNotes:     po.DeclaredCount,
			DeclaringTemplates: append([]string(nil), po.TemplateNotes...),
		}
		return ip
	}
	// The name says nothing this package is willing to act on, so nothing
	// distinguishes the eight types and text remains the declaration — not
	// because it is neutral (it is not; see above) but because changing it
	// on no signal at all would be churn, and text is the type whose
	// validator asks the least of a value that does not exist yet.
	ip.Type = records.TypeText
	ip.Kind = ClassifyText
	return ip
}

// dateNameExact is the closed list of property names that ARE a date, whole.
//
// THE BAR FOR AN ENTRY, and it is deliberately high: a wrong name-guess
// produces a schema that REJECTS the operator's first real note, which is a
// worse outcome than the lost filter this rule exists to recover. So an
// entry needs the name to be unambiguous in ordinary usage AND, where the
// vault can speak to it, evidence from the vault itself.
//
//	date       a date, by definition.
//	deadline   denotes a point in time in English and nothing else.
//	due        admitted on EVIDENCE, not on the word alone. "Due" can
//	           certainly mean an amount ("balance due"), but in the
//	           founder's vault `due` holds twelve values and every single
//	           one of them is an ISO date (plus eight notes writing `due:
//	           ""`, which FR-007a makes absence on any non-text type). Where
//	           he fills the key in, he fills it in with a date. Note the
//	           rule still never fires on those notes — they HAVE values, so
//	           they classify by value — it fires only on `template.due`,
//	           which 35 template notes declare and every one leaves blank.
//
// REJECTED, with the measurement that rejected it:
//
//	_at        SUGGESTED and REFUSED. `created_at`, `updated_at`,
//	           `started_at`, `captured_at` and `attached_at` all appear in
//	           the founder's vault and every one of them holds the literal
//	           string `timestamp` as a placeholder. records.TypeDate accepts
//	           only the six ISO layouts in dateLayouts, so `timestamp` does
//	           not parse. Those particular properties are safe today because
//	           they HAVE values and data beats the name — but they are proof
//	           that `_at` is exactly the shape this vault writes non-dates
//	           into, and the first all-blank `_at` property would be typed
//	           `date` and would reject his first real note.
//	expiry, completed, last_activity, last_refreshed, term, period, close
//	           Each reads just as naturally as an amount, a term, a checkbox
//	           or a free-text note, and none costs a measured filter. If a
//	           name needs an argument about what it "implies", that is the
//	           signal the guess has gone past the evidence.
var dateNameExact = map[string]bool{
	"date":     true,
	"deadline": true,
	"due":      true,
}

// nameEvidencedDate reports whether a property's NAME, on its own, names a
// calendar date beyond reasonable argument.
//
// The suffix and the prefix are both accepted because `signed_date` and
// `date_signed` are the same naming convention written in two word orders,
// and a rule that took one and refused the other would be deciding on the
// operator's grammar rather than on what the name means. `-` is folded to
// `_` for the same reason: Obsidian permits either separator in a property
// name and they mean the same thing.
func nameEvidencedDate(name string) bool {
	f := records.FoldKey(strings.TrimSpace(name))
	f = strings.ReplaceAll(f, "-", "_")
	if dateNameExact[f] {
		return true
	}
	return strings.HasSuffix(f, "_date") || strings.HasPrefix(f, "date_")
}

// ReportLines renders one name-evidenced guess, first line first, INCLUDING
// any contradiction between the entry and its own premise.
//
// It lives here, next to the rule that produces the entry, for the reason
// ProvisionedType.ReportLines states about itself: the same account is read
// by the operator in the run report, and a second spelling of it elsewhere is
// how two accounts of one decision drift apart. It also puts the premise
// check beside the premise — this section asserts that something DECLARED the
// key and left it blank, and when the payload says nothing did, that is the
// impossible case and it is named rather than narrated as fine.
//
// The record-type half of the check stays in the report renderer: whether the
// type is among the ones this run inferred is a question about the whole
// report, which one inference cannot see.
func (n NameEvidencedInference) ReportLines() []string {
	var where string
	switch {
	case n.DeclaringNotes > 0 && len(n.DeclaringTemplates) > 0:
		where = fmt.Sprintf("declared by %d note(s), every one blank, and named by %s",
			n.DeclaringNotes, joinTemplateNotes(n.DeclaringTemplates))
	case n.DeclaringNotes > 0:
		where = fmt.Sprintf("declared by %d note(s), every one blank", n.DeclaringNotes)
	case len(n.DeclaringTemplates) > 0:
		where = fmt.Sprintf("declared by no %s note — named, and left blank, by %s, which is where the operator writes what a %s note carries",
			n.RecordType, joinTemplateNotes(n.DeclaringTemplates), n.RecordType)
	default:
		where = "declared by nothing at all"
	}
	lines := []string{fmt.Sprintf("%s.%s -> %s (%s)", n.RecordType, n.Property, n.Type, where)}
	if n.DeclaringNotes <= 0 && len(n.DeclaringTemplates) == 0 {
		lines = append(lines, fmt.Sprintf(
			"CONTRADICTION — nothing declares `%s`: no note of this type wrote the key and no template names it, so there was no key here to type from a name. The premise of this whole section fails for this entry; file it rather than trusting the type.",
			n.Property))
	}
	return lines
}

// joinTemplateNotes renders the template paths a guess rests on.
func joinTemplateNotes(paths []string) string {
	if len(paths) == 1 {
		return "the template `" + paths[0] + "`"
	}
	return "the templates `" + strings.Join(paths, "`, `") + "`"
}

// CollectNameEvidencedInferences gathers every name-based type decision an
// inference pass made, sorted, ready for the run report to print.
//
// It exists so the report does not have to know the shape of the rule that
// produced these — it asks this package for its own guesses and prints them.
func CollectNameEvidencedInferences(inferred map[string][]InferredProperty) []NameEvidencedInference {
	var out []NameEvidencedInference
	for _, props := range inferred {
		for _, p := range props {
			if p.NameEvidenced != nil {
				out = append(out, *p.NameEvidenced)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RecordType != out[j].RecordType {
			return out[i].RecordType < out[j].RecordType
		}
		return out[i].Property < out[j].Property
	})
	return out
}

func classifyKindFor(t records.PropertyType) ClassifyKind {
	switch t {
	case records.TypeDate:
		return ClassifyDate
	case records.TypeInteger:
		return ClassifyInteger
	case records.TypeDecimal:
		return ClassifyDecimal
	default:
		return ClassifyText
	}
}

// anyValue reports whether any observation satisfies the predicate.
func anyValue(vs []observedValue, test func(observedValue) bool) bool {
	for _, v := range vs {
		if test(v) {
			return true
		}
	}
	return false
}

// allMatchValue is allMatch for a predicate that needs the whole observation,
// not just its text — see observedValue.Block.
func allMatchValue(vs []observedValue, test func(observedValue) bool) bool {
	for _, v := range vs {
		if !test(v) {
			return false
		}
	}
	return true
}

func allMatch(vs []observedValue, test func(string) bool) bool {
	for _, v := range vs {
		if !test(v.Text) {
			return false
		}
	}
	return true
}

func partitionMatch(vs []observedValue, test func(string) bool) (matched int, bad []observedValue) {
	for _, v := range vs {
		if test(v.Text) {
			matched++
		} else {
			bad = append(bad, v)
		}
	}
	return matched, bad
}

func distinctFolded(vs []observedValue) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range vs {
		out[records.FoldKey(strings.TrimSpace(v.Text))] = struct{}{}
	}
	return out
}

// sortedDistinctDisplay renders an enum's declared value list: the first
// original-cased spelling seen for each folded value, sorted lexically over
// the folded form — the same order R-5 sorts enum values in, so the
// generated schema's own `values:` list already reads the way the product
// will compare it.
func sortedDistinctDisplay(vs []observedValue, distinct map[string]struct{}) []string {
	firstSpelling := map[string]string{}
	for _, v := range vs {
		text := strings.TrimSpace(v.Text)
		key := records.FoldKey(text)
		if _, ok := firstSpelling[key]; !ok {
			firstSpelling[key] = text
		}
	}
	keys := make([]string, 0, len(distinct))
	for k := range distinct {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return records.FoldCompare(keys[i], keys[j]) < 0 })
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, firstSpelling[k])
	}
	return out
}

func isWikilink(s string) bool {
	_, ok := records.ParseWikilink(s)
	return ok
}

// isWikilinkValue is the oracle that agrees with records.parseLinkValue: a
// BLOCK scalar is never a wikilink however its folded text reads (FR-030a).
func isWikilinkValue(v observedValue) bool {
	if v.Block {
		return false
	}
	return isWikilink(v.Text)
}

func isBooleanLiteral(s string) bool {
	f := records.FoldKey(strings.TrimSpace(s))
	return f == "true" || f == "false"
}

func isDate(s string) bool {
	_, err := records.ParseValue(&records.Property{Type: records.TypeDate, Name: "_probe"}, records.Node{Kind: records.KindScalar, Text: s})
	return err == nil
}

func isInteger(s string) bool {
	_, err := records.ParseValue(&records.Property{Type: records.TypeInteger, Name: "_probe"}, records.Node{Kind: records.KindScalar, Text: s})
	return err == nil
}

func isDecimal(s string) bool {
	_, err := records.ParseValue(&records.Property{Type: records.TypeDecimal, Name: "_probe"}, records.Node{Kind: records.KindScalar, Text: s})
	return err == nil
}

const (
	// RelationUnanimous: every link that resolved pointed at ONE type.
	RelationUnanimous RelationRule = "unanimous"
	// RelationSupermajority: one type held at least 2/3 of the resolved
	// links; `to:` is declared and the minority is named.
	RelationSupermajority RelationRule = "supermajority"
	// RelationNoMajority: the evidence is genuinely mixed — no type reached
	// 2/3 — so the property is declared `text` and the remedy is named.
	// THIS BRANCH IS THE WHOLE POINT OF THE RULING. Before it, a plurality
	// won outright: `contact.related` was declared `to: task` on 2 of 5
	// resolved links, and nothing in the run said where guessing had to
	// stop.
	RelationNoMajority RelationRule = "no-majority"
	// RelationUnresolved: no link resolved to any record type at all.
	RelationUnresolved RelationRule = "unresolved"
)

// relationSupermajorityNum/Den are FR-104a's threshold, as an exact integer
// ratio rather than a float: the test is `count/total >= 2/3`, evaluated as
// `3*count >= 2*total`, so 2 of 3 passes and 4 of 7 does not, on every
// platform, with no rounding to argue about.
const (
	relationSupermajorityNum = 2
	relationSupermajorityDen = 3
)

// inferRelationTarget resolves a relation property's `to:` from what its
// links ACTUALLY point at — FR-104a, the founder's ruling of 2026-08-31.
//
// THE RULE, STATED SO TWO IMPLEMENTATIONS AGREE:
//
//	unanimous          every resolved link points at ONE type  -> declare it
//	supermajority      one type holds >= 2/3 of the property's LINKS -> declare
//	                   it, and NAME the minority (those links are real type
//	                   mismatches; D5/FR-034's relation_type_mismatch finding
//	                   is where they belong, now visible instead of buried)
//	otherwise          declare `text`, SAY a relation could not be typed, and
//	                   name the one-line knowledge_configure edit that fixes
//	                   it once the operator decides
//
// WHICH DENOMINATOR — READ THIS BEFORE "CORRECTING" IT. FR-104a's wording is
// "a supermajority (>= 2/3 of RESOLVED targets)", and the same requirement
// states the purpose the threshold exists for: to stop the one guess this
// run was observed making, `contact.related` -> `to: task` "on a 2-of-5
// plurality".
//
// Those two halves of FR-104a disagree, and the vault settles which is meant.
// `contact.related` holds FIVE wikilink values: two resolve to `task`, one to
// `person`, and two resolve to nothing at all. Counting RESOLVED targets only,
// task holds 2 of 3 — which clears 2/3 exactly, and the property is declared
// `to: task` again. The threshold would have been added, and the guess it was
// written to stop would have survived it. Counting the property's LINKS, task
// holds 2 of 5, and it is refused, which is the outcome the requirement
// describes in words.
//
// So the denominator is every link value: resolved, resolved-to-a-non-record,
// and dangling alike. That is also the reading that makes sense on its own
// terms — a link pointing at nothing is evidence that this property is not a
// reliably typed relation, and dropping those links from the denominator
// inflates confidence in exactly the properties where the vault is messiest.
// BOTH ratios are carried on the report (StrictNumerator/StrictDenominator)
// and printed, so a reader can see what the narrower reading would have said
// without taking this comment's word for it.
//
// WHAT THIS REPLACED, AND WHY THE THRESHOLD EXISTS. The previous rule was
// "return the PLURALITY whenever ANY link resolved to ANY record type at
// all (even a weak plurality), and report the split". Run against the
// founder's vault that produced `contact.related` -> `to: task` on a
// 2-of-5 plurality: a declaration with 60% of the evidence against it,
// emitted with the same confidence as a unanimous one. The old comment
// argued the REPORT made that honest. It did not: the schema on disk says
// `to: task` either way, validation then reports every non-task link as a
// mismatch, and the operator is left reading 3 findings caused by the
// importer's own guess. 2/3 is where guessing has to stop.
//
// FR-034 has not moved: a `relation` with no `to:` is REJECTED at load
// time, taking the whole record type down with it. So "no majority" cannot
// mean "declare a relation and leave `to:` blank" — it means declare the
// property `text`, which is exactly what classifyProperty does with the
// empty string this function returns.
func inferRelationTarget(recordType string, po *PropertyObservation, names *NameIndex) (string, *RelationSplitReport) {
	byType := map[string]int{}
	unresolved := 0
	ambiguous := 0
	resolvedTotal := 0
	linkTotal := 0
	for _, v := range po.Values {
		if v.Block {
			continue // FR-030a: a block scalar is not a link (isWikilinkValue)
		}
		link, ok := records.ParseWikilink(v.Text)
		if !ok {
			continue
		}
		linkTotal++
		types, found := names.Resolve(link.Target)
		if !found {
			unresolved++
			continue
		}
		// EVERY COUNT HERE IS PER LINK. The numerator used to be incremented
		// once per (link x matching type) while the denominator counted links,
		// and the two are not comparable: NameIndex.Resolve returns a SLICE
		// precisely because two notes in different folders can share a title.
		// Three `deal` notes each linking `[[Acme]]`, where `companies/Acme.md`
		// is a company and `vendors/Acme.md` a vendor, gave bestCount=3 against
		// linkTotal=3 — 9 >= 6, a "supermajority" — when the true evidence is
		// an exact 3-of-6 tie and the winner was decided by the alphabetical
		// tie-break in highestCount. `to: company` on a coin toss.
		//
		// So a link that resolves to MORE THAN ONE record type is evidence for
		// NEITHER. It is not unresolved (the target exists) and it is not a
		// vote (which target the operator meant is exactly what this package
		// cannot know), so it is counted on its own and named in the report.
		distinct := distinctRecordTypes(types)
		switch len(distinct) {
		case 0:
			// Resolved to a real note that is not a record. Neither a vote nor
			// a dangling link — it simply carries no type evidence.
		case 1:
			byType[distinct[0]]++
			resolvedTotal++
		default:
			ambiguous++
		}
	}

	rep := &RelationSplitReport{
		RecordType:     recordType,
		Property:       po.Name,
		ByType:         byType,
		ResolvedTotal:  resolvedTotal,
		LinkTotal:      linkTotal,
		Unresolved:     unresolved,
		AmbiguousLinks: ambiguous,
	}

	if resolvedTotal == 0 {
		rep.Rule = RelationUnresolved
		rep.Declared = "text"
		rep.Remedy = relationRemedy(recordType, po.Name, nil)
		return "", rep
	}

	bestType, bestCount := highestCount(byType)
	rep.MajorityType, rep.MajorityCount = bestType, bestCount
	rep.StrictNumerator, rep.StrictDenominator = bestCount, resolvedTotal

	if len(byType) == 1 && unresolved == 0 && ambiguous == 0 {
		// Unanimous: every link this property holds resolved, and every
		// one of them pointed at the same type. Nothing to report beyond
		// the schema itself, which is why this is the one branch that
		// returns a nil report.
		rep.Rule = RelationUnanimous
		return bestType, nil
	}

	rep.Minority = minorityCounts(byType, bestType)

	// FR-104a's threshold, in exact integer arithmetic (see the constants),
	// over the property's LINKS — see this function's header on why that is
	// the denominator and not the resolved subset.
	if relationSupermajorityDen*bestCount >= relationSupermajorityNum*linkTotal {
		rep.Rule = RelationSupermajority
		rep.Declared = "relation"
		return bestType, rep
	}

	rep.Rule = RelationNoMajority
	rep.Declared = "text"
	rep.MajorityType, rep.MajorityCount = "", 0
	rep.Minority = minorityCounts(byType, "")
	rep.Remedy = relationRemedy(recordType, po.Name, sortedTypeNames(byType))
	return "", rep
}

// highestCount returns the type with the most resolved links, breaking a tie
// by name so the answer does not depend on Go's randomised map iteration
// order — the previous implementation's tie-break compared against an empty
// initial string, which no name is ever less than, making a tie resolve to
// whichever key the runtime happened to visit first.
func highestCount(byType map[string]int) (string, int) {
	best, bestCount := "", 0
	for _, t := range sortedTypeNames(byType) {
		if byType[t] > bestCount {
			best, bestCount = t, byType[t]
		}
	}
	return best, bestCount
}

// distinctRecordTypes reduces one link's resolved types to the distinct,
// non-empty record types it points at. The empty string means "a real note
// that is not a record" (BuildNameIndex's own encoding) and is not a type.
func distinctRecordTypes(types []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(types))
	for _, t := range types {
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func sortedTypeNames(byType map[string]int) []string {
	out := make([]string, 0, len(byType))
	for t := range byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// minorityCounts renders every resolved type other than `majority` as
// "<type> x<count>", sorted. With an empty majority it renders every type —
// the no-majority branch's whole evidence set.
func minorityCounts(byType map[string]int, majority string) []string {
	var out []string
	for _, t := range sortedTypeNames(byType) {
		if t == majority {
			continue
		}
		out = append(out, fmt.Sprintf("%s x%d", t, byType[t]))
	}
	return out
}

// relationRemedy is FR-104a's "names the one-line knowledge_configure edit"
// — the report must hand the operator the fix, not just the problem.
func relationRemedy(recordType, property string, candidates []string) string {
	if len(candidates) == 0 {
		return fmt.Sprintf(
			"no link resolved to a record type, so there is no candidate to propose; once you know the target, one edit settles it: knowledge_configure set schema %s property %s type=relation to=<target-type>",
			recordType, property)
	}
	return fmt.Sprintf(
		"the evidence is genuinely mixed (%s); pick one and it is a one-line edit: knowledge_configure set schema %s property %s type=relation to=<%s>",
		strings.Join(candidates, " | "), recordType, property, strings.Join(candidates, "|"))
}

// adoptionSourcesNamed caps how many observing record types an account line
// names before it counts the rest.
const adoptionSourcesNamed = 4

// AccountLine renders one adoption the way the schema file reports it — what
// changed, what decided it, and the single edit that overrides it.
func (a AdoptedDomain) AccountLine() string {
	names := make([]string, 0, len(a.Sources))
	total := 0
	for _, s := range a.Sources {
		names = append(names, s.RecordType)
		total += s.Observed
	}
	shown := names
	if len(shown) > adoptionSourcesNamed {
		shown = shown[:adoptionSourcesNamed]
	}
	more := ""
	if len(names) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(names)-len(shown))
	}
	values := ""
	if a.Type == records.TypeEnum {
		values = fmt.Sprintf(" values=%s", strings.Join(a.EnumValues, ","))
	}
	to := ""
	if a.To != "" {
		to = " to=" + a.To
	}
	return fmt.Sprintf(
		"`%s` is declared %s here, adopted from the %d record type(s) that observed it (%s%s — %d note(s) with a value between them). No note of `%s` carries a value for it, so this run had no evidence of its own and %s was only the fallback. Override in one edit: knowledge_configure set schema %s property %s type=%s%s%s",
		a.Property, a.Type, len(a.Sources), strings.Join(shown, ", "), more, total,
		a.RecordType, a.Was, a.RecordType, a.Property, a.Type, to, values)
}

// AccountLine renders one refusal the same way.
func (d DeclinedAdoption) AccountLine() string {
	names := make([]string, 0, len(d.Sources))
	for _, s := range d.Sources {
		names = append(names, s.RecordType)
	}
	shown := names
	if len(shown) > adoptionSourcesNamed {
		shown = shown[:adoptionSourcesNamed]
	}
	more := ""
	if len(names) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(names)-len(shown))
	}
	return fmt.Sprintf(
		"`%s` is left `text`, which is this run's fallback and not a reading of any data: no note of `%s` carries a value for it. %d other record type(s) DO observe it (%s%s) and every one of them declares it an enum, but their vocabularies union to %d distinct values — past the %d at which this run stops treating a set as a closed vocabulary at all. That is evidence they are separate vocabularies sharing a name, not one enum, so nothing was adopted. If this type really does use one of them, one edit says so: knowledge_configure set schema %s property %s type=enum values=<the values it uses>",
		d.Property, d.RecordType, len(d.Sources), strings.Join(shown, ", "), more,
		d.UnionSize, d.Bound, d.RecordType, d.Property)
}

// AdoptObservedDomains applies the precedence rule above IN PLACE and returns
// the account of every adoption and every refusal, in (record type, property)
// order.
//
// IT TAKES `notes` RATHER THAN LEANING ON InferredProperty.ObservedCount, for
// the reason TypePropertiesFromBaseFormulas states next door: ObservedCount is
// frozen by CollectTypeGroups BEFORE FR-104b's InferTypesForUntypedNotes writes
// `type:` into untyped notes. A note that JOINS a record type mid-run is
// invisible to that count, and this rule's whole safety argument is that no
// note holds a value the adopted type could reject.
func AdoptObservedDomains(inferred map[string][]InferredProperty, notes []NoteRecord) ([]AdoptedDomain, []DeclinedAdoption) {
	valued := valuedPropertiesByType(notes)
	listed := listShapedPropertiesByType(notes)
	recordTypes := sortedStringKeys(inferred)

	var adopted []AdoptedDomain
	var declined []DeclinedAdoption
	for _, rt := range recordTypes {
		props := append([]string(nil), propertyNames(inferred[rt])...)
		sort.Strings(props)
		for _, name := range props {
			if !adoptionEligibleTarget(inferred[rt], name, valued[rt], listed[rt]) {
				continue
			}
			domain, sources, ok := observedDomainFor(inferred, recordTypes, rt, name, valued)
			if !ok {
				continue
			}
			if domain.Type == records.TypeEnum && len(domain.EnumValues) > adoptedEnumMaxDistinct {
				d := DeclinedAdoption{
					RecordType: rt, Property: name, Sources: sources,
					UnionSize: len(domain.EnumValues), Bound: adoptedEnumMaxDistinct,
				}
				declined = append(declined, d)
				if idx := indexOfProperty(inferred[rt], name); idx >= 0 {
					stored := d
					inferred[rt][idx].DomainAdoptionDeclined = &stored
				}
				continue
			}
			idx := indexOfProperty(inferred[rt], name)
			if idx < 0 {
				continue
			}
			acct := AdoptedDomain{
				RecordType: rt,
				Property:   name,
				Was:        inferred[rt][idx].Type,
				Type:       domain.Type,
				To:         domain.To,
				EnumValues: append([]string(nil), domain.EnumValues...),
				Sources:    sources,
			}
			inferred[rt][idx].Type = domain.Type
			inferred[rt][idx].To = domain.To
			inferred[rt][idx].EnumValues = append([]string(nil), domain.EnumValues...)
			inferred[rt][idx].Kind = ClassifyDomainAdopted
			stored := acct
			inferred[rt][idx].DomainAdopted = &stored
			adopted = append(adopted, acct)
		}
	}
	return adopted, declined
}

// adoptionEligibleTarget is clauses 1-4, in one place, so a reviewer reads them
// together rather than reconstructing them from the call site.
func adoptionEligibleTarget(props []InferredProperty, name string, valued, listed map[string]bool) bool {
	p, ok := findInferredProperty(props, name)
	if !ok {
		return false // (1) never invents a property
	}
	if valued[records.FoldKey(name)] {
		return false // (2) data beats an absence of data, as data beats a name
	}
	if p.Type != records.TypeText {
		return false // (3) only ever strengthens the fallback
	}
	// (4) adoption changes the type and never the arity, and `tags: []` is the
	// one shape clause 2 cannot see.
	return !p.Many && !listed[records.FoldKey(name)]
}

// observedDomainFor is clauses 5-6: the single domain every record type that
// OBSERVED this property agrees on, with the observing types named. It returns
// ok=false when nothing observed the property, when the observers disagree, or
// when the agreed domain is one there would be nothing to adopt from (`text`,
// or a `many` the target may not take).
func observedDomainFor(inferred map[string][]InferredProperty, recordTypes []string, target, name string,
	valued map[string]map[string]bool) (InferredProperty, []AdoptionSource, bool) {
	var domain InferredProperty
	var sources []AdoptionSource
	var enumUnion []string
	seenValue := map[string]bool{}
	first := true
	for _, rt := range recordTypes {
		if rt == target || !valued[rt][records.FoldKey(name)] {
			continue
		}
		p, ok := findInferredProperty(inferred[rt], name)
		if !ok {
			continue
		}
		if first {
			domain, first = p, false
		} else if !sameInferredDomain(domain, p) {
			return InferredProperty{}, nil, false // (6) the observations disagree
		}
		sources = append(sources, AdoptionSource{RecordType: rt, Observed: p.ObservedCount})
		for _, v := range p.EnumValues {
			key := records.FoldKey(strings.TrimSpace(v))
			if seenValue[key] {
				continue
			}
			seenValue[key] = true
			enumUnion = append(enumUnion, v)
		}
	}
	if first {
		return InferredProperty{}, nil, false // (5) nothing observed it
	}
	if domain.Type == records.TypeText || domain.Many {
		return InferredProperty{}, nil, false // nothing to adopt, or clause 4
	}
	sort.Slice(enumUnion, func(i, j int) bool {
		return records.FoldKey(strings.TrimSpace(enumUnion[i])) < records.FoldKey(strings.TrimSpace(enumUnion[j]))
	})
	return InferredProperty{Type: domain.Type, To: domain.To, EnumValues: enumUnion}, sources, true
}

// listShapedPropertiesByType answers clause 4 over the FINAL note set: for each
// record type, which of its properties any note wrote as a YAML SEQUENCE —
// including an EMPTY one. An empty list contributes no value (so
// valuedPropertiesByType cannot see it) and yet records.Validate reaches the
// arity check on it, which is exactly the case a type change must not disturb.
// Property names are FOLDED, for the same reason its sibling folds them.
func listShapedPropertiesByType(notes []NoteRecord) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for i := range notes {
		rec := notes[i].Rec
		rt := rec.TypeName()
		if rt == "" {
			continue
		}
		for _, key := range rec.Frontmatter.Keys {
			var po PropertyObservation
			po.Name = key
			collectNodeValues(&po, rec.Frontmatter.Values[key], notes[i].RelPath)
			if len(po.ListNotes) == 0 {
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

// propertyNames lists an inferred schema's property names in declaration order.
func propertyNames(props []InferredProperty) []string {
	out := make([]string, 0, len(props))
	for _, p := range props {
		out = append(out, p.Name)
	}
	return out
}

// CollectDomainAdoptions gathers every adoption account off an inferred schema
// set, in (record type, property) order — the same shape
// CollectNameEvidencedInferences and CollectEnumWidenings already have, so a
// caller asks this package for its own decisions rather than threading a list.
func CollectDomainAdoptions(inferred map[string][]InferredProperty) []AdoptedDomain {
	var out []AdoptedDomain
	for _, rt := range sortedStringKeys(inferred) {
		for _, p := range inferred[rt] {
			if p.DomainAdopted != nil {
				out = append(out, *p.DomainAdopted)
			}
		}
	}
	return out
}
