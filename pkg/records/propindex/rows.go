// Omnipus — ADR-068 D16.2 / FR-020, FR-021a..c, FR-076a: what one note contributes
// to the properties index.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The index is derived. This file is the derivation: it turns one parsed note
// plus its schema into the exact set of rows the store persists, and it does so
// in pure Go with no SQLite in sight — which is FR-021c stated as a package
// boundary rather than as a rule somebody has to remember. Two of SQLite's own
// parsers return NULL WITH NO ERROR on malformed input (`unixepoch('bad')`,
// `unixepoch('2026-8-26')`) and a third saturates silently
// (CAST('9223372036854775808' AS INTEGER) -> int64 max). Any of the three would
// write a parse failure into the storage cell reserved for ABSENCE, defeating
// FR-021b in the same transaction FR-021b exists to protect.
//
// So every value is typed HERE, by pkg/records' own parser — the same one
// validation uses, so the index and the validator can never disagree about what
// a note says.
// ---------------------------------------------------------------------------

// KindNote and KindAttachment are the note kinds the index narrows on. They
// mirror pkg/knowledge's `indexDoc.Kind`, which only ever holds these two
// (scan.go:45,48) — which is exactly why `kind: task` cannot be served from the
// text index and is served from note_tasks instead (FR-076a).
const (
	KindNote       = "note"
	KindAttachment = "attachment"
)

// StateRowElem marks the row that carries a property's STATE rather than one of
// its values.
//
// FR-021b: a property has three distinguishable states — present-and-conforming,
// present-and-non-conforming, absent — and "no row at all" cannot express the
// difference between the last two, because a non-conforming value is stored in
// no typed column and would leave the same NULL absence leaves. So every
// DECLARED property of a record gets exactly one state row, whatever it holds,
// and value rows carry the elements. Value rows use their SOURCE position as
// `elem` (records.PropertyValue.SourcePosition), which is >= 0, so -1 can never
// collide with one.
const StateRowElem = -1

// PropRow is one row of the note_props child table.
//
// Exactly one of Text/Num/Time/Link is set on a value row, chosen by the
// DECLARED type; a state row sets none of them. Nothing here is ever a binary
// float: Num holds the exact decimal digits records.Decimal printed (FR-020b),
// and Time holds strict ISO-8601 text (FR-021d), never an epoch integer SQLite
// might be tempted to order.
type PropRow struct {
	Prop  string
	Elem  int
	State records.PropertyState
	Type  records.PropertyType

	Text string
	Num  string
	Time string
	Link string

	// Raw is the element's source text exactly as the file had it. It is what a
	// report quotes, and it is the fallback decode path for any property type
	// this file does not know how to project into a typed column.
	Raw string
	// Quoted records whether the scalar was written in quotes, so the fallback
	// decode reconstructs the same records.Node the parser first saw.
	Quoted bool
}

// RelationRow is one row of the note_relations child table — one edge, stored
// once (D5). The inverse is DERIVED and appears in no file and no row.
//
// Resolving Target to the target's record identifier is FR-031, and it is W3's
// work: this table deliberately carries no half-filled "resolved target" column,
// because a column that is always NULL is a claim the code does not honour.
type RelationRow struct {
	Prop    string
	Elem    int
	Target  string
	Heading string
	Display string
	Raw     string
}

// TaskRow is one checkbox line — FR-076a's body projection.
//
// A row is one real thing at a path: a note, or a checkbox line within one. It
// carries the same source_hash every other row carries (through its owning
// note), so freshness, bounds and paging apply to it unchanged.
type TaskRow struct {
	Line   int
	Status string
	Text   string
}

// Task statuses. Two values, closed, because the regex admits exactly two.
const (
	TaskOpen = "open"
	TaskDone = "done"
)

