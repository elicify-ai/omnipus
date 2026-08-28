// Package catalog is the in-memory, registry-fed provider catalog (ADR-067
// D1/D11): one 2.0.0 document — providers with nested models — validated on
// load and queried by the exact (provider id, model id) pair.
//
// # Shape
//
//   - document.go — the validated domain types (Document, Provider, Model).
//   - parse.go    — ParseDocument: JSON → Document under the FR-002 invariants
//     and the FR-033 URL rule; every rejection names the offending path.
//   - locality.go — DeriveLocality, the single "local endpoint" predicate
//     (FR-039) every other consumer reads.
//   - resolve.go  — Handle, what Resolve hands to the media pipeline and the
//     agent loop; a miss is a miss (FR-003), never a prefix-stripped hit.
//   - refresh.go  — the serialized refresh transaction (T067-04): pull →
//     degraded check → parse → schema gate → anti-downgrade → apply →
//     persist, with the FR-009 reason-keyed WARNs.
//   - store.go    — Boot and the persisted last-known-good under
//     $OMNIPUS_HOME/providers_catalog.json (FR-010, E6, E7).
//   - served.go   — the pre-serialised GET body + quoted strong SHA-256
//     ETag pair, swapped atomically with the snapshot (FR-017), and
//     Degraded() for /health (FR-037).
//
// # Boundaries
//
// This package imports neither pkg/providers, pkg/gateway, pkg/agent nor the
// generated wire types (spec §3 "Cluster Placement"); the factory imports the
// catalog, not the other way round. The wire envelope served by
// GET /providers/catalog is built by the gateway from Document (T067-10).
//
// # Embedded snapshot
//
// EmbeddedSnapshot is the committed copy of the assembly repository's daily
// release (refreshed by a scheduled pull request, never fetched at build —
// US-2.AC5). It is parsed by the gateway at boot, not at package init, so a
// corrupt snapshot degrades (E7) instead of panicking.
package catalog

import (
	_ "embed"
	"sync"
	"sync/atomic"
	"time"
)

// EmbeddedSnapshot is the committed providers_catalog.json — a byte-for-byte
// copy of the assembly repository's release document (schema 2.0.0, first
// landed from elicify-ai/omnipus-provider-catalog v2026.8.23.1 in T067-06),
// refreshed only by pull request (FR-006) and pinned by embed_test.go
// (T16–T19) and the hermetic-build CI gate (T48).
//
//go:embed data/providers_catalog.json
var EmbeddedSnapshot []byte

// pairKey is the exact lookup key (FR-003). Two strings, compared exactly —
// no trimming, no case folding, no prefix arithmetic.
type pairKey struct {
	provider string
	model    string
}

// snapshot is one immutable, fully indexed document, together with its
// pre-serialised serving pair. Catalog swaps whole snapshots atomically so
// readers never observe a torn index — and never a body from one apply
// with the ETag of another (FR-017, T34c).
type snapshot struct {
	doc        *Document
	byProvider map[string]*Provider
	byPair     map[pairKey]*Model
	served     servedPair
}

func index(doc *Document) *snapshot {
	s := &snapshot{
		doc:        doc,
		byProvider: make(map[string]*Provider, len(doc.Providers)),
		byPair:     make(map[pairKey]*Model),
	}
	for i := range doc.Providers {
		p := &doc.Providers[i]
		s.byProvider[p.ID] = p
		for j := range p.Models {
			s.byPair[pairKey{p.ID, p.Models[j].ID}] = &p.Models[j]
		}
	}
	return s
}

