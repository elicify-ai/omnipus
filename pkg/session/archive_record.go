// archive_record.go: the private, disk-only archive envelope and its stable
// address — the storage shape Decisions B and C of session-core C-ARCHIVE / U2
// are built on (docs/internal/specs/session-core-spec.md, FR-004/FR-005).
//
// SCOPE (deliberately narrow — the standalone new path, not the runtime
// cutover): this file defines the envelope object, the byte-address mark and
// the trusted-source member. It does NOT wire the new archive into
// UnifiedStore, does not remove `.context` and does not touch the day-partition
// transcript the running product reads — that is Decision D's cutover lane.
//
// The envelope is the ONE append-only content record the feature's FR-004
// describes: it carries the existing chat identity/event fields (via the
// embedded TranscriptEntry), a REQUIRED producer-chosen view_membership, and
// the PRIVATE members that must never cross the gateway/SPA boundary — the
// lossless model_message (Decision A, model_payload.go), the body-free
// model_ref consumption reference (Decision B) and the trusted source
// (Decision A's provenance member). Private members are disk-only: the raw
// record is scanned by archive tests and the archive reader, never serialized
// into REST/WS/replay, which project to the generated public shapes.
package session

import "fmt"

// ArchiveAddress is the stable, disk-only mark identifying the START of one
// complete encoded archive record (session-core C-ARCHIVE / U2, Decision C; spec
// FR-005). It is never on the REST/WS wire.
//
//   - PartitionKey is stable through the current-file rename and collision
//     suffixes: the current partition's key is the day it holds, and that same
//     key names the rolled file after a rollover, so an address taken before a
//     rename still resolves afterwards.
//   - ByteOffset counts ENCODED UTF-8 file bytes from the start of that
//     partition's data file (not runes, not decoded-record indices), so a reader
//     can seek straight to the record without decoding the bytes before it.
//   - EntryID is the record's own id, re-checked on read: an offset that lands
//     on a different record is a corrupt or stale mark, not a silent
//     wrong-record read.
type ArchiveAddress struct {
	PartitionKey string `json:"partition_key"`
	ByteOffset   int64  `json:"byte_offset"`
	EntryID      string `json:"entry_id"`
}

// EntrySource is the trusted, server-minted provenance of a content record
// (session-core C-ARCHIVE / U2, Decision A; FR-008/027/028/045). It is filled
// from what the server actually appended, never from caller claims, and is a
// PRIVATE member of the envelope — provenance is not a public wire field, and an
// archived source never grants connector/egress authority (C-REPLY's binding
// check still owns egress). Credentials and connector authority are never
// inserted here.
type EntrySource struct {
	// Kind names the intake class the server minted: "user", "connector",
	// "parent", "agent" or "anonymous". An empty principal with kind
	// "anonymous" is a genuinely anonymous/shared source, not permission to
	// invent a human.
	Kind      string `json:"kind,omitempty"`
	Principal string `json:"principal,omitempty"`
	// RequestID / correlation reuse the C-ADDRESS request/source-owner/return
	// correlation where applicable. They are identifiers only.
	RequestID              string `json:"request_id,omitempty"`
	SourceOwnerWorkspaceID string `json:"source_owner_workspace_id,omitempty"`
	SourceOwnerAgentID     string `json:"source_owner_agent_id,omitempty"`
	ReturnCorrelationID    string `json:"return_correlation_id,omitempty"`
}

