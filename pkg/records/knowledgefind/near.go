// Omnipus — ADR-068 D15.3 / spec FR-065, FR-076, AC-F2: the relation EDGE
// TRAVERSAL `near`/`hops` walks, and its composition with every other filter.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHAT LIVES HERE, AND WHAT DOES NOT (doc.go's boundary, made concrete)
//
// Stage 2 already built the VALIDATION (request.go's applyHops, FR-065's
// third-hop refusal) and the CAPABILITY GATE (query.capabilities' relation-join
// entry, consulted in Find before any retrieval). Neither is touched here.
//
// What this file adds is the WALK: turning `near` + `hops` into the set of
// record identities a candidate must belong to, so findRecords can intersect
// it with `words` and the typed filter exactly the way it already intersects
// those two with each other.
//
// THE GRAPH IS UNDIRECTED. D5: "a relation is declared on one side and stored
// on one side; the inverse is derived." For a READER WALKING THE GRAPH a
// stored edge and its derived inverse are the SAME HOP — pkg/knowledge's own
// Neighborhood (ADR-067, which D15.3 names as near/hops' conceptual ancestor)
// already walks outbound and inbound links identically, and this graph does
// the same over TYPED relation edges instead of ordinary wikilinks.
//
// THE ORIGIN COUNTS AS "WITHIN 0 HOPS" and is included in the reachable set —
// again matching Neighborhood's own "Nodes ... including the origin". A query
// with a type filter the seed itself does not satisfy never notices; a query
// with no type filter (or one the seed DOES satisfy) is allowed to return the
// seed's own record, which is the answer a reader actually wants for "Acme and
// everything within 1 hop of it".
// ---------------------------------------------------------------------------

// MaxHopTraversalEdges bounds the relation-edge rows a near/hops query may
// visit while building its reachability graph.
//
// It reuses FR-065a's own reasoning rather than inventing a fourth number: "the
// total number of borrowed values a query may materialise is capped at 50,000
// — the same ceiling as FR-064's B1, deliberately, so there is one number to
// reason about." This traversal has the identical shape — an unbounded cost
// B1 never sees, because the graph scan CANNOT be narrowed by the query's own
// `type`/`kind` (a path from a company to a deal crosses record types by
// definition, D6/D10) — so it borrows the same ceiling for the same reason.
//
// It is a var, not a const, so a bound test can shrink it rather than writing
// 50,001 real relation rows for every assertion that needs to see the OTHER
// side of the boundary; production code never assigns it.
var MaxHopTraversalEdges = propindex.BoundNarrowedCandidates

// errHopTraversalBoundExceeded aborts the relation stream. It never reaches a
// caller: buildRelationGraph translates it into the wire refusal, exactly the
// way propindex's own BoundError never reaches knowledge_find's response either.
var errHopTraversalBoundExceeded = errors.New("knowledgefind: relation graph traversal exceeded its edge bound")

// nearWikilink turns `near`'s two accepted forms — a bare note path or name,
// or a [[wikilink]] — into ONE Wikilink, so both route through the SAME
// resolution seam relation VALUES already use (Deps.Resolve).
//
// This package does not own note-name or path resolution and must not grow a
// second one (doc.go's "there is exactly ONE ... this package calls both and
// reimplements neither", applied to resolution rather than comparison):
// comparator.ResolveRelation already IS the "on-disk reference text -> record
// identity" authority (R-8), and a bare path is exactly what D5.1 says a
// relation may hold on disk, so whatever the concrete resolver accepts for a
// stored relation value it must also accept here.
func nearWikilink(near string) records.Wikilink {
	if wl, ok := records.ParseWikilink(near); ok {
		return wl
	}
	return records.Wikilink{Target: near, Raw: "[[" + near + "]]"}
}

// relationGraph is the undirected reachability graph one query computes once
// and re-uses for every hop level it walks.
type relationGraph struct {
	adjacency map[string]map[string]bool
}

