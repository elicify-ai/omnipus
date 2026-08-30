// Omnipus — record-type inference from observed frontmatter (importer HALF 1).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
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
// It does NOT decide `required`/`many` by guessing either: `many` is true
// the moment ANY note of the type wrote the property as a YAML sequence
// (records.KindSequence), and `required` is true only when EVERY note of the
// type carries a non-null, non-empty value for it (D3.2/FR-007 treat an
// explicit null exactly like absence).
// ---------------------------------------------------------------------------

// ClassifyKind names which inference rule decided a property's type. It is
// exported so the CLI report can render WHY, not just WHAT.
type ClassifyKind string

const (
	ClassifyRelation  ClassifyKind = "relation" // every value is a wikilink
	ClassifyBoolean   ClassifyKind = "boolean"  // every value folds to true/false
	ClassifyDate      ClassifyKind = "date"     // every value parses as records.TypeDate
	ClassifyInteger   ClassifyKind = "integer"  // every value parses as records.TypeInteger
	ClassifyDecimal   ClassifyKind = "decimal"  // every value parses as records.TypeDecimal
	ClassifyEnum      ClassifyKind = "enum"     // small, repeated, closed vocabulary
	ClassifyText      ClassifyKind = "text"     // the fallback every value accepts
	ClassifyAmbiguous ClassifyKind = "ambiguous_defaulted_to_text"
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
	// ambiguousMatchFloor: when the single best-matching non-text candidate
	// type (date/integer/decimal/relation) matches at least this fraction of
	// a property's observed values but NOT all of them, the property is
	// reported as an ambiguous inference instead of silently defaulted. Below
	// this floor the partial match is treated as coincidence (e.g. one
	// free-text title that happens to parse as a number) and not reported.
	ambiguousMatchFloor = 0.60
)

// PropertyObservation is everything this package saw about one property on
// one record type, across every note of that type.
type PropertyObservation struct {
	Name string
	// PresentNonEmptyCount is how many notes of the type carried a genuine
	// (non-null, non-empty) value — the numerator for `required`.
	PresentNonEmptyCount int
	// Many is true the moment any note wrote this property as a list.
	Many bool
	// Values is every individual scalar value observed (a many-valued
	// property contributes each element), each tagged with the note it came
	// from so an ambiguity report can name real examples.
	Values []observedValue
}

type observedValue struct {
	Text     string
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
	return groups
}

