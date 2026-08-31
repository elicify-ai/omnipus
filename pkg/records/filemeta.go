// Omnipus — ADR-068 D24.2 / spec FR-130..FR-135: the `file.*` virtual
// properties, resolved in Go and compared by the ONE comparator.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS, AND THE ONE PROPERTY THAT MAKES IT SAFE
//
// Obsidian's Bases grammar lets a view ask about the FILE rather than about the
// note's frontmatter: `file.inFolder("Clients")`, `sort: file.mtime desc`,
// `file.hasTag("project")`. FR-130 admits thirteen such properties under the
// reserved `file.` namespace. This file resolves them.
//
// THE SAFETY PROPERTY IS STRUCTURAL, NOT PROCEDURAL (ruling R-A, FR-135).
// Every function here takes a FileMeta — a plain struct of already-retrieved
// facts — and returns a PropertyValue. There is no context.Context, no store,
// no *sql.DB, and this package cannot import propindex at all (propindex
// imports records; the reverse is an import cycle the compiler refuses). So
// "no file.* predicate reaches SQL" is not a rule anyone has to remember while
// editing: THERE IS NOTHING HERE THAT COULD REACH SQL. The comparison then runs
// in the same Comparator every declared property's does, because a file.*
// property IS a *Property — same type, same truth table, same refusals.
//
// The four METHODS are the same idea applied to the query side. FR-134: they
// are TRANSLATIONS to ordinary filter leaves, never a function grammar. There
// is no parser here and there must never be one — O-3 admits only the
// structured `{property, op, value}` object, and a method that became grammar
// would be a second query language reaching the same engine.
// ---------------------------------------------------------------------------

// The thirteen names (FR-130). They are the wire spelling, so they are plain
// strings rather than a named type: a filter's `property` field is a string and
// a refusal has to list these back to the caller verbatim.
const (
	// FileNameProp is the note's base name WITHOUT its extension.
	//
	// THIS IS A DECISION, and it is recorded as one because the spec names the
	// property and not its content. Two readings exist — Obsidian's own API has
	// both (`TFile.name` is "note.md", `TFile.basename` is "note") — and picking
	// the wrong one is invisible: every view still renders, the strings are just
	// four characters longer than the operator expected, and `file.name = "Acme"`
	// quietly matches nothing.
	//
	// Without the extension, because `file.ext` exists as a SEPARATE property.
	// If `file.name` carried the extension, `file.ext` would be pure redundancy;
	// with the split, `file.name + "." + file.ext` reconstructs the file name and
	// neither property is derivable from the other. It is also the value a reader
	// sees in Obsidian's own UI, which is what an imported view was written
	// against.
	FileNameProp = "file.name"

	// FilePathProp is the vault-relative path, extension included, exactly as
	// the candidate stream delivered it.
	FilePathProp = "file.path"

	// FileFolderProp is the parent folder, vault-relative, with no trailing
	// slash. A note at the vault root has the EMPTY STRING here, and under
	// FR-007a that is a PRESENT empty text value rather than absence — the root
	// folder is a folder. `file.folder IS NULL` is therefore FALSE for every
	// candidate, which is correct and is asserted rather than assumed.
	FileFolderProp = "file.folder"

	// FileExtProp is the extension without its dot ("md"), or a present empty
	// string for a file that has none.
	FileExtProp = "file.ext"

	// FileSizeProp is the file size in bytes (integer).
	FileSizeProp = "file.size"

	// FileCtimeProp is the file's BIRTH time — and, where the platform records
	// none, it is ABSENT rather than the POSIX inode-change time that shares the
	// abbreviation (FR-133).
	FileCtimeProp = "file.ctime"

	// FileMtimeProp is the file's last content-modification time.
	FileMtimeProp = "file.mtime"

	// FileTagsProp is the note's tags, fully qualified ("a/b"), with no leading
	// "#". Many.
	FileTagsProp = "file.tags"

	// FileLinksProp is the note's outgoing non-embed links. Many.
	FileLinksProp = "file.links"

	// FileEmbedsProp is the note's embeds (`![[x]]`). Many.
	FileEmbedsProp = "file.embeds"

	// FileBacklinksProp is the notes that link HERE — derived, never stored,
	// scoped and bounded (FR-132). See filemeta_backlinks.go.
	FileBacklinksProp = "file.backlinks"

	// FilePropertiesProp is the note's frontmatter KEY SET, read from FR-021e's
	// rows (which exist for every note, typed or not). Many.
	FilePropertiesProp = "file.properties"

	// FileSelfProp is the note itself as a display/formula operand. FR-130 puts
	// it outside the filterable set explicitly, and ResolveFileProperty refuses
	// it by name rather than resolving something a comparison would then answer
	// nonsense about.
	FileSelfProp = "file.file"
)

