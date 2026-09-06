// Omnipus — ADR-068 D15.3 / spec FR-075, FR-075a: the bounded integrity sweep.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS IS, AND THE INHERITED DEFECT IT REFUSES TO CARRY FORWARD
//
// check_integrity is knowledge_describe's whole-vault health sweep: duplicate
// identifiers, relation targets that resolve to nothing or to the wrong record
// type, broken ORDINARY wikilinks, orphan notes, and rows in the properties
// index with no note behind them.
//
// The nearest thing that exists today is knowledge_graph's `unresolved` and
// `orphans`, and BOTH ARE UNBOUNDED — `resp.Links = toGraphLinks(g.Unresolved())`
// and `resp.Nodes = g.Orphans()` run over the whole collection with no cap of
// any kind. (knowledge_graph's hops<=3 / max_nodes<=500 clamps apply to
// `neighborhood` only; they are read inside that case and nowhere else.) So the
// unboundedness here is INHERITED, not introduced — and inheriting it into an
// operation this ADR advertises as bounded would be worse than shipping one.
//
// TWO BOUNDS, and they are different in kind:
//
//	IntegritySweepLimit          REFUSES. A collection above it is not swept at
//	                             all, and the refusal names the collection and
//	                             the scoped remedy. It never returns a partial
//	                             sweep presented as whole (AC-D4).
//	IntegrityFindingsPerCategory CLAMPS. Findings past it are dropped, and the
//	                             clamp line states the count that WOULD have
//	                             been returned. A clamp that does not say what
//	                             it hid is a truncation, not a bound.
//
// THE SWEEP LIMIT IS CHECKED BEFORE THE EXPENSIVE WORK, which is why this file
// walks the collection itself rather than letting BuildLinkGraph do it. The
// walk is a directory traversal that opens nothing; the graph opens and scans
// every note. Checking the bound after the graph was built would be a bound
// that fires once the work it bounds is already done.
// ---------------------------------------------------------------------------

const (
	// IntegritySweepLimit is FR-075a's ceiling on notes swept. A collection
	// above it is REFUSED with the scoped remedy named, never partly swept.
	IntegritySweepLimit = 100_000

	// IntegrityFindingsPerCategory is FR-075a's per-category clamp. Findings
	// past it are dropped and the clamp is reported with the would-be total.
	// It stays the default cap for callers that construct a sink directly (e.g.
	// the restructure-trash listing); check_integrity itself now retains far
	// more (IntegrityRetentionPerCategory) so its D3 cursor can page the
	// remainder rather than pointing at findings a low cap already discarded.
	IntegrityFindingsPerCategory = 500

	// IntegrityRetentionPerCategory is how many findings ONE category keeps for
	// the D3 cursor (Issue 8) to page through. It is far larger than any single
	// response shows — the render layer samples a page at a time — so the cursor
	// genuinely enumerates the remainder. It is still a bound: a category whose
	// true total exceeds it drops the excess and reports it as a non-enumerable
	// remnant with the same "narrow the scope" remedy the old clamp gave, which
	// is FR-075a's guarantee preserved at a bound high enough that realistic
	// vaults page in full.
	IntegrityRetentionPerCategory = 5000
)

// IntegrityCategory is one kind of finding. The set is closed, and its order
// here is the order a report renders in.
type IntegrityCategory string

const (
	// CategoryDuplicateID — two notes of one record type carry one identifier
	// (FR-039). Both paths are named and neither is preferred.
	CategoryDuplicateID IntegrityCategory = "duplicate id"
	// CategoryUnresolvedRelation — a typed relation whose wikilink names no
	// note in the collection (FR-033).
	CategoryUnresolvedRelation IntegrityCategory = "unresolved"
	// CategoryWrongType — a typed relation that resolves to a note of the
	// wrong record type (FR-034).
	CategoryWrongType IntegrityCategory = "wrong type"
	// CategoryBrokenLink — an ORDINARY wikilink that resolves to nothing.
	// This is the half knowledge_graph's retirement would otherwise lose:
	// most notes in a vault are not records.
	CategoryBrokenLink IntegrityCategory = "broken link"
	// CategoryOrphan — a note nothing links to.
	CategoryOrphan IntegrityCategory = "orphan"
	// CategoryOrphanRow — a row in the properties index whose note is gone
	// (FR-020c, D16.5).
	CategoryOrphanRow IntegrityCategory = "orphan row"
	// CategoryAmbiguousName — two or more notes share a basename, so a bare
	// wikilink like [[Composio]] is ambiguous (Issue 10 / V1). It resolves to
	// exactly one of them by NoteIndex's tie-break rule and the others are
	// silently unreachable by that name. This is a name-uniqueness check over
	// the walk, not a typed one: it needs no properties index and runs on every
	// build, which is why it is NOT in typedCategories below.
	CategoryAmbiguousName IntegrityCategory = "ambiguous name"
)

