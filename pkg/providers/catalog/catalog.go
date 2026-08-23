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
	"sync/atomic"
)

// EmbeddedSnapshot is the committed providers_catalog.json (schema 2.0.0 once
// T067-06 lands the first real release; until then its content is the legacy
// artefact and ParseDocument rejects it by schema_version).
//
//go:embed data/providers_catalog.json
var EmbeddedSnapshot []byte

// pairKey is the exact lookup key (FR-003). Two strings, compared exactly —
// no trimming, no case folding, no prefix arithmetic.
type pairKey struct {
	provider string
	model    string
}

// snapshot is one immutable, fully indexed document. Catalog swaps whole
// snapshots atomically so readers never observe a torn index.
type snapshot struct {
	doc        *Document
	byProvider map[string]*Provider
	byPair     map[pairKey]*Model
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
// document. On any error the previously loaded document is retained
// (US-1.AC2/AC3) — the caller decides what to log.
func (c *Catalog) Apply(data []byte) error {
	doc, err := ParseDocument(data)
	if err != nil {
		return err
	}
	c.cur.Store(index(doc))
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
