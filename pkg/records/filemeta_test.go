// Omnipus — tests for FR-130..FR-135: the thirteen file.* virtual properties,
// the four method translations, and the structural reason none of it can reach
// SQL.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// THE FIXTURE
//
// One note, chosen so that every derivation has something to get wrong: a
// nested folder (so file.folder is not ""), a space in the base name (so a
// naive split on "." or " " shows up), a two-level tag (so hasTag's hierarchy
// leaf has a target), an embed as well as a link (so the two lists cannot be
// silently merged), and a frontmatter key set.
//
// Every expected value below is written from FR-130's OWN description of the
// property, not read off the implementation.
// ---------------------------------------------------------------------------

func fmFixture() FileMeta {
	mtime := time.Date(2026, 8, 30, 14, 5, 0, 0, time.UTC)
	ctime := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	return FileMeta{
		Path:       "Clients/Acme Corp.md",
		Mtime:      mtime,
		MtimeKnown: true,
		Ctime:      ctime,
		CtimeKnown: true,
		Size:       4096,
		SizeKnown:  true,
		Tags:       []string{"clients/active", "priority"},
		Links: []Wikilink{
			{Target: "Deals/Q3 Renewal", Raw: "[[Deals/Q3 Renewal]]"},
			{Target: "People/Jane Roe", Display: "Jane", Raw: "[[People/Jane Roe|Jane]]"},
		},
		Embeds: []Wikilink{
			{Target: "Assets/logo.png", Raw: "![[Assets/logo.png]]"},
		},
		Backlinks:        []Wikilink{{Target: "Index/Clients.md", Raw: "[[Index/Clients.md]]"}},
		BacklinksDerived: true,
		PropertyKeys:     []string{"type", "status", "owner"},
		PropertyValues:   map[string]string{"type": "company", "status": "active", "owner": "[[People/Jane Roe]]"},
	}
}

// fmMustResolve is the happy path, so a failure names the property rather than
// panicking somewhere further down.
func fmMustResolve(t *testing.T, name string, m FileMeta) PropertyValue {
	t.Helper()
	pv, err := ResolveFileProperty(name, m)
	if err != nil {
		t.Fatalf("ResolveFileProperty(%q): unexpected error: %v", name, err)
	}
	return pv
}

// texts renders a resolved value's comparison keys, which is what every
// assertion below is actually about.
func fmTexts(pv PropertyValue) []string {
	out := make([]string, 0, len(pv.Values))
	for _, v := range pv.Values {
		out = append(out, v.Text)
	}
	return out
}

func fmEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// FR-130 — all thirteen, on one fixture
// ---------------------------------------------------------------------------

