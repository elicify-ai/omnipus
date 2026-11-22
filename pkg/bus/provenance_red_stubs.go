// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED stubs — connected command-line programs, slice S1 (#1960).
//
// qa-lead RED scaffolding sanctioned by the dispatch brief. The spec pins the
// algebra, the zero value and the field carrier ("bus.InboundMessage.
// Provenance", spec invariant I-2 / FR-010 / FR-041), which places the value
// type in the package that owns the envelope (pkg/bus). The NAMES here are
// inventions listed in /workspace/logs/1960/s1-red/test-disputes.md; GREEN may
// rename them and reconcile with qa-lead (same test author).
//
// Every function is an EMPTY stub: Provenance is always the zero value
// (unresolved), so the S1 tests fail with real expected-vs-actual instead of
// not compiling. GREEN implements the real algebra in place.
//
// What is real vs stub (CHECK F20's qualification): the VALUE SCAFFOLDING is
// real code — Origin and its constants, ConnectorOrigin, IsConnectorOrigin,
// the Provenance field carrier, IsUnresolved and Origins (the zero-value
// definition and the field's own reader). The FEATURE functions are empty:
// NewProvenance returns unresolved, Join returns unresolved (never the
// union), Eligible is constant false, WithheldOrigins constant nil. A test
// that passes here passes on scaffolding alone; every algebra assertion that
// matters rides the feature functions and is red until GREEN implements
// them.

package bus

// Origin is one provenance origin: "web", "independent" or
// "connector:<id>" (spec I-2).
type Origin string

const (
	// OriginWeb is a person typing in the web chat.
	OriginWeb Origin = "web"
	// OriginIndependent is work with no starting message (founder Q1 = A).
	OriginIndependent Origin = "independent"
	// OriginConnectorPrefix prefixes a channel connection id.
	OriginConnectorPrefix Origin = "connector:"
)

// ConnectorOrigin returns the origin of the channel connection with the given
// instance id ("connector:<id>", spec I-2).
func ConnectorOrigin(id string) Origin {
	return Origin(string(OriginConnectorPrefix) + id)
}

// IsConnectorOrigin reports whether o is a connector:<id> origin.
func IsConnectorOrigin(o Origin) bool {
	return len(o) > len(OriginConnectorPrefix) && o[:len(OriginConnectorPrefix)] == OriginConnectorPrefix
}

// Provenance is a set of origins; its zero value is `unresolved` (spec I-2).
// Joining two provenances is the union of their origin sets; work is eligible
// only if every origin is allowed by that origin's own setting (FR-010,
// monotone restriction).
//
// RED stub: always unresolved.
type Provenance struct {
	origins []Origin
}

// NewProvenance returns a resolved provenance over the given origins.
//
// RED stub: returns the zero value (unresolved).
func NewProvenance(origins ...Origin) Provenance {
	return Provenance{}
}

// unresolvedProvenance is the zero value (spec: "its zero value is
// `unresolved`").
var unresolvedProvenance = Provenance{}

// UnresolvedProvenance returns the absorbing zero value.
func UnresolvedProvenance() Provenance { return unresolvedProvenance }

// IsUnresolved reports whether p is the absorbing zero value. Reading the
// zero-value condition is the type's definition, not the feature: the
// FEATURE (join and eligibility) is still stubbed below.
func (p Provenance) IsUnresolved() bool { return len(p.origins) == 0 }

// Origins returns the origin set in construction order (the value's own
// field, not the feature).
func (p Provenance) Origins() []Origin {
	return append([]Origin(nil), p.origins...)
}

// Join joins two provenances: unresolved if either side is unresolved,
// otherwise the union of the two origin sets (spec I-2). The result never
// drops or converts an origin.
//
// RED stub: returns the zero value.
func Join(a, b Provenance) Provenance { return unresolvedProvenance }

// Eligible reports whether a turn carrying p may use connected programs:
// not unresolved, and every origin in the set allowed by its own setting
// (FR-010). `allowed` answers for one origin; connector origins get their
// connection's setting, web and independent the web-chat setting
// (founder Q1 = A).
//
// RED stub: always false (unresolved is never eligible — that half of the
// contract is the zero-value behaviour, not an implementation).
func (p Provenance) Eligible(allowed func(Origin) bool) bool { return false }

// WithheldOrigins returns the origins that are not allowed now; empty when
// the turn is eligible or unresolved (FR-012 names them in the notice).
//
// RED stub: always empty.
func (p Provenance) WithheldOrigins(allowed func(Origin) bool) []Origin { return nil }
