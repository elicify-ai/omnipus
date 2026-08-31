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

	// ---------------------------------------------------------------------
	// DeclaredType and SchemaFingerprint are THE SECOND INPUT THIS ROW WAS
	// DERIVED FROM, written down. Read them together; neither answers the
	// question on its own.
	//
	// A row is a function of two things: the note's BYTES and the SCHEMA its
	// `type:` names. Only the first had a freshness token, and the consequence
	// was the worst shape of defect this project has a name for. Schema files
	// live under a directory the note walk does not visit, so declaring a new
	// record type moves no note's bytes and therefore no note's hash: an
	// operator with 412 notes already carrying `type: company` who created the
	// `company` type got 412 hash-matched skips, and `knowledge_find
	// type=company` then returned ZERO rows and reported COMPLETE. Permanently,
	// with no error, no problem entry and no staleness flag. `edit_record_type`
	// was silently ineffective by the same mechanism.
	//
	// WHY BOTH FIELDS. SchemaFingerprint alone cannot be compared, because to
	// know which schema governs a note you must know the type the note
	// declares — and reading that means re-parsing the note's frontmatter,
	// which is precisely the work the hash-equal skip exists to avoid. So the
	// DECLARED type is stored too, and the indexer's freshness test becomes:
	// the bytes have not moved AND the schema now registered for the type this
	// row already says the note declares still has the fingerprint this row was
	// built from.
	//
	// That test is exact in both directions. It re-derives every note the
	// changed type governs and NOT ONE note it does not, so a schema edit costs
	// a re-index proportional to that type rather than a re-index of the vault.
	//
	// DeclaredType IS NOT RecordType. RecordType is the type a note RESOLVED
	// to, and it is "" for a note declaring a type nobody has defined — FR-005,
	// an ordinary note, and the majority state of a real vault mid-authoring.
	// DeclaredType is what the note's frontmatter SAYS, resolved or not, and it
	// is the only one of the two that can find the notes a newly created type
	// is about to start governing.
	//
	// SchemaFingerprint is records.Schema.Fingerprint — the content hash of the
	// schema file, which FR-015 already defines for exactly this purpose — or
	// "" when no schema matched. "" is therefore a real, comparable state: it
	// says "this row was derived with no schema", and it stops agreeing the
	// moment one exists.
	// ---------------------------------------------------------------------
	DeclaredType      string
	SchemaFingerprint string

	// Size, MtimeNanos, CtimeNanos and HasCtime are the file's stat AS THE WALK
	// OBSERVED IT — FR-131's three new `notes` columns, backing `file.size`,
	// `file.mtime` and `file.ctime`.
	//
	// THEY ARE SET BY THE INDEXER, NOT BY BuildNoteRows. This package derives
	// rows from BYTES and does no filesystem walking (see this file's header),
	// and the caller that read the note already holds the os.FileInfo —
	// re-stat'ing here would be a second syscall per note for a value the caller
	// has. propindex.StatFile / StatFromInfo produce a FileMeta and
	// NoteRows.SetFileMeta copies it onto these four fields, so no caller has to
	// remember the encoding.
	//
	// THEY ARE NOT DERIVED FROM SourceHash AND THEY MOVE INDEPENDENTLY OF IT.
	// That is FR-136: `git checkout`, rsync, `touch` and an iCloud resync all
	// change the stat and leave the bytes identical, so a sync that skips on
	// hash equality alone freezes `file.mtime` at the last CONTENT change — and
	// `sort by file.mtime desc`, the commonest Bases view there is, then returns
	// a plausible, WRONG, stable ordering with no error anywhere. Store's
	// RefreshNoteStat exists so that skip can correct them without re-parsing.
	//
	// A ZERO MtimeNanos IS UNKNOWN, NOT 1970. All three columns go to NULL for a
	// row whose walk carried no stat, which reads back as absent — rather than
	// planting the epoch on every such note and sorting them all to the front
	// forever. Writers should carry the walk's stat on every UpsertNote; the
	// honest NULL is the safety net, not the plan.
	//
	// HasCtime is FR-133 in a struct field: Linux without statx birth-time
	// support, a filesystem that records none, and every platform whose stat
	// structure has no birth field all leave it false, and `file.ctime` is then
	// ABSENT. It is NEVER the POSIX inode-change time that shares the name.
	Size       int64
	MtimeNanos int64
	CtimeNanos int64
	HasCtime   bool

	Props     []PropRow
	Relations []RelationRow
	Tasks     []TaskRow
	// Tags and Links are the body projections behind `file.tags`, `file.links`
	// and `file.embeds` (body.go). They live in their OWN child tables and are
	// streamed by their OWN statements — never joined to note_props or to each
	// other, which is FR-131's named assembly strategy and D16.6's fan-out
	// defect kept shut.
	Tags  []TagRow
	Links []LinkRow
}