// IntegrityCategories is the closed set, in render order. Typed categories
// come first because they are the ones a SQLite-less build cannot run, so a
// report that could not run them says so at the top rather than in the middle.
var IntegrityCategories = []IntegrityCategory{
	CategoryDuplicateID,
	CategoryUnresolvedRelation,
	CategoryWrongType,
	CategoryOrphanRow,
	CategoryBrokenLink,
	CategoryOrphan,
	CategoryAmbiguousName,
}

// typedCategories are the ones that need the properties index. On a build
// without it these are NOT RUN and say so by name; they never report zero.
var typedCategories = map[IntegrityCategory]bool{
	CategoryDuplicateID:        true,
	CategoryUnresolvedRelation: true,
	CategoryWrongType:          true,
	CategoryOrphanRow:          true,
}

// IntegrityFinding is one problem, and it always names a path.
type IntegrityFinding struct {
	Category IntegrityCategory
	// Path is the note the reader should open. For a duplicate identifier it
	// is the first of the conflicting paths; every other path is in Detail,
	// because FR-039 requires all of them to be named.
	Path string
	// Detail is the rest of the line — already rendered, because the shape of
	// a finding differs by category and a caller that had to re-render it
	// would eventually render one of them differently.
	Detail string
}

// CategoryResult is one category's outcome: what was found, how much was
// hidden by the clamp, or why it could not run at all.
type CategoryResult struct {
	Category IntegrityCategory
	// Findings is at most IntegrityFindingsPerCategory entries.
	Findings []IntegrityFinding
	// Total is how many findings there were BEFORE the clamp. It is the
	// number the clamp line quotes, and it is why a clamp is a bound rather
	// than a truncation.
	Total int
	// NotRun, when non-empty, is why this category produced no findings — a
	// build with no properties index, or a store that could not be read. A
	// category that could not run reports this INSTEAD of zero, because
	// "0 findings" and "not checked" are opposite verdicts (AC-D6).
	NotRun string
}

// Clamped reports whether findings were dropped.
func (c *CategoryResult) Clamped() bool { return c != nil && c.Total > len(c.Findings) }

// IntegrityReport is the whole sweep.
type IntegrityReport struct {
	// ScopeLabel is what the report says it swept: "whole vault", or a
	// collection, or a record type.
	ScopeLabel string
	// NotesSwept is the number of markdown notes the walk found in scope.
	NotesSwept int
	// Categories is every category, in IntegrityCategories order, including
	// the ones with nothing to report — a category missing from the list and
	// a category with no findings are indistinguishable to a reader.
	Categories []*CategoryResult
}

// TotalFindings is the sum of every category's pre-clamp total.
func (r *IntegrityReport) TotalFindings() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, c := range r.Categories {
		n += c.Total
	}
	return n
}