// NoteRows is everything one note contributes to the index.
type NoteRows struct {
	// Path is the collection-relative path — the note's identity in this store
	// and the key the freshness comparison and the text index share.
	Path string
	Kind string
	// RecordType is "" for an ordinary note. FR-005: that is the majority of
	// every real vault and it is not an error.
	RecordType string
	// RecordID is the note's identifier, byte-exact. It is stored as a BLOB and
	// is NEVER folded and never compared in SQL: R-8 makes CO-0142 and co-0142
	// two distinct records, and a NOCASE column would collide them into one.
	RecordID string
	// SourceHash is the hex SHA-256 of the file's bytes — the same value
	// pkg/knowledge writes into ManifestEntry.Hash (FR-020c). An empty hash is
	// UNKNOWN freshness, which is flagged, never assumed fresh.
	SourceHash string

	Props     []PropRow
	Relations []RelationRow
	Tasks     []TaskRow
}

// SourceHash computes the freshness token for a note's bytes.
//
// It is deliberately the same definition as pkg/knowledge's ManifestEntry.Hash
// ("the hex SHA-256 of the file's contents") rather than a second token with the
// same job: D16.5's whole mechanism is that the two indexes can be compared, and
// two hashes computed two ways compare unequal forever.
func SourceHash(src []byte) string {
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

// taskLine is pkg/knowledge/authoring_tools.go:1251's regex, character for
// character. FR-076a replaces the WALK, not the definition of a task: a checkbox
// that `knowledge_tasks` returned and `vault_find` did not would be a silent
// behaviour change dressed as an optimisation.
var taskLine = regexp.MustCompile(`^[ \t]*[-*+][ \t]+\[([ xX])\][ \t]*(.*)$`)

// ExtractTasks projects a note's body into checkbox rows.
//
// Line numbers are 1-based and count from the first byte of the file, including
// any frontmatter block, because that is the line the operator's editor shows.
func ExtractTasks(src []byte) []TaskRow {
	if len(src) == 0 {
		return nil
	}
	var out []TaskRow
	for i, line := range strings.Split(string(src), "\n") {
		m := taskLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if m == nil {
			continue
		}
		status := TaskOpen
		if m[1] != " " {
			status = TaskDone
		}
		out = append(out, TaskRow{Line: i + 1, Status: status, Text: strings.TrimSpace(m[2])})
	}
	return out
}

// BuildNoteRows derives every row one note contributes.
//
// schema may be nil: a note whose `type` matches no schema is an ORDINARY NOTE
// (FR-005). It still gets a note row — so it is still narrowable by kind and
// path, and its checkboxes are still indexed — and it contributes no property
// rows, because it declares no properties to have a state about.
func BuildNoteRows(rec records.Record, schema *records.Schema, src []byte, hash string) NoteRows {
	rows := NoteRows{
		Path:       rec.Path,
		Kind:       KindNote,
		RecordID:   rec.ID(),
		SourceHash: hash,
		Tasks:      ExtractTasks(src),
	}
	if schema == nil {
		return rows
	}
	rows.RecordType = schema.Type

	for _, name := range schema.PropertyOrder {
		prop, ok := schema.Property(name)
		if !ok {
			continue
		}
		pv := records.ResolveProperty(rec, prop)

		rows.Props = append(rows.Props, PropRow{
			Prop:  name,
			Elem:  StateRowElem,
			State: pv.State,
			Type:  prop.Type,
		})

		// FR-021a: only CONFORMING values reach a typed column. A
		// non-conforming value is recorded by the state row above and by
		// nothing else — it is never coerced into shape so that a comparison
		// can be attempted on it later.
		if pv.State != records.StatePresent {
			continue
		}
		for i, v := range pv.Values {
			pos := pv.SourcePosition(i)
			rows.Props = append(rows.Props, projectValue(name, pos, prop, v))
			if prop.Type == records.TypeRelation || prop.Type == records.TypePerson {
				rows.Relations = append(rows.Relations, RelationRow{
					Prop:    name,
					Elem:    pos,
					Target:  v.Link.Target,
					Heading: v.Link.Heading,
					Display: v.Link.Display,
					Raw:     v.Link.Raw,
				})
			}
		}
	}
	return rows
}

// projectValue puts one typed value into the column its DECLARED type owns.
//
// The default arm is not a shrug: an eighth property type is an ADR change
// (schema.go's "the set is CLOSED"), and until that change reaches this file the
// honest projection is the source text, which the decode path re-types through
// the same parser that produced this value. A value silently dropped because a
// switch did not know about it is the failure mode this package is built to
// remove.
func projectValue(name string, pos int, prop *records.Property, v records.TypedValue) PropRow {
	row := PropRow{
		Prop:  name,
		Elem:  pos,
		State: records.StatePresent,
		Type:  prop.Type,
		Raw:   v.Raw,
	}
	switch prop.Type {
	case records.TypeText:
		row.Text = v.Text
	case records.TypeEnum:
		// The DECLARED spelling, not the file's (records.TypedValue.Enum is the
		// value the file resolved to). The file's spelling stays in Raw.
		row.Text = v.Enum.Name
	case records.TypeRelation, records.TypePerson:
		row.Link = v.Link.Raw
	case records.TypeDate:
		// Strict ISO-8601, and a day stays a day: DateValue.String() renders
		// RFC3339 only when a time was written, so the decode can restore
		// HasTime without a second flag.
		row.Time = v.Date.String()
	case records.TypeInteger, records.TypeDecimal:
		// The exact decimal digits. Never a REAL, never a float, in either
		// direction (FR-020b).
		row.Num = v.Number.String()
	}
	return row
}

// Typed re-types one stored property back into the form the comparator consumes.
//
// It reads the typed columns for the types projectValue knows and falls back to
// the source text for anything else, and BOTH paths end at pkg/records' own
// parser or at a value the schema itself owns. There is deliberately no second
// parser here: the round-trip is asserted against records.ResolveProperty over
// the original frontmatter in TestRoundTrip_DecodeMatchesTheParser.
func (p StoredProp) Typed(prop *records.Property) (records.PropertyValue, error) {
	pv := records.PropertyValue{Property: prop, State: p.State}
	if p.State != records.StatePresent {
		return pv, nil
	}
	for _, e := range p.Elems {
		v, err := decodeElem(prop, e)
		if err != nil {
			return records.PropertyValue{}, err
		}
		pv.Values = append(pv.Values, v)
		pv.SourceIndex = append(pv.SourceIndex, e.SourcePos)
	}
	return pv, nil
}

func decodeElem(prop *records.Property, e StoredElem) (records.TypedValue, error) {
	v := records.TypedValue{Type: prop.Type, Raw: e.Raw}
	switch prop.Type {
	case records.TypeText:
		v.Text = e.Text
		return v, nil
	case records.TypeEnum:
		for _, dv := range prop.Values {
			if dv.Name == e.Text {
				v.Enum = dv
				return v, nil
			}
		}
		// The stored spelling is no longer a declared value: the schema changed
		// under the index. That is FR-015's invalidation, and reporting it beats
		// returning a value the schema no longer admits.
		return records.TypedValue{}, &StaleValueError{Prop: prop.Name, Value: e.Text}
	case records.TypeRelation, records.TypePerson:
		link, ok := records.ParseWikilink(e.Link)
		if !ok {
			return records.TypedValue{}, &StaleValueError{Prop: prop.Name, Value: e.Link}
		}
		v.Link = link
		return v, nil
	}
	// Every remaining type is decoded by the SAME parser that wrote it, from the
	// source text, through the schema's own declaration.
	src := e.Raw
	if prop.Type == records.TypeDate && e.Time != "" {
		src = e.Time
	}
	if (prop.Type == records.TypeInteger || prop.Type == records.TypeDecimal) && e.Num != "" {
		src = e.Num
	}
	parsed, verr := records.ParseValue(prop, records.Node{
		Kind:   records.KindScalar,
		Text:   src,
		Quoted: e.Quoted,
	})
	if verr != nil {
		return records.TypedValue{}, &StaleValueError{Prop: prop.Name, Value: src, Reason: verr.Error()}
	}
	parsed.Raw = e.Raw
	return parsed, nil
}

// StaleValueError is a stored value the current schema no longer admits.
//
// It is an error rather than a silent skip because the alternative is a record
// quietly missing a property it has on disk — a wrong answer with no error
// channel, which is §1.3's failure mode.
type StaleValueError struct {
	Prop   string
	Value  string
	Reason string
}

func (e *StaleValueError) Error() string {
	msg := "property " + e.Prop + ": indexed value " + quote(e.Value) +
		" is no longer admitted by the schema; re-index this note"
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg
}

func quote(s string) string { return `"` + s + `"` }
