// Omnipus — ADR-068 D15.3 / spec §4.1.1: vault_describe, the mandatory cheap
// first call. THE LOGIC HALF — the tool ADAPTER is in tools.go.
//
// THE SPLIT IS ARCHITECTURAL, NOT COSMETIC, and there is a guard on it.
// TestKnowledge_NoLanguageModelInTheGraphPath (links_test.go) asserts that
// `pkg/tools` — the only import in this package whose own transitive closure
// reaches a language-model client — appears in the TOOL-ADAPTER files and
// nowhere else. pkg/knowledge is the indexing, link-resolution and note-
// rewriting path, and FR-045 is that nothing on that path can reach a model.
//
// So this file holds the describe and render logic and imports no `pkg/tools`;
// the tools.Tool implementation lives in tools.go beside the other adapters.
// Do not merge them back: the guard's allow-list is a deliberate literal, and
// re-adding the import here fails the build with the reason attached.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS TOOL EXISTS, IN ONE MEASUREMENT
//
// "Which of my deals are stuck?" costs ~11 tool calls across three tool
// families today, several of them outside the audited knowledge boundary. The
// FIRST of those calls is a coin flip on the collection name, because the only
// way to learn the valid names is to get one wrong and read the failure. And
// ListTemplates has existed in pkg/knowledge/template.go, complete and tested,
// with ZERO CALLERS — so an agent guesses a template name and finds out it was
// wrong by being refused.
//
// vault_describe is the answer to all of that: one cheap call that says what
// this vault actually contains, so nothing after it is a guess.
//
// COMPACT TEXT, NEVER JSON (FR-072). This is not a style preference. Notion
// measured a ~91% context-token reduction moving their AI surface from JSON
// schema objects to a compact textual schema, and this response is read by a
// model at the start of every session that touches a vault. The renderer below
// is deliberately the ONLY place the response shape is decided, so the saving
// cannot be given back one handler at a time.
//
// SAVED VIEWS ARE A FIRST-CLASS SECTION, and that is the point of them being
// here at all: an agent's opening move should be "is there already a view for
// this?" rather than "let me invent a filter". A view that exists and is never
// found is a view nobody wrote.
// ---------------------------------------------------------------------------

// PropertiesIndexFileName is the derived properties index inside a
// collection's index directory — beside the bleve index and the manifest,
// under $OMNIPUS_HOME rather than inside the operator's vault (FR-030).
//
// It is named here, once, because three tools need to open the same file and
// three spellings of a path is three databases.
const PropertiesIndexFileName = "properties.db"

// PropertiesIndexPath returns the properties index path for a collection root.
func PropertiesIndexPath(home, collectionRoot string) (string, error) {
	dir, err := IndexDirFor(home, collectionRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PropertiesIndexFileName), nil
}

// ---------------------------------------------------------------------------
// Sections
// ---------------------------------------------------------------------------

// Describe sections, as the model spells them in `include`.
const (
	DescribeSectionIndex     = "index"
	DescribeSectionTypes     = "types"
	DescribeSectionViews     = "views"
	DescribeSectionTemplates = "templates"
)

// describeSectionOrder is the order sections RENDER in, which spec §4.1.1
// states normatively: index freshness -> collections -> record types -> saved
// views -> templates -> integrity findings.
//
// It is a list rather than a hardcoded sequence of calls so the order is one
// edit, and so a test can assert the rendered order against this list rather
// than against a transcription of it.
var describeSectionOrder = []string{
	DescribeSectionIndex,
	DescribeSectionTypes,
	DescribeSectionViews,
	DescribeSectionTemplates,
}

// Detail levels.
const (
	DetailStandard = "standard"
	DetailMinimal  = "minimal"
)

// ---------------------------------------------------------------------------
// The data the renderer projects
// ---------------------------------------------------------------------------

