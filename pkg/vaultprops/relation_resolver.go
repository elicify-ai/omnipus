// Omnipus — ADR-068 D5.1 / FR-031: the wikilink -> file -> record identity
// resolver, in production.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS LIVES HERE, NOT IN pkg/records
//
// D5.1 draws the resolution in two steps: "on disk, a quoted wikilink...
// in the index, the target's record ID, resolved at index time by following
// the wikilink to a file and reading its id." The first half of that —
// wikilink -> file — is pkg/knowledge's own resolution order (NoteIndex,
// FR-040), the SAME mechanism an ordinary body link goes through and the
// SAME mechanism check_integrity's checkRelationEdge already calls, so a
// relation and a body link can never disagree about where a name points. The
// second half — file -> record identity — is the properties index
// (pkg/records/propindex). Both are needed to answer records.RelationResolver
// (compare_oracle.go: "func(link Wikilink) (recordID string, ok bool)"), and
// pkg/records may import neither (doc.go: it depends on nothing else in
// Omnipus). This package already exists to hold exactly this join — see
// reader.go's header for the cycle this avoids — so the resolver belongs
// beside it rather than inventing a second cycle-break package.
// ---------------------------------------------------------------------------
package vaultprops

import (
	"context"
	"log/slog"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// RelatedIdentity is what resolving a wikilink all the way to a record
// actually learns — more than records.RelationResolver's signature can
// carry (it returns an identity alone, R-8's whole comparison), but exactly
// what a caller with SCHEMA CONTEXT needs to tell "the target is missing"
// (FR-033) apart from "the target exists but is the wrong type" (FR-034) —
// pkg/knowledge/integrity.go renders both, distinctly, and this is the same
// distinction made available at query time rather than only at sweep time.
type RelatedIdentity struct {
	// Path is the collection-relative path the wikilink resolved to.
	Path string
	// RecordType is the declared type of the record living at Path, or "" if
	// the note declares none (an ordinary note, not a record at all).
	RecordType string
	// RecordID is the record's identifier, or "" if Path names a note with
	// no declared record type. R-8 compares by THIS, never by the wikilink's
	// own text.
	RecordID string
	// Ambiguous reports that the wikilink's target NAME matched more than one
	// note and Path is the FR-040 tie-break winner among them — the exact
	// fact pkg/knowledge.ResolvedLink.Ambiguous carries, preserved rather
	// than discarded here (FR-041: "the link still resolves; the ambiguity
	// is reported IN ADDITION, so determinism never hides it"). A caller
	// with schema context (vault_find's comparator, check_integrity) can
	// then report the ambiguity itself instead of presenting the tie-break
	// winner as the only possible reading.
	Ambiguous bool
	// Candidates lists every note the target name matched, in tie-break
	// order, when Ambiguous. Nil otherwise.
	Candidates []string
}

// HasIdentity reports whether the resolved note is actually a record — a
// note that exists but carries no id has a Path and nothing else, and R-8
// has nothing to compare (ADR-068 O-5: reported as missing, the cause is
// not guessed at — a file with no record identity is, for R-8's purposes,
// indistinguishable from no file at all).
func (r RelatedIdentity) HasIdentity() bool { return r.RecordID != "" }

// RelationResolver resolves an on-disk wikilink (D5.1) to the identity of
// the record it names, through the SAME two steps D5.1 specifies: wikilink
// -> file (pkg/knowledge.NoteIndex), then file -> record id
// (pkg/records/propindex.Store).
//
// It is built fresh per call — per vault_find/vault_describe request — and
// memoises what it resolves for the lifetime of that one call. A relation
// property is commonly `many: true` and a corpus commonly repeats the same
// target across many source records (every deal pointing at one company),
// so the memo is not an optimisation nicety: without it, grouping 10,000
// deals by their company would issue up to 10,000 redundant point lookups
// against a population that resolves to a handful of distinct companies.
type RelationResolver struct {
	ctx   context.Context
	notes *knowledge.NoteIndex
	store propindex.Store

	mu    sync.Mutex
	cache map[string]RelatedIdentity
	miss  map[string]bool
	// ambiguousWarned is every wikilink TARGET TEXT this resolver has already
	// logged an ambiguity warning for. Ambiguity (like the resolved path
	// itself) is a function of the target text alone — NoteIndex.Resolve
	// computes basenameMatches from l.Target only, never from the source
	// note — so a `many: true` relation with thousands of source records all
	// naming the same ambiguous target logs ONCE per call, not once per
	// edge, the same economy this file's path cache already applies to
	// identity lookups.
	ambiguousWarned map[string]bool
}

// NewRelationResolver builds a resolver over an already-built NoteIndex and
// an already-open properties index. Both may be nil; every method below
// answers "not resolved" rather than panicking, because a resolver with
// nothing to resolve against is a legitimate degraded state (ADR-068 O-5's
// posture applied to the resolver's own dependencies, not only to its
// targets) and vault_find already has its own refusal for a missing store.
func NewRelationResolver(ctx context.Context, notes *knowledge.NoteIndex, store propindex.Store) *RelationResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return &RelationResolver{ctx: ctx, notes: notes, store: store, cache: map[string]RelatedIdentity{}}
}

// AsFunc adapts this resolver to records.RelationResolver — the seam
// Stage 2's comparator (pkg/records/compare_oracle.go) was built against.
// A nil resolver adapts to a nil func, which the comparator already treats
// as "no resolver configured" (compare_oracle.go: "the zero Comparator is
// usable").
func (r *RelationResolver) AsFunc() records.RelationResolver {
	if r == nil {
		return nil
	}
	return func(link records.Wikilink) (string, bool) {
		id, ok := r.ResolveIdentity(link)
		if !ok || !id.HasIdentity() {
			return "", false
		}
		return id.RecordID, true
	}
}

