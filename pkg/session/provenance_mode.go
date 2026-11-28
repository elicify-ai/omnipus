// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

// ProvenanceMode is the `provenance_mode` of a lifecycle record (connected-
// programs spec FR-050): "root" for an identified standing conversation (no
// stored provenance) or "lineage" (stored provenance required; missing is
// unresolved).
type ProvenanceMode string

const (
	// ProvenanceModeRoot marks an identified standing conversation: origin
	// kind chat, channel or main, no parent, no task evidence, not
	// plan-owned. A root stores no provenance; each of its turns is the join
	// of the inputs it consumes.
	ProvenanceModeRoot ProvenanceMode = "root"
	// ProvenanceModeLineage marks everything else: every child, delegated or
	// steered session, every work session whether or not it has a parent,
	// every durable work record and every unknown kind. Missing or
	// unreadable lineage is unresolved.
	ProvenanceModeLineage ProvenanceMode = "lineage"
)

// ProvenanceModeRow is one row of the S-83 classification table: one row per
// session.OriginKind (carrying the kind's own session type, if it has one),
// naming the mode and where the stored provenance comes from.
type ProvenanceModeRow struct {
	Origin      OriginKind         // the origin kind the row covers
	SessionType UnifiedSessionType // "" when the kind has no session type of its own (plan, human)
	Mode        ProvenanceMode     // root or lineage (FR-050 table)
	Producer    string             // where stored provenance comes from ("" for root)
	// TurnStartPath is "" on kind/type rows; non-empty it covers exactly one
	// registered turn-start path.
	TurnStartPath string
}

// provenanceModeRows is the FR-050 classification table (rows 2–7 of
// "Classification (R5-M1)") per origin kind.
var provenanceModeRows = []ProvenanceModeRow{
	{Origin: OriginKindDelegate, SessionType: SessionTypeDelegate, Mode: ProvenanceModeLineage,
		Producer: "the launching turn, stamped at launch"},
	{Origin: OriginKindTask, SessionType: SessionTypeTask, Mode: ProvenanceModeLineage,
		Producer: "the task record at dispatch"},
	{Origin: OriginKindChat, SessionType: SessionTypeChat, Mode: ProvenanceModeRoot},
	{Origin: OriginKindChannel, SessionType: SessionTypeChannel, Mode: ProvenanceModeRoot},
	{Origin: OriginKindMain, SessionType: SessionTypeMain, Mode: ProvenanceModeRoot},
	{Origin: OriginKindScheduled, SessionType: SessionTypeScheduled, Mode: ProvenanceModeLineage,
		Producer: "the schedule record at creation"},
	{Origin: OriginKindHeartbeat, SessionType: SessionTypeHeartbeat, Mode: ProvenanceModeLineage,
		Producer: "the heartbeat scheduler: {independent}"},
	{Origin: OriginKindVerifier, SessionType: SessionTypeVerifier, Mode: ProvenanceModeLineage,
		Producer: "the verified work's provenance"},
	{Origin: OriginKindPlan, Mode: ProvenanceModeLineage,
		Producer: "the plan record"},
	{Origin: OriginKindHuman, Mode: ProvenanceModeLineage,
		Producer: "none: no producer row, so unresolved (fail closed)"},
	// Turn-start path rows (S-83, ruling-check-delta "S-83 path rows"): what
	// each handoff contributes to the receiving turn, from the spec's handoff
	// table. The receiving session keeps its own mode (a steered main chat
	// stays root for its own turns).
	{TurnStartPath: "launch", Mode: ProvenanceModeLineage,
		Producer: "the launching turn or work record, stamped at creation"},
	{TurnStartPath: "steering", Mode: ProvenanceModeLineage,
		Producer: "the steer message's provenance, joined into the receiving turn; a missing one is unresolved, never root"},
	{TurnStartPath: "peer_request", Mode: ProvenanceModeLineage,
		Producer: "the request ledger record"},
	{TurnStartPath: "background_callback", Mode: ProvenanceModeLineage,
		Producer: "the ProcessSession and its async callback"},
	{TurnStartPath: "goal_follow_up", Mode: ProvenanceModeLineage,
		Producer: "the goal record"},
}

// ProvenanceModeRows returns the classification table in row order.
func ProvenanceModeRows() []ProvenanceModeRow {
	return append([]ProvenanceModeRow(nil), provenanceModeRows...)
}

// ProvenanceModeEvidence is the work-identity evidence the mode is decided
// from, in the FR-050 order.
type ProvenanceModeEvidence struct {
	MetadataUnreadable bool
	Origin             OriginKind // "" when the lifecycle origin is missing
	ParentSessionID    string
	TaskID             string
	PlanOwned          bool
}

// ClassifyProvenanceMode applies the FR-050 classification: the first
// matching row decides; only an identified standing conversation is root.
func ClassifyProvenanceMode(ev ProvenanceModeEvidence) ProvenanceMode {
	switch {
	case ev.MetadataUnreadable || ev.Origin == "":
		return ProvenanceModeLineage
	case ev.ParentSessionID != "" || ev.Origin == OriginKindDelegate:
		return ProvenanceModeLineage
	case ev.TaskID != "", ev.PlanOwned:
		return ProvenanceModeLineage
	}
	switch ev.Origin {
	case OriginKindChat, OriginKindChannel, OriginKindMain:
		return ProvenanceModeRoot
	}
	return ProvenanceModeLineage
}