// FileNamespace is the reserved prefix. A property name starting with it is
// this file's business and never a schema's — FR-130 calls the namespace
// reserved, so a schema declaring `file.x` is not shadowing anything here.
const FileNamespace = "file."

// FilePropertyNames is the thirteen, in FR-130's own order. It is what a
// refusal lists back to a caller who misspelled one.
var FilePropertyNames = []string{
	FileNameProp, FilePathProp, FileFolderProp, FileExtProp,
	FileMtimeProp, FileCtimeProp, FileSizeProp,
	FileTagsProp, FileLinksProp, FileEmbedsProp, FileBacklinksProp,
	FilePropertiesProp, FileSelfProp,
}

// FileFilterablePropertyNames is the twelve that may appear in a filter, a
// sort, a group_by or a summary target — everything except `file.file`.
var FileFilterablePropertyNames = filterableFileNames()

func filterableFileNames() []string {
	out := make([]string, 0, len(FilePropertyNames)-1)
	for _, n := range FilePropertyNames {
		if n == FileSelfProp {
			continue
		}
		out = append(out, n)
	}
	return out
}

// fileRecordType is the RecordType stamped on every file.* Property.
//
// FR-009 scopes a property to its record type, and these belong to no record
// type: `file.mtime` means the same thing on a deal, on a contact and on a note
// that declares nothing. The sentinel says so out loud instead of leaving the
// field empty, which would read as "unset" rather than "deliberately global".
const fileRecordType = "file"

// fileProperties is the declaration table — the whole type system of the
// namespace in one place.
//
// TYPING NOTE, because the spec's parenthetical is not a PropertyType.
// FR-130 calls `file.links`/`embeds`/`backlinks` "many link". There is no
// `link` in the declared type set, and the nearest one — `relation` — would be
// WRONG here, not merely approximate: R-8 compares relations by RESOLVED RECORD
// IDENTITY through Comparator.ResolveRelation, and an ordinary wikilink points
// at an ordinary note, which has no record identity at all. Typed `relation`,
// `file.links = "Acme"` would emit CompareRelationUnresolved for every link in
// a vault whose notes are not records — a refusal instead of an answer, for the
// commonest case there is.
//
// They are therefore `text`, and the text they carry is the link's TARGET, which
// is R-8's own rule ("compare by target, never by display text") expressed in
// the type that can actually carry it. The Wikilink survives on the TypedValue's
// Link field for `asLink()` and for rendering, so nothing is lost — only the
// comparison changes, and it changes to the one that works.
var fileProperties = map[string]*Property{
	FileNameProp:       {Name: FileNameProp, Type: TypeText, RecordType: fileRecordType},
	FilePathProp:       {Name: FilePathProp, Type: TypeText, RecordType: fileRecordType},
	FileFolderProp:     {Name: FileFolderProp, Type: TypeText, RecordType: fileRecordType},
	FileExtProp:        {Name: FileExtProp, Type: TypeText, RecordType: fileRecordType},
	FileMtimeProp:      {Name: FileMtimeProp, Type: TypeDate, RecordType: fileRecordType},
	FileCtimeProp:      {Name: FileCtimeProp, Type: TypeDate, RecordType: fileRecordType},
	FileSizeProp:       {Name: FileSizeProp, Type: TypeInteger, RecordType: fileRecordType, Unit: "bytes"},
	FileTagsProp:       {Name: FileTagsProp, Type: TypeText, Many: true, RecordType: fileRecordType},
	FileLinksProp:      {Name: FileLinksProp, Type: TypeText, Many: true, RecordType: fileRecordType},
	FileEmbedsProp:     {Name: FileEmbedsProp, Type: TypeText, Many: true, RecordType: fileRecordType},
	FileBacklinksProp:  {Name: FileBacklinksProp, Type: TypeText, Many: true, RecordType: fileRecordType},
	FilePropertiesProp: {Name: FilePropertiesProp, Type: TypeText, Many: true, RecordType: fileRecordType},
	// file.file is declared so a formula layer can ask what it IS, and is kept
	// OUT of every filterable path by FileProperty/ResolveFileProperty rather
	// than by omitting it here — an absent declaration would produce "unknown
	// property", which tells the caller they spelled it wrong when they did not.
	FileSelfProp: {Name: FileSelfProp, Type: TypeText, RecordType: fileRecordType},
}