func TestFileMeta_AllThirteenResolveOnOneFixture(t *testing.T) {
	m := fmFixture()

	// The twelve that resolve. Expected values are FR-130's descriptions
	// applied to the fixture by hand.
	cases := []struct {
		name  string
		typ   PropertyType
		many  bool
		want  []string
		state PropertyState
	}{
		{FileNameProp, TypeText, false, []string{"Acme Corp"}, StatePresent},
		{FilePathProp, TypeText, false, []string{"Clients/Acme Corp.md"}, StatePresent},
		{FileFolderProp, TypeText, false, []string{"Clients"}, StatePresent},
		{FileExtProp, TypeText, false, []string{"md"}, StatePresent},
		{FileMtimeProp, TypeDate, false, []string{"2026-08-30T14:05:00Z"}, StatePresent},
		{FileCtimeProp, TypeDate, false, []string{"2026-01-02T09:00:00Z"}, StatePresent},
		{FileSizeProp, TypeInteger, false, []string{"4096"}, StatePresent},
		{FileTagsProp, TypeText, true, []string{"clients/active", "priority"}, StatePresent},
		{FileLinksProp, TypeText, true, []string{"Deals/Q3 Renewal", "People/Jane Roe"}, StatePresent},
		{FileEmbedsProp, TypeText, true, []string{"Assets/logo.png"}, StatePresent},
		{FileBacklinksProp, TypeText, true, []string{"Index/Clients.md"}, StatePresent},
		{FilePropertiesProp, TypeText, true, []string{"type", "status", "owner"}, StatePresent},
	}

	if len(cases)+1 != len(FilePropertyNames) {
		t.Fatalf("this test covers %d properties plus file.file, but FR-130 declares %d: %v",
			len(cases), len(FilePropertyNames), FilePropertyNames)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pv := fmMustResolve(t, tc.name, m)
			if pv.State != tc.state {
				t.Fatalf("state = %v, want %v", pv.State, tc.state)
			}
			if pv.Property == nil || pv.Property.Type != tc.typ || pv.Property.Many != tc.many {
				t.Fatalf("declaration = %+v, want type %s many %v", pv.Property, tc.typ, tc.many)
			}
			var got []string
			switch tc.typ {
			case TypeDate:
				for _, v := range pv.Values {
					got = append(got, v.Date.Instant.UTC().Format(time.RFC3339))
				}
			case TypeInteger:
				for _, v := range pv.Values {
					got = append(got, v.Number.String())
				}
			default:
				got = fmTexts(pv)
			}
			if !fmEqualStrings(got, tc.want) {
				t.Fatalf("values = %q, want %q", got, tc.want)
			}
			if len(pv.SourceIndex) != len(pv.Values) {
				t.Fatalf("SourceIndex has %d entries for %d values — a report would name the wrong element",
					len(pv.SourceIndex), len(pv.Values))
			}
		})
	}

	// The thirteenth. FR-130: file.file is a formula/display operand only and is
	// NEVER a filter target, so it must refuse rather than resolve.
	t.Run(FileSelfProp, func(t *testing.T) {
		pv, err := ResolveFileProperty(FileSelfProp, m)
		if err == nil {
			t.Fatalf("file.file resolved to %+v; FR-130 excludes it from every filter position", pv)
		}
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("error is %T, want *QueryError", err)
		}
		for _, listed := range qe.ValidNames {
			if listed == FileSelfProp {
				t.Fatalf("the refusal lists file.file as a valid alternative to itself: %v", qe.ValidNames)
			}
		}
		if len(qe.ValidNames) != 12 {
			t.Fatalf("refusal lists %d alternatives, want the twelve filterable ones: %v", len(qe.ValidNames), qe.ValidNames)
		}
		if _, ok := FileProperty(FileSelfProp); ok {
			t.Fatal("FileProperty returned a declaration for file.file — that is the handle that makes a comparison possible")
		}
	})
}

// FR-133 / spec test 93.
func TestFileMeta_CtimeAbsentWhereBirthtimeUnknown(t *testing.T) {
	m := fmFixture()
	m.CtimeKnown = false
	m.Ctime = time.Time{}

	pv := fmMustResolve(t, FileCtimeProp, m)
	if pv.State != StateAbsent {
		t.Fatalf("state = %v, want absent — FR-133 forbids substituting the inode-change time", pv.State)
	}
	if len(pv.Values) != 0 {
		t.Fatalf("absent ctime carries %d values: %+v", len(pv.Values), pv.Values)
	}

	// "IS NULL matches" — asserted through the real filter path, not by reading
	// the state field a second time.
	res, err := Filter{Property: FileCtimeProp, Op: OpIsNull}.
		MatchValue(Comparator{}, FilePropertySchema(), pv)
	if err != nil {
		t.Fatalf("IS NULL on file.ctime: %v", err)
	}
	if !res.Matched {
		t.Fatal("file.ctime IS NULL did not match a file with no recorded birth time")
	}

	// "the problem list names unknown-creation-time".
	if len(pv.Findings) != 1 || pv.Findings[0].Code != FindingUnknownCreationTime {
		t.Fatalf("findings = %+v, want exactly one %s", pv.Findings, FindingUnknownCreationTime)
	}
	if pv.Findings[0].Severity != SeverityWarning {
		t.Fatalf("severity = %q, want warning — the note is not wrong, the filesystem does not know",
			pv.Findings[0].Severity)
	}
	if !strings.Contains(pv.Findings[0].Reason, "inode-change time") {
		t.Fatalf("the finding does not say the inode-change time was NOT substituted: %q", pv.Findings[0].Reason)
	}

	// A ctime the platform DOES record must not carry the finding — otherwise
	// the assertion above passes for a reason unrelated to birth times.
	if pv := fmMustResolve(t, FileCtimeProp, fmFixture()); len(pv.Findings) != 0 {
		t.Fatalf("a known birth time carries findings %+v", pv.Findings)
	}
}

