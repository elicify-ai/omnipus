// Omnipus — ADR-068 D15.3 / spec FR-065, FR-076, AC-F2: the `near`/`hops`
// relation-edge WALK, and its composition with words/type/filter.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultfind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// A SECOND, GRAPH-SHAPED FIXTURE
//
// fixture_test.go's greenhouse corpus is one record type with a `bed` relation
// declared but no `bed` schema behind it — near/hops needs an ACTUAL two-type
// graph to cross (D6/D10: a path from a bed to a plant crosses record types by
// definition), so this is its own schema pair rather than an extension of the
// shared fixture. Same non-CRM "greenhouse" vocabulary (ADR-068 D0), a second,
// independent declaration of it — nothing here is shared state with
// fixture_test.go, which several other Stage 3 agents are also extending.
// ---------------------------------------------------------------------------

// nearSet loads the two-type schema through records.LoadSchemas, the path
// production uses, same discipline fixture_test.go's plantSet follows.
func nearSchemaSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bed.yaml"), []byte(nearBedYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(bed.yaml): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plant.yaml"), []byte(nearPlantYAML), 0o600); err != nil {
		t.Fatalf("WriteFile(plant.yaml): %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schema was rejected: %v", report.Rejections)
	}
	return set
}

const nearBedYAML = `
schema_version: 1
type: bed
label: Bed
identity:
  prefix: BED
properties:
  name: { type: text, required: true }
  zone: { type: enum, values: [greenhouse, patio] }
`

const nearPlantYAML = `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  species:   { type: text, required: true }
  bed:       { type: relation, to: bed }
  companion: { type: relation, to: plant }
`

// nearFixture is a live properties index over a small relation GRAPH, plus a
// resolver this test controls directly rather than depending on however the
// eventual production wikilink resolver interprets a name or a path — the
// production resolver is out of this package's scope entirely (doc.go: "there
// is exactly ONE ... resolution ... this package calls it and reimplements
// neither"), so this stub is what nearWikilink's contract is tested AGAINST,
// not a guess at its implementation.
type nearFixture struct {
	t       *testing.T
	store   propindex.Store
	set     *records.SchemaSet
	text    *stubText
	targets map[string]string // wikilink Target text -> record identity
}

func newNearFixture(t *testing.T) *nearFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return &nearFixture{
		t: t, store: store, set: nearSchemaSet(t),
		text:    &stubText{hits: map[string]TextHit{}},
		targets: map[string]string{},
	}
}

func (f *nearFixture) write(path, src string) {
	f.t.Helper()
	b := []byte(src)
	rec := records.ParseRecord(path, b)
	sc, _ := f.set.Get(rec.TypeName())
	rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
	if err := f.store.UpsertNote(context.Background(), rows); err != nil {
		f.t.Fatalf("UpsertNote(%s): %v", path, err)
	}
	f.text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
}

// bed writes a bed record and registers its display name as a resolvable
// wikilink target — the same target text `near=<name>` and `bed: "[[name]]"`
// both spell.
func (f *nearFixture) bed(id, name, zone string) {
	f.t.Helper()
	f.write(fmt.Sprintf("garden/beds/%s.md", id), fmt.Sprintf(`---
type: bed
id: %s
name: %s
zone: %s
---
`, id, name, zone))
	f.targets[name] = id
}

// plant writes a plant record whose `bed` and (optionally) `companion`
// relations are wikilinks the resolver already knows about — bed and
// companionID are Target TEXT, not paths (D5.1: what is on disk is the
// Target the resolver joins on, not the file it eventually opens).
func (f *nearFixture) plant(id, species, bedName, companionID string) {
	f.t.Helper()
	companion := ""
	if companionID != "" {
		companion = fmt.Sprintf("companion: \"[[%s]]\"\n", companionID)
	}
	f.write(fmt.Sprintf("garden/plants/%s.md", id), fmt.Sprintf(`---
type: plant
id: %s
species: %s
bed: "[[%s]]"
%s---
`, id, species, bedName, companion))
	// A plant is itself a legal wikilink target (companion points at one by
	// its ID directly) — the identity mapping is trivial because the target
	// text and the record identity are chosen to be the same string.
	f.targets[id] = id
}