// FindingUnknownCreationTime is FR-133's honest absence: the platform does not
// record a birth time for this file, so `file.ctime` has no value and the
// problem list SAYS SO.
//
// It is a warning, not an error: the note is not wrong, the filesystem simply
// does not know. Without the finding, `file.ctime IS NULL` would match and the
// caller would have no way to tell "this file was never created" (impossible)
// from "this platform does not keep birth times" (the actual fact).
const FindingUnknownCreationTime FindingCode = "unknown_creation_time"

// IsFileProperty reports whether name is one of the thirteen.
func IsFileProperty(name string) bool {
	_, ok := fileProperties[name]
	return ok
}

// IsFileNamespace reports whether name is addressed to the reserved namespace
// at all — true for `file.mtime` AND for the misspelled `file.mtimes`.
//
// The two questions are different and the difference decides the refusal: a
// name in the namespace that is not one of the thirteen is "you misspelled a
// file property, here are the thirteen"; a name outside it is the schema's
// problem and FR-024's ordinary undeclared-property rejection.
func IsFileNamespace(name string) bool {
	return strings.HasPrefix(name, FileNamespace)
}

// FileProperty returns the declaration for one file.* property.
//
// `file.file` is deliberately NOT returned: it is not a comparison target
// (FR-130), and handing a caller a *Property for it is handing them the thing
// that makes a comparison possible.
func FileProperty(name string) (*Property, bool) {
	if name == FileSelfProp {
		return nil, false
	}
	p, ok := fileProperties[name]
	return p, ok
}

// FilePropertySchema is the twelve filterable file.* properties as a *Schema,
// so Filter.Validate, literal parsing and every FR-024 refusal work on them
// through the code that already exists rather than through a second path.
//
// A fresh Schema is built per call. The properties are pointers into a package
// table and a caller must not mutate them; handing out one shared *Schema would
// make that mistake silent instead of merely possible.
func FilePropertySchema() *Schema {
	s := &Schema{
		SchemaVersion: SupportedSchemaVersion,
		Type:          fileRecordType,
		Label:         "file metadata",
		Properties:    make(map[string]*Property, len(FileFilterablePropertyNames)),
		PropertyOrder: append([]string(nil), FileFilterablePropertyNames...),
	}
	for _, n := range FileFilterablePropertyNames {
		s.Properties[n] = fileProperties[n]
	}
	return s
}

// ---------------------------------------------------------------------------
// THE INPUT
// ---------------------------------------------------------------------------

