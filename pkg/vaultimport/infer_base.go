// infer_base.go: Type properties from the vault's own `.base` file declarations (enum widening, formula evidence, summary evidence)

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
)

// ReportLines renders this account for the import report and for the schema
// file's own header, in the operator's terms.
func (w EnumWidening) ReportLines() []string {
	var lines []string
	if w.Refused {
		lines = append(lines, fmt.Sprintf(
			"%s.%s: NOT widened. %s filter(s) on %s, which no note of this type carries; admitting them would take the closed set to %d values, past the %d at which this run stops treating a property as an enum at all. The set is left exactly as the notes made it (%s) and those clauses stay named losses — a closed set that large is evidence the property was mis-inferred, not evidence for a wider set.",
			w.RecordType, w.Property, joinBaseFiles(w.Bases), quoteJoin(w.Requested),
			len(w.Observed)+len(w.Requested), w.Bound, quoteJoin(w.Observed)))
	} else {
		lines = append(lines, fmt.Sprintf(
			"%s.%s: the closed set gained %s, which no note of this type carries — %s filter(s) on it, so it is the operator's own word for a legal value rather than an observation. Observed values were %s. An imported view filtering on it returns exactly what the Obsidian original returns, which today is no rows.",
			w.RecordType, w.Property, quoteJoin(w.Added), joinBaseFiles(w.Bases), quoteJoin(w.Observed)))
	}
	for _, v := range w.Requested {
		near, ok := w.Nearest[v]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"CHECK FOR A TYPO — %q is one edit from the observed value %q. If it is a slip, this filter matches nothing for as long as the `.base` file says %q — in Obsidian too, which is why it was carried across rather than dropped. Fix it in the base file, not here.",
			v, near, v))
	}
	lines = append(lines, fmt.Sprintf(
		"correct the set in a single edit: knowledge_configure set schema %s property %s type=enum values=%s",
		w.RecordType, w.Property, strings.Join(w.Observed, ",")))
	return lines
}

// enumWideningAcc accumulates one property's requested literals across every
// base and view that named them, before any decision is made.
type enumWideningAcc struct {
	bases     map[string]struct{}
	requested map[string]string // folded key -> the operator's spelling
}