// ---------------------------------------------------------------------------
// The two absence decisions, stated in filemeta.go and asserted here so neither
// can be quietly reversed.
// ---------------------------------------------------------------------------

func TestFileMeta_EmptyManyIsAbsentAndRootFolderIsPresent(t *testing.T) {
	// A note at the vault root with no tags, no links, no embeds and no
	// frontmatter.
	m := FileMeta{Path: "Inbox.md", BacklinksDerived: true}

	for _, name := range []string{FileTagsProp, FileLinksProp, FileEmbedsProp, FileBacklinksProp, FilePropertiesProp} {
		pv := fmMustResolve(t, name, m)
		if pv.State != StateAbsent {
			t.Fatalf("%s on a note with none = %v, want absent (a derived property has no key to have written an empty list)",
				name, pv.State)
		}
		res, err := Filter{Property: name, Op: OpIsNull}.MatchValue(Comparator{}, FilePropertySchema(), pv)
		if err != nil {
			t.Fatalf("%s IS NULL: %v", name, err)
		}
		if !res.Matched {
			t.Fatalf("%s IS NULL did not match a note with none — the untagged-notes query returns nothing", name)
		}
	}

	// file.folder is text, and FR-007a's empty-is-absent rule is for NON-text
	// types: "" IS the root folder.
	folder := fmMustResolve(t, FileFolderProp, m)
	if folder.State != StatePresent {
		t.Fatalf("file.folder at the vault root = %v, want present — the root is a folder", folder.State)
	}
	if got := fmTexts(folder); !fmEqualStrings(got, []string{""}) {
		t.Fatalf("file.folder at the root = %q, want one empty string", got)
	}
	res, err := Filter{Property: FileFolderProp, Op: OpIsNull}.MatchValue(Comparator{}, FilePropertySchema(), folder)
	if err != nil {
		t.Fatalf("file.folder IS NULL: %v", err)
	}
	if res.Matched {
		t.Fatal("file.folder IS NULL matched a note at the vault root")
	}
}

// ---------------------------------------------------------------------------
// FR-135 — every file.* comparison runs in the Go comparator
// ---------------------------------------------------------------------------

func TestFileMeta_ComparisonsRunInTheGoComparator(t *testing.T) {
	m := fmFixture()
	schema := FilePropertySchema()

	cases := []struct {
		desc     string
		property string
		op       Operator
		literal  string
		want     bool
	}{
		// text, scalar, case-insensitive equality (R-10 / FR-011a)
		{"name equality is case-insensitive", FileNameProp, OpEqual, "acme corp", true},
		{"name equality is anchored", FileNameProp, OpEqual, "Acme", false},
		{"path LIKE is anchored to the whole value", FilePathProp, OpLike, "Clients/%", true},
		{"path LIKE does not substring-match", FilePathProp, OpLike, "Clients", false},
		// date, ordering (R-7)
		{"mtime after a date-only literal", FileMtimeProp, OpGreater, "2026-08-01", true},
		{"mtime before a date-only literal", FileMtimeProp, OpLess, "2026-08-01", false},
		// integer, ordering (R-1)
		{"size ordering", FileSizeProp, OpGreater, "1024", true},
		{"size ordering, other side", FileSizeProp, OpLess, "1024", false},
		// many text, element-wise equality (R-9)
		{"tags match element-wise", FileTagsProp, OpEqual, "priority", true},
		{"tags do not match a sub-tag by equality", FileTagsProp, OpEqual, "clients", false},
		{"tags match a sub-tag by pattern", FileTagsProp, OpLike, "clients/%", true},
		// many link, compared by TARGET and never by display text (R-8's rule)
		{"links compare by target", FileLinksProp, OpEqual, "People/Jane Roe", true},
		{"links do not compare by display text", FileLinksProp, OpEqual, "Jane", false},
		{"embeds are a separate list", FileEmbedsProp, OpEqual, "Deals/Q3 Renewal", false},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			pv := fmMustResolve(t, tc.property, m)
			f := Filter{Property: tc.property, Op: tc.op, Literal: tc.literal, LiteralGiven: true}
			// The ZERO Comparator: no RelationResolver anywhere. If any file.*
			// property were typed `relation`, every link case here would report
			// CompareRelationUnresolved instead of answering.
			res, err := f.MatchValue(Comparator{}, schema, pv)
			if err != nil {
				t.Fatalf("MatchValue: %v", err)
			}
			if len(res.ComparisonProblems) != 0 {
				t.Fatalf("the comparator could not make the comparison: %+v", res.ComparisonProblems)
			}
			if res.Matched != tc.want {
				t.Fatalf("%s %s %q = %v, want %v", tc.property, tc.op, tc.literal, res.Matched, tc.want)
			}
		})
	}
}