// StatKnown reports whether the indexer supplied a stat for this note at all.
//
// It reads MtimeNanos rather than carrying a fifth flag, and the cost of that is
// stated rather than hidden: a file whose real modification time is exactly
// 1970-01-01T00:00:00Z is indistinguishable from one that was never stat'ed, and
// is stored as unknown. The alternative — a flag a caller can forget to set
// while the three numbers are already filled in — writes a CONFIDENT 1970
// instead of an honest absence, and that is not the direction this package errs
// in.
// The receiver is a POINTER only to match SetFileMeta's, which must be one.
// Mixing the two forms on one type is what `recvcheck` flags, and the mix is
// worth avoiding for a better reason than the linter: a value-receiver method
// beside a pointer-receiver mutator is how someone comes to call the mutator on
// a copy and watch the change evaporate.
func (r *NoteRows) StatKnown() bool { return r.MtimeNanos != 0 }

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
// that `knowledge_tasks` returned and `knowledge_find` did not would be a silent
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
		Tags:       ExtractTags(rec, src),
		Links:      ExtractLinks(src),
		// The two derivation inputs, recorded together with the rows they
		// produced. They are set HERE, in the one function that has both the
		// note and the schema in hand, so they cannot drift from what was
		// actually used — see the fields' own comment for why the freshness
		// test needs both.
		DeclaredType: rec.TypeName(),
	}
	if schema != nil {
		rows.SchemaFingerprint = schema.Fingerprint
	}
	if schema == nil {
		// FR-021e, founder ruling 1: "can you not just fix them, use common
		// sense". THIS USED TO BE AN EARLY RETURN, and the early return was the
		// defect. A note with no matching schema contributed a bare `notes` row
		// and NO property rows — so a note whose file plainly says
		// `status: open` answered TRUE to `status IS NULL` in any untyped view.
		// The index held less than the vault did, and said nothing about it.
		//
		// Storage is now UNCONDITIONAL. Interpretation stays a query-time
		// question (FR-018b's untyped-view resolution rule); what happens here
		// is only that the frontmatter is written down.
		rows.Props = rawFrontmatterRows(rec, nil)
		return rows
	}
	rows.RecordType = schema.Type

	// The same rule applies to a TYPED note's undeclared keys. A `contact` note
	// that also carries `status: open`, where the contact type declares no
	// `status`, is in exactly the position the ruling is about: the key is on
	// disk and an untyped view names it. Declared properties are appended below
	// and are never duplicated here — rawFrontmatterRows skips every key the
	// schema declares, because two rows for one (note_id, prop, elem) is a
	// primary-key collision, not a merge.
	rows.Props = rawFrontmatterRows(rec, schema)

	for _, name := range schema.PropertyOrder {
		prop, ok := schema.Property(name)
		if !ok {
			continue
		}
		pv := records.ResolveProperty(rec, prop)

		// THE STATE ROW CARRIES THE OFFENDING TEXT WHEN THERE IS ONE.
		//
		// A non-conforming SCALAR contributes no value row — projectValue runs
		// only over pv.Values, and a value that failed to parse is not in them —
		// so without this the index retained the FACT of the failure and none of
		// its evidence. A reader then got "height_cm does not conform to
		// declared type decimal" and could not tell whether the note says `50k`,
		// `fifty thousand` or `[]`.
		//
		// spec 4.2's problem line is "arr is '50k' where a decimal is required —
		// write 50000", and both halves are already computed here: Finding.Got
		// is what the file held and Finding.Expected is the shape that would
		// have been accepted. They were being dropped on the floor.
		//
		// IT IS DIAGNOSTIC TEXT, NOT A VALUE. It goes on the STATE row, never
		// into a typed column and never into StoredProp.Elems, so nothing can
		// decode it back into something the comparator would compare. R-4 is
		// unchanged: a non-conforming value has no value.
		stateRow := PropRow{
			Prop:  name,
			Elem:  StateRowElem,
			State: pv.State,
			Type:  prop.Type,
		}
		if pv.State == records.StateNonConforming {
			stateRow.Raw, stateRow.Text = nonConformingEvidence(pv)
		}
		rows.Props = append(rows.Props, stateRow)

		// FR-021a is a rule about a VALUE, not about a property: a value that
		// does not conform is recorded by the state row above and reaches no
		// typed column, and its CONFORMING SIBLINGS are stored as usual.
		//
		// The distinction is load-bearing for a `many` property. `labels:
		// [indoor, {a: b}]` resolves to StateNonConforming with `indoor` still
		// in Values (records.ResolveProperty filters, it does not discard the
		// list), and skipping the whole property here would delete a value the
		// note demonstrably contains — an index quietly holding less than the
		// vault does. What the property's non-conformance MEANS for a
		// comparison is R-4's business, in Go, with the state flag in hand.
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

// RawPropertyType is the `vtype` a raw frontmatter row carries: none.
//
// It is the empty string rather than a "raw" or "unknown" spelling on purpose.
// StoredProp.Type is a records.PropertyType, and the set of those is CLOSED
// (schema.go); inventing an eighth value here so that a row with NO declared
// type could name one would put a non-type into a type-shaped field, where the
// first thing any switch does with it is fall through a default arm. Empty says
// what is true: nothing declared this key, so it has no declared type, and
// FR-018b's untyped-view resolution decides the domain at QUERY time.
const RawPropertyType records.PropertyType = ""

// rawFrontmatterRows is FR-021e: every frontmatter key of every note, stored.
//
// WHAT IT WRITES, and why each choice is the one that keeps the ruling true:
//
//   - ONE STATE ROW PER KEY, unconditionally — including for a key whose value
//     is an explicit null. The state row is what makes `file.properties` (the
//     note's frontmatter KEY SET) a real thing to read: a key present with no
//     value is still a key the note carries, and dropping its row would make
//     `file.properties` disagree with the file.
//   - THE STATE ITSELF still follows R-3. `status:` with nothing after it is
//     StateAbsent — FR-007's rule that a key with no value is not a value —
//     while `status: open` is StatePresent. The ruling is that a note SAYING
//     `status: open` must never answer TRUE to `status IS NULL`; it is not that
//     a bare key must answer FALSE, and quietly promoting an empty key to
//     "present" here would break R-3 in the act of satisfying the ruling.
//   - VALUE ROWS HOLD THE SOURCE TEXT, in v_text and v_raw both, with the
//     quoting flag the parser saw. No typed column is filled, because no type
//     was declared and guessing one is the failure D3 names: a date stored as
//     free text, silently unmatchable.
//   - A KEY THE SCHEMA DECLARES IS SKIPPED. Its typed rows are written by the
//     caller's own loop; writing a raw row for it too would collide on the
//     (note_id, prop, elem) primary key and fail the whole note's transaction.
//
// A mapping-valued key gets its state row and no value rows: the key is present
// (so it appears in `file.properties` and is not absent), and a mapping has no
// scalar text that any comparison could be made against. Same for a list
// element that is itself a list or a mapping — it is skipped, and its siblings
// keep their SOURCE positions, so an element index still names the line the
// operator can go and look at.
func rawFrontmatterRows(rec records.Record, schema *records.Schema) []PropRow {
	if !rec.Frontmatter.Present || len(rec.Frontmatter.Keys) == 0 {
		return nil
	}
	var out []PropRow
	for _, key := range rec.Frontmatter.Keys {
		if schema != nil {
			if _, declared := schema.Property(key); declared {
				continue
			}
		}
		n, ok := rec.Frontmatter.Get(key)
		if !ok {
			continue
		}

		state := records.StatePresent
		if n.Kind == records.KindNull {
			state = records.StateAbsent
		}
		out = append(out, PropRow{
			Prop:  key,
			Elem:  StateRowElem,
			State: state,
			Type:  RawPropertyType,
		})

		elements := []records.Node{n}
		if n.Kind == records.KindSequence {
			elements = n.Items
		} else if n.Kind != records.KindScalar {
			continue
		}
		for i, el := range elements {
			if el.Kind != records.KindScalar {
				continue
			}
			out = append(out, PropRow{
				Prop:   key,
				Elem:   i,
				State:  records.StatePresent,
				Type:   RawPropertyType,
				Text:   el.Text,
				Raw:    el.Text,
				Quoted: el.Quoted,
			})
		}
	}
	return out
}

// nonConformingEvidence extracts what the file held and what was expected, from
// the findings records.ResolveProperty already produced.
//
// It reads the FIRST error-severity finding rather than concatenating them: a
// problem line names one record, one reason and one fix, and a property with
// three broken elements is still one property to go and look at.
func nonConformingEvidence(pv records.PropertyValue) (got, expected string) {
	for _, f := range pv.Findings {
		if f.Got == "" && f.Expected == "" {
			continue
		}
		return f.Got, f.Expected
	}
	return "", ""
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
	// Every remaining type is decoded by the SAME parser that wrote it, through
	// the schema's own declaration — from the TYPED COLUMN, never from the raw
	// source text where the type has one.
	//
	// The fallback to Raw is deliberately NOT silent. A typed column that may
	// quietly be skipped is a typed column nothing reads, and a decode bug in it
	// then looks exactly like correct behaviour: the raw text usually parses to
	// the same value, so the test passes and the column is dead.
	src := e.Raw
	switch prop.Type {
	case records.TypeDate:
		if e.Time == "" {
			return records.TypedValue{}, &StaleValueError{
				Prop:   prop.Name,
				Value:  e.Raw,
				Reason: "a date element was indexed with no value in its typed column",
			}
		}
		src = e.Time
	case records.TypeInteger, records.TypeDecimal:
		if e.Num == "" {
			return records.TypedValue{}, &StaleValueError{
				Prop:   prop.Name,
				Value:  e.Raw,
				Reason: "a numeric element was indexed with no value in its typed column",
			}
		}
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
	// The two causes are named separately because they point the reader at
	// different files. A schema that moved on is the operator's edit; a column
	// that came back empty is this index's own defect, and reporting the second
	// as the first would send someone to read a schema that is perfectly fine.
	if e.Reason != "" {
		return "property " + e.Prop + ": indexed value " + quote(e.Value) +
			" could not be decoded: " + e.Reason + "; re-index this note"
	}
	return "property " + e.Prop + ": indexed value " + quote(e.Value) +
		" is no longer admitted by the schema; re-index this note"
}

func quote(s string) string { return `"` + s + `"` }
