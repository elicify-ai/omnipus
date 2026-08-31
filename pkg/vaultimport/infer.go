// Omnipus — record-type inference from observed frontmatter (importer HALF 1).
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
)

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

// Many is the arity this importer declares: the shape the MAJORITY of the
// notes carrying a value wrote it in. A tie declares a list — with an equal
// split there is no majority to follow, and a list is the shape that can hold
// what both halves of the vault wrote.
func (po *PropertyObservation) Many() bool {
	return len(po.ListNotes) >= len(po.ScalarNotes) && len(po.ListNotes) > 0
}

// AritySplit reports the disagreement, or nil when every note agreed.
func (po *PropertyObservation) AritySplit(recordType string) *AritySplitReport {
	if len(po.ListNotes) == 0 || len(po.ScalarNotes) == 0 {
		return nil
	}
	many := po.Many()
	minority := po.ScalarNotes
	if !many {
		minority = po.ListNotes
	}
	ex := minority
	if len(ex) > maritySplitExamples {
		ex = ex[:maritySplitExamples]
	}
	return &AritySplitReport{
		RecordType:  recordType,
		Property:    po.Name,
		Many:        many,
		ListCount:   len(po.ListNotes),
		ScalarCount: len(po.ScalarNotes),
		Examples:    append([]string(nil), ex...),
	}
}

// maritySplitExamples caps how many minority notes an arity split names.
const maritySplitExamples = 3

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