// FileMeta is everything the resolution layer is allowed to know about one
// candidate's file: facts already retrieved, never a handle to retrieve more.
//
// Four fields carry the values FR-131 derives from the path with NO storage at
// all (Path alone yields name, path, folder and ext). Three come from the
// `notes` stat columns. Four come from the child tables. One — Backlinks — is
// derived by filemeta_backlinks.go and is the only one with a bound.
type FileMeta struct {
	// Path is the vault-relative path, e.g. "Clients/Acme.md". It is the sole
	// source for file.name, file.path, file.folder and file.ext.
	Path string

	// Mtime is the last content-modification time; MtimeKnown says whether the
	// row carried one. A zero time.Time is a real instant (year 1), so the flag
	// is what separates "not indexed yet" from "modified in the year 1".
	Mtime      time.Time
	MtimeKnown bool

	// Ctime is the file's BIRTH time and CtimeKnown says whether the platform
	// recorded one (FR-133). On a platform that does not, CtimeKnown is FALSE
	// and Ctime MUST be left zero — writing the inode-change time here is the
	// exact substitution FR-133 forbids, and this layer cannot detect it.
	Ctime      time.Time
	CtimeKnown bool

	// Size is the file size in bytes; SizeKnown separates a zero-byte file from
	// an unindexed one.
	Size      int64
	SizeKnown bool

	// Tags are fully qualified and carry no leading "#" — "projects/active",
	// never "#projects/active". Frontmatter and inline tags arrive in one list;
	// FR-131 stores them in one table and this layer does not distinguish them,
	// because neither Obsidian's `hasTag` nor a reader does.
	Tags []string

	// Links and Embeds are the note's outgoing wikilinks, partitioned by the
	// `embed` column of FR-131's note_links table: Links holds embed=false rows
	// and Embeds holds embed=true. SplitLinkRows performs that partition, so the
	// expectation is executable rather than prose.
	Links  []Wikilink
	Embeds []Wikilink

	// Backlinks is the derived inverse edge set for this note, as one Wikilink
	// per SOURCE note (FR-132).
	//
	// BacklinksDerived exists because a nil slice cannot tell "this note has no
	// backlinks" from "nobody built the index". Those are a correct answer and a
	// caller defect, and answering the second as the first is how a query
	// returns confidently empty. Without the flag, ResolveFileProperty would
	// have to guess; with it, it refuses.
	Backlinks        []Wikilink
	BacklinksDerived bool

	// PropertyKeys is the note's frontmatter key set in document order — FR-021e's
	// rows, which exist for EVERY note since Draft 11, typed or not.
	PropertyKeys []string

	// PropertyValues is the same frontmatter as a map, for the formula layer's
	// `file.properties["x"]` operand (FR-130: "the full map is a formula
	// operand"). It is NEVER read by resolution or comparison — file.properties
	// compares over the KEY SET — and a nil map is not a defect.
	PropertyValues map[string]string
}