// DescribeData is everything one vault_describe response is rendered from.
//
// It is the projection source FR-092 describes: the compact text the model
// reads is a rendering of this, and nothing in the renderer reaches past it to
// the filesystem. That is what makes RenderDescribe testable without a vault.
type DescribeData struct {
	Collection         string
	CollectionsInScope []string

	// IndexProgress is the live build state; ManifestCount is how many
	// documents the last completed index left behind. Both are needed: a
	// count with no phase reads as complete during a first index, which is
	// the US-6 failure this package already refuses everywhere else.
	IndexProgress IndexProgress
	ManifestCount int
	ManifestKnown bool
	// NotesOnDisk is meaningful only when a walk actually happened (the
	// integrity sweep does one). Zero with NotesCounted false means "not
	// counted", never "none".
	NotesOnDisk  int
	NotesCounted bool

	Schemas      *records.SchemaSet
	SchemaReport *records.SchemaLoadReport
	// OnlyType, when set, restricts the TYPES section to one declared type.
	// The set itself is not subsetted: a rejection report about a different
	// type is still the truth about this vault and is not hidden by a
	// narrowing the caller applied to the description.
	OnlyType string

	Views      *records.ViewSet
	ViewReport *records.ViewLoadReport

	Templates    []TemplateInfo
	TemplatesDir string

	Integrity *IntegrityReport

	// Sections is which of describeSectionOrder to render.
	Sections map[string]bool
	Detail   string
}

// ---------------------------------------------------------------------------
// The renderer
// ---------------------------------------------------------------------------