// MinorityCount is how many notes disagree with the declared arity.
func (a AritySplitReport) MinorityCount() int {
	if a.Many {
		return a.ScalarCount
	}
	return a.ListCount
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

func (g *TypeGroup) prop(name string) *PropertyObservation {
	p, ok := g.Props[name]
	if !ok {
		p = &PropertyObservation{Name: name}
		g.Props[name] = p
		g.PropOrder = append(g.PropOrder, name)
	}
	return p
}

// NoteRecord pairs a parsed records.Record with its provenance.
type NoteRecord struct {
	AbsPath string
	RelPath string
	Rec     records.Record
}

// LoadProblem is one note this package could not read or parse, named rather
// than silently skipped.
type LoadProblem struct {
	RelPath string
	Reason  string
}

// LoadNotes reads every note in the inventory through
// knowledge.ReadNoteContent (eviction-aware: a cloud-dematerialised file on
// an iCloud-backed vault is a real, not theoretical, risk here) and parses
// its frontmatter through records.ParseRecord — never a second reader.
func LoadNotes(inv *Inventory) ([]NoteRecord, []LoadProblem, error) {
	out := make([]NoteRecord, 0, len(inv.Notes))
	var problems []LoadProblem
	for _, abs := range inv.Notes {
		data, err := knowledge.ReadNoteContent(nil, abs)
		if err != nil {
			problems = append(problems, LoadProblem{RelPath: inv.NoteRel[abs], Reason: err.Error()})
			continue
		}
		rec := records.ParseRecord(abs, data)
		if rec.ParseError != "" {
			problems = append(problems, LoadProblem{RelPath: inv.NoteRel[abs], Reason: rec.ParseError})
			continue
		}
		out = append(out, NoteRecord{AbsPath: abs, RelPath: inv.NoteRel[abs], Rec: rec})
	}
	return out, problems, nil
}

// TypeDiscriminatorCheck is US half-1's mandated sanity check: verify `type`
// is a real discriminator BEFORE relying on it, rather than assuming.
type TypeDiscriminatorCheck struct {
	TotalNotes    int
	WithType      int
	WithoutType   int
	DistinctTypes int
}

// CheckTypeDiscriminator reports how many notes carry a usable `type:` key.
func CheckTypeDiscriminator(notes []NoteRecord) TypeDiscriminatorCheck {
	c := TypeDiscriminatorCheck{TotalNotes: len(notes)}
	seen := map[string]struct{}{}
	for _, n := range notes {
		t := n.Rec.TypeName()
		if t == "" {
			c.WithoutType++
			continue
		}
		c.WithType++
		seen[t] = struct{}{}
	}
	c.DistinctTypes = len(seen)
	return c
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

// BuildNameIndex indexes every note by its filename stem.
func BuildNameIndex(notes []NoteRecord) *NameIndex {
	idx := &NameIndex{byStem: map[string][]string{}}
	for _, n := range notes {
		stem := strings.TrimSuffix(filepath.Base(n.RelPath), filepath.Ext(n.RelPath))
		key := records.FoldKey(stem)
		t := n.Rec.TypeName()
		if t == "" {
			// A resolved link to a non-record note is tracked as an empty
			// type so Resolve can tell "resolved, but not a record" apart
			// from "no note has this title at all".
			idx.byStem[key] = append(idx.byStem[key], "")
			continue
		}
		idx.byStem[key] = append(idx.byStem[key], t)
	}
	return idx
}

// Resolve looks up a wikilink target's observed record type(s).
func (idx *NameIndex) Resolve(target string) (types []string, found bool) {
	if idx == nil {
		return nil, false
	}
	ts, ok := idx.byStem[records.FoldKey(target)]
	return ts, ok
}

// CollectTypeGroups groups notes by their declared `type:` and records every
// other frontmatter property observed on notes of that type. `type`, `id`
// and `omni_id` are excluded — the first is the discriminator itself
// (record.go's RecordTypeKey), the latter two are D7/D8's identifier keys,
// and none of the three is a property a schema declares (validate.go treats
// all three as reserved on the very same list this function mirrors).
func CollectTypeGroups(notes []NoteRecord) map[string]*TypeGroup {
	groups := map[string]*TypeGroup{}
	for _, n := range notes {
		t := n.Rec.TypeName()
		if t == "" {
			continue
		}
		g, ok := groups[t]
		if !ok {
			g = &TypeGroup{Type: t, Props: map[string]*PropertyObservation{}}
			groups[t] = g
		}
		g.NoteCount++
		g.NotePaths = append(g.NotePaths, n.RelPath)

		for _, key := range n.Rec.Frontmatter.Keys {
			if key == records.RecordTypeKey || key == records.RecordIDKey || key == records.RecordIDKeyNamespaced {
				continue
			}
			node := n.Rec.Frontmatter.Values[key]
			po := g.prop(key)
			collectNodeValues(po, node, n.RelPath)
		}
	}
	applyTemplateDeclarations(groups, notes)
	return groups
}

// ---------------------------------------------------------------------------
// A TEMPLATE NOTE IS A PROPERTY DECLARATION
//
// A property can be REAL and still be invisible to a pass that reads only
// values, because "no note of this type has filled it in yet" and "this type
// has no such property" look identical from inside the frontmatter. The
// importer used to report the second when the truth was the first, and the
// bill on the founder's vault was ten named losses across three `.base`
// files: `legal-entity.registration_renewal_date`, `legal-entity.
// last_refreshed`, `invoice.amount` and `round.target` were each filtered on
// or displayed by a base he wrote, and each was refused with "never observed
// on a <type> note".
//
// The evidence that settles it was already in the vault, in his own hand.
// `03-Reference/Ops-Templates/Template — legal-entity.md` is a note with
// `type: template` and `template_type: legal-entity`, and its frontmatter
// lists — blank, ready to fill in — every property a legal-entity note
// carries, `last_refreshed` and `registration_renewal_date` among them. That
// is not an inference about a name or a shape. It is the operator writing
// down what this record type IS.
//
// THIS IS THE SAME MOVE FR-018d MADE ONE LEVEL UP (see run.go): a `.base`
// file naming a record type no note carries DECLARES that type, because the
// operator wrote the base. A template naming a property no note carries
// declares that property, for the same reason and on stronger evidence — a
// base file says "I filter this type by X", a template says "an X note has
// these properties", which is the schema question itself.
//
// WHAT IT MAY DO, AND THE FOUR THINGS IT MAY NOT
//
// It may add a property NAME to a record type that already exists. That is
// the whole of it. In particular:
//
//   - It never contributes a VALUE. The founder's connected-account template
//     writes `status: active`; that is a DEFAULT for the next note, not an
//     observation of an existing one, and admitting it would put `active`
//     into an inferred enum's closed set on the strength of a file that is
//     not a record of that type. Only the key crosses over.
//   - It never touches a property real notes carry. Data wins, always, and
//     the counts (`DeclaredCount`, `PresentNonEmptyCount`) stay exactly what
//     the notes of the type made them, so `required`, `many` and FR-104b's
//     tie-break weight are computed from real notes and nothing else.
//   - It never invents a record type. A `template_type` naming a type no
//     note carries is skipped here and left to FR-018d provisioning, which
//     decides that question from the `.base` files. Two rules creating the
//     same type would be two answers to one question.
//   - It never brings `template_type`/`template_kind` across. Those describe
//     the template file; they are not properties of the thing it templates.
//
// WHY THE ADDED PROPERTY CANNOT INVALIDATE A NOTE. It arrives with zero
// values, so it takes classifyWithNoValues — `required` is false because
// PresentNonEmptyCount is 0 against a NoteCount above 0, `many` is false
// because there is no arity evidence, and the type is `text`, or `date` when
// the name is on that function's closed list. Every note of the type is
// silent on this key, and an absent value is checked against nothing. The
// acceptance bar (a note this run typed is never reported invalid by the same
// run) is untouched by arithmetic, not by luck.
//
// WHY IT CANNOT BROADEN A VIEW (FR-105). Declaring a property can only turn a
// clause the translator DROPPED into one it translates. A dropped clause is
// the broadening this project refuses — `and: [a, b]` losing `b` matches more
// rows than the original — so restoring it moves in the narrowing direction
// at the leaf. And at the view: a restored clause under a `not:` inverts, so
// the question is whether the restored translation is EQUIVALENT to Obsidian's
// or merely a subset. It is not decided here. Whatever type the property ends
// up with, view_write.go's leaf builder judges each filter position on its own
// terms and refuses the ones it cannot translate faithfully — that is exactly
// what keeps `last_refreshed != ""` a named loss on `text` after this change,
// where `registration_renewal_date != ""` translates on `date`. This function
// widens the set of properties that reach that judgement; it does not widen
// the judgement.
// ---------------------------------------------------------------------------

// templateRecordType is the `type:` a template note declares, and
// templateTypeKey / templateKindKey are the two scaffolding keys such a note
// carries about ITSELF rather than about the record it templates.
const (
	templateRecordType = "template"
	templateTypeKey    = "template_type"
	templateKindKey    = "template_kind"
)

// applyTemplateDeclarations adds every property name a template note declares
// to the record type its `template_type` names, for types that already exist.
//
// It runs as a SECOND pass, after every note has been grouped, because it has
// to know whether a record type exists at all before it can decline to invent
// one — and the template may be read before the notes it templates.
func applyTemplateDeclarations(groups map[string]*TypeGroup, notes []NoteRecord) {
	for i := range notes {
		n := &notes[i]
		if n.Rec.TypeName() != templateRecordType {
			continue
		}
		target := templateTargetType(n.Rec)
		if target == "" || target == templateRecordType {
			continue
		}
		g, ok := groups[target]
		if !ok {
			// No note carries this type. Declaring it from a template is
			// FR-018d provisioning's question, not this one.
			continue
		}
		for _, key := range n.Rec.Frontmatter.Keys {
			if !templateDonatesKey(key) {
				continue
			}
			if _, observed := g.Props[key]; observed {
				// Real notes of the type already speak for this property.
				continue
			}
			// g.prop registers the name with a zero observation: no value,
			// no arity, and a DeclaredCount that stays 0 because no note of
			// THIS type wrote the key. classifyWithNoValues takes it from
			// here, and TemplateNotes is how the report says where the key
			// came from instead of claiming a note declared it.
			po := g.prop(key)
			po.TemplateNotes = append(po.TemplateNotes, n.RelPath)
		}
	}
}

// templateTargetType reads the record type a template note templates, or ""
// when it names none. A blank or non-scalar `template_type` names nothing.
func templateTargetType(rec records.Record) string {
	n, ok := rec.Frontmatter.Get(templateTypeKey)
	if !ok || n.Kind != records.KindScalar {
		return ""
	}
	return strings.TrimSpace(n.Text)
}

// templateDonatesKey reports whether one of a template's frontmatter keys is
// a property of the record it templates. The two scaffolding keys and the
// three reserved keys CollectTypeGroups already excludes are the whole of the
// exclusion list.
func templateDonatesKey(key string) bool {
	switch key {
	case records.RecordTypeKey, records.RecordIDKey, records.RecordIDKeyNamespaced,
		templateTypeKey, templateKindKey:
		return false
	}
	return true
}

// collectNodeValues flattens one frontmatter value into a property's
// observation set. A sequence sets Many and contributes each element; a
// scalar contributes itself when non-empty; a null or an empty scalar
// contributes to neither Values nor the required-count (FR-007: null is
// absence).
func collectNodeValues(po *PropertyObservation, node records.Node, notePath string) {
	// Counted BEFORE the switch and outside every branch: this is "the note
	// wrote this key", which is true of a null, an empty string and an empty
	// list alike. CollectTypeGroups calls this exactly once per (note, key),
	// so the count is the number of notes declaring the key.
	po.DeclaredCount++

	switch node.Kind {
	case records.KindSequence:
		// ARITY AND VALUE ARE COUNTED SEPARATELY, AND `tags: []` IS WHY.
		// An empty list carries no VALUE evidence (nothing to classify, and
		// FR-007 makes it absent for `required`) but it is unambiguous ARITY
		// evidence: the operator wrote a list. records.Validate agrees — its
		// absence gate (missing key / explicit null / FR-007a's empty string)
		// does not catch an empty SEQUENCE, so `tags: []` reaches the arity
		// check and fails against `many: false`. Gating the arity count on
		// non-emptiness turned four `tags: []` company notes into four arity
		// errors this importer created itself.
		po.ListNotes = append(po.ListNotes, notePath)
		nonEmpty := false
		for _, item := range node.Items {
			if item.Kind == records.KindScalar && strings.TrimSpace(item.Text) != "" {
				po.Values = append(po.Values, observedValue{Text: item.Text, Block: item.Block, NotePath: notePath})
				nonEmpty = true
			}
		}
		if nonEmpty {
			po.PresentNonEmptyCount++
		}
	case records.KindScalar:
		if strings.TrimSpace(node.Text) != "" {
			po.Values = append(po.Values, observedValue{Text: node.Text, Block: node.Block, NotePath: notePath})
			po.PresentNonEmptyCount++
			po.ScalarNotes = append(po.ScalarNotes, notePath)
		}
		// An EMPTY scalar is counted as neither shape. FR-007a makes it the
		// absent state on every non-text type, and records.Validate settles
		// absence before arity, so it is not evidence that the operator meant
		// a single value.
	case records.KindNull, records.KindMapping:
		// KindNull is explicit absence (FR-007). KindMapping never conforms
		// to any of the seven property types; it contributes nothing to
		// shape inference and simply depresses the required-count, exactly
		// as absence would — a note that wrote a nested mapping here holds
		// no usable value for this property either.
	}
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
