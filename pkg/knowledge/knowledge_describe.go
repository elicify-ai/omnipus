// Omnipus — ADR-068 D15.3 / spec §4.1.1: knowledge_describe, the mandatory cheap
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
	"reflect"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
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
// knowledge_describe is the answer to all of that: one cheap call that says what
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

// DescribeData is everything one knowledge_describe response is rendered from.
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
	// Built once for the whole listing rather than per view: the loader is a
	// thin wrapper over the set this function already holds, and asking it is
	// what keeps this listing and knowledge_find's own answer in agreement.
	loader := records.NewViewFindLoader(d.Views)
	if len(views) == 0 {
		b.WriteString("VIEWS (0) — no saved views; a query here starts from scratch\n")
	} else {
		fmt.Fprintf(b, "VIEWS (%d) — ask for one by name before inventing a filter\n", len(views))
	}
	for _, v := range views {
		// ViewDef.Type is a *string since FR-018b made `type` optional (an
		// untyped view spans record types). Rendered through %s a nil — or a
		// non-nil — pointer prints an ADDRESS, which is why go vet flags it:
		// an operator reading "type 0xc000123456" learns nothing and cannot
		// tell it from a real type name.
		viewType := "(untyped)"
		if v.Def.Type != nil {
			viewType = *v.Def.Type
		}
		head := fmt.Sprintf("  %s  type %s", v.Name(), viewType)
		if lbl := v.DisplayLabel(); lbl != v.Name() {
			head += " " + quoteDisplay(lbl)
		}
		b.WriteString(head + "\n")
		if body := renderViewBody(v); body != "" {
			b.WriteString(body)
		}
		b.WriteString(renderViewServeRefusal(loader, v))
	}
	renderViewRejections(b, d.ViewReport)
}

// renderViewServeRefusal states, per view, whether knowledge_find will
// actually run it.
//
// THE HEAD LINE ABOVE SAYS "ask for one by name before inventing a filter",
// AND FOR SOME VIEWS THAT INSTRUCTION FAILS. A view declaring `formulas`, or
// a grouping key in DESCENDING order, is written faithfully, listed here in
// full, and then refused by knowledge_find with "no saved view named X" — a
// statement that is false about a view this very listing just showed. The
// refusal itself is right (VaultFindRequest has no field for either, and
// serving the view anyway would answer a broader question than it asks); what
// was missing is that the agent following this listing had no way to know
// before it called.
//
// The reason comes from records.ViewFindLoader — the SAME object knowledge_find
// resolves views through — rather than from a second copy of its rules here.
// view_find_bridge.go's header names this caller explicitly:
// "knowledge_describe and knowledge_configure hold a *ViewFindLoader and can
// ask."
//
// ServeRefusalDisabled is deliberately skipped: renderViewDisabled already
// prints "DISABLED — stored but never applied; this view returns nothing" for
// exactly that view, and saying it twice makes a reader look for two problems.
func renderViewServeRefusal(loader *records.ViewFindLoader, v *records.SavedView) string {
	if loader == nil || v == nil {
		return ""
	}
	refusal, refused := loader.ServeRefusal(v.Name())
	if !refused || refusal.Code == records.ServeRefusalDisabled {
		return ""
	}
	return fmt.Sprintf("    !! NOT SERVABLE by knowledge_find: %s (%s) — %s\n",
		refusal.Reason, refusal.Code, refusal.Remedy)
}

// ---------------------------------------------------------------------------
// RENDERING A SAVED VIEW — AND THE ONE THING THIS RENDERER MAY NEVER DO
//
// A saved view is read by a model that is deciding whether to reuse it. So the
// single worst output this file can produce is not a blank line or an ugly
// one: it is a CONFIDENT WRONG ONE — "every record of this type, every
// property" printed over a view that in fact returns a narrow, filtered slice.
// An agent that reads that believes the view is unconstrained and reasons from
// there, and nothing downstream ever contradicts it.
//
// That is not hypothetical, and the way it happened is the reason for every
// mechanism below. A view's filtering lives in `filter` (a tree) and its
// grouping in `grouping`; this renderer read NEITHER, because it had been
// written against an earlier, flatter set of keys and nobody revisited it when
// the format grew. It produced an EMPTY parts list and printed "every record
// of this type, every property" over a filtered view. Nothing failed. The
// output was well-formed, confident, and wrong.
//
// THE FIX IS STRUCTURAL, NOT A HABIT. Two independent mechanisms, each of
// which catches that failure on its own — because the renderer being CORRECT
// today is exactly what was true before, and it did not help:
//
//  1. A COVERAGE LEDGER. viewHeaderKeys and viewBodyKeys together declare
//     every wire key this file accounts for. Any key the view actually
//     carries that neither list claims is REPORTED in the output as a
//     constraint this description cannot show. A key added to
//     generated.ViewDef in the future and taught to nobody therefore surfaces
//     as an explicit gap rather than as silence — and
//     TestDescribeViews_EveryViewDefKeyIsAccountedFor fails at test time as
//     well, by name. This is the mechanism that would have caught the
//     original bug the day the key was added.
//
//  2. THE UNFILTERED CLAIM IS GUARDED AT ITS SOURCE. The "every record of this
//     type, every property" line is emitted only when the view declares
//     NOTHING beyond its own identity. It is not reachable from "the renderer
//     produced no parts", which is exactly the state the original bug was in.
//
// Describing less than the whole truth is survivable. Describing a filtered
// view as unfiltered is not.
// ---------------------------------------------------------------------------

