package memory

// ProjectionState is the per-result projection state recorded in session
// meta (ADR-066 D4/D5, FR-019). The archive line itself is never modified
// (ADR-028 append-only); the state tells the projection function how the
// in-memory / provider view of that line differs from the archived bytes.
//
// This type is internal persistence state — it is NOT a gateway/SPA wire
// type. The SPA-facing `content_state` on the transcript is a separate,
// contract-defined enum (contracts/components/schemas/ToolCall.yaml).
type ProjectionState string

const (
	// ProjectionCapped — the archive holds the full result; the window
	// carries the D4 head-and-tail capped form plus the cap mark, cut at
	// the SUCCESS cap for the owning tool's surface.
	ProjectionCapped ProjectionState = "capped"
	// ProjectionCappedFailure — as ProjectionCapped, but the live cut used
	// the D4 "builtin-failure" surface because the result was a failed,
	// denied or skipped call.
	//
	// The surface has to be part of the recorded state: the window carries
	// no IsError, so a reload that re-derived the surface from the tool name
	// alone re-cut a failure at the SUCCESS cap (64,000 — or 62,500 for an
	// `mcp_*` name) when the model had actually been given 10,000. That is a
	// direct violation of FR-019 / B-12 / B-22, which require the bytes
	// assembled on reload to be identical to the bytes the model saw live.
	// Entries written before this state existed read back as
	// ProjectionCapped, i.e. the old success-cap behaviour, which is the
	// safe direction (still bounded, still marked).
	ProjectionCappedFailure ProjectionState = "capped_failure"
	// ProjectionEmptied — the archive holds the full result; the window
	// carries only the D5 recall mark.
	ProjectionEmptied ProjectionState = "emptied"
)

// ProjectionKey addresses one archived tool result. The key is composite
// because tool_call_ids are not unique across a session (B-29b: providers
// reuse ids such as call_0 on every turn) — the archive line index is what
// makes the address exact.
type ProjectionKey struct {
	ToolCallID  string
	ArchiveLine int
}

// ProjectionSet is the in-memory form of the persisted projection state:
// one entry per (tool_call_id, archive_line) that is capped or emptied.
// Absent key == full content.
type ProjectionSet map[ProjectionKey]ProjectionState

// Clone returns an independent copy (nil in → empty, non-nil out).
func (p ProjectionSet) Clone() ProjectionSet {
	out := make(ProjectionSet, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// ProjectionMeta is what a reader gets back: the projection set plus the
// retained-source limits and transcript identities. (An archive converted from
// a hydrated source is flagged per record, model_origin == conv_rebuilt, not
// here.)
type ProjectionMeta struct {
	Entries ProjectionSet
	// SourceRunes records the exact retained source limit, excluding the mark.
	// Presence, including zero, wins over later cap-setting changes.
	SourceRunes map[ProjectionKey]int
	// TranscriptAddr is the exact address of the chat tool_call record that
	// presents the result, as returned by the append that wrote it. It exists for
	// full results too; absence means no recorded transcript identity. (An
	// address with offset 0 is valid: presence in the map is the identity.)
	TranscriptAddr map[ProjectionKey]RecordAddress
}

// RecordAddress identifies one complete record of a session's day-partitioned
// archive: the partition it was written under, the byte offset of its first
// byte within that partition, and its own entry id (re-checked on read, so an
// offset that lands on another record is a stale mark, never a wrong-record
// read). The partition key is the UTC day the record was written under and
// names the rolled file after a rollover, so an address survives the rename.
// It is internal persistence state, never a gateway wire type.
type RecordAddress struct {
	PartitionKey string `json:"partition_key"`
	ByteOffset   int64  `json:"byte_offset"`
	EntryID      string `json:"entry_id"`
}