// FR-135's structural half, as far as THIS layer can carry it: the resolution
// code has no way to reach a database, and that is a property of what it
// imports and what its signatures accept — not of anyone's discipline.
//
// The recorder and Selector halves of spec test 39a/92 live in
// pkg/records/propindex, where the statements are. This assertion is the one
// that belongs here.
func TestFileMeta_ResolutionCannotReachSQL(t *testing.T) {
	files := []string{"filemeta.go", "filemeta_backlinks.go"}
	forbidden := []string{"database/sql", "propindex", "modernc.org/sqlite"}

	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.Contains(p, bad) {
					t.Fatalf("%s imports %q — the file-metadata layer must have no path to a database", name, p)
				}
			}
		}
	}

	// The signature is the other half: a function that takes no context and no
	// store cannot start a query however it is edited.
	fset2 := token.NewFileSet()
	f, err := parser.ParseFile(fset2, "filemeta.go", nil, 0)
	if err != nil {
		t.Fatalf("parse filemeta.go: %v", err)
	}
	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ResolveFileProperty" {
			return true
		}
		found = true
		for _, param := range fn.Type.Params.List {
			var b strings.Builder
			if err := fmPrintType(&b, param.Type); err != nil {
				t.Fatalf("render parameter type: %v", err)
			}
			typ := b.String()
			if typ != "string" && typ != "FileMeta" {
				t.Fatalf("ResolveFileProperty takes a %s parameter; it must take only a property name and already-retrieved facts", typ)
			}
		}
		return false
	})
	if !found {
		t.Fatal("ResolveFileProperty not found in filemeta.go — this guard would have passed vacuously")
	}
}

// fmPrintType renders the small subset of type expressions the guarded signature
// can legally contain. Anything else — a pointer, a selector like sql.DB, a
// channel — is deliberately reported as itself so the assertion above fails
// naming what arrived.
func fmPrintType(b *strings.Builder, e ast.Expr) error {
	switch t := e.(type) {
	case *ast.Ident:
		b.WriteString(t.Name)
	case *ast.StarExpr:
		b.WriteString("*")
		return fmPrintType(b, t.X)
	case *ast.SelectorExpr:
		if err := fmPrintType(b, t.X); err != nil {
			return err
		}
		b.WriteString("." + t.Sel.Name)
	default:
		b.WriteString("unrecognised-type-expression")
	}
	return nil
}

// ---------------------------------------------------------------------------
// FR-134 — the methods become ordinary leaves
// ---------------------------------------------------------------------------

// fmRenderNode renders a translated tree so a failure shows the tree rather than
// a soup of pointers, and so the test log carries the translated tree as the
// evidence FR-134 asks for.
func fmRenderNode(n generated.VaultFilterNode) string {
	switch {
	case n.All != nil:
		return "{all: [" + fmRenderChildren(*n.All) + "]}"
	case n.Any != nil:
		return "{any: [" + fmRenderChildren(*n.Any) + "]}"
	case n.Not != nil:
		return "{not: " + fmRenderNode(*n.Not) + "}"
	}
	var b strings.Builder
	b.WriteString("{")
	if n.Property != nil {
		b.WriteString(*n.Property)
	}
	if n.Op != nil {
		b.WriteString(" " + string(*n.Op))
	}
	if n.Value != nil {
		b.WriteString(` "` + *n.Value + `"`)
	}
	if n.Values != nil {
		b.WriteString(" [" + strings.Join(*n.Values, ", ") + "]")
	}
	b.WriteString("}")
	return b.String()
}