// Resolve implements the identity-only half — records.RelationResolver's own
// shape — directly, for a caller that wants exactly that function value
// without going through AsFunc's closure. It is otherwise identical to
// AsFunc's behaviour.
func (r *RelationResolver) Resolve(link records.Wikilink) (string, bool) {
	id, ok := r.ResolveIdentity(link)
	if !ok || !id.HasIdentity() {
		return "", false
	}
	return id.RecordID, true
}

// ResolveIdentity is D5.1's two steps, spelled out: the wikilink resolves to
// a FILE through pkg/knowledge's own resolution order, and the file resolves
// to a RECORD through the properties index. ok=false means the wikilink
// itself did not resolve to any file in the collection — FR-033's "the
// target does not exist". ok=true with !HasIdentity() means the file exists
// but is not a record of any declared type — a caller with the property's
// declared `to:` in hand can then tell that apart from FR-034's "resolves,
// but to the wrong type" (RecordType is populated either way a caller with
// schema context needs to compare it).
func (r *RelationResolver) ResolveIdentity(link records.Wikilink) (RelatedIdentity, bool) {
	if r == nil || r.notes == nil || r.store == nil {
		return RelatedIdentity{}, false
	}
	rl := r.notes.Resolve("", knowledge.Link{Kind: knowledge.LinkWikilink, Target: link.Target})
	if rl.State != knowledge.ResolveResolved {
		return RelatedIdentity{}, false
	}
	id, ok := r.identityAt(rl.To)
	if !ok {
		return id, false
	}
	// F9 (code review A) — rl.Ambiguous/.Candidates were being discarded
	// here: the link still resolves to the FR-040 tie-break winner, but
	// R-8's guarantee is that determinism never HIDES the ambiguity, only
	// resolves it deterministically. Carry both onto the identity a caller
	// with schema context can report, and warn once per distinct target text
	// so the ambiguity is visible even to a caller still going through the
	// narrow records.RelationResolver shape (Resolve/AsFunc below), which
	// cannot carry it in its own return value.
	if rl.Ambiguous {
		id.Ambiguous = true
		id.Candidates = rl.Candidates
		r.warnAmbiguousOnce(link.Target, rl.To, rl.Candidates)
	}
	return id, true
}

// warnAmbiguousOnce logs one warning per distinct wikilink target text this
// resolver has resolved ambiguously, the same "reported, not swallowed"
// posture identityAt already applies to a store failure — a caller using the
// narrow records.RelationResolver shape (a plain (string, bool)) has no field
// to carry Ambiguous/Candidates through, so the operator's log is the only
// surface left for it in that path.
func (r *RelationResolver) warnAmbiguousOnce(target, resolvedTo string, candidates []string) {
	r.mu.Lock()
	if r.ambiguousWarned == nil {
		r.ambiguousWarned = map[string]bool{}
	}
	if r.ambiguousWarned[target] {
		r.mu.Unlock()
		return
	}
	r.ambiguousWarned[target] = true
	r.mu.Unlock()
	slog.Warn("vaultprops: relation target name is ambiguous; resolved to the FR-040 tie-break winner",
		"target", target, "resolved_to", resolvedTo, "candidates", candidates)
}

// identityAt is the second step, memoised. A path that resolved once in this
// call resolves the same way every later time — the properties index does
// not change mid-request — so a repeat lookup is served from the map rather
// than re-querying the store.
func (r *RelationResolver) identityAt(path string) (RelatedIdentity, bool) {
	r.mu.Lock()
	if id, ok := r.cache[path]; ok {
		r.mu.Unlock()
		return id, true
	}
	if r.miss[path] {
		r.mu.Unlock()
		return RelatedIdentity{}, false
	}
	r.mu.Unlock()

	// A point lookup by exact path, expressed as propindex's own narrowing
	// primitive (Selector.PathPrefix, FR-060's scope mechanism) rather than a
	// new query shape. It is bounded to at most one real match: paths are
	// individual files, so no second indexed row can carry this exact path
	// as a strict prefix of its own — and the visit callback below still
	// checks c.Path == path exactly, rather than trusting the prefix alone,
	// so a same-prefixed sibling (however that could arise) can never be
	// mistaken for the note actually being resolved.
	var found RelatedIdentity
	var hit bool
	err := r.store.Candidates(r.ctx, propindex.Selector{PathPrefix: path},
		func(c propindex.Candidate) (propindex.Verdict, error) {
			if c.Path == path {
				found = RelatedIdentity{Path: c.Path, RecordType: c.RecordType, RecordID: c.RecordID}
				hit = true
			}
			// REJECTED, always: a point lookup keeps no survivor, and
			// propindex.BoundSurvivors (B2) is a bound on ANSWERS, not on
			// resolution plumbing that discards everything it reads.
			return propindex.Rejected, nil
		})

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		// A store failure resolves to "not found" (ADR-068 O-5: whatever the
		// cause, an unresolvable target is reported as missing, and the
		// cause is not guessed at) — but it is not swallowed silently: it is
		// distinguishable from a genuine miss in the operator's logs, which
		// a caller comparing two booleans could never tell apart on its own.
		slog.Warn("vaultprops: relation resolver could not read the properties index",
			"path", path, "error", err)
		if r.miss == nil {
			r.miss = map[string]bool{}
		}
		r.miss[path] = true
		return RelatedIdentity{}, false
	}
	if !hit {
		if r.miss == nil {
			r.miss = map[string]bool{}
		}
		r.miss[path] = true
		return RelatedIdentity{}, false
	}
	r.cache[path] = found
	return found, true
}