// viewHeaderKeys are accounted for by renderViews' HEAD line, or are
// provenance that cannot change what a view returns.
//
// `source` is here rather than in a body renderer deliberately: it names the
// file an imported view came from (FR-102) and is quoted by the `untranslated`
// clause when there is a loss to attribute. It is not a constraint, so it does
// not belong in a constraint report.
var viewHeaderKeys = []string{
	"name",
	"type",
	"label",
	"source",
}

// viewBodyKeys is every wire key renderViewClauses knows what to do with.
//
// ADDING A KEY HERE IS A PROMISE THAT renderViewClauses RENDERS IT. Listing a
// key without rendering it re-opens the exact hole this file was written to
// close, and mechanism 2 above cannot see the difference — it only sees that
// somebody claimed the key.
var viewBodyKeys = []string{
	"disabled",
	"filter",
	"grouping",
	"sort",
	"properties",
	"aggregates",
	"limit",
	"layout",
	"formulas",
	"property_config",
	"untranslated",
}

// viewBodyRender accumulates one view's description.
type viewBodyRender struct {
	parts []string
}

func (r *viewBodyRender) add(s string) { r.parts = append(r.parts, s) }

// renderViewBody renders a view's query in one line, so a reader can tell two
// views apart without opening either file.
//
// The filter operator is rendered as the OPAQUE STRING the contract carries.
// This renderer deliberately knows nothing about which operators exist — a
// renderer that enumerated the ten SQL spellings would have to be found and
// changed again the next time the contract grew one.
func renderViewBody(v *records.SavedView) string {
	if v == nil {
		return ""
	}
	r := &viewBodyRender{}

	renderViewClauses(v, r)

	// Mechanism 1 — the coverage ledger. A key this renderer does not claim is
	// REPORTED, never silently dropped.
	if gaps := unaccountedViewKeys(v.Def, viewBodyKeys); len(gaps) > 0 {
		r.add(fmt.Sprintf("!! %d declared key(s) this description cannot show (%s) — treat this view as CONSTRAINED in ways not shown here",
			len(gaps), strings.Join(gaps, ", ")))
	}

	if len(r.parts) == 0 {
		// Mechanism 2 — the unfiltered claim, made only when it is provably
		// true. The test is "this view declares nothing beyond its identity",
		// NOT "the renderer produced no parts": the second is satisfied by a
		// renderer that failed to read the file, which is the whole defect.
		if beyond := populatedViewKeysBeyond(v.Def, viewHeaderKeys); len(beyond) > 0 {
			return fmt.Sprintf("    !! this view CONSTRAINS its result (%s) and this description cannot show how — do NOT treat it as unfiltered\n",
				strings.Join(beyond, ", "))
		}
		return "    every record of this type, every property\n"
	}
	return "    " + strings.Join(r.parts, "; ") + "\n"
}