func fmRenderChildren(children []generated.VaultFilterNode) string {
	parts := make([]string, 0, len(children))
	for _, c := range children {
		parts = append(parts, fmRenderNode(c))
	}
	return strings.Join(parts, ", ")
}

// fmAssertOrdinaryLeaves is FR-134's actual claim: what a method translates to is
// indistinguishable from a hand-written filter. Every leaf names one of the
// twelve filterable file.* properties and one of FR-022b's closed ten operators,
// and there is no other kind of node anywhere in the tree.
func fmAssertOrdinaryLeaves(t *testing.T, n generated.VaultFilterNode) {
	t.Helper()
	switch {
	case n.All != nil:
		for _, c := range *n.All {
			fmAssertOrdinaryLeaves(t, c)
		}
		return
	case n.Any != nil:
		for _, c := range *n.Any {
			fmAssertOrdinaryLeaves(t, c)
		}
		return
	case n.Not != nil:
		fmAssertOrdinaryLeaves(t, *n.Not)
		return
	}
	if n.Property == nil || n.Op == nil {
		t.Fatalf("leaf %s is not a {property, op, value} object", fmRenderNode(n))
	}
	if _, ok := FileProperty(*n.Property); !ok {
		t.Fatalf("leaf names %q, which is not one of the twelve filterable file properties", *n.Property)
	}
	if !isKnownOperator(Operator(*n.Op)) {
		t.Fatalf("leaf uses operator %q, which is outside FR-022b's closed ten", string(*n.Op))
	}
}