// Catalog serves the currently loaded document. It is safe for concurrent
// use: Resolve/Provider/Document read one atomically published snapshot;
// Apply publishes a new one or leaves the current one untouched.
type Catalog struct {
	cur atomic.Pointer[snapshot]

	// The refresh transaction (T067-04). refreshMu serializes Refresh
	// (FR-028); puller/store/log/nowFn are set at construction (Boot) and
	// never mutated afterwards.
	refreshMu sync.Mutex
	puller    Puller
	store     Store
	log       Logger
	nowFn     func() time.Time // test seam; nil → time.Now

	// stateMu guards the /health state and the applied hooks. Never held
	// across I/O or a hook call.
	stateMu            sync.Mutex
	lastRefreshErr     error
	degradedTransport  bool
	degradedReleaseErr error
	onApplied          []func()
}

// now returns the injected clock, defaulting to time.Now.
func (c *Catalog) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

// New returns an empty catalog: every lookup misses (FR-004), Document is
// nil. This is the E7 state — the gateway boots with no catalog.
func New() *Catalog {
	return &Catalog{}
}

// NewCatalog parses data as a 2.0.0 document and returns a catalog serving
// it. A rejected document yields a nil catalog and the parse error.
func NewCatalog(data []byte) (*Catalog, error) {
	c := New()
	if err := c.Apply(data); err != nil {
		return nil, err
	}
	return c, nil
}

// Apply validates data and, only on success, swaps it in as the served
// document with embedded provenance. On any error the previously loaded
// document is retained (US-1.AC2/AC3) — the caller decides what to log.
// The refresh transaction and the boot read use applyDoc directly with
// the correct ServedFrom marker.
func (c *Catalog) Apply(data []byte) error {
	doc, err := ParseDocument(data)
	if err != nil {
		return err
	}
	return c.applyDoc(doc, ServedEmbedded)
}

// applyDoc indexes doc, pre-serialises its serving pair (FR-017) and
// publishes both as one atomic snapshot swap.
func (c *Catalog) applyDoc(doc *Document, from ServedFrom) error {
	s := index(doc)
	pair, err := buildServed(doc, from, c.now())
	if err != nil {
		return err
	}
	s.served = pair
	c.cur.Store(s)
	return nil
}

// Document returns the served document, or nil when none is loaded. The
// pointer identifies one Apply; callers MUST treat it as read-only.
func (c *Catalog) Document() *Document {
	s := c.cur.Load()
	if s == nil {
		return nil
	}
	return s.doc
}

// Provider returns a deep copy of the provider row with the exact id
// (FR-030 picker fields included) and whether it exists. Aliases never
// resolve (FR-030).
func (c *Catalog) Provider(id string) (Provider, bool) {
	s := c.cur.Load()
	if s == nil {
		return Provider{}, false
	}
	p, ok := s.byProvider[id]
	if !ok {
		return Provider{}, false
	}
	return p.clone(), true
}

// Resolve looks up the exact (provider, model) pair (FR-003). It never
// allocates (SC-011); a miss returns a usable Handle carrying the FR-004
// defaults for every consumer.
func (c *Catalog) Resolve(provider, model string) Handle {
	h := Handle{provider: provider, model: model, budget: DefaultResizeLimits}
	s := c.cur.Load()
	if s == nil {
		return h
	}
	h.budget = s.doc.DefaultResizeLimits
	p, ok := s.byProvider[provider]
	if !ok {
		return h
	}
	h.p = p
	h.m = s.byPair[pairKey{provider, model}]
	if h.m != nil {
		// Only a hit carries the provider's own resize budget; a miss serves
		// the document default (FR-004).
		h.budget = p.ResizeLimits
	}
	return h
}

// clone deep-copies a provider row so a caller can never reach catalog
// state through the returned value.
func (p *Provider) clone() Provider {
	out := *p
	out.Protocols = append([]Endpoint(nil), p.Protocols...)
	out.Env = append([]string(nil), p.Env...)
	out.AuthMethods = append([]AuthMethod(nil), p.AuthMethods...)
	out.Aliases = append([]string(nil), p.Aliases...)
	out.Models = make([]Model, len(p.Models))
	for i := range p.Models {
		out.Models[i] = p.Models[i]
		out.Models[i].InputModalities = append([]Modality(nil), p.Models[i].InputModalities...)
	}
	return out
}