// RenderDescribe renders one response as compact text.
//
// FR-072: no JSON document is emitted here, and none may be. The one place a
// brace could legitimately appear is a template placeholder token, which is
// literal text an operator typed.
func RenderDescribe(d DescribeData) string {
	var b strings.Builder
	detail := d.Detail
	if detail != DetailMinimal {
		detail = DetailStandard
	}
	sections := d.Sections
	if sections == nil {
		sections = allDescribeSections()
	}

	for _, section := range describeSectionOrder {
		if !sections[section] {
			continue
		}
		switch section {
		case DescribeSectionIndex:
			renderIndexAndCollections(&b, d)
		case DescribeSectionTypes:
			renderTypes(&b, d, detail)
		case DescribeSectionViews:
			renderViews(&b, d)
		case DescribeSectionTemplates:
			renderTemplates(&b, d)
		}
	}
	if d.Integrity != nil {
		renderIntegrity(&b, d.Integrity)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func allDescribeSections() map[string]bool {
	out := make(map[string]bool, len(describeSectionOrder))
	for _, s := range describeSectionOrder {
		out[s] = true
	}
	return out
}

func renderIndexAndCollections(b *strings.Builder, d DescribeData) {
	fmt.Fprintf(b, "VAULT %s — %s\n", nonEmpty(d.Collection, "(unnamed)"), indexFreshness(d))
	if len(d.CollectionsInScope) > 0 {
		fmt.Fprintf(b, "COLLECTIONS in scope (%d): %s\n",
			len(d.CollectionsInScope), strings.Join(d.CollectionsInScope, ", "))
	}
}

// indexFreshness states what the index holds, and refuses to state a ratio it
// does not have. IndexProgress.Ratio returns ok=false whenever the total is
// unknown or zero, which is why "0 of 0" cannot be produced from here.
func indexFreshness(d DescribeData) string {
	p := d.IndexProgress
	// An UNSET phase is not a phase. IndexProgress.InFlight compares against
	// IndexPhaseIdle ("idle"), so a zero-valued IndexProgress — one nobody
	// populated — reports InFlight()==true and would render this vault as
	// "INDEXING" forever. That is a caveat attached to a correct answer, which
	// is the harmless direction, but it is still a statement nobody checked:
	// it would appear on every response from a host that has not wired a
	// progress tracker, and a warning that is always on is a warning nobody
	// reads.
	if p.Phase != "" && p.InFlight() {
		if done, total, ok := p.Ratio(); ok {
			return fmt.Sprintf("INDEXING, %s of %s notes — anything below is a fraction of this vault",
				group(done), group(total))
		}
		return "INDEXING (total not yet known) — anything below is a fraction of this vault"
	}
	switch {
	case !d.ManifestKnown:
		return "NOT INDEXED yet — nothing has been indexed for this collection"
	case d.NotesCounted && d.NotesOnDisk != d.ManifestCount:
		return fmt.Sprintf("index holds %s notes, %s on disk — the two disagree; re-index to reconcile",
			group(d.ManifestCount), group(d.NotesOnDisk))
	case d.ManifestCount == 0:
		return "indexed and empty — this collection holds no indexable notes"
	default:
		return fmt.Sprintf("index holds %s notes", group(d.ManifestCount))
	}
}

func renderTypes(b *strings.Builder, d DescribeData, detail string) {
	types := d.Schemas.Types()
	if d.OnlyType != "" {
		types = []string{d.OnlyType}
	}
	if len(types) == 0 {
		b.WriteString("TYPES (0) — this vault declares no record types; every note in it is an ordinary note\n")
	} else {
		fmt.Fprintf(b, "TYPES (%d)\n", len(types))
	}
	for _, name := range types {
		sc, ok := d.Schemas.Get(name)
		if !ok {
			continue
		}
		head := "  " + sc.Type
		if p := sc.Identity.Prefix; p != "" {
			// FR-036b — the prefix is SCHEMA DATA and is rendered as
			// declared. It is never derived from the type name; a schema that
			// declares none mints the counter alone, and this line says so by
			// simply not appearing.
			head += " id " + p + "-<n>"
		}
		if sc.Label != "" && sc.Label != sc.Type {
			head += " " + quoteDisplay(sc.Label)
		}
		b.WriteString(head + "\n")
		renderProperties(b, sc, detail)
	}
	renderSchemaRejections(b, d.SchemaReport)
}

func renderProperties(b *strings.Builder, sc *records.Schema, detail string) {
	names := sc.PropertyNames()
	width := 0
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}
	for _, n := range names {
		p, ok := sc.Property(n)
		if !ok {
			continue
		}
		// FR-004's `integer` and `decimal` are rendered DISTINCTLY. They are
		// one comparison domain (R-1) and two storage decisions, and a reader
		// choosing a property to filter on needs to see which they have.
		kind := string(p.Type)
		if p.Many {
			kind += " many"
		}
		if p.Required {
			kind += " required"
		}
		if p.Unit != "" {
			kind += " in " + p.Unit
		}
		if (p.Type == records.TypeRelation || p.Type == records.TypePerson) && p.To != "" {
			kind += " -> " + p.To
		}
		line := fmt.Sprintf("    %-*s  %s", width, n, kind)
		if p.Type == records.TypeEnum && detail != DetailMinimal {
			// The declared set is UNORDERED and a reader must not infer a
			// sort order from this sequence — R-5 sorts lexically over the
			// folded value. It is rendered in DECLARATION order so the
			// operator sees their own file back (FR-011c).
			line += ": " + strings.Join(p.PermittedValues(), " | ")
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
}

func renderSchemaRejections(b *strings.Builder, r *records.SchemaLoadReport) {
	if r == nil || len(r.Rejections) == 0 {
		return
	}
	fmt.Fprintf(b, "  %d schema file(s) REJECTED and enforcing nothing:\n", len(r.Rejections))
	for _, rej := range r.Rejections {
		fmt.Fprintf(b, "    %s\n", rej.String())
	}
}

func renderViews(b *strings.Builder, d DescribeData) {
	views := d.Views.Views()
	if len(views) == 0 {
		b.WriteString("VIEWS (0) — no saved views; a query here starts from scratch\n")
	} else {
		fmt.Fprintf(b, "VIEWS (%d) — ask for one by name before inventing a filter\n", len(views))
	}
	for _, v := range views {
		head := fmt.Sprintf("  %s  type %s", v.Name(), v.Def.Type)
		if lbl := v.DisplayLabel(); lbl != v.Name() {
			head += " " + quoteDisplay(lbl)
		}
		b.WriteString(head + "\n")
		if body := renderViewBody(v); body != "" {
			b.WriteString(body)
		}
	}
	renderViewRejections(b, d.ViewReport)
}

// renderViewBody renders a view's query in one line, so a reader can tell two
// views apart without opening either file.
//
// The filter operator is rendered as the OPAQUE STRING the contract carries.
// This renderer deliberately knows nothing about which operators exist: the
// operator vocabulary is being replaced with SQL's, and a renderer that
// enumerated the old set would have to be found and changed again.
func renderViewBody(v *records.SavedView) string {
	var parts []string
	if v.Def.Filters != nil && len(*v.Def.Filters) > 0 {
		clauses := make([]string, 0, len(*v.Def.Filters))
		for _, f := range *v.Def.Filters {
			clause := f.Property + " " + string(f.Op)
			if lit := renderFilterLiterals(records.ViewFilterLiterals(f)); lit != "" {
				clause += " " + lit
			}
			if f.Negate != nil && *f.Negate {
				clause = "NOT(" + clause + ")"
			}
			if f.Via != nil && len(*f.Via) > 0 {
				clause = strings.Join(*f.Via, "->") + "->" + clause
			}
			clauses = append(clauses, clause)
		}
		parts = append(parts, "filter "+strings.Join(clauses, " AND "))
	}
	if v.Def.GroupBy != nil && len(*v.Def.GroupBy) > 0 {
		parts = append(parts, "group "+strings.Join(*v.Def.GroupBy, ", "))
	}
	if v.Def.Sort != nil && len(*v.Def.Sort) > 0 {
		keys := make([]string, 0, len(*v.Def.Sort))
		for _, s := range *v.Def.Sort {
			keys = append(keys, s.Property+" "+string(s.Direction))
		}
		parts = append(parts, "sort "+strings.Join(keys, ", "))
	}
	if v.Def.Properties != nil && len(*v.Def.Properties) > 0 {
		parts = append(parts, "show "+strings.Join(*v.Def.Properties, ", "))
	}
	if v.Def.Aggregates != nil && len(*v.Def.Aggregates) > 0 {
		aggs := make([]string, 0, len(*v.Def.Aggregates))
		for _, a := range *v.Def.Aggregates {
			s := string(a.Op)
			if a.Property != nil && *a.Property != "" {
				s += "(" + *a.Property + ")"
			}
			aggs = append(aggs, s)
		}
		parts = append(parts, "totals "+strings.Join(aggs, ", "))
	}
	if v.Def.Limit != nil {
		parts = append(parts, fmt.Sprintf("limit %d", *v.Def.Limit))
	}
	if v.Def.Untranslated != nil && len(*v.Def.Untranslated) > 0 {
		// FR-101 — an imported expression nobody could translate is preserved
		// verbatim and REPORTED. An approximation that looks like a
		// translation is worse than an honest gap, because nobody reviews a
		// filter that appears to have imported cleanly.
		parts = append(parts, fmt.Sprintf("%d expression(s) from %s NOT translated and NOT applied",
			len(*v.Def.Untranslated), nonEmpty(derefString(v.Def.Source), "the imported file")))
	}
	if len(parts) == 0 {
		return "    every record of this type, every property\n"
	}
	return "    " + strings.Join(parts, "; ") + "\n"
}

func renderFilterLiterals(out []string) string {
	if len(out) == 0 {
		return ""
	}
	if len(out) == 1 {
		return out[0]
	}
	return "(" + strings.Join(out, ", ") + ")"
}

func renderViewRejections(b *strings.Builder, r *records.ViewLoadReport) {
	if r == nil || len(r.Rejections) == 0 {
		return
	}
	fmt.Fprintf(b, "  %d saved view(s) REJECTED and unusable:\n", len(r.Rejections))
	for _, rej := range r.Rejections {
		fmt.Fprintf(b, "    %s\n", rej.String())
	}
}

func renderTemplates(b *strings.Builder, d DescribeData) {
	// FR-047a — the location and the closed token set are stated, because an
	// agent that has to guess a template name finds out it guessed wrong by
	// being refused.
	tokens := strings.Join(TemplatePlaceholders(), " ")
	if len(d.Templates) == 0 {
		fmt.Fprintf(b, "TEMPLATES (0) in %s — none yet; a new note starts empty\n",
			shortTemplatesDir(d.TemplatesDir))
		return
	}
	fmt.Fprintf(b, "TEMPLATES (%d) in %s, tokens %s\n",
		len(d.Templates), shortTemplatesDir(d.TemplatesDir), tokens)
	for _, t := range d.Templates {
		fmt.Fprintf(b, "  %s  %dB\n", t.Name, t.Size)
	}
}

// shortTemplatesDir renders the templates directory relative to the vault,
// because the absolute path is long, changes per machine, and is not what the
// caller passes back.
func shortTemplatesDir(abs string) string {
	if abs == "" {
		return MarkerDirName + "/" + DefaultTemplatesDirName + "/"
	}
	if i := strings.Index(abs, MarkerDirName); i >= 0 {
		return filepath.ToSlash(abs[i:]) + "/"
	}
	return filepath.ToSlash(abs) + "/"
}

func renderIntegrity(b *strings.Builder, r *IntegrityReport) {
	ran, notRun := 0, 0
	for _, c := range r.Categories {
		if c.NotRun != "" {
			notRun++
			continue
		}
		ran += c.Total
	}
	header := fmt.Sprintf("INTEGRITY: %s finding(s)", group(ran))
	if notRun > 0 {
		header += fmt.Sprintf(", %d categories NOT CHECKED", notRun)
	}
	fmt.Fprintf(b, "%s (scope: %s, %s notes swept)\n", header, r.ScopeLabel, group(r.NotesSwept))

	width := 0
	for _, c := range IntegrityCategories {
		if len(c) > width {
			width = len(c)
		}
	}

	// AC-D6 — the categories that could not run are named FIRST and by name,
	// and they report the reason INSTEAD of zero. "0 findings" and "not
	// checked" are opposite verdicts and must never render the same.
	var blocked []string
	reason := ""
	for _, c := range r.Categories {
		if c.NotRun != "" {
			blocked = append(blocked, string(c.Category))
			reason = c.NotRun
		}
	}
	if len(blocked) > 0 {
		fmt.Fprintf(b, "  NOT CHECKED: %s\n", strings.Join(blocked, ", "))
		fmt.Fprintf(b, "    %s\n", reason)
	}

	for _, c := range r.Categories {
		if c.NotRun != "" {
			continue
		}
		for _, f := range c.Findings {
			fmt.Fprintf(b, "  %-*s  %s\n", width, string(c.Category), f.Detail)
		}
		if c.Clamped() {
			// FR-075a — a clamp that does not name what it hid is a
			// truncation. The would-be count is mandatory and is not itself
			// clampable.
			fmt.Fprintf(b, "  %s: showing %s of %s — narrow with collection=<name> or record_type=<name>\n",
				string(c.Category), group(len(c.Findings)), group(c.Total))
		}
	}
	if ran == 0 && notRun == 0 {
		b.WriteString("  nothing to report\n")
	}
}

// group renders an integer with thousands separators, because these numbers
// are read by a human as often as by a model and "214900" is harder to judge
// against a limit than "214,900".
func group(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// quoteDisplay wraps a human label so it cannot be confused with an
// identifier the caller may pass back.
func quoteDisplay(s string) string { return "'" + s + "'" }