// collectNodeValues flattens one frontmatter value into a property's
// observation set. A sequence sets Many and contributes each element; a
// scalar contributes itself when non-empty; a null or an empty scalar
// contributes to neither Values nor the required-count (FR-007: null is
// absence).
func collectNodeValues(po *PropertyObservation, node records.Node, notePath string) {
	switch node.Kind {
	case records.KindSequence:
		po.Many = true
		nonEmpty := false
		for _, item := range node.Items {
			if item.Kind == records.KindScalar && strings.TrimSpace(item.Text) != "" {
				po.Values = append(po.Values, observedValue{Text: item.Text, NotePath: notePath})
				nonEmpty = true
			}
		}
		if nonEmpty {
			po.PresentNonEmptyCount++
		}
	case records.KindScalar:
		if strings.TrimSpace(node.Text) != "" {
			po.Values = append(po.Values, observedValue{Text: node.Text, NotePath: notePath})
			po.PresentNonEmptyCount++
		}
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

	// Ambiguity is set when this property's type was NOT a unanimous match
	// and had to be defaulted to text — the honesty-contract payload.
	Ambiguity *AmbiguousInference
	// RelationSplit is set when a relation's targets did not converge
	// unanimously on one record type (or converged on none at all) —
	// reported rather than left silent, whatever `to:` ended up being.
	RelationSplit *RelationSplitReport
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
// converge cleanly on one record type — either a real split (several
// resolved types, no ≥90% majority) or total non-resolution (every target
// dangling or pointing at a non-record note).
type RelationSplitReport struct {
	RecordType string
	Property   string
	// ByType is target-type -> count of resolved links, sorted for display
	// by the caller. Empty when nothing resolved at all.
	ByType map[string]int
	// Unresolved is how many link targets matched no known note at all.
	Unresolved int
	// Declared is what this property ended up declared as: "relation" (a
	// plurality target existed, so `to:` FR-034 could be satisfied) or
	// "text" (no resolved target of ANY kind existed, so there was no
	// evidence to put behind a mandatory `to:` at all — schema.go's
	// finalize() REJECTS a relation with `to: ""` outright, so this is not
	// optional: a relation with zero resolvable evidence cannot be declared
	// a relation, full stop).
	Declared string
}

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
		Name:     po.Name,
		Many:     po.Many,
		Required: po.PresentNonEmptyCount == noteCount && noteCount > 0,
	}
	total := len(po.Values)
	if total == 0 {
		// Every observation was null/empty. There is no shape evidence at
		// all; text is the only type that never rejects an absent-in-effect
		// value written as `x: ""` on the rare note that had it.
		ip.Type = records.TypeText
		ip.Kind = ClassifyText
		return ip
	}

	// --- relation -----------------------------------------------------
	if allMatch(po.Values, isWikilink) {
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

	// --- boolean (modelled as a 2-value enum; the schema has no bool) --
	if allMatch(po.Values, isBooleanLiteral) {
		ip.Type = records.TypeEnum
		ip.Kind = ClassifyBoolean
		ip.EnumValues = []string{"false", "true"}
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
	bestFrac := 0.0
	var bestType records.PropertyType
	var bestMatched, bestTotal int
	var bestBad []observedValue
	for _, c := range candidates {
		matched, bad := partitionMatch(po.Values, c.test)
		frac := float64(matched) / float64(total)
		if frac == 1.0 {
			ip.Type = c.t
			ip.Kind = classifyKindFor(c.t)
			return ip
		}
		if frac > bestFrac {
			bestFrac, bestType, bestMatched, bestTotal, bestBad = frac, c.t, matched, total, bad
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
	if bestFrac >= ambiguousMatchFloor {
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
			MatchFrac:    bestFrac,
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

// inferRelationTarget resolves a relation property's `to:` from its observed
// link targets.
//
// FR-034 (schema.go's Property.finalize) makes `to:` MANDATORY on a
// `relation` property — a schema declaring one with no `to:` is REJECTED
// entirely at load time, not merely validated more loosely. That changes
// what "do not guess" has to mean here relative to every other property:
// leaving `to:` blank is not a safe, conservative fallback the way
// defaulting an ambiguous scalar property to `text` is — it produces a
// schema file that does not load AT ALL, taking the whole record type down
// with it. So the rule this function follows is: return the PLURALITY
// target type whenever ANY link resolved to ANY known record type at all
// (even a weak plurality), and report the full split whenever it is not
// unanimous — "reported, not guessed" is satisfied by the report, not by
// silence. Only when NOTHING resolved (`resolvedTotal == 0`, every link
// either dangling or pointing at a non-record note) is there truly no
// evidence to put behind ANY `to:`; that case returns ("", report) and
// classifyProperty falls back to declaring the property `text` instead of
// an unrepresentable relation.
func inferRelationTarget(recordType string, po *PropertyObservation, names *NameIndex) (string, *RelationSplitReport) {
	byType := map[string]int{}
	unresolved := 0
	resolvedTotal := 0
	for _, v := range po.Values {
		link, ok := records.ParseWikilink(v.Text)
		if !ok {
			continue
		}
		types, found := names.Resolve(link.Target)
		if !found {
			unresolved++
			continue
		}
		for _, t := range types {
			if t == "" {
				continue // resolved to a real note, but not a record
			}
			byType[t]++
			resolvedTotal++
		}
	}
	if resolvedTotal == 0 {
		return "", &RelationSplitReport{
			RecordType: recordType, Property: po.Name,
			ByType: byType, Unresolved: unresolved, Declared: "text",
		}
	}
	var bestType string
	bestCount := 0
	for t, c := range byType {
		if c > bestCount || (c == bestCount && t < bestType) {
			bestType, bestCount = t, c
		}
	}
	if len(byType) == 1 {
		// Unanimous among every link that resolved at all — nothing to
		// report beyond the schema itself.
		return bestType, nil
	}
	// A real split: more than one resolved target type. `to:` is set to the
	// plurality regardless of how thin it is — FR-034 leaves no room for
	// "leave it blank because the plurality is weak", and a weak plurality
	// is exactly what the returned RelationSplitReport exists to surface.
	// A wikilink resolving to the wrong declared target type is exactly
	// what D5/FR-034's `relation_type_mismatch` finding exists to report at
	// validation time, not something this importer needs to prevent by
	// refusing to pick.
	return bestType, &RelationSplitReport{
		RecordType: recordType, Property: po.Name,
		ByType: byType, Unresolved: unresolved, Declared: "relation",
	}
}
