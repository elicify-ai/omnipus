// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package bus

import (
	"encoding/json"
	"sort"
	"strings"
)

// Origin is one provenance origin: "web", "independent" or
// "connector:<id>" (connected-programs spec, invariant I-2).
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
// instance id ("connector:<id>").
func ConnectorOrigin(id string) Origin {
	return Origin(string(OriginConnectorPrefix) + id)
}

// IsConnectorOrigin reports whether o is a connector:<id> origin.
func IsConnectorOrigin(o Origin) bool {
	return len(o) > len(OriginConnectorPrefix) && strings.HasPrefix(string(o), string(OriginConnectorPrefix))
}

// ConnectorID returns the connection id of a connector origin and true, or ""
// and false for any other origin.
func ConnectorID(o Origin) (string, bool) {
	if !IsConnectorOrigin(o) {
		return "", false
	}
	return string(o[len(OriginConnectorPrefix):]), true
}

// validOrigin reports whether o is one of the three origin forms.
func validOrigin(o Origin) bool {
	return o == OriginWeb || o == OriginIndependent || IsConnectorOrigin(o)
}

// Provenance is either unresolved (the zero value) or a non-empty set of
// origins (I-2). It is server-derived only; model text never sets it.
// Joining two provenances is the union of their origin sets, and work is
// eligible only if every origin is allowed by that origin's own setting
// (FR-010, monotone restriction). Values are immutable.
type Provenance struct {
	origins []Origin
}

// NewProvenance returns the provenance over the given origins, deduplicated
// in first-seen order. With no origin, or any malformed origin, it is
// unresolved: a bad value never widens anything.
func NewProvenance(origins ...Origin) Provenance {
	var out []Origin
	for _, o := range origins {
		if !validOrigin(o) {
			return Provenance{}
		}
		if !containsOrigin(out, o) {
			out = append(out, o)
		}
	}
	return Provenance{origins: canonicalOrigins(out)}
}

func containsOrigin(set []Origin, o Origin) bool {
	for _, x := range set {
		if x == o {
			return true
		}
	}
	return false
}

// UnresolvedProvenance returns the absorbing zero value.
func UnresolvedProvenance() Provenance { return Provenance{} }

// IsUnresolved reports whether p is the absorbing zero value.
func (p Provenance) IsUnresolved() bool { return len(p.origins) == 0 }

// Origins returns the origin set in first-seen order.
func (p Provenance) Origins() []Origin {
	return append([]Origin(nil), p.origins...)
}

// Join returns unresolved if either side is unresolved, otherwise the union
// of the two origin sets. No origin is ever dropped or converted.
func Join(a, b Provenance) Provenance {
	if a.IsUnresolved() || b.IsUnresolved() {
		return Provenance{}
	}
	out := append([]Origin(nil), a.origins...)
	for _, o := range b.origins {
		if !containsOrigin(out, o) {
			out = append(out, o)
		}
	}
	return Provenance{origins: canonicalOrigins(out)}
}

// Eligible reports whether a turn carrying p may use connected programs: not
// unresolved, and every origin allowed by its own setting (FR-010). The
// allowed function answers for one origin: connector origins get their
// connection setting (an unknown connection is not allowed), web and
// independent the web-chat setting (founder Q1 = A).
func (p Provenance) Eligible(allowed func(Origin) bool) bool {
	if p.IsUnresolved() || allowed == nil {
		return false
	}
	for _, o := range p.origins {
		if !allowed(o) {
			return false
		}
	}
	return true
}

// WithheldOrigins returns the origins that are not allowed now, in the
// provenance order; nil when the turn is eligible or unresolved (FR-012
// names them in the notice).
func (p Provenance) WithheldOrigins(allowed func(Origin) bool) []Origin {
	if p.IsUnresolved() {
		return nil
	}
	var out []Origin
	for _, o := range p.origins {
		if allowed == nil || !allowed(o) {
			out = append(out, o)
		}
	}
	return out
}

// MarshalJSON writes the origin set as a JSON array; unresolved is null.
// Durable records (lifecycle, peer request ledger) persist provenance with it.
func (p Provenance) MarshalJSON() ([]byte, error) {
	if p.IsUnresolved() {
		return []byte("null"), nil
	}
	return json.Marshal(p.origins)
}

// UnmarshalJSON reads an origin array back through NewProvenance, so a
// missing, empty or malformed value loads as unresolved (fail closed, I-2).
func (p *Provenance) UnmarshalJSON(b []byte) error {
	var origins []Origin
	if err := json.Unmarshal(b, &origins); err != nil {
		*p = Provenance{}
		return nil //nolint:nilerr // a malformed stored provenance is unresolved by design (fail closed), not a load failure
	}
	*p = NewProvenance(origins...)
	return nil
}

// canonicalOrigins puts an origin set in its one canonical (sorted) order. A
// provenance is a set (I-2): two provenances with the same origins are the
// same value, whichever order the origins were joined in.
func canonicalOrigins(origins []Origin) []Origin {
	sort.Slice(origins, func(i, j int) bool { return origins[i] < origins[j] })
	return origins
}