func (f *nearFixture) resolve(w records.Wikilink) (string, bool) {
	id, ok := f.targets[w.Target]
	return id, ok
}

func (f *nearFixture) deps() Deps {
	return Deps{Schemas: f.set, Store: f.store, Text: f.text, Resolve: f.resolve, Epoch: 8814}
}

// ---------------------------------------------------------------------------
// THE CORPUS EVERY COMPOSITION/BOUND/DETERMINISM TEST BELOW SHARES
//
// Edges (undirected, as the graph is built):
//
//	BED-GH <-> PL-0001   (PL-0001.bed = Greenhouse)
//	BED-GH <-> PL-0002   (PL-0002.bed = Greenhouse)
//	BED-PT <-> PL-0003   (PL-0003.bed = Patio)
//	BED-PT <-> PL-0004   (PL-0004.bed = Patio)
//	PL-0004 <-> PL-0001  (PL-0004.companion = PL-0001)
//
// So from BED-GH: hop 1 reaches {PL-0001, PL-0002}; hop 2 additionally reaches
// PL-0004 (via PL-0001's companion edge) but NOT BED-PT and NOT PL-0003 — that
// would need a THIRD hop (BED-GH -> PL-0001 -> PL-0004 -> BED-PT), which is
// exactly the shape that proves hops is a real bound and not "everything
// eventually connected".
// ---------------------------------------------------------------------------

func gardenCorpus(t *testing.T) *nearFixture {
	t.Helper()
	f := newNearFixture(t)
	f.bed("BED-GH", "Greenhouse", "greenhouse")
	f.bed("BED-PT", "Patio", "patio")
	f.plant("PL-0001", "Monstera", "Greenhouse", "")
	f.plant("PL-0002", "Fern", "Greenhouse", "")
	f.plant("PL-0003", "Cactus", "Patio", "")
	f.plant("PL-0004", "Basil", "Patio", "PL-0001")
	return f
}

func withNear(near string, hops int) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) {
		r.Near = &near
		h := hops
		r.Hops = &h
	}
}

func withWords(words string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { r.Words = &words }
}

// ---------------------------------------------------------------------------
// 1. TRAVERSAL AT HOPS 1 AND 2
// ---------------------------------------------------------------------------

func TestNear_HopOneReachesOnlyDirectNeighbours(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 1)))
	got := rowIDs(resp)
	want := []string{"PL-0001", "PL-0002"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("hops=1 from Greenhouse: got %v, want %v (PL-0004 needs a second hop and must be ABSENT)", got, want)
	}
}

func TestNear_HopTwoReachesTheSecondRingButNotTheThird(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 2)))
	got := rowIDs(resp)
	want := []string{"PL-0001", "PL-0002", "PL-0004"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("hops=2 from Greenhouse: got %v, want %v (PL-0003 is behind BED-PT, a THIRD hop away, and must be ABSENT)", got, want)
	}
}

// TestNear_OriginItselfIsReachableAtZeroHops matches ADR-067's own Neighborhood
// convention ("Nodes ... including the origin", pkg/knowledge/graph.go) — a
// type filter the origin itself satisfies must be able to return it.
func TestNear_OriginItselfIsReachableAtZeroHops(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("bed"), withNear("Greenhouse", 1)))
	got := rowIDs(resp)
	want := []string{"BED-GH"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near=Greenhouse type=bed: got %v, want %v (the seed's OWN record, no bed is within 1 hop of itself)", got, want)
	}
}

