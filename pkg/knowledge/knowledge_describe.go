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
	"strconv"
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
	// DetailFull forces every SAVED VIEW's full definition inline, even when
	// there are too many to show at DetailStandard. It is the "on demand" half
	// of D4 (Issue 9): the default response names the views and counts them per
	// type, and a caller who actually needs a view's query asks for it with
	// detail=full rather than paying for 69 view bodies on every orientation
	// read.
	DetailFull = "full"
)

// viewsInlineThreshold is how many saved views DetailStandard will render in
// full before it switches to the compact per-type catalog (D4 / Issue 9). Below
// it, a reader gets every view's query without asking — the common small-vault
// case, and the shape the definition-of-done artifact pins. Above it, the full
// bodies would flood the context window a model reads at the start of every
// session, so the response lists names grouped by type and points at
// detail=full for the definitions.
const viewsInlineThreshold = 12

// integrityFindingsPageSize is how many findings ONE integrity category shows
// in a single response — both the default sample and the size of a cursor page
// (D3 / Issue 8). It is deliberately small: the whole point of the per-category
// totals and the cursor is that the orientation response no longer dumps
// hundreds of finding lines a model must read past. The remainder is reached by
// paging, not by making one response longer.
const integrityFindingsPageSize = 20

// integrityCursorSep separates the category from the offset in an integrity
// paging cursor ("broken link#20"). It is a character no IntegrityCategory
// contains, so the category — which itself contains spaces — parses back
// unambiguously by splitting on the LAST separator.
const integrityCursorSep = "#"

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
	// IntegrityCursor, when set, pages the INTEGRITY findings. It names one
	// category and an offset into that category's findings ("broken link#20"),
	// and the render shows that one page plus the token for the next. Empty
	// renders the first page (a bounded sample) of every category. It is only
	// meaningful alongside a non-nil Integrity.
	IntegrityCursor string

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
	detail := normalizeDetail(d.Detail)
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
			renderViews(&b, d, detail)
		case DescribeSectionTemplates:
			renderTemplates(&b, d)
		}
	}
	if d.Integrity != nil {
		renderIntegrity(&b, d.Integrity, d.IntegrityCursor)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// normalizeDetail collapses any unrecognised detail to DetailStandard, so the
// renderer has exactly three states to reason about and an unknown string
// never silently reads as "minimal".
func normalizeDetail(detail string) string {
	switch detail {
	case DetailMinimal, DetailFull:
		return detail
	default:
		return DetailStandard
	}
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
		// Reuses vaultprops/reader.go::errIndexNotBuilt's wording ("has not
		// been built yet") rather than inventing a third phrasing for the
		// same fact — adapted to this collection's own index (the
		// full-text/manifest index tools.go loads via ManifestExists, not
		// vaultprops' properties index; the two are separate subsystems and
		// this must not claim to be the other one). Deliberately names no
		// remedy: until the indexer enumerates from ResolveScope instead of
		// the mount store (docs/internal/design/knowledge-tools-remediation.md
		// R2, plan item 6 — not done here), there is genuinely no agent
		// tool, CLI verb or REST endpoint that can index this collection.
		// Naming one would repeat the exact defect (F-3) this branch exists
		// to fix, in different words.
		return "NOT INDEXED yet — the index for this collection has not been built yet, so nothing below reflects its contents"
	case d.NotesCounted && d.NotesOnDisk != d.ManifestCount:
		// NAMES NO REMEDY, FOR THE SAME REASON THE !ManifestKnown CASE ABOVE
		// NAMES NONE (F-9, reopened a second time). This branch used to read
		// "the two disagree; re-index to reconcile" — a phrase written when
		// the manifest was ONLY ever produced by a full CheckIntegrity/
		// SyncWith sweep, so any mismatch really was "this used to be fully
		// synced and has since drifted", and "re-index" at least named a real
		// mental model even though no agent tool, CLI verb or REST endpoint
		// could actually perform it (that half of the defect survives here:
		// there genuinely is no on-demand re-index surface — see the
		// !ManifestKnown case's comment).
		//
		// Since pkg/knowledge/author.go's instant-indexing path, a manifest
		// can ALSO be seeded by nothing but single-path knowledge_edit writes
		// on a collection nobody has ever swept — Index.UpdatePath saves the
		// SAME manifest file after touching exactly one path
		// (docs/internal/design/knowledge-index-freshness.md). One create on
		// such a collection lands here with ManifestCount=1 against whatever
		// NotesOnDisk actually is, and "the two disagree" reads as an alarm
		// about drift when the honest description is "this collection is
		// still mostly unindexed" — pkg/vaultprops/find_tool.go's Populated()
		// treats that exact shape as "not populated" for the same reason.
		//
		// This function has no way to tell those two histories apart — the
		// manifest records no provenance for how each entry was written, on
		// purpose (docs/internal/design/knowledge-index-freshness.md never
		// asked it to) — so rather than guess, the message states only what
		// is actually known (how much of what is on disk the index currently
		// covers) and stops there: no "disagree" framing that implies a
		// conflict needing resolution, no invented remedy, in either
		// direction.
		return fmt.Sprintf("index holds %s of %s notes on disk — the rest are not indexed yet and will not appear in search results",
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
		renderProperties(b, sc)
		renderAvailableViews(b, sc, detail)
	}
	renderSchemaRejections(b, d.SchemaReport)
}

// renderAvailableViews is design view-kinds-design-2026-09-03 §6.2's
// discovery block: for each of the eight closed view kinds, states whether
// THIS record type can back it and, if not, exactly what it is missing —
// "the agent asks, it does not remember" (§6.3). The gate rules themselves
// live in view_kinds.go (RenderAvailableViews / ViewKindAvailabilityFor),
// shared with knowledge_configure's create_view composer, so the two can
// never disagree about which kind is offered.
//
// Skipped at DetailMinimal: it is elaboration on properties this section
// already named — which of the eight view kinds each type could back — not the
// fact of a property's existence. This is now the ONLY thing minimal trims from
// the TYPES section; enum value lists stayed, because a value list is the
// property's domain rather than elaboration on it (renderProperties above).
func renderAvailableViews(b *strings.Builder, sc *records.Schema, detail string) {
	if detail == DetailMinimal {
		return
	}
	b.WriteString("    " + RenderAvailableViews(sc) + "\n")
}

// renderProperties lists a record type's properties and their types.
//
// ENUM VALUES ARE SHOWN AT EVERY DETAIL LEVEL, DetailMinimal included (D4 /
// Issue 9). They were once hidden at minimal as "elaboration", but the
// permitted set of an enum is not elaboration — it is the property's domain,
// and hiding it made a minimal describe report that `task.status` is an enum
// while withholding that "open" is one of the values it accepts, so an agent
// could neither filter on it nor set it without a second, wider call. The
// token cost minimal is for comes from the available-views block
// (renderAvailableViews), which minimal still skips.
func renderProperties(b *strings.Builder, sc *records.Schema) {
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
		if p.Type == records.TypeEnum {
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

// renderViews lists the saved views, in one of two shapes (D4 / Issue 9).
//
// A vault with a handful of views gets each one in FULL — its query on one
// line, so a reader can reuse it without opening the file. A vault with dozens
// (the shape that motivated this: 69 views rendered inline flooded the context
// a model reads at the start of every session) gets a COMPACT CATALOG instead —
// names grouped by type, counted, with the definitions available on demand via
// detail=full. DetailMinimal always takes the catalog; DetailFull always takes
// the full bodies, however many there are.
//
// Either way the two safety facts a reader must not miss are kept: a REJECTED
// view file is reported (renderViewRejections), and a DISABLED or NOT SERVABLE
// view is flagged even in the catalog — an agent that picks one of those off a
// bare name list has chosen a view that can never answer.
func renderViews(b *strings.Builder, d DescribeData, detail string) {
	views := d.Views.Views()
	// Built once for the whole listing rather than per view: the loader is a
	// thin wrapper over the set this function already holds, and asking it is
	// what keeps this listing and knowledge_find's own answer in agreement.
	loader := records.NewViewFindLoader(d.Views)
	if len(views) == 0 {
		b.WriteString("VIEWS (0) — no saved views; a query here starts from scratch\n")
		renderViewRejections(b, d.ViewReport)
		return
	}
	if renderViewsInFull(detail, len(views)) {
		fmt.Fprintf(b, "VIEWS (%d) — ask for one by name before inventing a filter\n", len(views))
		for _, v := range views {
			renderViewFull(b, loader, v)
		}
		renderViewRejections(b, d.ViewReport)
		return
	}
	renderViewCatalog(b, loader, views)
	renderViewRejections(b, d.ViewReport)
}

// renderViewsInFull decides between the full bodies and the compact catalog.
func renderViewsInFull(detail string, count int) bool {
	switch detail {
	case DetailFull:
		return true
	case DetailMinimal:
		return false
	default:
		return count <= viewsInlineThreshold
	}
}

// renderViewFull renders one view's head line and its whole query — the shape
// the definition-of-done artifact pins and the shape detail=full always gives.
func renderViewFull(b *strings.Builder, loader *records.ViewFindLoader, v *records.SavedView) {
	// ViewDef.Type is a *string since FR-018b made `type` optional (an
	// untyped view spans record types). Rendered through %s a nil — or a
	// non-nil — pointer prints an ADDRESS, which is why go vet flags it:
	// an operator reading "type 0xc000123456" learns nothing and cannot
	// tell it from a real type name.
	head := fmt.Sprintf("  %s  type %s", v.Name(), viewTypeLabel(v))
	if lbl := v.DisplayLabel(); lbl != v.Name() {
		head += " " + quoteDisplay(lbl)
	}
	b.WriteString(head + "\n")
	if body := renderViewBody(v); body != "" {
		b.WriteString(body)
	}
	b.WriteString(renderViewServeRefusal(loader, v))
}

// renderViewCatalog is D4's compact answer to the 69-view flood: view names
// grouped by declared type, counted, with the full definitions one detail=full
// call away. Untyped views are listed last, under their own heading, because
// "(untyped)" is a real category (a view that spans record types) rather than a
// missing value.
func renderViewCatalog(b *strings.Builder, loader *records.ViewFindLoader, views []*records.SavedView) {
	fmt.Fprintf(b, "VIEWS (%d) — names by type; ask for one by name before inventing a filter; "+
		"call knowledge_describe with detail=full for the definitions\n", len(views))

	byType := map[string][]*records.SavedView{}
	for _, v := range views {
		byType[viewTypeLabel(v)] = append(byType[viewTypeLabel(v)], v)
	}
	typed := make([]string, 0, len(byType))
	hasUntyped := false
	for k := range byType {
		if k == "(untyped)" {
			hasUntyped = true
			continue
		}
		typed = append(typed, k)
	}
	sort.Strings(typed)
	if hasUntyped {
		typed = append(typed, "(untyped)")
	}

	// Attention lines are collected while the names are listed, so the reader
	// sees WHICH names carry a "*" and then, once, WHY — without the catalog
	// having to interleave a warning between two names.
	var attention []string
	for _, key := range typed {
		group := byType[key]
		names := make([]string, 0, len(group))
		for _, v := range group {
			name := v.Name()
			if note := viewAttentionNote(loader, v); note != "" {
				name += "*"
				attention = append(attention, fmt.Sprintf("    %s — %s", v.Name(), note))
			}
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(b, "  %s (%d): %s\n", key, len(group), strings.Join(names, ", "))
	}
	if len(attention) > 0 {
		sort.Strings(attention)
		fmt.Fprintf(b, "  !! %d view(s) marked * cannot answer as listed:\n", len(attention))
		for _, line := range attention {
			b.WriteString(line + "\n")
		}
	}
}

// viewTypeLabel is the declared record type a view is filed under, or
// "(untyped)" for one that spans record types (FR-018b made `type` optional).
func viewTypeLabel(v *records.SavedView) string {
	if v.Def.Type != nil && *v.Def.Type != "" {
		return *v.Def.Type
	}
	return "(untyped)"
}

// viewAttentionNote returns why a view cannot answer as listed — disabled, or
// refused by knowledge_find — or "" when it is fine. It is the compact
// catalog's equivalent of the inline DISABLED / NOT SERVABLE lines the full
// listing prints, so no reader picks a dead view off a bare name list.
func viewAttentionNote(loader *records.ViewFindLoader, v *records.SavedView) string {
	if v == nil {
		return ""
	}
	if v.Def.Disabled != nil && *v.Def.Disabled {
		return "DISABLED; stored but never applied, returns nothing"
	}
	if loader != nil {
		if refusal, refused := loader.ServeRefusal(v.Name()); refused && refusal.Code != records.ServeRefusalDisabled {
			return fmt.Sprintf("NOT SERVABLE by knowledge_find: %s (%s) — %s",
				refusal.Reason, refusal.Code, refusal.Remedy)
		}
	}
	return ""
}

// renderViewServeRefusal states, per view, whether knowledge_find will
// actually run it.
//
// THE HEAD LINE ABOVE SAYS "ask for one by name before inventing a filter",
// AND FOR SOME VIEWS THAT INSTRUCTION USED TO FAIL. A view declaring
// `formulas`, or a grouping key in DESCENDING order, was written faithfully,
// listed here in full, and then refused by knowledge_find with "no saved view
// named X" — a statement that is false about a view this very listing just
// showed. Each refusal was right while it stood (VaultFindRequest had a field
// for neither, and serving the view anyway would have answered a broader or a
// differently-ordered question than it asks); what was missing is that the
// agent following this listing had no way to know before it called.
//
// BOTH OF THOSE SEAMS ARE NOW CLOSED, so as of today this function has NO LIVE
// EMITTER and renders nothing: formulas travel beside the request through the
// loader, `group_by` carries a direction per key, and the only refusal left —
// ServeRefusalDisabled — is skipped here on purpose (see the last paragraph).
// It is kept rather than deleted because it reads the refusal GENERICALLY off
// the loader, so the day a new refusal code is added the listing states it
// without anyone remembering to come back here. That is the whole of its
// value, and it is stated plainly so no reader mistakes it for a guard that is
// currently catching something. The mark an operator actually sees today comes
// from renderViewDisabled.
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
	// kind: which of the eight view kinds authored this view
	// (view-kinds-design-2026-09-03 §4). Provenance for a reader deciding how
	// to re-edit, so it is RENDERED rather than silently claimed.
	"kind",
	// parts: the ordered stack the renderer walks (same design, §2.2/§4).
	// Parts carry grouping/subtotals/aggregation of their own, so they are a
	// constraint report, not provenance.
	"parts",
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
	if v.Def.Kind != nil && *v.Def.Kind != "" {
		r.add("kind " + string(*v.Def.Kind))
	}
	if v.Def.Parts != nil && len(*v.Def.Parts) > 0 {
		segs := make([]string, 0, len(*v.Def.Parts))
		for _, p := range *v.Def.Parts {
			seg := string(p.Part)
			var details []string
			if p.Number != nil && *p.Number != "" {
				n := *p.Number
				if p.Unit != nil && *p.Unit != "" {
					// G2: the unit companion is restated so a reader can see
					// which pairing the part was composed against.
					n += " per " + *p.Unit
				}
				if p.Aggregate != nil && *p.Aggregate != "" {
					n = string(*p.Aggregate) + " of " + n
				}
				details = append(details, n)
			}
			if p.Date != nil && *p.Date != "" {
				details = append(details, "over "+*p.Date)
			}
			if p.Image != nil && *p.Image != "" {
				details = append(details, "images from "+*p.Image)
			}
			if p.Choice != nil && *p.Choice != "" {
				details = append(details, "columns from "+*p.Choice)
			}
			if p.Grouping != nil && len(*p.Grouping) > 0 {
				keys := make([]string, 0, len(*p.Grouping))
				for _, g := range *p.Grouping {
					// Same omitted-means-asc reporting rule as the view-level
					// grouping clause above.
					dir := string(generated.ViewGroupByDirectionAsc)
					if g.Direction != nil && *g.Direction != "" {
						dir = string(*g.Direction)
					}
					keys = append(keys, g.Property+" "+dir)
				}
				details = append(details, "group "+strings.Join(keys, ", "))
			}
			if p.Subtotals != nil && len(*p.Subtotals) > 0 {
				names := sortedMapKeys(*p.Subtotals)
				out := make([]string, 0, len(names))
				for _, n := range names {
					out = append(out, n+" "+string((*p.Subtotals)[n]))
				}
				details = append(details, "subtotal "+strings.Join(out, ", "))
			}
			if p.Properties != nil && len(*p.Properties) > 0 {
				details = append(details, "columns "+strings.Join(*p.Properties, ", "))
			}
			if len(details) > 0 {
				seg += " (" + strings.Join(details, "; ") + ")"
			}
			segs = append(segs, seg)
		}
		r.add("parts " + strings.Join(segs, " then "))
	}
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

// renderIntegrity renders the health sweep (D3 / Issue 8).
//
// THE PROBLEM IT SOLVES: a sweep that found 849 findings used to dump every
// retained finding line inline, with no per-category totals and no way to reach
// the ones the per-category cap had dropped. A model reading an orientation
// response had to scroll past hundreds of lines to find the counts, and could
// never see finding 501.
//
// So the response now leads with a per-category TOTALS line, shows a bounded
// SAMPLE of each category (integrityFindingsPageSize), and hands back a CURSOR
// for any category with more — `cursor=<category>#<offset>` on the next
// knowledge_describe call renders the next page. The category retains far more
// findings than one page shows (IntegrityRetentionPerCategory), so the cursor
// genuinely enumerates the remainder rather than paging within a truncated
// sample; only a category whose true total exceeds even the retention cap
// reports a non-enumerable remnant, with the same "narrow the scope" remedy the
// old clamp gave.
func renderIntegrity(b *strings.Builder, r *IntegrityReport, cursor string) {
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

	renderIntegrityTotals(b, r)

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

	// A cursor pages ONE category. A bad cursor is reported, never silently
	// ignored — a caller that believes it is paging and is not has been told
	// something false.
	if cat, offset, ok := parseIntegrityCursor(cursor); ok {
		c := r.Category(IntegrityCategory(cat))
		if c == nil || c.NotRun != "" || c.Total == 0 {
			fmt.Fprintf(b, "  cursor names no category with findings to page: %q\n", cat)
			return
		}
		renderIntegrityCategoryPage(b, width, c, offset)
		return
	}

	for _, c := range r.Categories {
		if c.NotRun != "" {
			continue
		}
		renderIntegrityCategoryPage(b, width, c, 0)
	}
	if ran == 0 && notRun == 0 {
		b.WriteString("  nothing to report\n")
	}
}

// renderIntegrityTotals is D3's per-category count line: every category that
// found something, with its full pre-sample total, so a reader learns the shape
// of the report before reading a single finding line. Categories with nothing
// are omitted here (they contribute nothing to read) and NOT-CHECKED ones are
// named in their own block instead.
func renderIntegrityTotals(b *strings.Builder, r *IntegrityReport) {
	parts := make([]string, 0, len(r.Categories))
	for _, c := range r.Categories {
		if c.NotRun != "" || c.Total == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", string(c.Category), group(c.Total)))
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  by category: %s\n", strings.Join(parts, ", "))
	}
}

// renderIntegrityCategoryPage renders one category's findings from offset up to
// one page, then the status line that says what remains and how to reach it.
func renderIntegrityCategoryPage(b *strings.Builder, width int, c *CategoryResult, offset int) {
	if offset < 0 {
		offset = 0
	}
	if offset > 0 && offset >= len(c.Findings) {
		// The cursor points at or beyond the last retained finding, so there is
		// no page here to show. Clamping the offset to len and then formatting
		// offset+1..end produced a reversed "26-25 of 25 — end" range (Finding
		// 4); say plainly that the offset is past the end instead. offset==0 is
		// deliberately excluded so an empty category still renders as nothing on
		// the normal (non-cursor) path.
		renderIntegrityCategoryPastEnd(b, c, offset)
		return
	}
	end := offset + integrityFindingsPageSize
	if end > len(c.Findings) {
		end = len(c.Findings)
	}
	for _, f := range c.Findings[offset:end] {
		fmt.Fprintf(b, "  %-*s  %s\n", width, string(c.Category), f.Detail)
	}
	renderIntegrityCategoryStatus(b, c, offset, end)
}

// renderIntegrityCategoryStatus prints, for one category, the paging line and —
// independently — the retention-overflow line.
//
// The two are separate FACTS and must not be conflated: the paging line says
// how to reach the next RETAINED findings, and the overflow line says how many
// findings the retention cap dropped entirely, which are not reachable by any
// cursor. The overflow is a property of the category, not of the page, so it is
// stated on EVERY page it applies to — a reader on page 1 must not have to walk
// to the last page to learn that findings were discarded (FR-075a's "say when
// it clamps"). A category that fits one page with nothing dropped prints
// neither line, which is the common case the definition-of-done artifact pins.
func renderIntegrityCategoryStatus(b *strings.Builder, c *CategoryResult, offset, end int) {
	cat := string(c.Category)
	switch {
	case end < len(c.Findings):
		// More findings are RETAINED — the cursor reaches them.
		fmt.Fprintf(b, "  %s: showing %s-%s of %s — next page: cursor=%s%s%d\n",
			cat, group(offset+1), group(end), group(c.Total), cat, integrityCursorSep, end)
	case offset > 0 || len(c.Findings) > integrityFindingsPageSize:
		// A multi-page category fully walked — say so, so the reader knows the
		// cursor has run out rather than wondering whether it broke.
		fmt.Fprintf(b, "  %s: showing %s-%s of %s — end\n",
			cat, group(offset+1), group(end), group(c.Total))
	}
	if c.Total > len(c.Findings) {
		// The sweep found more than the retention cap kept. Name how many were
		// dropped and how to narrow, because those findings are not enumerable
		// by paging at all.
		fmt.Fprintf(b, "  %s: %s more findings exceed the retention cap of %s and are not enumerable — narrow with collection=<name> or record_type=<name>\n",
			cat, group(c.Total-len(c.Findings)), group(len(c.Findings)))
	}
}

// renderIntegrityCategoryPastEnd handles a cursor offset that lands at or beyond
// the category's last retained finding.
//
// Such an offset names no page — the caller over-ran the enumeration (a cursor
// kept past the last "next page" line, or one typed by hand). The old renderer
// clamped the offset to len(Findings) and then formatted offset+1..end, which
// printed a reversed "26-25 of 25 — end" range that reads as nonsense. State the
// fact plainly instead: this offset is past the end, and how many findings the
// category actually has. The retention-overflow line is still stated because it
// is a property of the category, not of any one page.
func renderIntegrityCategoryPastEnd(b *strings.Builder, c *CategoryResult, offset int) {
	cat := string(c.Category)
	fmt.Fprintf(b, "  %s: cursor offset %s is past the end — this category has %s finding(s) — end\n",
		cat, group(offset), group(len(c.Findings)))
	if c.Total > len(c.Findings) {
		fmt.Fprintf(b, "  %s: %s more findings exceed the retention cap of %s and are not enumerable — narrow with collection=<name> or record_type=<name>\n",
			cat, group(c.Total-len(c.Findings)), group(len(c.Findings)))
	}
}

// parseIntegrityCursor splits a "<category>#<offset>" cursor. The category may
// contain spaces but never the separator, so the split is on the LAST one.
func parseIntegrityCursor(cursor string) (category string, offset int, ok bool) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", 0, false
	}
	i := strings.LastIndex(cursor, integrityCursorSep)
	if i <= 0 || i == len(cursor)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(cursor[i+1:])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return cursor[:i], n, true
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