// FileName is the base name without the extension. See FileNameProp for why.
func (m FileMeta) FileName() string {
	base := path.Base(cleanVaultPath(m.Path))
	if base == "." || base == "/" {
		return ""
	}
	if ext := path.Ext(base); ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

// FileFolder is the parent folder with no trailing slash; "" at the vault root.
func (m FileMeta) FileFolder() string {
	p := cleanVaultPath(m.Path)
	if p == "" {
		return ""
	}
	dir := path.Dir(p)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// FileExt is the extension with no leading dot; "" for a file that has none.
func (m FileMeta) FileExt() string {
	ext := path.Ext(path.Base(cleanVaultPath(m.Path)))
	return strings.TrimPrefix(ext, ".")
}

// AsLink is FR-134's fourth method — `file.asLink()`.
//
// It is PRESENTATION and it never compares. It is a method on FileMeta rather
// than a resolvable property precisely so that it cannot arrive in a filter
// leaf: there is no property name that reaches it.
func (m FileMeta) AsLink() Wikilink {
	name := m.FileName()
	target := cleanVaultPath(m.Path)
	return Wikilink{
		Target:  target,
		Display: name,
		Raw:     "[[" + target + "|" + name + "]]",
	}
}

// cleanVaultPath normalises separators and strips a leading "./" or "/" so that
// "Clients/Acme.md", "./Clients/Acme.md" and "/Clients/Acme.md" are one path.
//
// Windows separators are folded to "/" because a vault-relative path is a VAULT
// path, not an OS path: the same vault synced to a Mac and a PC must give
// `file.folder` the same answer, or an imported view's folder filter matches on
// one machine and not the other.
func cleanVaultPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

// SplitLinkRows partitions one note's link rows into (links, embeds) by the
// `embed` column, which is the partition FileMeta.Links and FileMeta.Embeds
// document and the only one FR-131's schema supports.
//
// It exists so the assembly step and this layer cannot disagree about which
// list an embed belongs in — a disagreement that would show up as `file.embeds`
// answering empty on every note while `file.links` over-counted, with nothing
// failing anywhere.
func SplitLinkRows(rows []FileLinkRow) (links, embeds []Wikilink) {
	for _, r := range rows {
		wl := r.Wikilink()
		if r.Embed {
			embeds = append(embeds, wl)
		} else {
			links = append(links, wl)
		}
	}
	return links, embeds
}

// ---------------------------------------------------------------------------
// RESOLUTION
// ---------------------------------------------------------------------------

// ResolveFileProperty resolves one file.* property on one candidate into the
// same PropertyValue shape ResolveProperty produces for a declared property —
// so the comparator, the sorter and the grouper cannot tell the two apart and
// there is no second set of rules for them to disagree about.
//
// It refuses, rather than answers, in exactly three cases:
//
//   - `file.file`, which FR-130 excludes from every filter position;
//   - a name in the `file.` namespace that is not one of the thirteen;
//   - `file.backlinks` on a FileMeta whose backlinks were never derived.
//
// A name outside the namespace is also refused, because reaching here with one
// means a caller routed a schema property into the file layer.
func ResolveFileProperty(name string, m FileMeta) (PropertyValue, error) {
	switch {
	case name == FileSelfProp:
		return PropertyValue{}, &QueryError{
			Property: name,
			Reason: "file.file is the note itself — a display and formula operand, not a comparison target; " +
				"there is no value a comparison could be made against",
			ValidNames: append([]string(nil), FileFilterablePropertyNames...),
			Remedy:     "compare file.path or file.name instead, or use file.file only in a formula",
		}
	case !IsFileProperty(name):
		reason := fmt.Sprintf("%q is not a file property", name)
		if !IsFileNamespace(name) {
			reason = fmt.Sprintf("%q is not in the reserved %q namespace and cannot be resolved as file metadata", name, FileNamespace)
		}
		return PropertyValue{}, &QueryError{
			Property:   name,
			Reason:     reason,
			ValidNames: append([]string(nil), FilePropertyNames...),
			Remedy:     "name one of the thirteen file properties, or a property the record type declares",
		}
	}

	prop := fileProperties[name]

	switch name {
	case FileNameProp:
		return fileTextValue(prop, m.FileName(), m.Path != ""), nil
	case FilePathProp:
		p := cleanVaultPath(m.Path)
		return fileTextValue(prop, p, p != ""), nil
	case FileFolderProp:
		// Present even when empty: "" IS the root folder, and FR-007a's
		// empty-is-absent rule is explicitly for NON-text types.
		return fileTextValue(prop, m.FileFolder(), m.Path != ""), nil
	case FileExtProp:
		return fileTextValue(prop, m.FileExt(), m.Path != ""), nil

	case FileMtimeProp:
		if !m.MtimeKnown {
			return absentFileValue(prop), nil
		}
		return fileDateValue(prop, m.Mtime), nil

	case FileCtimeProp:
		if !m.CtimeKnown {
			// FR-133, and test 93's assertion: absent, and the problem list
			// names WHY, so "no creation time" cannot be read as "this file
			// has no history".
			pv := absentFileValue(prop)
			pv.Findings = []Finding{{
				RecordPath:   cleanVaultPath(m.Path),
				Property:     name,
				ElementIndex: -1,
				Code:         FindingUnknownCreationTime,
				Severity:     SeverityWarning,
				Reason: "this platform does not record a creation (birth) time for this file, " +
					"so file.ctime is absent; the POSIX inode-change time is NOT substituted for it",
				Expected: "a birth timestamp from the filesystem",
			}}
			return pv, nil
		}
		return fileDateValue(prop, m.Ctime), nil

	case FileSizeProp:
		if !m.SizeKnown {
			return absentFileValue(prop), nil
		}
		return PropertyValue{
			Property: prop,
			State:    StatePresent,
			Values: []TypedValue{{
				Type:   TypeInteger,
				Raw:    fmt.Sprintf("%d", m.Size),
				Number: DecimalFromInt64(m.Size, 0),
			}},
			SourceIndex: []int{0},
		}, nil

	case FileTagsProp:
		return fileTextList(prop, m.Tags), nil
	case FilePropertiesProp:
		return fileTextList(prop, m.PropertyKeys), nil

	case FileLinksProp:
		return fileLinkList(prop, m.Links), nil
	case FileEmbedsProp:
		return fileLinkList(prop, m.Embeds), nil

	case FileBacklinksProp:
		if !m.BacklinksDerived {
			return PropertyValue{}, &QueryError{
				Property: name,
				Reason: "file.backlinks was asked for on a candidate whose backlink index was never derived; " +
					"answering an empty list here would report 'nothing links to this note' for every note in the vault",
				Remedy: "build the index once per query with BuildBacklinkIndex and set FileMeta.Backlinks / BacklinksDerived from it",
			}
		}
		return fileLinkList(prop, m.Backlinks), nil
	}

	// Unreachable: every entry in fileProperties has a case above, and
	// TestFileMeta_EveryDeclaredPropertyResolves proves it by resolving the
	// whole table rather than by trusting this comment.
	return PropertyValue{}, &QueryError{
		Property: name,
		Reason:   fmt.Sprintf("file property %q is declared but has no resolution", name),
		Remedy:   "this is a defect in the file-metadata layer; report it",
	}
}

// fileTextValue builds a scalar text value. present=false means the fact the
// value is derived FROM is missing (an empty Path), which is absence rather
// than an empty string.
func fileTextValue(prop *Property, text string, present bool) PropertyValue {
	if !present {
		return absentFileValue(prop)
	}
	return PropertyValue{
		Property:    prop,
		State:       StatePresent,
		Values:      []TypedValue{{Type: TypeText, Raw: text, Text: text}},
		SourceIndex: []int{0},
	}
}

func fileDateValue(prop *Property, t time.Time) PropertyValue {
	utc := t.UTC()
	return PropertyValue{
		Property: prop,
		State:    StatePresent,
		Values: []TypedValue{{
			Type: TypeDate,
			Raw:  utc.Format(time.RFC3339),
			// HasTime is true because a stat timestamp IS an instant. FR-130's
			// date-only rule works the other way — a date-only LITERAL means the
			// start of that day, UTC — and that is the literal side's job, done
			// by the same parser every date literal already goes through.
			Date: DateValue{Instant: utc, HasTime: true},
		}},
		SourceIndex: []int{0},
	}
}

// fileTextList builds a `many` text value.
//
// AN EMPTY LIST IS ABSENCE HERE, and that is a deliberate departure from R-3,
// stated rather than slipped in. R-3 ("an empty list is a value, not absence")
// distinguishes a note that WROTE `tags: []` from one that wrote no `tags` key
// at all — a real distinction with a real referent in the file. A derived
// property has no key to have written: a note with no tags did not write an
// empty list, it wrote nothing, and there is no state in which it could have
// done otherwise. Absence is the only reachable reading, and it is also the
// useful one — `file.tags IS NULL` answers "untagged", which is what the
// question means and what Obsidian's own `isEmpty(file.tags)` idiom asks.
func fileTextList(prop *Property, items []string) PropertyValue {
	if len(items) == 0 {
		return absentFileValue(prop)
	}
	pv := PropertyValue{Property: prop, State: StatePresent}
	for i, it := range items {
		pv.Values = append(pv.Values, TypedValue{Type: TypeText, Raw: it, Text: it})
		pv.SourceIndex = append(pv.SourceIndex, i)
	}
	return pv
}

// fileLinkList builds a `many` text value over wikilinks.
//
// Text is the link TARGET — the comparison key, R-8's rule in the type that can
// carry it — while Link keeps the whole Wikilink so `asLink()`, rendering and
// the formula layer see the display text and the raw form the file wrote. The
// comparator reads Text; nothing reads Display, which is R-8's point.
func fileLinkList(prop *Property, links []Wikilink) PropertyValue {
	if len(links) == 0 {
		return absentFileValue(prop)
	}
	pv := PropertyValue{Property: prop, State: StatePresent}
	for i, wl := range links {
		raw := wl.Raw
		if raw == "" {
			raw = "[[" + wl.Target + "]]"
		}
		pv.Values = append(pv.Values, TypedValue{
			Type: TypeText,
			Raw:  raw,
			Text: wl.Target,
			Link: wl,
		})
		pv.SourceIndex = append(pv.SourceIndex, i)
	}
	return pv
}

func absentFileValue(prop *Property) PropertyValue {
	return PropertyValue{Property: prop, State: StateAbsent}
}

// ---------------------------------------------------------------------------
// FR-134 — THE FOUR METHODS, AS TRANSLATIONS
// ---------------------------------------------------------------------------

// FileMethod is one of Obsidian's four file methods. It is a closed set: a name
// outside it is refused BY NAME listing the four, never parsed.
type FileMethod string

const (
	// FileMethodInFolder is `file.inFolder(x)` — the folder AND its descendants.
	FileMethodInFolder FileMethod = "inFolder"
	// FileMethodHasTag is `file.hasTag(x)` — the tag AND its sub-tags.
	FileMethodHasTag FileMethod = "hasTag"
	// FileMethodHasLink is `file.hasLink(x)` — an exact outgoing link target.
	FileMethodHasLink FileMethod = "hasLink"
	// FileMethodAsLink is `file.asLink()` — PRESENTATION, and never a filter.
	FileMethodAsLink FileMethod = "asLink"
)

// FileMethods is the closed four, in the order a refusal lists them.
var FileMethods = []FileMethod{
	FileMethodInFolder, FileMethodHasTag, FileMethodHasLink, FileMethodAsLink,
}

func fileMethodRefusalList() []string {
	out := make([]string, 0, len(FileMethods))
	for _, m := range FileMethods {
		out = append(out, "file."+string(m)+"()")
	}
	return out
}

// TranslateFileMethod turns one file method call into an ordinary filter node —
// the SAME `{property, op, value}` objects a hand-written filter uses, over the
// SAME twelve property names, with ops drawn from the SAME closed ten.
//
// This is the whole of FR-134. There is no function grammar anywhere in the
// query path: by the time a filter is evaluated, a method has ceased to exist
// and what remains is a tree the engine already knew how to evaluate. Nothing
// downstream needs to learn about methods, which is why a method can never
// acquire a special case in the comparator.
//
// The caller (the importer, or knowledge_configure) owns the PARSING of
// `file.inFolder("Clients")` into a method and an argument. This function owns
// the MEANING, and it is the only place that meaning is written down.
func TranslateFileMethod(method FileMethod, arg string) (generated.VaultFilterNode, error) {
	switch method {
	case FileMethodAsLink:
		return generated.VaultFilterNode{}, &QueryError{
			Property: "file." + string(method) + "()",
			Reason: "file.asLink() renders the note as a link; it is presentation and produces nothing a filter can compare — " +
				"a view that filters on it is asking whether a rendering is true",
			ValidNames: fileMethodRefusalList(),
			Remedy:     "use file.asLink() in a formula or a displayed column; filter on file.path or file.name",
		}
	case FileMethodInFolder, FileMethodHasTag, FileMethodHasLink:
		// fall through
	default:
		return generated.VaultFilterNode{}, &QueryError{
			Property: string(method),
			Reason:   fmt.Sprintf("%q is not a file method", string(method)),
			// The four are listed even though only three translate: a caller who
			// wrote asLink needs to see it named as one of the four and refused
			// for a reason, not told it does not exist.
			ValidNames: fileMethodRefusalList(),
			Remedy:     "use file.inFolder(), file.hasTag() or file.hasLink() in a filter",
		}
	}

	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return generated.VaultFilterNode{}, &QueryError{
			Property: "file." + string(method) + "()",
			Reason: fmt.Sprintf("file.%s() was given an empty argument; it would match either everything or nothing "+
				"depending on which reading is taken, and a filter nobody can predict is worse than a refusal", string(method)),
			Remedy: "name the folder, tag or link target the view meant",
		}
	}

	switch method {
	case FileMethodInFolder:
		folder := strings.TrimSuffix(cleanVaultPath(trimmed), "/")
		if folder == "" {
			// The vault root contains everything. Saying so as a refusal beats
			// emitting a tautological filter the operator will not recognise as
			// one when it returns their whole vault.
			return generated.VaultFilterNode{}, &QueryError{
				Property: "file.inFolder()",
				Reason: "the vault root was named as a folder; every note is in it, so the filter would select the whole vault " +
					"while looking like it narrowed something",
				Remedy: "name a subfolder, or drop the filter",
			}
		}
		return hierarchyNode(FileFolderProp, folder), nil

	case FileMethodHasTag:
		// A leading "#" is how a tag is written in prose and in some .base
		// files; it is not part of the tag. Stripping ONE is deliberate:
		// "##x" is not a tag with an extra hash, it is a heading, and quietly
		// turning it into one would hide the operator's mistake.
		tag := strings.TrimPrefix(trimmed, "#")
		if tag == "" {
			return generated.VaultFilterNode{}, &QueryError{
				Property: "file.hasTag()",
				Reason:   `file.hasTag("#") names no tag`,
				Remedy:   "name the tag, with or without its leading #",
			}
		}
		return hierarchyNode(FileTagsProp, tag), nil

	case FileMethodHasLink:
		// Exact, and only exact. FR-134 gives hasLink a single leaf, not the
		// hierarchy pair: a link target has no sub-targets, and a prefix match
		// would make [[Acme]] match [[Acme Holdings]] — a wrong answer wearing
		// the shape of a helpful one.
		target := linkTargetArgument(trimmed)
		prop := FileLinksProp
		op := generated.Equal
		return generated.VaultFilterNode{Property: &prop, Op: &op, Value: &target}, nil
	}

	// Unreachable — the switch above is exhaustive over the three translating
	// methods and every other value returned earlier.
	return generated.VaultFilterNode{}, &QueryError{
		Property: string(method),
		Reason:   "file method has no translation",
		Remedy:   "this is a defect in the file-metadata layer; report it",
	}
}

// hierarchyNode is the shape FR-134 states for the two hierarchical methods:
//
//	{any: [{p, "=", x}, {p, "LIKE", x/%}]}
//
// The equality leaf catches the exact folder or tag; the LIKE leaf catches
// everything beneath it. Both are ordinary leaves over an ordinary property
// with ops from the closed ten — nothing here is a new construct, which is the
// point of the translation.
//
// The LIKE operand is ESCAPED (FR-134's parenthetical): a folder or tag
// containing `%`, `_` or `\` is a literal name, and leaving it unescaped would
// turn "Q1_2026" into a pattern matching "Q1x2026" — silently including notes
// the operator never asked for.
func hierarchyNode(property, value string) generated.VaultFilterNode {
	prop := property
	eqOp := generated.Equal
	likeOp := generated.LIKE
	exact := value
	pattern := escapeLikeLiteral(value) + "/%"

	children := []generated.VaultFilterNode{
		{Property: &prop, Op: &eqOp, Value: &exact},
		{Property: &prop, Op: &likeOp, Value: &pattern},
	}
	return generated.VaultFilterNode{Any: &children}
}

// escapeLikeLiteral makes a literal string safe as a LIKE operand: `\` first,
// then the two wildcards. Order matters — escaping `%` before `\` would then
// escape the backslash this function just added.
func escapeLikeLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// linkTargetArgument accepts both spellings a .base file uses for a link
// argument — `hasLink("Acme")` and `hasLink("[[Acme]]")` — and yields the
// target, which is what file.links compares on.
func linkTargetArgument(arg string) string {
	if wl, ok := ParseWikilink(arg); ok {
		return wl.Target
	}
	return cleanVaultPath(arg)
}