// TestNear_HopTwoExcludesTheThirdHopEvenAcrossTypes isolates the hop BOUNDARY
// from the type filter that TestNear_HopTwoReachesTheSecondRingButNotTheThird
// happens to also apply. BED-PT sits exactly THREE hops from Greenhouse
// (BED-GH -> PL-0001 -> PL-0004 -> BED-PT, via PL-0004's own `bed`), so
// `type=bed` alone would never have excluded it — only the hop count does.
//
// This is the test that catches an off-by-one that walks one hop too many
// (`level <= hops` instead of `level < hops`): under that mutation, hops=2
// reaches BED-PT, and the OTHER hops=2 test above does not notice, because
// its own `type=plant` filter throws BED-PT away anyway for an unrelated
// reason — a "the test passed" that would have meant nothing about hop
// counting specifically. Mutation-verified: this test is what actually
// fails when that mutation is introduced; the plant-typed one does not.
func TestNear_HopTwoExcludesTheThirdHopEvenAcrossTypes(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("bed"), withNear("Greenhouse", 2)))
	got := rowIDs(resp)
	want := []string{"BED-GH"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near=Greenhouse hops=2 type=bed: got %v, want %v "+
			"(BED-PT is a THIRD hop away and must stay absent)", got, want)
	}
}

// ---------------------------------------------------------------------------
// 2. COMPOSITION WITH FILTERS — AC-F2, both directions in ONE call
// ---------------------------------------------------------------------------

// TestNear_ComposesWithFilterAsIntersectionNotUnion is the mutation-critical
// test: a filter matching two records where exactly ONE is in-radius must
// return exactly that one — proving BOTH failure directions AC-F2 names in a
// single assertion, so a bug that turns the intersection into a union (or
// drops either half) cannot pass by accident:
//
//   - PL-0004 (Basil) is IN-RADIUS (hop 2) and MATCHES the filter -> present.
//   - PL-0003 (Cactus) MATCHES the filter and is OUT-OF-RADIUS -> absent.
func TestNear_ComposesWithFilterAsIntersectionNotUnion(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(
		withType("plant"),
		withNear("Greenhouse", 2),
		withFilter(leaf("species", "IN", "Basil", "Cactus")),
	))
	got := rowIDs(resp)
	want := []string{"PL-0004"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near+filter intersection: got %v, want %v — either the in-radius/filter-failing "+
			"record leaked in, or the filter-matching/out-of-radius record did (or the answer "+
			"is a union of the two conditions instead of their intersection)", got, want)
	}
}

// TestNear_ComposesWithFilterExcludesInRadiusRecordsThatFail is AC-F2's first
// half in isolation: PL-0001 and PL-0002 are BOTH in-radius but neither is
// Basil, so a near query with a Basil filter must show only PL-0004 — proven
// again here with `words` also in the mix, since FR-076 requires near to
// compose with words AND filter in the SAME call, not either alone.
func TestNear_ComposesWithWordsAndFilterTogether(t *testing.T) {
	f := gardenCorpus(t)
	// Only PL-0004's file is a word-search hit; PL-0001 is in-radius but is
	// NOT a word hit, so it must be excluded by the word half even though the
	// graph half would have admitted it.
	f.text.only = []string{"garden/plants/PL-0004.md"}

	resp := mustFind(t, f.deps(), req(
		withType("plant"),
		withNear("Greenhouse", 2),
		withWords("basil"),
		withFilter(leaf("species", "=", "Basil")),
	))
	got := rowIDs(resp)
	want := []string{"PL-0004"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near+words+filter: got %v, want %v", got, want)
	}

	// And the converse: make PL-0001 (in-radius, species Monstera) the ONLY
	// word hit. The filter still asks for Basil, which PL-0001 is not, so
	// nothing must be returned — a caller who reads BOTH conditions as
	// satisfied by DIFFERENT records must not see either one.
	f.text.only = []string{"garden/plants/PL-0001.md"}
	resp2 := mustFind(t, f.deps(), req(
		withType("plant"),
		withNear("Greenhouse", 2),
		withWords("basil"),
		withFilter(leaf("species", "=", "Basil")),
	))
	if len(resp2.Rows) != 0 {
		t.Fatalf("near(PL-0001 in radius)+words(PL-0001)+filter(species=Basil, which PL-0001 fails): "+
			"got %d rows, want 0", len(resp2.Rows))
	}
}

// ---------------------------------------------------------------------------
// 3. `near` THAT DOES NOT RESOLVE — a ZERO-HIT answer, never a refusal
// ---------------------------------------------------------------------------