// renderViewClauses renders the view's query (ADR-068 D24.1, FR-018b): ONE
// `filter` tree of all/any/not over the ten SQL operators, `grouping` keys
// that each carry a direction, plus `layout`, `formulas` and
// `property_config`.
func renderViewClauses(v *records.SavedView, r *viewBodyRender) {
	renderViewDisabled(v, r)
	if v.Def.Filter != nil {
		r.add("filter " + renderViewFilterNode(*v.Def.Filter, 0))
	}
	if v.Def.Grouping != nil && len(*v.Def.Grouping) > 0 {
		keys := make([]string, 0, len(*v.Def.Grouping))
		for _, g := range *v.Def.Grouping {
			// The contract states the omitted direction MEANS asc rather than
			// declaring a JSON Schema default, so the effective order is asc
			// and printing it is reporting what happens, not inventing a
			// declaration. (The LOADER must not fill it in; a description of
			// the resulting behaviour is a different job.)
			dir := string(generated.ViewGroupByDirectionAsc)
			if g.Direction != nil && *g.Direction != "" {
				dir = string(*g.Direction)
			}
			keys = append(keys, g.Property+" "+dir)
		}
		r.add("group " + strings.Join(keys, ", "))
	}
	renderViewSharedTail(v, r)
	if v.Def.Layout != nil {
		layout := string(*v.Def.Layout)
		// FR-109: only `table` and `cards` are drawn. A layout this product
		// cannot draw is NAMED, because the failure that put this field in the
		// contract was an Obsidian cards view importing as a table, recording
		// no loss, and scoring clean.
		if !viewLayoutIsRendered(*v.Def.Layout) {
			layout += " (this product does not draw this layout; it shows as a table)"
		}
		r.add("layout " + layout)
	}
	if v.Def.Formulas != nil && len(*v.Def.Formulas) > 0 {
		names := sortedMapKeys(*v.Def.Formulas)
		out := make([]string, 0, len(names))
		for _, n := range names {
			// Rendered as SOURCE TEXT, which is what is stored (FR-141), so
			// what a reader sees here is directly comparable against the file.
			out = append(out, n+" = "+collapseWhitespace((*v.Def.Formulas)[n]))
		}
		r.add("formula " + strings.Join(out, ", "))
	}
	if v.Def.PropertyConfig != nil && len(*v.Def.PropertyConfig) > 0 {
		names := sortedMapKeys(*v.Def.PropertyConfig)
		out := make([]string, 0, len(names))
		for _, n := range names {
			cfg := (*v.Def.PropertyConfig)[n]
			if cfg.DisplayName != nil && *cfg.DisplayName != "" {
				out = append(out, n+" as "+quoteDisplay(*cfg.DisplayName))
				continue
			}
			out = append(out, n)
		}
		r.add("display " + strings.Join(out, ", "))
	}
	renderViewUntranslated(v, r)
}

// renderViewSharedTail renders the four keys that describe how the matched
// rows are PRESENTED rather than which rows they are — `sort`, `properties`,
// `aggregates`, `limit`.
//
// They are split out from renderViewClauses because they answer a different
// question, and a reader scanning for "what does this view actually return"
// should be able to stop before them.
func renderViewSharedTail(v *records.SavedView, r *viewBodyRender) {
	if v.Def.Sort != nil && len(*v.Def.Sort) > 0 {
		keys := make([]string, 0, len(*v.Def.Sort))
		for _, s := range *v.Def.Sort {
			keys = append(keys, s.Property+" "+string(s.Direction))
		}
		r.add("sort " + strings.Join(keys, ", "))
	}
	if v.Def.Properties != nil && len(*v.Def.Properties) > 0 {
		r.add("show " + strings.Join(*v.Def.Properties, ", "))
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
		r.add("totals " + strings.Join(aggs, ", "))
	}
	if v.Def.Limit != nil {
		r.add(fmt.Sprintf("limit %d", *v.Def.Limit))
	}
}

// renderViewDisabled reports FR-105's kill switch, which this renderer used to
// drop in silence.
//
// A disabled view is stored and REFUSED at serve time, so an agent that picks
// one off this list has chosen a view that can never answer. That refusal is
// at least loud when it arrives; the description that led the agent there was
// not. It fires only on a view that actually sets the flag.
func renderViewDisabled(v *records.SavedView, r *viewBodyRender) {
	if v.Def.Disabled != nil && *v.Def.Disabled {
		r.add("DISABLED — stored but never applied; this view returns nothing")
	}
}

// renderViewUntranslated reports FR-101's preserved-but-unapplied expressions.
func renderViewUntranslated(v *records.SavedView, r *viewBodyRender) {
	if v.Def.Untranslated != nil && len(*v.Def.Untranslated) > 0 {
		// FR-101 — an imported expression nobody could translate is preserved
		// verbatim and REPORTED. An approximation that looks like a
		// translation is worse than an honest gap, because nobody reviews a
		// filter that appears to have imported cleanly.
		r.add(fmt.Sprintf("%d expression(s) from %s NOT translated and NOT applied",
			len(*v.Def.Untranslated), nonEmpty(derefString(v.Def.Source), "the imported file")))
	}
}

// renderViewFilterNode renders one filter node as infix text a human
// and a model read the same way: `(a AND b)`, `(a OR b)`, `NOT(a)`.
//
// EVERY SHAPE PRODUCES TEXT, including a malformed one. A node that is neither
// a leaf nor a combinator renders as a named placeholder rather than as an
// empty string, because two different filters that render identically — or a
// filter that renders as nothing — is the failure mode this whole file is
// written against.
func renderViewFilterNode(n generated.VaultFilterNode, depth int) string {
	// The loader bounds a stored tree at FR-023c's depth 8. This cap is not
	// that bound restated; it is a guard against a hand-built cyclic tree
	// (`Not` is a pointer) turning a description into a stack overflow.
	const maxRenderDepth = 32
	if depth > maxRenderDepth {
		return "<filtering nested deeper than this description renders>"
	}
	switch {
	case n.All != nil:
		return renderViewFilterChildren(*n.All, " AND ", depth)
	case n.Any != nil:
		return renderViewFilterChildren(*n.Any, " OR ", depth)
	case n.Not != nil:
		return "NOT(" + renderViewFilterNode(*n.Not, depth+1) + ")"
	case n.Property != nil || n.Op != nil:
		return renderViewFilterLeaf(n)
	default:
		return "<unreadable filter node>"
	}
}