// add records ONE resolved edge in BOTH directions. A self-loop (a relation
// whose resolved target is its own owning record) and an edge with an empty
// identity on either end are dropped: they add no reachability an identity
// map does not already grant its own key, and a self-loop would otherwise
// make "reached" and "frontier" disagree about whether the seed is still
// worth re-visiting.
func (g *relationGraph) add(a, b string) {
	if a == "" || b == "" || a == b {
		return
	}
	if g.adjacency == nil {
		g.adjacency = map[string]map[string]bool{}
	}
	if g.adjacency[a] == nil {
		g.adjacency[a] = map[string]bool{}
	}
	if g.adjacency[b] == nil {
		g.adjacency[b] = map[string]bool{}
	}
	g.adjacency[a][b] = true
	g.adjacency[b][a] = true
}

// within returns every record identity reachable from seed in AT MOST hops
// undirected steps, INCLUDING seed itself.
//
// It is a pure SET computation: a node is reachable or it is not, and that
// answer does not depend on the ORDER edges were visited to discover it —
// Go's randomised map iteration changes which goroutine-local order the
// `range`s below run in from one call to the next, and the reachable SET is
// identical regardless, which is what TestNear_DeterministicAcross50Runs
// checks at the response's rendered ROW ORDER rather than at this
// intermediate structure (a set has no order to assert on by itself).
func (g *relationGraph) within(seed string, hops int) map[string]bool {
	reached := map[string]bool{seed: true}
	frontier := map[string]bool{seed: true}
	for level := 0; level < hops; level++ {
		next := map[string]bool{}
		for node := range frontier {
			for nb := range g.adjacency[node] {
				if !reached[nb] {
					next[nb] = true
				}
			}
		}
		if len(next) == 0 {
			break
		}
		for nb := range next {
			reached[nb] = true
		}
		frontier = next
	}
	return reached
}

// buildRelationGraph streams every relation edge in the caller's workspace
// scope EXACTLY ONCE and assembles it into an undirected adjacency graph.
//
// It cannot narrow by the query's own `type` or `kind`: a path from a company
// to a deal crosses record types by definition, so type-narrowing the SCAN
// would silently prune paths the query never asked to exclude — a worse
// defect than the one B1 exists to prevent, because it would look like a
// SMALLER answer rather than a WRONG one. `PathPrefix` (workspace scope,
// D15.5a) is the one narrowing this scan is allowed, and it is the caller's
// workspace, never the query's own argument.
//
// Bounded at MaxHopTraversalEdges, enforced as a STREAMING ABORT rather than a
// pre-count: the store exposes no COUNT over note_relations (ruling R-A keeps
// every aggregate off the SQL path — Stage 2 built exactly one aggregate,
// CountCandidates over `notes`, and note_relations is not `notes`), so unlike
// B1 this cannot be a precondition. It borrows FR-064's B2 SHAPE (a streaming
// abort during the work) while borrowing FR-065a's B1-sized NUMBER.
func buildRelationGraph(ctx context.Context, d Deps) (*relationGraph, *RefusalError) {
	if d.Store == nil {
		return nil, refuse(problem(generated.IndexUnavailable,
			"the properties index is not open, so the relation graph cannot be walked",
			"re-open the vault; run knowledge_describe check_integrity to see the index state"), nil)
	}

	g := &relationGraph{}
	sel := propindex.Selector{PathPrefix: d.PathPrefix}
	n := 0
	err := d.Store.Relations(ctx, sel, func(hit propindex.RelationHit) error {
		n++
		if n > MaxHopTraversalEdges {
			return errHopTraversalBoundExceeded
		}
		if hit.RecordID == "" {
			// A relation row with no owning record identity should not happen
			// for a declared relation property — BuildNoteRows only emits one
			// under a schema, and a schema-bearing note always has an ID —
			// but nothing here should ASSUME that and panic over a defect
			// that belongs to check_integrity, not to this query.
			return nil
		}
		target, ok := d.Resolve(records.Wikilink{
			Target:  hit.Relation.Target,
			Heading: hit.Relation.Heading,
			Display: hit.Relation.Display,
			Raw:     hit.Relation.Raw,
		})
		if !ok {
			// Unresolved or mistyped — D5.1's finding, reported by
			// knowledge_describe check_integrity. It leads nowhere for THIS
			// query: skipping it is not silence, because whatever record it
			// would have connected is still reachable by every OTHER edge
			// it has, and this response is not the one that reports broken
			// relations.
			return nil
		}
		g.add(hit.RecordID, target)
		return nil
	})
	if err != nil {
		if errors.Is(err, errHopTraversalBoundExceeded) {
			return nil, refuse(problem(generated.HopTraversalBoundExceeded,
				fmt.Sprintf("this workspace's relation graph holds more than %s edges; "+
					"near/hops scans every relation edge in scope to cross record types "+
					"safely, and cannot do that here as one call", group3(MaxHopTraversalEdges)),
				"there is no filter that narrows this scan — near/hops walks the whole "+
					"workspace's relation graph regardless of near or hops; mount a narrower "+
					"folder into this workspace, or read the neighbourhood a few knowledge_read "+
					"calls at a time instead"), err)
		}
		return nil, refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not stream relations: %v", err),
			"run knowledge_describe check_integrity"), err)
	}
	return g, nil
}

