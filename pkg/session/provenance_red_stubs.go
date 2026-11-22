// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED stubs — connected command-line programs, slice S1 (#1960).
//
// qa-lead RED scaffolding sanctioned by the dispatch brief. The spec places
// the lifecycle-record provenance classification next to the records the mode
// is decided from (FR-050, "Root versus lineage"; the evidence is the same
// work-identity evidence the approval classification uses). The row type,
// constants and accessor NAMES are inventions listed in
// /workspace/logs/1960/s1-red/test-disputes.md; GREEN may rename and
// reconcile with qa-lead (same test author).
//
// RED stub: ProvenanceModeRows returns nil, so the S-83 drift-table guard
// fails with "no row for <kind>" until GREEN provides the classification.

package session

// ProvenanceMode is the `provenance_mode` of a lifecycle record: "root" (an
// identified standing conversation; no stored provenance) or "lineage"
// (stored provenance required; missing → unresolved) (spec FR-050).
type ProvenanceMode string

const (
	// ProvenanceModeRoot marks an identified standing conversation
	// (origin kind chat, channel or main; no parent; no task evidence; not
	// plan-owned). A root stores no provenance; each of its turns is the
	// join of the inputs it consumes.
	ProvenanceModeRoot ProvenanceMode = "root"
	// ProvenanceModeLineage marks everything else: every child, delegated or
	// steered session, every work session whether or not it has a parent,
	// and every durable work record. Missing or unreadable lineage is
	// unresolved.
	ProvenanceModeLineage ProvenanceMode = "lineage"
)

// ProvenanceModeRow is one row of the S-83 classification table: exactly one
// row per session.OriginKind and per UnifiedSessionType, naming the mode and
// where the stored provenance comes from (the producer).
type ProvenanceModeRow struct {
	Origin      OriginKind         // the origin kind the row covers
	SessionType UnifiedSessionType // "" when the kind has no session type of its own (plan, human)
	Mode        ProvenanceMode     // root or lineage (spec FR-050 table)
	Producer    string             // where stored provenance comes from ("" for root)
	// TurnStartPath is "" on kind/type rows; non-empty it covers exactly one
	// registered turn-start path (S-83: the launch, steering and background
	// callback paths of the spec's producer table), one row per path.
	TurnStartPath string
}

// ProvenanceModeRows returns the classification table in row order.
//
// RED stub: returns nil — the classification is not implemented yet.
func ProvenanceModeRows() []ProvenanceModeRow { return nil }