// fmMatchNode is a TEST-LOCAL tree composer. It owns nothing but and/or/not:
// every leaf is delegated to the real Filter/Comparator path, which is the half
// the assertion is about. Composition itself belongs to the query engine and is
// not reimplemented here beyond what a two-leaf `any` needs.
func fmMatchNode(n generated.VaultFilterNode, schema *Schema, m FileMeta) (bool, error) {
	switch {
	case n.All != nil:
		for _, c := range *n.All {
			ok, err := fmMatchNode(c, schema, m)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	case n.Any != nil:
		for _, c := range *n.Any {
			ok, err := fmMatchNode(c, schema, m)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case n.Not != nil:
		ok, err := fmMatchNode(*n.Not, schema, m)
		return !ok, err
	}

	pv, err := ResolveFileProperty(*n.Property, m)
	if err != nil {
		return false, err
	}
	f := Filter{Property: *n.Property, Op: Operator(*n.Op)}
	if n.Value != nil {
		f.Literal = *n.Value
		f.LiteralGiven = true
	}
	if n.Values != nil {
		f.Literals = *n.Values
	}
	res, err := f.MatchValue(Comparator{}, schema, pv)
	if err != nil {
		return false, err
	}
	if len(res.ComparisonProblems) != 0 {
		return false, errors.New("comparator could not evaluate: " + res.ComparisonProblems[0].String())
	}
	return res.Matched, nil
}

func TestFileMeta_MethodsTranslateToOrdinaryLeaves(t *testing.T) {
	// The three shapes, written out from FR-134's normative text:
	//
	//   file.hasTag(x)    => {any: [{file.tags,   =, x}, {file.tags,   LIKE, x/%}]}
	//   file.inFolder(x)  => {any: [{file.folder, =, x}, {file.folder, LIKE, x/%}]}
	//   file.hasLink(x)   => {file.links, =, x}
	cases := []struct {
		method FileMethod
		arg    string
		want   string
	}{
		{FileMethodHasTag, "clients", `{any: [{file.tags = "clients"}, {file.tags LIKE "clients/%"}]}`},
		{FileMethodInFolder, "Clients", `{any: [{file.folder = "Clients"}, {file.folder LIKE "Clients/%"}]}`},
		{FileMethodHasLink, "Acme", `{file.links = "Acme"}`},
	}

	for _, tc := range cases {
		t.Run(string(tc.method), func(t *testing.T) {
			node, err := TranslateFileMethod(tc.method, tc.arg)
			if err != nil {
				t.Fatalf("TranslateFileMethod(%s, %q): %v", tc.method, tc.arg, err)
			}
			got := fmRenderNode(node)
			t.Logf("file.%s(%q) => %s", tc.method, tc.arg, got)
			if got != tc.want {
				t.Fatalf("translated tree = %s, want %s", got, tc.want)
			}
			fmAssertOrdinaryLeaves(t, node)
		})
	}

	// The methods are not merely SHAPED like filters — the leaves evaluate
	// through the real Filter/Comparator path and give the hierarchy-aware
	// answer FR-134 promises.
	schema := FilePropertySchema()
	tagNode, err := TranslateFileMethod(FileMethodHasTag, "clients")
	if err != nil {
		t.Fatalf("hasTag: %v", err)
	}
	folderNode, err := TranslateFileMethod(FileMethodInFolder, "Clients")
	if err != nil {
		t.Fatalf("inFolder: %v", err)
	}
	linkNode, err := TranslateFileMethod(FileMethodHasLink, "People/Jane Roe")
	if err != nil {
		t.Fatalf("hasLink: %v", err)
	}

	inScope := fmFixture() // Clients/Acme Corp.md, tags clients/active + priority
	outOfScope := FileMeta{
		Path:             "Archive/Old Note.md",
		Tags:             []string{"clientsofmine"}, // NOT under clients/ — a prefix match without the "/" would wrongly include it
		BacklinksDerived: true,
	}

	evalCases := []struct {
		desc string
		node generated.VaultFilterNode
		meta FileMeta
		want bool
	}{
		{"hasTag matches a sub-tag", tagNode, inScope, true},
		{"hasTag does not match a tag that merely starts with the text", tagNode, outOfScope, false},
		{"inFolder matches a note in the folder", folderNode, inScope, true},
		{"inFolder does not match another folder", folderNode, outOfScope, false},
		{"hasLink matches an outgoing link", linkNode, inScope, true},
		{"hasLink does not match a note without it", linkNode, outOfScope, false},
	}
	for _, tc := range evalCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, merr := fmMatchNode(tc.node, schema, tc.meta)
			if merr != nil {
				t.Fatalf("fmMatchNode: %v", merr)
			}
			if got != tc.want {
				t.Fatalf("matched = %v, want %v", got, tc.want)
			}
		})
	}

	// The exact tag, not only its children.
	exact := FileMeta{Path: "n.md", Tags: []string{"clients"}, BacklinksDerived: true}
	got, err := fmMatchNode(tagNode, schema, exact)
	if err != nil {
		t.Fatalf("fmMatchNode: %v", err)
	}
	if !got {
		t.Fatal("hasTag(\"clients\") did not match a note tagged exactly clients")
	}
}

func TestFileMeta_LikeOperandIsEscaped(t *testing.T) {
	// FR-134's parenthetical: "pattern escaped". A folder literally named
	// "Q1_2026" must not become a pattern matching "Q1x2026".
	node, err := TranslateFileMethod(FileMethodInFolder, "Q1_2026")
	if err != nil {
		t.Fatalf("inFolder: %v", err)
	}
	t.Logf(`file.inFolder("Q1_2026") => %s`, fmRenderNode(node))
	want := `{any: [{file.folder = "Q1_2026"}, {file.folder LIKE "Q1\_2026/%"}]}`
	if got := fmRenderNode(node); got != want {
		t.Fatalf("translated tree = %s, want %s", got, want)
	}

	schema := FilePropertySchema()
	decoy := FileMeta{Path: "Q1x2026/note.md", BacklinksDerived: true}
	matched, err := fmMatchNode(node, schema, decoy)
	if err != nil {
		t.Fatalf("fmMatchNode: %v", err)
	}
	if matched {
		t.Fatal(`inFolder("Q1_2026") matched a note in "Q1x2026" — the underscore reached LIKE unescaped`)
	}
	inside := FileMeta{Path: "Q1_2026/note.md", BacklinksDerived: true}
	matched, err = fmMatchNode(node, schema, inside)
	if err != nil {
		t.Fatalf("fmMatchNode: %v", err)
	}
	if !matched {
		t.Fatal(`inFolder("Q1_2026") did not match a note in "Q1_2026" — the escape broke the real match`)
	}
}