func TestNear_UnresolvedSeedIsZeroHitNotRefusal(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("plant"), withNear("Nonexistent Note", 1)))
	if len(resp.Rows) != 0 {
		t.Fatalf("got %d rows for an unresolved near target, want 0", len(resp.Rows))
	}
	if !resp.Complete {
		t.Errorf("an unresolved near target must read as a legitimate zero-hit answer "+
			"(D3.2: absence is a state, not a fault) — complete=%v, want true", resp.Complete)
	}
}

// TestNear_NoResolverIsARefusalNotASilentEmptyNeighbourhood is the negative
// mirror of the zero-hit case above: a MISSING resolver is a capability gap,
// not an empty vault, and must say so — the same distinction Deps.Text draws
// (find.go's own comment: "A nil Text used to mean 'skip the check', which is
// the quiet degradation this package refuses everywhere else").
func TestNear_NoResolverIsARefusalNotASilentEmptyNeighbourhood(t *testing.T) {
	f := gardenCorpus(t)
	d := f.deps()
	d.Resolve = nil
	resp := mustRefuse(t, d, req(withType("plant"), withNear("Greenhouse", 1)))
	if resp.Problems[0].Code != generated.IndexUnavailable {
		t.Errorf("code = %s, want index_unavailable", resp.Problems[0].Code)
	}
}

// ---------------------------------------------------------------------------
// 4. BOUNDING — the traversal is where a query stops being bounded, and this
//    is the proof it does not
// ---------------------------------------------------------------------------

// withHopBound temporarily lowers the package var for the duration of one
// test, so the boundary can be exercised without writing 50,001 real relation
// rows for every assertion that needs to see both sides of it.
func withHopBound(t *testing.T, n int) {
	t.Helper()
	old := MaxHopTraversalEdges
	MaxHopTraversalEdges = n
	t.Cleanup(func() { MaxHopTraversalEdges = old })
}

// bulkBed writes n beds, each with ONE plant pointing at it — n relation
// edges, batched, the same pattern propindex's own bounds_test.go uses for
// its 50,001-row B1/B2 proofs (bulkCorpus), so this stays fast even though the
// SHAPE under test (edges, not candidate records) is different.
func bulkBeds(t *testing.T, store propindex.Store, set *records.SchemaSet, n int, offset int) {
	t.Helper()
	sc, _ := set.Get("plant")
	const batchSize = 2000
	batch := make([]propindex.NoteRows, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := store.(*propindex.Index).UpsertNotes(context.Background(), batch); err != nil {
			t.Fatalf("UpsertNotes: %v", err)
		}
		batch = batch[:0]
	}
	for i := range n {
		id := fmt.Sprintf("PL-BULK-%06d", offset+i)
		src := fmt.Sprintf("---\ntype: plant\nid: %s\nspecies: Sedum\nbed: \"[[bulk-target-%06d]]\"\n---\n", id, offset+i)
		rec := records.ParseRecord(fmt.Sprintf("garden/bulk/%s.md", id), []byte(src))
		batch = append(batch, propindex.BuildNoteRows(rec, sc, []byte(src), propindex.SourceHash([]byte(src))))
		if len(batch) == batchSize {
			flush()
		}
	}
	flush()
}

// TestNear_AtTheBoundSucceeds is the boundary's OTHER side: exactly the
// permitted number of edges must still answer, never refuse defensively early
// — a guard with an off-by-one (n >= limit instead of n > limit) would fail
// ONLY this test, which is why it exists alongside the over-bound test below
// rather than being inferred from it.
func TestNear_AtTheBoundSucceeds(t *testing.T) {
	f := gardenCorpus(t) // 5 edges already indexed by the shared corpus
	withHopBound(t, 5)

	resp := mustFind(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 2)))
	if len(resp.Problems) != 0 {
		for _, p := range resp.Problems {
			if p.Code == generated.HopTraversalBoundExceeded {
				t.Fatalf("exactly %d edges (the bound itself) refused: %v", 5, p.Reason)
			}
		}
	}
	want := []string{"PL-0001", "PL-0002", "PL-0004"}
	if got := rowIDs(resp); !stringSliceEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestNear_ExceedingTheBoundRefusesNamingTheRemedy is the over-bound case:
// ONE more edge than the fixed bound, ANYWHERE in workspace scope — not
// necessarily reachable from the seed at all — must refuse. That "anywhere"
// is deliberate and asserted: the graph scan cannot be narrowed by `near`,
// `hops`, `type` or `kind` (a path must be allowed to cross record types), so
// the bound is a property of the WORKSPACE's relation graph, not of this
// particular query, and the remedy must say so honestly rather than naming a
// filter that would not reduce it.
func TestNear_ExceedingTheBoundRefusesNamingTheRemedy(t *testing.T) {
	f := gardenCorpus(t) // 5 edges, none of them touching the bulk seeds below
	withHopBound(t, 5)
	bulkBeds(t, f.store, f.set, 1, 0) // the 6th edge, irrelevant to Greenhouse's own neighbourhood

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 1)))
	p := resp.Problems[0]
	if p.Code != generated.HopTraversalBoundExceeded {
		t.Fatalf("code = %s, want hop_traversal_bound_exceeded", p.Code)
	}
	if p.Fix == nil || *p.Fix == "" {
		t.Fatalf("a bound refusal with no remedy: %+v", p)
	}
	if containsFold(*p.Fix, "add a filter") || containsFold(*p.Fix, "add or tighten a filter") {
		t.Fatalf("the remedy names a filter, which does not shrink a scan `type`/`kind` cannot "+
			"narrow in the first place: %q", *p.Fix)
	}
}

// TestNear_NeverReturnsAPartialNeighbourhoodOnRefusal is the "worst outcome"
// the brief names explicitly: over the bound, rows must be EMPTY, not a
// silently truncated slice of the true neighbourhood presented as complete.
func TestNear_NeverReturnsAPartialNeighbourhoodOnRefusal(t *testing.T) {
	f := gardenCorpus(t)
	withHopBound(t, 5)
	bulkBeds(t, f.store, f.set, 3, 100)

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 2)))
	if len(resp.Rows) != 0 {
		t.Fatalf("a refused hop-bound query returned %d rows; a partial traversal presented "+
			"as complete is the worst outcome available here", len(resp.Rows))
	}
	if resp.Complete {
		t.Fatalf("complete=true on a refused query")
	}
}

// ---------------------------------------------------------------------------
// 5. DETERMINISM — traversal order must not depend on map iteration order
//
// Modelled on pkg/knowledge/rank_test.go's TestFuseRRF_TiesBreakOnPathNotMapOrder:
// run the SAME call 50 times and assert the row list — membership AND order —
// never varies. The reachable set itself is order-independent by construction
// (it is a plain BFS-reachability SET, not a "first path wins" structure), so
// this is the regression guard that keeps it that way rather than a proof the
// current code is fine by inspection.
// ---------------------------------------------------------------------------

func TestNear_DeterministicAcross50Runs(t *testing.T) {
	f := gardenCorpus(t)
	want := []string{"PL-0001", "PL-0002", "PL-0004"}
	for i := 0; i < 50; i++ {
		resp := mustFind(t, f.deps(), req(withType("plant"), withNear("Greenhouse", 2)))
		if got := rowIDs(resp); !stringSliceEqual(got, want) {
			t.Fatalf("run %d: near/hops traversal order or membership was non-deterministic: got %v, want %v", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stringSliceEqual(a, b []string) bool {
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

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && indexFold(s, sub) >= 0
}

// indexFold is a tiny ASCII case-insensitive substring search — this test
// file has no need of full Unicode folding (R-4's rule is about VALUES, not
// about a test's own assertion helper), only enough to catch "add a filter"
// however it happens to be capitalised.
func indexFold(s, sub string) int {
	ls, lsub := len(s), len(sub)
	if lsub == 0 {
		return 0
	}
	for i := 0; i+lsub <= ls; i++ {
		match := true
		for j := 0; j < lsub; j++ {
			a, b := s[i+j], sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
