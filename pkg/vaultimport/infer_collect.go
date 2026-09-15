// infer_collect.go: Collect candidate signals from files and frontmatter (note loading, type grouping, name indexing, template declarations, observed values)

package vaultimport

import (
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// Many is the arity this importer declares: the shape the MAJORITY of the
// notes carrying a value wrote it in. A tie declares a list — with an equal
// split there is no majority to follow, and a list is the shape that can hold
// what both halves of the vault wrote.
func (po *PropertyObservation) Many() bool {
	return len(po.ListNotes) >= len(po.ScalarNotes) && len(po.ListNotes) > 0
}

// maritySplitExamples caps how many minority notes an arity split names.
const maritySplitExamples = 3

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

// MinorityCount is how many notes disagree with the declared arity.
func (a AritySplitReport) MinorityCount() int {
	if a.Many {
		return a.ScalarCount
	}
	return a.ListCount
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