func TestFileMeta_MethodTranslationRefusals(t *testing.T) {
	cases := []struct {
		desc      string
		method    FileMethod
		arg       string
		wantWords []string
	}{
		{"asLink is presentation", FileMethodAsLink, "", []string{"presentation"}},
		{"asLink with an argument is still presentation", FileMethodAsLink, "x", []string{"presentation"}},
		{"an unknown method is refused by name", FileMethod("contains"), "x", []string{"not a file method"}},
		{"an empty argument is refused", FileMethodInFolder, "  ", []string{"empty argument"}},
		{"the vault root is refused", FileMethodInFolder, "/", []string{"vault root"}},
		{"a bare hash names no tag", FileMethodHasTag, "#", []string{"names no tag"}},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			node, err := TranslateFileMethod(tc.method, tc.arg)
			if err == nil {
				t.Fatalf("translated to %s; a refusal was required", fmRenderNode(node))
			}
			var qe *QueryError
			if !errors.As(err, &qe) {
				t.Fatalf("error is %T, want *QueryError", err)
			}
			for _, w := range tc.wantWords {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("refusal %q does not contain %q", err.Error(), w)
				}
			}
			if qe.Remedy == "" {
				t.Fatal("refusal carries no remedy")
			}
		})
	}

	// asLink is one of the four and must be NAMED as such, not reported missing.
	_, err := TranslateFileMethod(FileMethodAsLink, "")
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("error is %T", err)
	}
	var listed bool
	for _, n := range qe.ValidNames {
		if n == "file.asLink()" {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("the asLink refusal does not list asLink among the four methods: %v", qe.ValidNames)
	}

	// hasTag accepts the "#tag" spelling and strips exactly one hash.
	node, err := TranslateFileMethod(FileMethodHasTag, "#clients")
	if err != nil {
		t.Fatalf("hasTag(#clients): %v", err)
	}
	if got, want := fmRenderNode(node), `{any: [{file.tags = "clients"}, {file.tags LIKE "clients/%"}]}`; got != want {
		t.Fatalf("hasTag(#clients) = %s, want %s", got, want)
	}
}

// AsLink is reachable only as presentation — there is no property name that
// resolves to it, which is what makes "presentation never compares" structural.
func TestFileMeta_AsLinkIsPresentationOnly(t *testing.T) {
	m := fmFixture()
	link := m.AsLink()
	if link.Target != "Clients/Acme Corp.md" || link.Display != "Acme Corp" {
		t.Fatalf("AsLink = %+v, want target the path and display the name", link)
	}
	for _, name := range FilePropertyNames {
		pv, err := ResolveFileProperty(name, m)
		if err != nil {
			continue
		}
		for _, v := range pv.Values {
			if v.Text == link.Raw {
				t.Fatalf("%s resolves to the asLink rendering %q — a presentation value reached a comparison", name, link.Raw)
			}
		}
	}
}

func TestFileMeta_SchemaExposesTwelveNotThirteen(t *testing.T) {
	s := FilePropertySchema()
	if len(s.PropertyNames()) != 12 {
		t.Fatalf("FilePropertySchema declares %d properties: %v", len(s.PropertyNames()), s.PropertyNames())
	}
	if _, ok := s.Property(FileSelfProp); ok {
		t.Fatal("FilePropertySchema declares file.file — Filter.Validate would then accept it")
	}
	// FR-024's refusal must list the twelve back.
	_, err := Filter{Property: "file.modified", Op: OpEqual, Literal: "x", LiteralGiven: true}.Prepare(s)
	if err == nil {
		t.Fatal("a misspelled file property validated")
	}
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("error is %T, want *QueryError", err)
	}
	got := append([]string(nil), qe.ValidNames...)
	sort.Strings(got)
	want := append([]string(nil), FileFilterablePropertyNames...)
	sort.Strings(want)
	if !fmEqualStrings(got, want) {
		t.Fatalf("refusal lists %v, want the twelve %v", got, want)
	}

	// Two callers must not be able to mutate one another's declarations.
	if FilePropertySchema() == s {
		t.Fatal("FilePropertySchema returns one shared *Schema")
	}
}