// ArchiveRecord is the private envelope of one archive entry. It embeds the
// existing TranscriptEntry so the record carries the same chat
// identity/timestamp/author/turn/event fields the rest of the archive already
// understands, and adds the private members Decisions A/B require. Embedding is
// anonymous, so the JSON is ONE flat object per record.
//
// The private members MUST NOT be copied onto TranscriptEntry itself while the
// raw REST serialization seam still exists: rest_sessions.go::getSessionMessages
// returns ordinary entries' fields verbatim, so attaching these to the shared
// transcript struct would leak model-only content onto the wire before Decision
// D installs the generated public projection. Keeping them on this separate
// envelope is why the new path can be built and tested in isolation.
type ArchiveRecord struct {
	TranscriptEntry

	// ModelMessage is the lossless private model payload (Decision A). Exactly
	// one model content slot resolves per record: an inline ModelMessage, or
	// the referenced source payload of a ModelRef, never both and never
	// neither for a model slot.
	ModelMessage *ModelPayload `json:"model_message,omitempty"`

	// ModelRef is the body-free consumption reference of a model-only
	// EntryTypeModelRef record (Decision B). It carries a same-session
	// already-accepted payload record's exact address and NO copied content.
	ModelRef *ModelRef `json:"model_ref,omitempty"`

	// Source is the trusted server-minted provenance of a content record
	// (Decision A). Nil on pure effects (a model_ref, a status change).
	Source *EntrySource `json:"source,omitempty"`

	// ToolResultFor names the exact assistant content occurrence that issued
	// the tool call of a role "tool" payload (Decision A): the producing
	// assistant entry plus the call id, not tool_call_id alone.
	ToolResultFor *ToolResultFor `json:"tool_result_for,omitempty"`
}

// Validate refuses an envelope that is malformed or that violates the
// Decision A/B shape rules, rather than letting a bad record reach disk. It is
// the write-side gate: every Append runs it before any byte is written.
func (r ArchiveRecord) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("archive record: empty id")
	}
	switch r.ViewMembership {
	case ViewMembershipChat, ViewMembershipModel, ViewMembershipBoth:
	case "":
		return fmt.Errorf("archive record %s: view_membership is required and producer-chosen (chat/model/both)", r.ID)
	default:
		return fmt.Errorf("archive record %s: invalid view_membership %q", r.ID, r.ViewMembership)
	}

	if r.Type == EntryTypeModelRef {
		if r.ModelRef == nil {
			return fmt.Errorf("archive record %s: model_ref record has no model_ref payload", r.ID)
		}
		if r.ViewMembership != ViewMembershipModel {
			return fmt.Errorf("archive record %s: model_ref record must be view_membership=model, got %q", r.ID, r.ViewMembership)
		}
		if r.ModelMessage != nil {
			return fmt.Errorf("archive record %s: model_ref record must carry no model_message body", r.ID)
		}
		if r.Content != "" {
			return fmt.Errorf("archive record %s: model_ref record must not copy content", r.ID)
		}
	}

	if r.ModelRef != nil {
		if r.Type != EntryTypeModelRef {
			return fmt.Errorf("archive record %s: model_ref payload only on a %q record", r.ID, EntryTypeModelRef)
		}
		if r.ModelRef.EntryID == "" || r.ModelRef.PartitionKey == "" {
			return fmt.Errorf("archive record %s: model_ref must name a same-session entry id and partition key", r.ID)
		}
		if r.ModelRef.ByteOffset < 0 {
			return fmt.Errorf("archive record %s: model_ref byte offset must not be negative", r.ID)
		}
	}

	if r.ModelMessage != nil && r.ModelMessage.Role == "tool" {
		if r.ToolResultFor == nil {
			return fmt.Errorf("archive record %s: a role \"tool\" payload must carry tool_result_for", r.ID)
		}
		if r.ToolResultFor.AssistantEntryID == "" || r.ToolResultFor.ToolCallID == "" {
			return fmt.Errorf("archive record %s: tool_result_for must name the assistant entry and call id", r.ID)
		}
		if r.ToolResultFor.ToolCallID != r.ModelMessage.ToolCallID {
			return fmt.Errorf("archive record %s: tool_result_for call id %q must equal model_message.tool_call_id %q",
				r.ID, r.ToolResultFor.ToolCallID, r.ModelMessage.ToolCallID)
		}
	}
	return nil
}

// ReferenceAddress returns the address a model_ref record points at. It is a
// convenience for readers; the stored ModelRef already carries the same triple.
func (r ArchiveRecord) ReferenceAddress() (ArchiveAddress, bool) {
	if r.ModelRef == nil {
		return ArchiveAddress{}, false
	}
	return ArchiveAddress{
		PartitionKey: r.ModelRef.PartitionKey,
		ByteOffset:   r.ModelRef.ByteOffset,
		EntryID:      r.ModelRef.EntryID,
	}, true
}