// nearReachable resolves q.near/q.hops into the set of record identities the
// candidate stream must intersect against.
//
// A nil map with a nil refusal means `near` did not resolve to any record at
// all — a legitimate ZERO-HIT answer (D3.2: absence is a state, not a fault),
// exactly the way a `words` search matching nothing is zero hits and not an
// error. It is NOT a refusal: "you spelled the note wrong" and "that note has
// no neighbours" are indistinguishable from here, and the caller finds out
// which by reading NEAREST INDEXED TERMS / knowledge_describe the same way a
// zero-hit word search already tells them.
//
// It returns TWO things a `near` query intersects against, and they are not the
// same set:
//
//   - reached — the record IDENTITIES within `hops` typed-relation steps of the
//     anchor's record (empty when the anchor is not a record). This is the graph
//     half, and it is keyed by record ID.
//   - anchorPath — the anchor NOTE's own collection-relative path, resolved
//     through Deps.ResolveNear, or "" when ResolveNear is unwired or the
//     reference names nothing on disk. This is how the origin counts as hop 0
//     even for an ORDINARY note that has no record identity and therefore never
//     appears in reached.
//
// A nil map AND an empty anchorPath, with a nil refusal, means `near` did not
// resolve to anything at all — a legitimate ZERO-HIT answer (D3.2: absence is a
// state, not a fault), exactly the way a `words` search matching nothing is zero
// hits and not an error. It is NOT a refusal: "you spelled the note wrong" and
// "that note has no neighbours" are indistinguishable from here, and the caller
// finds out which by reading NEAREST INDEXED TERMS / knowledge_describe the same
// way a zero-hit word search already tells them.
func nearReachable(ctx context.Context, d Deps, q *query) (reached map[string]bool, anchorPath string, _ *RefusalError) {
	if q.near == "" {
		return nil, "", nil
	}
	if d.Resolve == nil {
		// Falling through silently here would answer EVERY near/hops query
		// with an empty neighbourhood forever, indistinguishable from "that
		// note has no relations" — precisely the quiet degradation Deps.Text
		// is required, not optional, to prevent for the SAME reason.
		return nil, "", refuse(problem(generated.IndexUnavailable,
			"near/hops needs relation resolution, and this vault has none wired in",
			"re-open the vault; run knowledge_describe check_integrity to see the index state"), nil)
	}

	// The anchor note's OWN path (hop 0), resolved independently of whether it
	// is a record. This is what lets `near=<ordinary note> + words=<term>`
	// return the anchor when it contains the term — the field-reported case a
	// record-only near silently answered with zero.
	if d.ResolveNear != nil {
		if p, ok := d.ResolveNear(q.near); ok {
			anchorPath = p
		}
	}

	seed, ok := d.Resolve(nearWikilink(q.near))
	if !ok {
		// Not a record — but the anchor may still exist as an ordinary note,
		// in which case anchorPath above carries it as hop 0. reached is empty
		// (a plain note is not a graph node); the caller proceeds on anchorPath
		// alone rather than short-circuiting to zero.
		return map[string]bool{}, anchorPath, nil
	}
	g, refusal := buildRelationGraph(ctx, d)
	if refusal != nil {
		return nil, "", refusal
	}
	return g.within(seed, q.hops), anchorPath, nil
}