func TestFileMeta_NamespaceRefusalsDistinguishTypoFromForeignName(t *testing.T) {
	m := fmFixture()

	_, err := ResolveFileProperty("file.modified", m)
	if err == nil || !strings.Contains(err.Error(), "is not a file property") {
		t.Fatalf("a misspelled file property gave %v", err)
	}
	_, err = ResolveFileProperty("status", m)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("a schema property routed into the file layer gave %v", err)
	}
	if !IsFileNamespace("file.modified") {
		t.Fatal("IsFileNamespace missed a misspelling inside the namespace")
	}
	if IsFileProperty("file.modified") {
		t.Fatal("IsFileProperty accepted a misspelling")
	}
}

func TestFileMeta_PathDerivationsAreVaultPathsNotOSPaths(t *testing.T) {
	cases := []struct {
		path                     string
		name, folder, ext, clean string
	}{
		{"Clients/Acme Corp.md", "Acme Corp", "Clients", "md", "Clients/Acme Corp.md"},
		{"Inbox.md", "Inbox", "", "md", "Inbox.md"},
		{"./Clients/Acme.md", "Acme", "Clients", "md", "Clients/Acme.md"},
		{"/Clients/Acme.md", "Acme", "Clients", "md", "Clients/Acme.md"},
		{`Clients\Acme.md`, "Acme", "Clients", "md", "Clients/Acme.md"},
		{"a/b/c/note.tar.gz", "note.tar", "a/b/c", "gz", "a/b/c/note.tar.gz"},
		{"README", "README", "", "", "README"},
	}
	for _, tc := range cases {
		m := FileMeta{Path: tc.path}
		if got := m.FileName(); got != tc.name {
			t.Errorf("%q: name = %q, want %q", tc.path, got, tc.name)
		}
		if got := m.FileFolder(); got != tc.folder {
			t.Errorf("%q: folder = %q, want %q", tc.path, got, tc.folder)
		}
		if got := m.FileExt(); got != tc.ext {
			t.Errorf("%q: ext = %q, want %q", tc.path, got, tc.ext)
		}
		pv := fmMustResolve(t, FilePathProp, m)
		if got := fmTexts(pv); !fmEqualStrings(got, []string{tc.clean}) {
			t.Errorf("%q: path = %q, want %q", tc.path, got, tc.clean)
		}
	}
}

func TestFileMeta_UnknownStatIsAbsentNotZero(t *testing.T) {
	m := FileMeta{Path: "n.md", BacklinksDerived: true}
	for _, name := range []string{FileMtimeProp, FileCtimeProp, FileSizeProp} {
		pv := fmMustResolve(t, name, m)
		if pv.State != StateAbsent {
			t.Fatalf("%s with no indexed stat = %v, want absent", name, pv.State)
		}
	}
	// A genuinely zero-byte file is a VALUE, not absence — the flag is what
	// separates them, and a test that only checked the unknown side would pass
	// for an implementation that reported every zero as absent.
	m.SizeKnown = true
	pv := fmMustResolve(t, FileSizeProp, m)
	if pv.State != StatePresent || pv.Values[0].Number.String() != "0" {
		t.Fatalf("a zero-byte file resolved to %+v, want a present 0", pv)
	}
}

func TestFileMeta_SplitLinkRowsPartitionsByEmbed(t *testing.T) {
	rows := []FileLinkRow{
		{NotePath: "a.md", Target: "X", Raw: "[[X]]"},
		{NotePath: "a.md", Target: "Y", Raw: "![[Y]]", Embed: true},
		{NotePath: "a.md", Target: "Z", Display: "zed", Raw: "[[Z|zed]]"},
	}
	links, embeds := SplitLinkRows(rows)
	if len(links) != 2 || links[0].Target != "X" || links[1].Target != "Z" {
		t.Fatalf("links = %+v", links)
	}
	if len(embeds) != 1 || embeds[0].Target != "Y" {
		t.Fatalf("embeds = %+v", embeds)
	}
	if links[1].Display != "zed" {
		t.Fatalf("display text was dropped: %+v", links[1])
	}
}
