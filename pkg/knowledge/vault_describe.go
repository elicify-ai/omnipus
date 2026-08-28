// Omnipus — ADR-068 D15.3 / spec §4.1.1: vault_describe, the mandatory cheap
// first call.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	if p.InFlight() {
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
		var clauses []string
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
		var keys []string
		for _, s := range *v.Def.Sort {
			keys = append(keys, s.Property+" "+string(s.Direction))
		}
		parts = append(parts, "sort "+strings.Join(keys, ", "))
	}
	if v.Def.Properties != nil && len(*v.Def.Properties) > 0 {
		parts = append(parts, "show "+strings.Join(*v.Def.Properties, ", "))
	}
	if v.Def.Aggregates != nil && len(*v.Def.Aggregates) > 0 {
		var aggs []string
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

// ---------------------------------------------------------------------------
// The tool
// ---------------------------------------------------------------------------

// OpenPropertyIndexFunc opens the derived properties index for one collection.
//
// It is INJECTED rather than called directly because pkg/knowledge must not
// import pkg/records/propindex — see PropertyIndexReader's doc comment for the
// test-build import cycle that forbids it. pkg/vaultprops supplies the real
// implementation; a test supplies a fake and needs no database.
//
// Returning (nil, err) is a first-class outcome: the typed integrity
// categories are then reported NOT CHECKED with that error as the reason,
// never as zero findings.
type OpenPropertyIndexFunc func(ctx context.Context, home, collectionRoot string) (PropertyIndexReader, error)

// DescribeTool is vault_describe.
type DescribeTool struct {
	tools.BaseTool
	deps      ToolDeps
	openIndex OpenPropertyIndexFunc
}

// NewDescribeTool builds the tool.
//
// openIndex may be nil, and that is a supported wiring rather than an
// oversight: a host with no properties index wired yet gets a fully working
// orientation response, and check_integrity reports the typed categories as
// NOT CHECKED, by name, with the reason. It never reports them as clean.
func NewDescribeTool(deps ToolDeps, openIndex OpenPropertyIndexFunc) *DescribeTool {
	if deps.RateLimiter == nil {
		deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{})
	}
	return &DescribeTool{deps: deps, openIndex: openIndex}
}

// Name is the registered tool name.
func (t *DescribeTool) Name() string { return "vault_describe" }

// Description is what the model reads, and it is the ONLY thing it reads
// before deciding whether to call.
//
// FR-079 caps it at ~150 tokens and requires it to name the WIDEST operation
// it grants — here the whole-vault integrity sweep, not the common
// orientation read.
func (t *DescribeTool) Description() string {
	return "Read this vault's shape before querying it. Returns compact text: the SAVED VIEWS " +
		"that already exist (look for one before inventing a filter), the collections in scope, " +
		"every declared record type with its typed properties and enum values, the note templates " +
		"available, and how fresh the index is. Call this first — a guessed property, type or " +
		"template name is refused, and this is where the real ones are. Set check_integrity to " +
		"also sweep the WHOLE vault for duplicate identifiers, relation targets that resolve to " +
		"nothing or to the wrong record type, broken wikilinks, orphan notes and index rows whose " +
		"note is gone; that sweep is bounded and says so when it clamps. Reads only; writes nothing."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *DescribeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *DescribeTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// describeArgNames is every argument this tool accepts. An argument outside it
// is REFUSED with the accepted names listed — the posture FR-024 takes for an
// unknown property, applied to the request envelope, because a silently
// ignored argument is a caller that believes it narrowed something.
var describeArgNames = []string{"collection", "record_type", "include", "check_integrity", "detail"}

// Parameters is the JSON schema the model fills in. Kept terse: FR-079's
// correction establishes that the WHOLE parameter schema is re-sent on every
// LLM request, so prose here is a standing per-turn cost, not a lazy one.
func (t *DescribeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": map[string]any{
				"type":        "string",
				"description": "Which knowledge base, by name. Unset when your workspace has one.",
			},
			"record_type": map[string]any{
				"type":        "string",
				"description": "Describe one record type only. Unknown names are refused with the declared ones listed.",
			},
			"include": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": describeSectionOrder},
				"description": "Trim the response. Default: all four.",
			},
			"check_integrity": map[string]any{
				"type":        "boolean",
				"description": "Also run the bounded health sweep. Scoped by collection/record_type when given.",
			},
			"detail": map[string]any{
				"type":        "string",
				"enum":        []string{DetailStandard, DetailMinimal},
				"description": "minimal omits enum value lists.",
			},
		},
	}
}