// WidenEnumsFromBases admits into each inferred enum's closed set the literals
// the operator's own `.base` files assert membership against, MUTATING
// `inferred` in place, and returns the account of every property it touched
// (including the ones it refused to touch).
//
// It must run after every schema is inferred and provisioned, and BEFORE both
// writeSchemas and NewSchemaIndex: the admitted value has to reach the WRITTEN
// schema, or the clause would be admitted at translate time and then refused
// by records at query time — a view that imports clean and returns an error.
func WidenEnumsFromBases(inferred map[string][]InferredProperty, relPaths []string, parsed map[string]*ParsedBase) []EnumWidening {
	byType := map[string]map[string]*enumWideningAcc{}

	for _, rel := range relPaths {
		pb := parsed[rel]
		if pb == nil {
			continue
		}
		outer := TranslateFilterTree(pb.Filters)
		outerLeaves := collectV2Leaves(outer.Root)

		for _, vraw := range pb.Views {
			viewTrans := TranslateFilterTree(vraw["filters"])
			rt, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
			if conflict != "" || rt == "" {
				// An UNTYPED view (FR-018b) queries every note in scope, so a
				// property name in it is not scoped to one record type and its
				// literal cannot be attributed to one vocabulary. Guessing
				// which type was meant would rewrite every type that happens
				// to share the property name.
				continue
			}
			declared := inferred[rt]
			if declared == nil {
				continue
			}
			leaves := append(append([]v2Leaf{}, outerLeaves...), collectV2Leaves(viewTrans.Root)...)
			for _, l := range leaves {
				prop, ok := findInferredProperty(declared, l.Property)
				if !ok || !leafAssertsEnumMembership(l, prop) {
					// Never invents a property. Adding a VALUE to a vocabulary
					// that exists and CREATING the vocabulary are different
					// questions, and the second one belongs to the template
					// rule and to FR-018d provisioning.
					continue
				}
				if _, already := canonicalEnumValue(prop, l.Value); already {
					// The translator would already resolve this literal, so
					// nothing is added and nothing is reported. Reporting it
					// would be a correction notice for a value that never
					// changed.
					continue
				}
				byProp := byType[rt]
				if byProp == nil {
					byProp = map[string]*enumWideningAcc{}
					byType[rt] = byProp
				}
				a := byProp[l.Property]
				if a == nil {
					a = &enumWideningAcc{bases: map[string]struct{}{}, requested: map[string]string{}}
					byProp[l.Property] = a
				}
				a.bases[rel] = struct{}{}
				if key := records.FoldKey(strings.TrimSpace(l.Value)); key != "" {
					if _, seen := a.requested[key]; !seen {
						a.requested[key] = strings.TrimSpace(l.Value)
					}
				}
			}
		}
	}

	var out []EnumWidening
	for _, rt := range sortedStringKeys(byType) {
		for _, propName := range sortedStringKeys(byType[rt]) {
			a := byType[rt][propName]
			idx := indexOfProperty(inferred[rt], propName)
			if idx < 0 {
				continue
			}
			requested := make([]string, 0, len(a.requested))
			for _, key := range sortedStringKeys(a.requested) {
				requested = append(requested, a.requested[key])
			}
			if len(requested) == 0 {
				continue
			}
			observed := append([]string(nil), inferred[rt][idx].EnumValues...)

			w := EnumWidening{
				RecordType: rt,
				Property:   propName,
				Observed:   observed,
				Requested:  requested,
				Bases:      sortedStringKeys(a.bases),
				Nearest:    records.NearestWithinOneEdit(requested, observed),
			}
			if inferred[rt][idx].DomainAdopted != nil {
				w.Bound = adoptedEnumMaxDistinct
			} else {
				w.Bound = enumMaxDistinct
			}
			// An ADOPTED vocabulary is already a union and is held to the
			// union bound; an OBSERVED one is this type's own data and keeps
			// the single-type ceiling it was inferred under. Without this
			// split the adoption above is self-defeating: `invoice.status`
			// takes the 26-value union, the base file then asks for `paid`,
			// and widening refuses at 15 — leaving the view working and the
			// filter that motivated it broken.
			if len(observed)+len(requested) > w.Bound {
				w.Refused = true
			} else {
				w.Added = requested
				widened := append(append([]string(nil), observed...), requested...)
				sort.Slice(widened, func(i, j int) bool {
					return records.FoldCompare(records.FoldKey(widened[i]), records.FoldKey(widened[j])) < 0
				})
				inferred[rt][idx].EnumValues = widened
			}
			stored := w
			inferred[rt][idx].EnumWidened = &stored
			out = append(out, w)
		}
	}
	return out
}