// Category returns one category's result.
func (r *IntegrityReport) Category(cat IntegrityCategory) *CategoryResult {
	if r == nil {
		return nil
	}
	for _, c := range r.Categories {
		if c.Category == cat {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// SweepTooLargeError is FR-075a's refusal: the collection holds more notes
// than the sweep will look at, so it is not swept at all.
//
// It is an error and never a partial report, because AC-D4 is explicit that a
// partial sweep must never be presented as whole — and a report that swept
// 100,000 of 214,900 notes and said "6 findings" is precisely that.
type SweepTooLargeError struct {
	Collection string
	Notes      int
	Limit      int
}

func (e *SweepTooLargeError) Error() string {
	where := e.Collection
	if where == "" {
		where = "this collection"
	}
	return fmt.Sprintf(
		"collection %q holds %d notes; the sweep limit is %d — run check_integrity with record_type=<name>, or on a narrower collection",
		where, e.Notes, e.Limit)
}

// UnknownRecordTypeError is FR-024's pattern applied to check_integrity's
// scope argument: a record type nobody declared is refused with the declared
// names listed, never answered with an empty sweep.
type UnknownRecordTypeError struct {
	Requested string
	Declared  []string
}

func (e *UnknownRecordTypeError) Error() string {
	return fmt.Sprintf("no record type %q is declared; declared types: %s",
		e.Requested, joinOrNone(e.Declared))
}

// joinOrNone renders a valid-names list. An empty list says so in words: a
// message trailing off after "declared types: " reads as truncated, and a
// reader cannot tell whether the list was empty or the message was cut.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------------------
// The sink — where the per-category clamp actually lives
// ---------------------------------------------------------------------------

// findingSink accumulates findings under the per-category cap while counting
// every one of them, so a clamp can always quote the number it hid.
//
// The limit is a FIELD rather than a constant read directly, so a test can
// drive the clamp at a small corpus AND a separate test can pin the production
// constant. Both are asserted: a bound only ever set from a test is a bound
// that could be anything in production.
type findingSink struct {
	limit  int
	byCat  map[IntegrityCategory]*CategoryResult
	catSet []IntegrityCategory
}

func newFindingSink(perCategory int) *findingSink {
	if perCategory < 0 {
		perCategory = 0
	}
	s := &findingSink{limit: perCategory, byCat: map[IntegrityCategory]*CategoryResult{}}
	for _, c := range IntegrityCategories {
		s.byCat[c] = &CategoryResult{Category: c}
		s.catSet = append(s.catSet, c)
	}
	return s
}

// add records one finding. It ALWAYS increments Total; it appends only while
// there is room. Counting past the limit is the whole reason the clamp line
// can name a real number.
func (s *findingSink) add(cat IntegrityCategory, path, detail string) {
	c, ok := s.byCat[cat]
	if !ok {
		return
	}
	c.Total++
	f := IntegrityFinding{Category: cat, Path: path, Detail: detail}
	if len(c.Findings) < s.limit {
		c.Findings = append(c.Findings, f)
		return
	}
	// At capacity (D3-01): retain the s.limit findings with the SMALLEST
	// (Path, Detail) keys, not the first s.limit the store happened to emit.
	// The store's row order is not stable across re-runs (no ORDER BY on the
	// candidate streams), so keeping insertion order would make WHICH findings
	// survive non-deterministic — the stateless cursor would then page a
	// different subset on a later request even though sortFindings normalizes
	// their order. Bounding to the smallest-N by key keeps memory at s.limit
	// AND makes the enumerable subset itself deterministic. The scan for the
	// current maximum is O(s.limit) and only runs once a single category
	// exceeds the cap (5000 in production), which no real vault reaches.
	if s.limit == 0 {
		return
	}
	maxi := 0
	for i := 1; i < len(c.Findings); i++ {
		if findingLess(c.Findings[maxi], c.Findings[i]) {
			maxi = i
		}
	}
	if findingLess(f, c.Findings[maxi]) {
		c.Findings[maxi] = f
	}
}

// notRun marks a whole category as unrunnable, with the reason.
func (s *findingSink) notRun(cat IntegrityCategory, reason string) {
	if c, ok := s.byCat[cat]; ok {
		c.NotRun = reason
	}
}

func (s *findingSink) results() []*CategoryResult {
	out := make([]*CategoryResult, 0, len(s.catSet))
	for _, c := range s.catSet {
		r := s.byCat[c]
		sortFindings(r.Findings)
		out = append(out, r)
	}
	return out
}

// sortFindings imposes a deterministic total order — Path, then Detail — on one
// category's retained findings.
//
// WHY IT IS LOAD-BEARING FOR PAGING (Finding 3): the D3 cursor is STATELESS.
// Every knowledge_describe?cursor=<cat>#<offset> re-runs the whole sweep and
// slices Findings[offset:offset+page]. Some categories collect their findings in
// the store's implicit row order — orphan rows and relations follow ScanRecords
// / ScanRelations iteration order, which SQLite makes no promise about across a
// re-plan, a rowid change, or any intervening write. If that order differed
// between the page-1 request and a later cursor request, the same offset landed
// on a different finding — silently skipping or duplicating findings across
// pages while the count line claimed a clean, contiguous range.
//
// Sorting here, at the single point where every category's findings are
// assembled into the report, makes the offset name the SAME finding on every
// re-run regardless of the order the store yielded rows in. (Path, Detail) is a
// total order: Detail already carries every distinguishing fact of a finding, so
// two findings that share a path still order deterministically.
// findingLess is the single (Path, Detail) total order used BOTH to sort a
// category's retained findings and to decide which findings survive the
// retention cap (D3-01) — the two must agree or the enumerable subset and its
// ordering could disagree.
func findingLess(a, b IntegrityFinding) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Detail < b.Detail
}

func sortFindings(findings []IntegrityFinding) {
	sort.Slice(findings, func(i, j int) bool {
		return findingLess(findings[i], findings[j])
	})
}

// ---------------------------------------------------------------------------
// The typed half — the entry point that owes FR-020h its refusal
// ---------------------------------------------------------------------------

// IndexedRecord is one record as the properties index holds it — the THREE
// facts the integrity sweep needs and nothing else.
type IndexedRecord struct {
	// Path is the note's collection-relative path.
	Path string
	// RecordType is the declared type. Empty for an ordinary note.
	RecordType string
	// RecordID is the identifier, byte-exact. R-8: CO-0142 and co-0142 are
	// two records, so nothing here folds it.
	RecordID string
}

// IndexedRelation is one relation edge as the properties index holds it.
type IndexedRelation struct {
	// Path is the note the edge starts from.
	Path string
	// RecordID identifies the record that owns the edge.
	RecordID string
	// Property is the declared relation property the edge belongs to.
	Property string
	// Target is the wikilink target, as written.
	Target string
}

// PropertyIndexReader is the READ SEAM check_integrity needs from the derived
// properties index — and it is deliberately an interface declared HERE rather
// than the concrete pkg/records/propindex.Store.
//
// THE REASON IS A REAL CYCLE, NOT A STYLE PREFERENCE, and the comment at the
// head of fields.go got it half right: "pkg/records imports nothing from
// Omnipus, so the direction knowledge->records is acyclic; pkg/records/propindex
// already depends on both". True of the PRODUCTION graph and false of the TEST
// build — pkg/records/propindex's own in-package tests import pkg/knowledge, so
// a knowledge -> propindex edge closes the loop and `go test
// ./pkg/records/propindex/` fails to build with "import cycle not allowed in
// test". An in-package _test.go file's imports count.
//
// Inverting the dependency is the fix and it is also the better shape: the
// sweep states the two questions it asks, an adapter over the real store
// satisfies them structurally (pkg/vaultprops), and a test drives the sweep
// against a fake without a database.
type PropertyIndexReader interface {
	// ScanRecords visits every indexed record of one declared type. An empty
	// recordType means every type, including notes that declare none.
	//
	// It materialises nothing: the visitor sees one record at a time and the
	// implementation is expected to keep exactly one in memory.
	ScanRecords(ctx context.Context, recordType string, visit func(IndexedRecord) error) error

	// ScanRelations visits every relation edge owned by records of one
	// declared type.
	ScanRelations(ctx context.Context, recordType string, visit func(IndexedRelation) error) error
}

// TypedIntegrityInput is everything the record-typed checks need.
type TypedIntegrityInput struct {
	// Store is the properties index. Nil is legal and means the same as an
	// unavailable one: the typed categories are NOT RUN and say so.
	Store PropertyIndexReader
	// Schemas is the vault's declared record types.
	Schemas *records.SchemaSet
	// Resolver resolves a relation's wikilink target to a note path. It is
	// the SAME resolver ordinary wikilinks go through, so a relation and a
	// body link cannot disagree about where "[[Acme]]" points.
	Resolver *NoteIndex
	// ExistingFiles is every path the walk actually found. A properties-index
	// row naming a path outside it is an orphan row.
	ExistingFiles map[string]struct{}
	// RecordType narrows the sweep to one declared type. Empty sweeps every
	// declared type.
	RecordType string
}

// TypedIntegrityResult is what the typed half learned, including the
// path -> record type map the wikilink half needs in order to honour a
// record_type scope.
type TypedIntegrityResult struct {
	// RecordTypeByPath is every indexed record's declared type, keyed by path.
	RecordTypeByPath map[string]string
}

// TypedIntegrity runs the record-typed half of the sweep: duplicate
// identifiers, unresolved and mistyped relations, and orphan index rows.
//
// FR-020h: on a build where the properties index cannot exist it returns
// records.RequirePropertyIndex's error UNCHANGED. It does not return an empty
// result, because an empty success here reads as "your vault is clean" when
// the truth is that the question cannot be asked on this platform — the exact
// confidently-wrong answer ADR-068 exists to remove.
//
// This is the entry point spec AC-F6 / SC-023 names for check_integrity, and
// the one records.AssertRefusesWhenIndexUnavailable is pointed at.
func TypedIntegrity(ctx context.Context, in TypedIntegrityInput, sink *findingSink) (*TypedIntegrityResult, error) {
	if err := records.RequirePropertyIndex(records.CapabilityIntegrityCheck); err != nil {
		return nil, err
	}
	if in.Store == nil {
		return nil, fmt.Errorf("knowledge: check_integrity was given no properties index to read")
	}

	types := declaredTypesToSweep(in.Schemas, in.RecordType)
	res := &TypedIntegrityResult{RecordTypeByPath: map[string]string{}}

	// One pass per declared type collects identity and existence. Sweeping
	// per type rather than in one go is what keeps each population inside the
	// store's own B1 bound: a type above it refuses NAMING THAT TYPE, which is
	// a remedy the caller can act on, where an undifferentiated
	// "too many records" would not be.
	idOwners := map[string][]string{}
	for _, t := range types {
		err := in.Store.ScanRecords(ctx, t, func(c IndexedRecord) error {
			res.RecordTypeByPath[c.Path] = c.RecordType
			if c.RecordID != "" {
				// R-8: identifiers are compared byte-exact. CO-0142 and
				// co-0142 are two records, so the key is not folded.
				key := c.RecordType + "\x00" + c.RecordID
				idOwners[key] = append(idOwners[key], c.Path)
			}
			if _, exists := in.ExistingFiles[c.Path]; !exists {
				sink.add(CategoryOrphanRow, c.Path, fmt.Sprintf(
					"properties index holds %s at %s; no note exists at that path",
					identityLabel(c.RecordID), c.Path))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("knowledge: sweeping record type %q: %w", t, err)
		}
	}

	// F3 (code review A) — a SCOPED sweep (in.RecordType set) only walked
	// `types` above, so res.RecordTypeByPath held identity for the SWEPT
	// type alone. checkRelationEdge's wrong-type check below reads that map
	// to learn what type a relation's TARGET actually is — and a target of a
	// perfectly clean, correctly-typed cross-type relation (widget.maker ->
	// foundry, scoped to record_type=widget) is never scanned, so
	// typeByPath[target] came back "" and every such target was reported as
	// "an ordinary note, not a record, expected foundry". Unscoped sweeps
	// never showed this: types == schemas.Types() there, so every target's
	// type was already known.
	//
	// The fix keeps the scoped ScanRecords calls above (they are what stays
	// inside the store's B1 bound and what drives duplicate-id/orphan-row
	// findings) and adds one MORE scan, per OTHER declared type, whose only
	// job is filling in RecordTypeByPath so a target's real type is known —
	// it records no findings of its own, because a record of an out-of-scope
	// type is not itself in scope. Each such scan is still ONE declared type
	// at a time, so it stays inside B1 the same way the scoped loop does; it
	// costs nothing at all on an unscoped sweep, where every declared type is
	// already in `types`.
	if in.Schemas != nil && strings.TrimSpace(in.RecordType) != "" {
		scoped := make(map[string]bool, len(types))
		for _, t := range types {
			scoped[t] = true
		}
		for _, t := range in.Schemas.Types() {
			if scoped[t] {
				continue
			}
			err := in.Store.ScanRecords(ctx, t, func(c IndexedRecord) error {
				res.RecordTypeByPath[c.Path] = c.RecordType
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf(
					"knowledge: resolving relation-target types for record type %q: %w", t, err)
			}
		}
	}

	// FR-039 — a duplicate identifier names EVERY path and states that
	// neither is preferred. "Neither is preferred" is the substance: the
	// system will not pick one, so the operator must.
	dupKeys := make([]string, 0, len(idOwners))
	for k, paths := range idOwners {
		if len(paths) > 1 {
			dupKeys = append(dupKeys, k)
		}
	}
	sort.Strings(dupKeys)
	for _, k := range dupKeys {
		paths := append([]string(nil), idOwners[k]...)
		sort.Strings(paths)
		id := k[strings.Index(k, "\x00")+1:]
		sink.add(CategoryDuplicateID, paths[0], fmt.Sprintf(
			"%s — %s; neither is preferred", id, strings.Join(paths, " and ")))
	}

	// Relations. Resolution goes through the SAME NoteIndex ordinary
	// wikilinks use, so a relation and a body link never disagree about where
	// one target name points.
	for _, t := range types {
		sc, ok := in.Schemas.Get(t)
		if !ok {
			continue
		}
		err := in.Store.ScanRelations(ctx, t, func(edge IndexedRelation) error {
			checkRelationEdge(sink, sc, edge, in.Resolver, res.RecordTypeByPath)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("knowledge: sweeping relations of record type %q: %w", t, err)
		}
	}
	return res, nil
}

// checkRelationEdge reports one relation edge that does not lead where the
// schema says it should.
func checkRelationEdge(
	sink *findingSink,
	sc *records.Schema,
	hit IndexedRelation,
	resolver *NoteIndex,
	typeByPath map[string]string,
) {
	prop, declared := sc.Property(hit.Property)
	if !declared {
		// The schema no longer declares this property. That is a schema
		// change, not a broken link, and knowledge_configure's cascade report is
		// where it belongs — reporting it here as an unresolved relation
		// would name the wrong fault.
		return
	}
	who := fmt.Sprintf("%s %s", identityLabel(hit.RecordID), hit.Property)

	if resolver == nil {
		return
	}
	res := resolver.Resolve(hit.Path, Link{Kind: LinkWikilink, Target: hit.Target})
	if res.State != ResolveResolved {
		detail := fmt.Sprintf("%s -> [[%s]] — no note resolves", who, hit.Target)
		if near := nearestByFoldedName(resolver, hit.Target); near != "" {
			detail += "; nearest: " + near
		}
		sink.add(CategoryUnresolvedRelation, hit.Path, detail)
		return
	}
	// FR-034 — a relation that resolves to a note of the wrong record type.
	// A property that declares no `to:` constrains nothing, so there is
	// nothing here to be wrong about.
	if prop.To == "" {
		return
	}
	got := typeByPath[res.To]
	if got == prop.To {
		return
	}
	sink.add(CategoryWrongType, hit.Path, fmt.Sprintf(
		"%s -> [[%s]] is %s, expected %s",
		who, hit.Target, noteTypeLabel(got), prop.To))
}

// nearestByFoldedName suggests a note whose basename differs from the target
// ONLY IN CASE.
//
// This is deliberately EXACT rather than fuzzy. A Levenshtein suggestion over
// note names would confidently propose a wrong note, and a wrong suggestion in
// an integrity report is worse than none: the operator acts on it. A
// case-only difference is a fact, not a guess — the file is right there and
// the link is one keystroke from working.
func nearestByFoldedName(resolver *NoteIndex, target string) string {
	target = strings.TrimSpace(target)
	if target == "" || resolver == nil {
		return ""
	}
	want := records.FoldKey(trimMarkdownExt(target))
	if want == "" {
		return ""
	}
	best := ""
	for _, p := range resolver.Paths() {
		base := trimMarkdownExt(pathBase(p))
		if records.FoldKey(base) != want {
			continue
		}
		if best == "" || p < best {
			best = p
		}
	}
	return best
}

// pathBase is the last slash-separated element of a collection-relative path.
func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// identityLabel renders a record's identifier, or says it has none rather
// than leaving a gap in the sentence.
func identityLabel(id string) string {
	if strings.TrimSpace(id) == "" {
		return "a record with no identifier"
	}
	return id
}

// noteTypeLabel renders a target's record type for the wrong-type finding.
func noteTypeLabel(t string) string {
	if strings.TrimSpace(t) == "" {
		return "an ordinary note, not a record"
	}
	return "a note of type " + t
}

// declaredTypesToSweep is the type list one sweep covers.
func declaredTypesToSweep(schemas *records.SchemaSet, only string) []string {
	if schemas == nil {
		return nil
	}
	if strings.TrimSpace(only) != "" {
		if _, ok := schemas.Get(only); ok {
			return []string{only}
		}
		return nil
	}
	return schemas.Types()
}

// ---------------------------------------------------------------------------
// The whole sweep
// ---------------------------------------------------------------------------

// IntegrityOptions is one check_integrity call.
type IntegrityOptions struct {
	// FS and Root locate the collection.
	FS   LinkFS
	Root CollectionRoot
	// CollectionName is what a refusal calls the collection.
	CollectionName string
	// Schemas is the vault's declared record types.
	Schemas *records.SchemaSet
	// Store is the properties index, or nil.
	Store PropertyIndexReader
	// RecordType narrows the sweep to one declared type. Unknown is REFUSED
	// with the declared names listed (FR-024), never answered empty.
	RecordType string
	// SweepLimit and FindingsPerCategory default to the package constants.
	// They are settable so a test can drive a clamp at a corpus it can
	// actually build; separate tests pin the production values, because a
	// bound only ever exercised through an override is a bound that could be
	// anything in production.
	SweepLimit          int
	FindingsPerCategory int
}

// CheckIntegrity runs the whole bounded sweep.
//
// The order matters and is the design:
//
//  1. WALK the collection. This opens nothing.
//  2. Check the sweep limit against what the walk found, and REFUSE above it.
//     Before the expensive work, so it is a bound rather than a postmortem.
//  3. Build the link graph (this is what opens every note).
//  4. Run the typed half. An FR-020h refusal from it is folded into the
//     report as NOT RUN, by name — it does not fail the whole call, because
//     the wikilink and orphan checks still run on such a build and the spec
//     says so in the same sentence.
func CheckIntegrity(ctx context.Context, opts IntegrityOptions) (*IntegrityReport, error) {
	sweepLimit := opts.SweepLimit
	if sweepLimit <= 0 {
		sweepLimit = IntegritySweepLimit
	}
	perCategory := opts.FindingsPerCategory
	if perCategory <= 0 {
		perCategory = IntegrityFindingsPerCategory
	}

	if strings.TrimSpace(opts.RecordType) != "" {
		if opts.Schemas == nil {
			return nil, &UnknownRecordTypeError{Requested: opts.RecordType}
		}
		if _, ok := opts.Schemas.Get(opts.RecordType); !ok {
			return nil, &UnknownRecordTypeError{
				Requested: opts.RecordType,
				Declared:  opts.Schemas.Types(),
			}
		}
	}

	walk, err := WalkContained(opts.FS, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("knowledge: check_integrity walking the collection: %w", err)
	}
	notes := 0
	existing := make(map[string]struct{}, len(walk.Files))
	for _, f := range walk.Files {
		existing[f] = struct{}{}
		if IsMarkdownPath(f) {
			notes++
		}
	}
	if limitErr := checkSweepLimit(opts.CollectionName, notes, sweepLimit); limitErr != nil {
		return nil, limitErr
	}

	graph, err := BuildLinkGraph(opts.FS, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("knowledge: check_integrity building the link graph: %w", err)
	}

	sink := newFindingSink(perCategory)

	typed, typedErr := TypedIntegrity(ctx, TypedIntegrityInput{
		Store:         opts.Store,
		Schemas:       opts.Schemas,
		Resolver:      graph.index,
		ExistingFiles: existing,
		RecordType:    opts.RecordType,
	}, sink)
	if typedErr != nil {
		// NOT RUN, by name, in every typed category — never zero findings.
		// AC-D6: a build that could not check duplicate identifiers must not
		// report that it found none.
		for cat := range typedCategories {
			sink.notRun(cat, typedErr.Error())
		}
	}
	if opts.RecordType != "" && typedErr != nil {
		// A record_type scope needs the index to know WHICH notes are of that
		// type. Without it the wikilink half would silently widen to the whole
		// vault while the report still said it was scoped — so the call is
		// refused instead.
		return nil, fmt.Errorf(
			"check_integrity cannot be scoped to record_type=%q here: %w — run it unscoped, which still checks wikilinks and orphans",
			opts.RecordType, typedErr)
	}

	inScope := func(path string) bool {
		if opts.RecordType == "" {
			return true
		}
		return typed != nil && typed.RecordTypeByPath[path] == opts.RecordType
	}

	for _, l := range graph.Unresolved() {
		if !inScope(l.From) {
			continue
		}
		sink.add(CategoryBrokenLink, l.From, fmt.Sprintf(
			"%s -> [[%s]] — %s (ordinary wikilink, not a relation)",
			l.From, l.Target, unresolvedReasonText(l.Reason)))
	}
	for _, n := range graph.Orphans() {
		if !inScope(n) {
			continue
		}
		sink.add(CategoryOrphan, n, fmt.Sprintf("%s — no note links to it", n))
	}

	// Issue 10 / V1 — colliding note names. Walked here, off walk.Files, rather
	// than off the link graph's Ambiguous() edges: the ambiguity is a property
	// of the vault's NAMESPACE (two notes share a basename), and it must be
	// reported whether or not any wikilink currently points at the name. An
	// edge-driven check would miss a collision nobody has linked to yet — the
	// one an agent is about to create a broken reference into.
	checkAmbiguousNames(sink, walk.Files, inScope)

	return &IntegrityReport{
		ScopeLabel: integrityScopeLabel(opts.CollectionName, opts.RecordType),
		NotesSwept: notes,
		Categories: sink.results(),
	}, nil
}

// checkAmbiguousNames reports every basename shared by two or more markdown
// notes in scope (Issue 10 / V1).
//
// The key is the CASE-FOLDED, extension-stripped basename, the exact key
// NoteIndex resolves a bare [[wikilink]] against — so a collision reported here
// is a collision a link would actually hit. One finding per colliding name; it
// names every note that carries the name, in sorted order, because the operator
// must decide which keeps it. A name only one note carries is not reported.
//
// A record_type scope narrows via inScope on the members: a collision is
// reported when at least one of the colliding notes is in scope, since that is
// the note whose links the scoped sweep is answering for.
func checkAmbiguousNames(sink *findingSink, files []string, inScope func(string) bool) {
	byName := map[string][]string{}
	for _, f := range files {
		if !IsMarkdownPath(f) {
			continue
		}
		key := records.FoldKey(trimMarkdownExt(pathBase(f)))
		if key == "" {
			continue
		}
		byName[key] = append(byName[key], f)
	}
	keys := make([]string, 0, len(byName))
	for k := range byName {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		paths := byName[k]
		if len(paths) < 2 {
			continue
		}
		anyInScope := false
		for _, p := range paths {
			if inScope(p) {
				anyInScope = true
				break
			}
		}
		if !anyInScope {
			continue
		}
		sort.Strings(paths)
		display := trimMarkdownExt(pathBase(paths[0]))
		sink.add(CategoryAmbiguousName, paths[0], fmt.Sprintf(
			"[[%s]] is ambiguous — %d notes share this name: %s; rename one, or link by a path-qualified target",
			display, len(paths), strings.Join(paths, ", ")))
	}
}

// checkSweepLimit is FR-075a's refusal, as a pure function of a count.
//
// It is separate from the walk so the bound can be asserted AT ITS BOUNDARY
// against the production constant, without building a 100,001-note corpus —
// which is a corpus nobody would build, which is how a bound comes to be
// tested only through an override and to be anything at all in production.
func checkSweepLimit(collection string, notes, limit int) error {
	if notes <= limit {
		return nil
	}
	return &SweepTooLargeError{Collection: collection, Notes: notes, Limit: limit}
}

// unresolvedReasonText renders why a link did not resolve, in words. The
// reasons are not equivalent — "no note resolves" is a typo the operator can
// fix, "the link leaves the collection" is a different problem entirely — and
// collapsing them into one message loses the distinction FR-042 records.
func unresolvedReasonText(r UnresolvedReason) string {
	switch r {
	case ReasonEmptyTarget:
		return "the link names nothing"
	case ReasonAbsoluteTarget:
		return "the link names an absolute filesystem path"
	case ReasonOutsideRoot:
		return "the link leaves the collection"
	default:
		return "no note resolves"
	}
}

// integrityScopeLabel is what the report says it swept.
func integrityScopeLabel(collection, recordType string) string {
	switch {
	case recordType != "" && collection != "":
		return fmt.Sprintf("record type %s in %s", recordType, collection)
	case recordType != "":
		return "record type " + recordType
	case collection != "":
		return collection
	default:
		return "whole vault"
	}
}