// Execute runs one describe.
func (t *DescribeTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// An unknown ARGUMENT is refused before anything else. A caller that
	// misspelled `record_types` and got a whole-vault answer has been told
	// something false about what it asked.
	if unknown := unknownArgs(args, describeArgNames); len(unknown) > 0 {
		return tools.ErrorResult(fmt.Sprintf(
			"vault_describe: unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(describeArgNames, ", ")))
	}
	if res := checkRetrievalRate(t.deps.RateLimiter, t.Name(), tools.ToolAgentID(ctx)); res != nil {
		return res
	}

	sections, err := parseIncludeSections(args["include"])
	if err != nil {
		return tools.ErrorResult("vault_describe: " + err.Error())
	}
	detail := strings.ToLower(strings.TrimSpace(stringArg(args["detail"])))
	switch detail {
	case "", DetailStandard:
		detail = DetailStandard
	case DetailMinimal:
	default:
		return tools.ErrorResult(fmt.Sprintf(
			"vault_describe: unknown detail %q; accepted: %s, %s", detail, DetailStandard, DetailMinimal))
	}

	scope, _ := ResolveTurnScope(ctx, t.deps.Home)
	collectionRef := strings.TrimSpace(stringArg(args["collection"]))
	col, ok := scope.Select(collectionRef)
	if !ok {
		// FR-024's posture, and the reason this tool exists: the valid names
		// are LISTED, so learning them never costs a failed call.
		return tools.ErrorResult(fmt.Sprintf(
			"vault_describe: no knowledge base %q is mounted into this workspace; in scope: %s",
			collectionRef, joinOrNone(scope.Names())))
	}

	data, execErr := t.gather(ctx, scope, col, gatherOptions{
		Sections:       sections,
		Detail:         detail,
		RecordType:     strings.TrimSpace(stringArg(args["record_type"])),
		CheckIntegrity: boolArg(args["check_integrity"]),
	})
	if execErr != nil {
		return tools.ErrorResult("vault_describe: " + execErr.Error())
	}
	return tools.NewToolResult(RenderDescribe(*data))
}

type gatherOptions struct {
	Sections       map[string]bool
	Detail         string
	RecordType     string
	CheckIntegrity bool
}

// gather does the reads. Every failure it can survive is folded into the
// response as a stated fault; every failure it cannot is returned, because a
// description assembled from a read that failed is a description of nothing.
func (t *DescribeTool) gather(
	ctx context.Context,
	scope Scope,
	col ScopedCollection,
	opts gatherOptions,
) (*DescribeData, error) {
	root, err := NewCollectionRoot(OSLinkFS(), col.Root)
	if err != nil {
		return nil, err
	}

	schemas, schemaReport, err := records.LoadSchemas(root.Path())
	if err != nil {
		return nil, err
	}
	if opts.RecordType != "" {
		if _, ok := schemas.Get(opts.RecordType); !ok {
			// AC-D2 — an unknown record_type is REFUSED with the declared
			// names listed. It does not return an empty description, because
			// an empty description and a typo are indistinguishable.
			return nil, &UnknownRecordTypeError{
				Requested: opts.RecordType,
				Declared:  schemas.Types(),
			}
		}
	}

	views, viewReport, err := records.LoadViews(root.Path(), schemas)
	if err != nil {
		return nil, err
	}

	data := &DescribeData{
		Collection:         col.Name,
		CollectionsInScope: scope.Names(),
		Schemas:            schemas,
		SchemaReport:       schemaReport,
		Views:              views,
		ViewReport:         viewReport,
		OnlyType:           opts.RecordType,
		Sections:           opts.Sections,
		Detail:             opts.Detail,
	}

	if opts.Sections[DescribeSectionTemplates] {
		if c, cerr := OpenCollection(root.Path()); cerr == nil {
			data.TemplatesDir = c.TemplatesDir()
			// ListTemplates has existed, complete and tested, with zero
			// callers. This is the first one.
			if list, terr := ListTemplates(OSLinkFS(), c); terr == nil {
				data.Templates = list
			} else {
				return nil, fmt.Errorf("listing templates: %w", terr)
			}
		}
	}

	if opts.Sections[DescribeSectionIndex] {
		data.IndexProgress = t.progress(root.Path())
		if dir, derr := IndexDirFor(t.deps.Home, root.Path()); derr == nil {
			if m, merr := LoadManifest(filepath.Join(dir, ManifestFileName), root.Path()); merr == nil {
				data.ManifestCount, data.ManifestKnown = m.Len(), true
			}
		}
	}

	if opts.CheckIntegrity {
		store, storeErr := t.openPropertyIndex(ctx, root.Path())
		if closer, ok := store.(interface{ Close() error }); ok && closer != nil {
			defer func() {
				if cerr := closer.Close(); cerr != nil {
					// A close failure cannot change an answer already
					// computed, but it must not vanish either.
					slog.Warn("vault_describe: closing the properties index failed",
						"collection_root", root.Path(), "error", cerr)
				}
			}()
		}
		report, ierr := CheckIntegrity(ctx, IntegrityOptions{
			FS:             OSLinkFS(),
			Root:           root,
			CollectionName: col.Name,
			Schemas:        schemas,
			Store:          store,
			RecordType:     opts.RecordType,
		})
		if ierr != nil {
			return nil, ierr
		}
		if storeErr != nil {
			for _, c := range report.Categories {
				if c.NotRun == "" && typedCategories[c.Category] {
					c.NotRun = storeErr.Error()
				}
			}
		}
		data.Integrity = report
		data.NotesOnDisk, data.NotesCounted = report.NotesSwept, true
	}
	return data, nil
}

// openPropertyIndex resolves the properties index, or explains why the typed
// checks cannot run. Either way the caller gets a usable answer: a reader, or
// a reason to print.
func (t *DescribeTool) openPropertyIndex(ctx context.Context, collectionRoot string) (PropertyIndexReader, error) {
	if t.openIndex == nil {
		return nil, fmt.Errorf(
			"no properties index is wired into this build of the gateway, so nothing typed can be checked; " +
				"duplicate identifiers, relation targets and orphan rows are unchecked here")
	}
	// On a build with no SQLite the injected opener returns
	// records.RequirePropertyIndex's refusal, naming the platform, and it is
	// passed through unchanged (FR-020h).
	return t.openIndex(ctx, t.deps.Home, collectionRoot)
}

func (t *DescribeTool) progress(collectionRoot string) IndexProgress {
	if t.deps.Progress != nil {
		if tr := t.deps.Progress(collectionRoot); tr != nil {
			return tr.Progress()
		}
	}
	return SharedProgressTracker(collectionRoot).Progress()
}

// unknownArgs lists supplied argument names that are not in accepted, sorted.
func unknownArgs(args map[string]any, accepted []string) []string {
	allowed := make(map[string]struct{}, len(accepted))
	for _, a := range accepted {
		allowed[a] = struct{}{}
	}
	var out []string
	for k := range args {
		if _, ok := allowed[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// parseIncludeSections reads `include`, refusing a member outside the set with
// the valid members listed.
func parseIncludeSections(raw any) (map[string]bool, error) {
	if raw == nil {
		return allDescribeSections(), nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("include must be a list of %s", strings.Join(describeSectionOrder, ", "))
	}
	if len(list) == 0 {
		return allDescribeSections(), nil
	}
	out := map[string]bool{}
	for _, item := range list {
		s, isStr := item.(string)
		if !isStr {
			return nil, fmt.Errorf("include must be a list of %s", strings.Join(describeSectionOrder, ", "))
		}
		s = strings.ToLower(strings.TrimSpace(s))
		valid := false
		for _, known := range describeSectionOrder {
			if s == known {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("unknown include section %q; accepted: %s",
				s, strings.Join(describeSectionOrder, ", "))
		}
		out[s] = true
	}
	return out, nil
}