// CollectEnumWidenings gathers every enum-widening account off an inferred
// schema set, in a stable order — the same shape as
// CollectNameEvidencedInferences, so the report renders all of this package's
// honesty payloads the same way.
func CollectEnumWidenings(inferred map[string][]InferredProperty) []EnumWidening {
	var out []EnumWidening
	for _, props := range inferred {
		for _, p := range props {
			if p.EnumWidened != nil {
				out = append(out, *p.EnumWidened)
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

// leafAssertsEnumMembership reports whether one filter leaf is the operator
// asserting that its literal BELONGS TO the property's closed set — which is
// exactly the set of positions where buildV2LeafNode tests the literal against
// that set, and no others.
//
// The alignment with buildV2LeafNode is the whole safety condition. A literal
// admitted from a position that clause never consults would change a schema an
// operator reads without changing one row of one view; a position that clause
// DOES consult and this one misses leaves the view disabled for no reason.
func leafAssertsEnumMembership(l v2Leaf, prop InferredProperty) bool {
	if prop.Type != records.TypeEnum || strings.TrimSpace(l.Value) == "" {
		return false
	}
	switch l.Shape {
	case shapeCompare:
		// Equality is the membership claim. An ORDERING comparison is not:
		// in `status > "m"`, `m` is a comparison BOUND, not a state any note
		// is meant to carry, and admitting it would put it in the vocabulary
		// forever — offered as a groupBy bucket and a filter suggestion.
		// (`!=` reaches here as a tree negation over an `=` leaf, per
		// nodeFromRawLeaf, so it is covered by this same branch.)
		return l.Op == generated.VaultFilterNodeOpEqual || l.Op == generated.VaultFilterNodeOpLessThanGreaterThan
	case shapeContains:
		// R-9 makes `.contains` element membership on a LIST — the same claim
		// `==` makes on a scalar. On a scalar enum buildV2LeafNode refuses it
		// before ever consulting the closed set, so it asserts nothing.
		return prop.Many
	}
	return false
}

func quoteJoin(vs []string) string {
	if len(vs) == 0 {
		return "(none)"
	}
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, fmt.Sprintf("%q", v))
	}
	return strings.Join(out, ", ")
}

func joinBaseFiles(vs []string) string {
	if len(vs) == 0 {
		return "a base"
	}
	return strings.Join(vs, ", ")
}

// dateEvidencingFunctions is the closed set of formula functions whose
// application to a BARE property name is read as the operator declaring that
// property a date.
//
// It is a map rather than a condition so the whole of it is visible at once.
// Adding an entry is a decision to take on the terms this section's header
// states — every accepted operand type of the function must denote the same
// concept — and never a convenience.
var dateEvidencingFunctions = map[string]bool{
	"date": true,
	"time": true,
}

// dateEvidenceRank orders the shapes when one property is reached through more
// than one of them in a single base, so the recorded spelling is a decision
// rather than an accident of lexicographic order.
//
// A CONVERSION outranks a SUBTRACTION: `date(x)` names the property inside a
// call the founder can point at, while the subtraction is a fact about the
// whole expression, and the more specific text is the more useful thing to
// quote back at him. Ties inside a rank fall to the spelling, alphabetically.
//
// Without this, the ordering would be plain string comparison over the four
// names, which puts `now` ahead of `time` — a subtraction beating a conversion
// because of where the letters land. Nothing in the founder's vault reaches
// one property through two shapes today, so this is not load-bearing on his
// data; it is here because "deterministic" and "chosen" are different claims
// and only the second one survives someone adding a fifth spelling.
func dateEvidenceRank(name string) int {
	if dateEvidencingFunctions[name] {
		return 0
	}
	return 1
}

// betterDateEvidence answers whether `cand` should replace `have` as the
// recorded spelling for one property. `have` empty means there is nothing to
// beat.
func betterDateEvidence(have, cand string) bool {
	if have == "" {
		return true
	}
	if r, h := dateEvidenceRank(cand), dateEvidenceRank(have); r != h {
		return r < h
	}
	return cand < have
}

// dateSubtrahendFunctions is the closed set of ZERO-ARGUMENT date constructors
// whose appearance on the other side of a `-` from a BARE property name is
// read as the operator declaring that property a date.
//
// The entries are the whole of `inferCall`'s `case "today", "now":` arm — one
// arm, returning `date` for both with no operand to inspect — so this map is a
// transcription of the grammar rather than a selection from it. That matters:
// the rule's soundness is "the token's type is a constant", and a spelling
// admitted here whose type were NOT constant would silently reintroduce the
// second inference the header's old objection (a) was about.
//
// It is deliberately SEPARATE from dateEvidencingFunctions rather than merged
// into one map with a flag. The two are read in different positions of
// different node kinds — one inside a Call's argument list, one across a
// BinaryOp — and a single map would let a `date` reach the subtraction branch
// or a `today` reach the call branch, neither of which means anything.
var dateSubtrahendFunctions = map[string]bool{
	"today": true,
	"now":   true,
}

// ReportLines renders one formula-evidenced type, first line first.
//
// It lives beside the rule that produces it for the reason
// NameEvidencedInference.ReportLines states about itself: the same account is
// what the operator reads in the run report, and a second spelling of it
// elsewhere is how two accounts of one decision drift apart.
func (f FormulaEvidencedType) ReportLines() []string {
	lines := make([]string, 0, len(f.Evidence)+2)
	lines = append(lines, fmt.Sprintf("%s.%s -> %s (was %s; no note of this type carries a value for it)",
		f.RecordType, f.Property, f.Type, f.Was))
	for _, e := range f.Evidence {
		// Two shapes, two sentences. The founder is being sent to a specific
		// piece of his own text, and telling him a subtraction "reads `x`
		// through today()" would send him looking for a call he never wrote.
		if e.Op != "" {
			lines = append(lines, fmt.Sprintf(
				"evidence: %s totals it — a view there declares `%s` under `summaries:`, and %s is defined over numbers and nothing else, so the only reading under which that view works at all is a number",
				e.Base, e.Source, e.Op))
			continue
		}
		if dateSubtrahendFunctions[e.Function] {
			lines = append(lines, fmt.Sprintf(
				"evidence: %s declares `%s: %s`, which subtracts the bare `%s` against %s() — and `-` is defined over two dates or two numbers, never one of each, so the only reading under which that expression works at all is a date",
				e.Base, e.Formula, e.Source, f.Property, e.Function))
			continue
		}
		lines = append(lines, fmt.Sprintf("evidence: %s declares `%s: %s`, which reads `%s` through %s()",
			e.Base, e.Formula, e.Source, f.Property, e.Function))
	}
	lines = append(lines, fmt.Sprintf(
		"overrule it in a single edit: knowledge_configure set schema %s property %s type=text",
		f.RecordType, f.Property))
	return lines
}

// CollectFormulaEvidencedTypes gathers every formula-based type decision an
// import made, in a stable order, ready for the run report to print.
//
// Same shape as CollectNameEvidencedInferences and CollectEnumWidenings, so
// the report renders all of this package's honesty payloads the same way and
// asks this package for its own decisions rather than having them threaded
// through by the caller.
func CollectFormulaEvidencedTypes(inferred map[string][]InferredProperty) []FormulaEvidencedType {
	var out []FormulaEvidencedType
	for _, props := range inferred {
		for _, p := range props {
			if p.FormulaEvidenced != nil {
				out = append(out, *p.FormulaEvidenced)
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

// TypePropertiesFromBaseFormulas re-reads as `date` every value-less `text`
// property that a `.base` file's own formula applies `date()` or `time()` to,
// MUTATING `inferred` in place, and returns the account of every property it
// touched.
//
// It must run after every schema is inferred and provisioned, and BEFORE both
// writeSchemas and NewSchemaIndex — see this section's header for why the
// position is load-bearing rather than incidental.
//
// `notes` is the note set as it stands AFTER FR-104b has written `type:` into
// what it could decide, and passing it is the whole of containment clause 2.
// The count frozen on InferredProperty at inference time answers a question
// about a population that is not final yet; see the clause.
func TypePropertiesFromBaseFormulas(
	inferred map[string][]InferredProperty,
	notes []NoteRecord,
	relPaths []string,
	parsed map[string]*ParsedBase,
) []FormulaEvidencedType {
	// recordType -> property -> the evidence gathered for it.
	byType := map[string]map[string][]FormulaEvidence{}

	// Clause 2's population, computed ONCE from the final note set rather
	// than per candidate, and built over EVERY property every note carries
	// rather than only the ones evidence is later found for — so the gate
	// cannot be narrowed by an error in the evidence walk.
	valued := valuedPropertiesByType(notes)

	for _, rel := range relPaths {
		pb := parsed[rel]
		if pb == nil || len(pb.FormulaNames) == 0 {
			continue
		}

		// Which properties this base's formulas read as dates, and on whose
		// authority. Computed once per base: the `formulas:` block is one
		// block, and TranslateFormulas type-checks all of it against every
		// view's record type, so the evidence is the base's, not a view's.
		dated := map[string][]FormulaEvidence{}
		for _, name := range pb.FormulaNames {
			src, readable := pb.Formulas[name]
			if !readable {
				// A formula whose value was not a scalar string. The
				// translator refuses it by name; this rule cannot read a
				// source that does not exist.
				continue
			}
			root, perr := records.ParseFormula(src)
			if perr != nil {
				// An unparseable formula is already a NAMED loss on every
				// view that uses it. Reading a type out of text this product
				// could not parse would be inventing evidence.
				continue
			}
			carried := datePropertyArguments(root)
			for _, prop := range sortedStringKeys(carried) {
				dated[prop] = append(dated[prop], FormulaEvidence{
					Base: rel, Formula: name, Source: strings.TrimSpace(src),
					Function: carried[prop],
				})
			}
		}
		if len(dated) == 0 {
			continue
		}

		outer := TranslateFilterTree(pb.Filters)
		for _, vraw := range pb.Views {
			viewTrans := TranslateFilterTree(vraw["filters"])
			rt, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
			if conflict != "" || rt == "" {
				// An UNTYPED view (FR-018b) queries every note in scope, so a
				// property name in it is not scoped to one record type and a
				// declaration cannot be attributed to one schema. The same
				// clause WidenEnumsFromBases states, for the same reason.
				continue
			}
			for _, prop := range sortedStringKeys(dated) {
				if !typeEligibleForFormulaEvidence(inferred[rt], prop, valued[rt]) {
					continue
				}
				byProp := byType[rt]
				if byProp == nil {
					byProp = map[string][]FormulaEvidence{}
					byType[rt] = byProp
				}
				byProp[prop] = appendUnseenFormulaEvidence(byProp[prop], dated[prop])
			}
		}
	}

	var out []FormulaEvidencedType
	for _, rt := range sortedStringKeys(byType) {
		for _, prop := range sortedStringKeys(byType[rt]) {
			idx := indexOfProperty(inferred[rt], prop)
			if idx < 0 {
				continue
			}
			evidence := byType[rt][prop]
			sort.Slice(evidence, func(i, j int) bool {
				if evidence[i].Base != evidence[j].Base {
					return evidence[i].Base < evidence[j].Base
				}
				return evidence[i].Formula < evidence[j].Formula
			})
			fe := FormulaEvidencedType{
				RecordType: rt,
				Property:   prop,
				Type:       records.TypeDate,
				Was:        inferred[rt][idx].Type,
				Evidence:   evidence,
			}
			inferred[rt][idx].Type = records.TypeDate
			inferred[rt][idx].Kind = ClassifyDateFromFormula
			stored := fe
			inferred[rt][idx].FormulaEvidenced = &stored
			out = append(out, fe)
		}
	}
	return out
}

// typeEligibleForFormulaEvidence is the four containment clauses, in one
// place, so a reviewer reads them together rather than reconstructing them
// from the call site. See this section's header for the argument behind each.
func typeEligibleForFormulaEvidence(props []InferredProperty, name string, valued map[string]bool) bool {
	p, ok := findInferredProperty(props, name)
	if !ok {
		return false // (1) never invents a property
	}
	if valued[records.FoldKey(name)] {
		return false // (2) data beats a base file, as data beats a name
	}
	if p.Type != records.TypeText {
		return false // (3) only ever strengthens the fallback
	}
	return !p.Many // (4) W2 refuses a `many` date anyway
}

// appendUnseenFormulaEvidence adds evidence not already recorded — one base's
// formulas are read once per VIEW that resolves to a type, and two views of
// one type would otherwise record the same formula twice.
func appendUnseenFormulaEvidence(have, add []FormulaEvidence) []FormulaEvidence {
	for _, e := range add {
		seen := false
		for _, h := range have {
			if h.Base == e.Base && h.Formula == e.Formula {
				seen = true
				break
			}
		}
		if !seen {
			have = append(have, e)
		}
	}
	return have
}

// datePropertyArguments returns, keyed by property name, every BARE property
// reference in an expression that the operator's own text declares to be a
// date, with the grammar term that carried each. Two shapes qualify and they
// are checked independently on every node:
//
//   - a dateEvidencingFunctions CALL applied to the property — `date(x)`;
//   - a `-` with the property bare on one side and a zero-argument
//     dateSubtrahendFunctions constructor on the other — `today() - x`.
//
// BARE is the whole restriction, in both. `date(x)` says x is a date;
// `date(x + "-01")` says something about an expression OVER x, and
// `date(file.name)` is not about a property at all. `today() - x` says x is a
// date; `today() - date(x)` is the first shape wearing the second's clothes,
// and `a - b` between two properties says nothing about either. Anything but a
// single RefProperty in the property position answers no, which keeps this a
// reading of what the operator wrote rather than an inference about it.
//
// One property reached through MORE THAN ONE of these in a single expression
// is reported ONCE, keeping the spelling betterDateEvidence picks: they are
// one declaration, and the founder is being pointed at a formula, not at a
// call count.
//
// The walk is ITERATIVE for the reason records.countNodes is: it runs on a
// freshly parsed tree, BEFORE FR-146's depth cap has been applied to it, so it
// must survive the tree that cap exists to refuse.
func datePropertyArguments(root records.FormulaNode) map[string]string {
	found := map[string]string{}
	stack := []records.FormulaNode{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		if call, ok := n.(*records.Call); ok && dateEvidencingFunctions[call.Name] && len(call.Args) == 1 {
			if ref, isRef := call.Args[0].(*records.Ref); isRef && ref.Kind == records.RefProperty {
				if betterDateEvidence(found[ref.Name], call.Name) {
					found[ref.Name] = call.Name
				}
			}
		}
		if bin, ok := n.(*records.BinaryOp); ok && bin.Op == "-" {
			if prop, ctor := bareMinusDateConstructor(bin); prop != "" {
				if betterDateEvidence(found[prop], ctor) {
					found[prop] = ctor
				}
			}
		}
		stack = append(stack, formulaChildren(n)...)
	}
	return found
}

// bareMinusDateConstructor reads one `-` node and answers, when the node is a
// bare property subtracted from a zero-argument date constructor or the
// reverse, the property name and the constructor that carried it. It answers
// "" for every other subtraction.
//
// WHY THIS IS A DECLARATION AND `-` ON ITS OWN IS NOT. records.inferBinary
// gives `-` exactly two typings: `date - date`, which is the only producer of
// a duration in the whole grammar, and number-minus-number, enforced by
// requireNumberOperands. There is no `date - number` and no
// `number - date`. So once ONE side is known to be a date — and `today()` /
// `now()` are known to be dates by the grammar itself, with no operand to
// infer — the expression the operator wrote has exactly one reading under
// which it type-checks at all, and that reading says the other side is a date.
//
// BOTH ORDERS MATCH. `today() - P` and `P - today()` are the same declaration;
// see the header for why refusing the second would make the rule about the
// operator's spelling rather than about his data.
//
// EVERYTHING ELSE ANSWERS "". The constructor side must be a Call with the
// exact name and ZERO arguments as written — a `today(x)` would already have
// been refused by the grammar, and reading evidence out of text the product
// refuses is the mistake the unparseable-formula branch above exists to avoid.
// The property side must be a single RefProperty: `today() - date(x)` belongs
// to the call rule one branch up, `today() - file.ctime` is not a declared
// property, and `today() - (a + b)` says something about an expression rather
// than about either name in it. A subtraction of one property from another
// carries nothing at all and is not matched — that case is the whole of the
// header's old objection (a), and it stays refused.
func bareMinusDateConstructor(bin *records.BinaryOp) (string, string) {
	isCtor := func(n records.FormulaNode) (string, bool) {
		call, ok := n.(*records.Call)
		if !ok || len(call.Args) != 0 || !dateSubtrahendFunctions[call.Name] {
			return "", false
		}
		return call.Name, true
	}
	isBareProp := func(n records.FormulaNode) (string, bool) {
		ref, ok := n.(*records.Ref)
		if !ok || ref.Kind != records.RefProperty {
			return "", false
		}
		return ref.Name, true
	}
	if ctor, ok := isCtor(bin.Left); ok {
		if prop, isProp := isBareProp(bin.Right); isProp {
			return prop, ctor
		}
	}
	if ctor, ok := isCtor(bin.Right); ok {
		if prop, isProp := isBareProp(bin.Left); isProp {
			return prop, ctor
		}
	}
	return "", ""
}

// formulaChildren is records.FormulaNode's operands, in source order.
//
// The `records` package has this as an unexported method on a closed
// interface, so a reader outside it re-states the shape. The node set is
// closed by FR-143 — a new node kind is a specification revision, not a code
// change — so this switch cannot silently fall behind: a kind added there
// arrives with its own spec diff. A kind this switch does not know contributes
// no children, which costs a rule that does not fire, never one that fires on
// a tree it misread.
func formulaChildren(n records.FormulaNode) []records.FormulaNode {
	switch node := n.(type) {
	case *records.UnaryOp:
		return []records.FormulaNode{node.Operand}
	case *records.BinaryOp:
		return []records.FormulaNode{node.Left, node.Right}
	case *records.Call:
		return node.Args
	case *records.FieldAccess:
		return []records.FormulaNode{node.Receiver}
	}
	return nil
}

// ---------------------------------------------------------------------------
// A `.base` SUMMARY AS TYPE EVIDENCE (the number half of the rule above)
//
// A base view that asks for `Sum` over a property is its operator stating, in
// his own file, that the property holds a number. That is the SAME class of
// evidence TypePropertiesFromBaseFormulas reads out of `date(x)` — the
// operator's own written statement about his own property — and it is read
// here under the SAME four containment clauses, by calling the same predicate
// rather than restating it.
//
// WHY THIS EXISTS. The founder's vault declares `invoice.amount` and
// `round.target` nowhere but in the base files that total them: no note of
// either type exists yet. Inference therefore fell back to text, the summary
// gate in view_write.go correctly refused `sum` over text, and three views
// lost their totals — a loss caused entirely by this package declining to read
// a statement the operator had already written down.
//
// WHICH OPS COUNT, AND WHY IT IS DERIVED RATHER THAN LISTED. Only an op that
// knowledgefind defines for the NUMERIC types and for no other type is
// evidence: `min`/`max`/`range` are defined over dates too, so they say
// "number or date" and this rule says nothing on them. That set is COMPUTED
// from knowledgefind's own table at first use, for the reason view_write.go's
// gate states about itself — a second copy of the op/type mapping is the
// mechanism by which the writer and the reader drifted apart, which is the
// defect that gate exists to close. If a future release moves an op between
// domains, this rule follows without being edited.
//
// WHICH NUMERIC TYPE. `decimal`, always. It is the SAFE side of the choice:
// every integer parses as a decimal, while a decimal does not parse as an
// integer, so reading `2500.50` as `integer` would invalidate the founder's
// first real invoice while reading `2500` as `decimal` invalidates nothing.
//
// WHAT PROTECTS THE FOUNDER'S PLACEHOLDER TEXT. Clause 2 of
// typeEligibleForFormulaEvidence — data beats a base file. If ANY note of the
// type carries a value for the property, this rule is silent and the observed
// values decide, exactly as they do today. The rule can only ever speak where
// there is nothing to contradict it.
// ---------------------------------------------------------------------------

// numberEvidencingSummaryOps is the set of summary ops that mean `number` and
// nothing else, derived from knowledgefind's table rather than transcribed
// from it. See this section's header for why it is computed.
func numberEvidencingSummaryOps() map[string]bool {
	numeric := []records.PropertyType{records.TypeInteger, records.TypeDecimal}
	other := []records.PropertyType{
		records.TypeText, records.TypeDate, records.TypeCheckbox,
		records.TypeEnum, records.TypeRelation, records.TypePerson,
	}
	out := map[string]bool{}
	for _, t := range numeric {
		for _, op := range knowledgefind.SummaryOpsDefinedFor(t) {
			out[op] = true
		}
	}
	// An op any NON-numeric type also defines is ambiguous, and an ambiguous
	// statement is not evidence. This removal is what keeps `min`, `max`,
	// `range` (dates), `empty`, `filled` and `unique` (everything) out.
	for op := range out {
		for _, t := range other {
			if knowledgefind.SummaryOpDefinedForType(op, t) {
				delete(out, op)
				break
			}
		}
	}
	return out
}

// TypePropertiesFromBaseSummaries types a property as `decimal` when a `.base`
// view totals it and the vault holds no value that could say otherwise.
//
// It is TypePropertiesFromBaseFormulas for the number case, and it is
// deliberately a SEPARATE function reusing that one's predicate rather than a
// mode flag on it: the two read different parts of a base file (a `formulas:`
// block against a view's `summaries:` map) and produce different types, while
// the containment argument they share is exactly the one they must not be
// allowed to diverge on.
func TypePropertiesFromBaseSummaries(
	inferred map[string][]InferredProperty,
	notes []NoteRecord,
	relPaths []string,
	parsed map[string]*ParsedBase,
) []FormulaEvidencedType {
	numberOps := numberEvidencingSummaryOps()
	byType := map[string]map[string][]FormulaEvidence{}
	valued := valuedPropertiesByType(notes)

	for _, rel := range relPaths {
		pb := parsed[rel]
		if pb == nil {
			continue
		}
		outer := TranslateFilterTree(pb.Filters)
		for _, vraw := range pb.Views {
			summ, isMap := vraw["summaries"].(map[string]any)
			if !isMap || len(summ) == 0 {
				continue
			}
			viewTrans := TranslateFilterTree(vraw["filters"])
			rt, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
			if conflict != "" || rt == "" {
				// An UNTYPED view names properties that are not scoped to one
				// record type, so a declaration in it cannot be attributed to
				// one schema. Same clause, same reason, as the formula rule.
				continue
			}
			for _, prop := range sortedKeys(summ) {
				if strings.HasPrefix(prop, formulaNamespace) || records.IsFileNamespace(prop) {
					// Neither resolves through a schema, so neither is a
					// statement about a schema property. view_write.go's gate
					// declines to judge these for the same reason.
					continue
				}
				opRaw := stringOf(summ[prop])
				opVal, known := aggregateOpFor(opRaw)
				if !known || !numberOps[string(opVal)] {
					continue
				}
				if !typeEligibleForFormulaEvidence(inferred[rt], prop, valued[rt]) {
					continue
				}
				byProp := byType[rt]
				if byProp == nil {
					byProp = map[string][]FormulaEvidence{}
					byType[rt] = byProp
				}
				byProp[prop] = appendUnseenFormulaEvidence(byProp[prop], []FormulaEvidence{{
					Base:     rel,
					Source:   fmt.Sprintf("%s: %s", prop, opRaw),
					Function: string(opVal),
					Op:       string(opVal),
				}})
			}
		}
	}

	var out []FormulaEvidencedType
	for _, rt := range sortedStringKeys(byType) {
		for _, prop := range sortedStringKeys(byType[rt]) {
			idx := indexOfProperty(inferred[rt], prop)
			if idx < 0 {
				continue
			}
			evidence := byType[rt][prop]
			sort.Slice(evidence, func(i, j int) bool {
				if evidence[i].Base != evidence[j].Base {
					return evidence[i].Base < evidence[j].Base
				}
				return evidence[i].Source < evidence[j].Source
			})
			fe := FormulaEvidencedType{
				RecordType: rt,
				Property:   prop,
				Type:       records.TypeDecimal,
				Was:        inferred[rt][idx].Type,
				Evidence:   evidence,
			}
			inferred[rt][idx].Type = records.TypeDecimal
			inferred[rt][idx].Kind = ClassifyNumberFromBaseSummary
			stored := fe
			inferred[rt][idx].FormulaEvidenced = &stored
			out = append(out, fe)
		}
	}
	return out
}