func renderViewFilterChildren(children []generated.VaultFilterNode, joiner string, depth int) string {
	if len(children) == 0 {
		// An empty combinator is not "no filtering" — it is a node whose
		// meaning this description cannot state, and saying so is the only
		// safe rendering.
		return "<empty filter group>"
	}
	out := make([]string, 0, len(children))
	for _, c := range children {
		out = append(out, renderViewFilterNode(c, depth+1))
	}
	if len(out) == 1 {
		return out[0]
	}
	return "(" + strings.Join(out, joiner) + ")"
}

func renderViewFilterLeaf(n generated.VaultFilterNode) string {
	prop := "<no property>"
	if n.Property != nil && *n.Property != "" {
		prop = *n.Property
	}
	op := "<no operator>"
	if n.Op != nil && *n.Op != "" {
		op = string(*n.Op)
	}
	leaf := prop + " " + op
	switch {
	case n.Values != nil && len(*n.Values) > 0:
		leaf += " " + renderFilterLiterals(*n.Values)
	case n.Value != nil:
		leaf += " " + *n.Value
	}
	return leaf
}

// viewLayoutIsRendered reports whether the SPA draws this layout. FR-109
// declares six and draws two; the other four exist so an import can RECORD
// what the original asked for.
func viewLayoutIsRendered(l generated.ViewDefLayout) bool {
	return l == generated.ViewDefLayoutTable || l == generated.ViewDefLayoutCards
}

// ---------------------------------------------------------------------------
// The coverage ledger — mechanisms 2 and 3
// ---------------------------------------------------------------------------

// viewDefWireKeys returns every `json:` key generated.ViewDef declares.
//
// It reads the GENERATED TYPE by reflection rather than a transcription of it,
// which is the whole point: a key added to ViewDef.yaml appears here without
// anybody remembering to add it, so it can be caught unaccounted for instead
// of silently becoming a key this renderer drops.
func viewDefWireKeys() []string {
	t := reflect.TypeOf(generated.ViewDef{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if key := jsonTagName(t.Field(i).Tag.Get("json")); key != "" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// populatedViewKeys returns the `json:` keys this view actually declares.
//
// An EMPTY list or map is not populated: `filters: []` narrows nothing, and
// reporting it as an unshown constraint would be a false alarm. `disabled:
// false` is likewise not populated, on the contract's own words — "Omitted is
// identical to false".
func populatedViewKeys(def generated.ViewDef) []string {
	v := reflect.ValueOf(def)
	t := v.Type()
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		key := jsonTagName(t.Field(i).Tag.Get("json"))
		if key == "" {
			continue
		}
		if viewFieldDeclaresSomething(v.Field(i)) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func viewFieldDeclaresSomething(f reflect.Value) bool {
	if f.Kind() == reflect.Ptr {
		if f.IsNil() {
			return false
		}
		return viewFieldDeclaresSomething(f.Elem())
	}
	switch f.Kind() {
	case reflect.Slice, reflect.Map:
		return f.Len() > 0
	case reflect.Bool:
		// `disabled: false` is identical to omitted (ViewDef.Disabled's own
		// contract text), so it declares nothing.
		return f.Bool()
	default:
		return !f.IsZero()
	}
}

// unaccountedViewKeys is the gap: keys the view declares that its version's
// renderer never claimed. Header keys are always accounted for.
func unaccountedViewKeys(def generated.ViewDef, accounted []string) []string {
	known := map[string]struct{}{}
	for _, k := range viewHeaderKeys {
		known[k] = struct{}{}
	}
	for _, k := range accounted {
		known[k] = struct{}{}
	}
	var out []string
	for _, k := range populatedViewKeys(def) {
		if _, ok := known[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// populatedViewKeysBeyond returns the declared keys outside the given set.
// It is what stands between a silent renderer and the words "every record".
func populatedViewKeysBeyond(def generated.ViewDef, allowed []string) []string {
	ok := map[string]struct{}{}
	for _, k := range allowed {
		ok[k] = struct{}{}
	}
	var out []string
	for _, k := range populatedViewKeys(def) {
		if _, fine := ok[k]; !fine {
			out = append(out, k)
		}
	}
	return out
}

func jsonTagName(tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	return name
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// collapseWhitespace folds a multi-line source expression onto the single line
// a view body occupies, without dropping any of it.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
