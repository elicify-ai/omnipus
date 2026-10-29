// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package rooms owns group chats ("rooms": the team room, squads and ad-hoc
// rooms of omnipus-system-agent US-17, §0.9, §0.10, §6.11, §6.13, §6.14).
//
// A room is a conversation the user owns in one workspace; no agent works
// inside it (FR-199). Every room message is fanned out as one request per
// addressed member into that member's MAIN session (FR-200), through the
// agent loop's room admission (pkg/agent/room_request.go). Each member
// answers from its main session; the answer comes back here (the room reply
// branch, FR-215), is appended to the room, mirrored once into the member's
// main chat and its @-mentions are fanned out (FR-165).
//
// Durable state lives under $OMNIPUS_HOME/entities/rooms/<room id>/ (out of
// every agent's reach, like the mount and delegation stores): the room record
// and append-only journals of entries, delivery states, answer mirrors and
// counted turns. Every write goes through the Journal seam (the "post-back"
// fault seam of FR-216); every change is persisted before it is published.
//
// Glossary (OR-MIN-012): these rooms are group chats. They are unrelated to
// the memory rooms of pkg/memrooms and to the retired rooms-era drafts.
package rooms

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Room kinds (K23).
const (
	KindTeam  = "team"
	KindSquad = "squad"
	KindAdhoc = "adhoc"
)

// Delivery states: exactly the §6.11 vocabulary (K27).
const (
	StateRecorded         = "recorded"
	StateAwaitingApproval = "awaiting_approval"
	StateAdmitted         = "admitted"
	StateHeld             = "held"
	StateWorking          = "working"
	StateWaitingOnUser    = "waiting_on_user"
	StateAnswered         = "answered"
	StateRefused          = "refused"
	StateFailed           = "failed"
	StateInterrupted      = "interrupted"
	StateUnavailable      = "unavailable"
	StateCancelled        = "cancelled"
	StateUnknown          = "unknown"
)

// Delivery reasons (K27 `reason`).
const (
	ReasonRateLimit       = "rate_limit"
	ReasonRemoved         = "removed"
	ReasonRoomDeleted     = "room_deleted"
	ReasonPaused          = "paused"
	ReasonRoomUnavailable = "room_unavailable"
	ReasonNotMember       = "not_a_member"
	ReasonStorage         = "storage"
	ReasonNoAnswer        = "no_answer"
	ReasonTurnFailed      = "turn_failed"
	ReasonRestart         = "restart"
)

// PrincipalSystem is the audited actor of a room change no signed-in person
// made (the boot-time or on-read reconciliation of a room with its
// workspace's team).
const PrincipalSystem = "system"

// Fixed user-facing texts (spec copy).
const (
	teamRoomName          = "Team room"
	textTwoAgents         = "a room needs at least two agents"
	textTwoAgentsDelete   = "a room needs at least two agents — delete the room instead"
	textReadOnlyTwoAgents = "This group has fewer than two agents — add another agent to continue."
	textTeamHidden        = "The team room needs at least two conversation agents in this workspace."
	textTeamMembership    = "Team room members are the workspace's agents — add the agent to the workspace"
	textRoomsDontSteer    = "Rooms don't steer agents - use %s main chat"
	textAmbiguousMention  = "Several members are called %s — pick from the list"
	textRoomUnavailable   = "the group this request came from is no longer available; the answer was not posted"
	textMemberLeftRoom    = "you are no longer a member of this group; the answer was not posted"
	auditEventRoomChange  = "room.changed"
	maxRoomNameUnits      = 80
	maxMembers            = 256
	textNameLength        = "a group name must be 1 to 80 characters"
	maxContextEntries     = 20
	maxContextBytes       = 8192
	maxSnapshotEntries    = 10000
	rateWindow            = time.Minute
)

// Error is a refusal the REST layer maps to an HTTP status with its text.
type Error struct {
	Status int
	Msg    string
	cause  error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.cause }

func notFound(format string, args ...any) error {
	return &Error{Status: http.StatusNotFound, Msg: fmt.Sprintf(format, args...)}
}

func unprocessable(format string, args ...any) error {
	return &Error{Status: http.StatusUnprocessableEntity, Msg: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) error {
	return &Error{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, args...)}
}

// storageError hides the cause (paths, OS errors) from the client; it stays in
// the error chain for the server log.
func storageError(what string, cause error) error {
	return &Error{
		Status: http.StatusInternalServerError,
		Msg:    what + ". Check that the data directory is available and writable, then try again; details are in the server log",
		cause:  cause,
	}
}

// StatusOf returns the HTTP status for err (500 for anything unclassified).
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return http.StatusInternalServerError
}

// Admission outcomes the Deps.Admit implementation wraps its errors in.
var (
	ErrAdmitRefused     = errors.New("rooms: the member refused the request")
	ErrAdmitUnavailable = errors.New("rooms: the member is unavailable")
	ErrAdmitCancelled   = errors.New("rooms: the request was cancelled")
)

// roomRecord is the durable room (room.json).
type roomRecord struct { // not-wire-format: the room's on-disk record; the wire shape is generated.Room
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	Kind            string    `json:"kind"`
	Name            string    `json:"name"`
	Members         []string  `json:"members"`
	Muted           []string  `json:"muted"`
	Paused          bool      `json:"paused"`
	Generation      int64     `json:"generation"`
	OriginSessionID string    `json:"origin_session_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	CostMicroUSD    int64     `json:"cost_micro_usd"`
	CostUnpriced    bool      `json:"cost_unpriced,omitempty"`
	// Deleted is the durable deletion intent: written before any pending
	// request is cancelled, so a failed or interrupted deletion never leaves
	// a live room with cancelled work. A deleted record is finished (its
	// directory removed) on the next load and is never served.
	Deleted bool `json:"deleted,omitempty"`
	// PendingReleases journals handoffs that failed after working was saved.
	// Replay these BEFORE restart recovery so unstarted work is queued, not interrupted.
	PendingReleases map[string]bool `json:"pending_releases,omitempty"`
	// Invitation is the invitation summary of an ad-hoc group started from
	// a main chat (#1449 FR-039); nil for every other group.
	Invitation *invitationState `json:"invitation,omitempty"`
	// Seq is the room stream position when the record was written.
	Seq int64 `json:"seq"`
}

// ConsumeDecision is the dequeue fence's answer (FR-217).
type ConsumeDecision int

const (
	// ConsumeRun: the request is current and is now "working"; run it.
	ConsumeRun ConsumeDecision = iota
	// ConsumeCancel: the room cancelled the request (pause, removal,
	// deletion) or it is already settled; discard it, never run it.
	ConsumeCancel
	// ConsumeDefer: the room could not decide or record now (a read or
	// write failure), or a turn already took the request. Neither run nor
	// discard it: it stays in the member's queue and is offered again on
	// the member's next turn or on a Retry.
	ConsumeDefer
)

func (r roomRecord) clone() roomRecord {
	r.Members = append([]string(nil), r.Members...)
	r.Muted = append([]string(nil), r.Muted...)
	if r.Invitation != nil {
		inv := *r.Invitation
		r.Invitation = &inv
	}
	return r
}

// storedEntry is one line of entries.jsonl.
type storedEntry struct { // not-wire-format: on-disk transcript line wrapping the generated RoomEntry with answer bookkeeping
	Entry generated.RoomEntry `json:"entry"`
	// AnswersRequest is the delivery id an agent answer answers.
	AnswersRequest string `json:"answers_request,omitempty"`
	// SenderAgentID is the authoring agent (empty for the user).
	SenderAgentID string `json:"sender_agent_id,omitempty"`
	// IntentID is the answer intent an agent answer with files came from
	// (#1449 FR-078); empty for every other entry.
	IntentID string `json:"intent_id,omitempty"`
}

// deliveryRecord is one line of deliveries.jsonl; the latest line per
// request id wins.
type deliveryRecord struct { // not-wire-format: durable per-delivery state; projected to generated.RoomDeliveryState
	RequestID  string `json:"request_id"`
	EntryID    string `json:"entry_id"`
	AgentID    string `json:"agent_id"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	// SessionID is the member's main session once admitted.
	SessionID string `json:"session_id,omitempty"`
	// Consumed is set once a turn of the member took the request.
	Consumed bool `json:"consumed,omitempty"`
	// SenderAgentID is the agent whose answer @-named this member (empty
	// when the user wrote the message).
	SenderAgentID string    `json:"sender_agent_id,omitempty"`
	Seq           int64     `json:"seq"`
	At            time.Time `json:"at"`
}

// mirrorRecord is one line of mirrors.jsonl: the answer's main-chat mirror
// was written.
type mirrorRecord struct { // not-wire-format: on-disk marker that an answer's main-chat mirror was written
	AnswerID string    `json:"answer_id"`
	At       time.Time `json:"at"`
}

// turnRecord is one line of turns.jsonl: a turn already counted in the
// room's allocated cost (§6.14: each turn counted once, by turn id).
type turnRecord struct { // not-wire-format: on-disk marker of a turn counted into the room's allocated cost
	TurnID   string    `json:"turn_id"`
	MicroUSD int64     `json:"micro_usd"`
	Unpriced bool      `json:"unpriced,omitempty"`
	At       time.Time `json:"at"`
}

// terminal reports whether a delivery state is final.
func terminal(state string) bool {
	switch state {
	case StateAnswered, StateRefused, StateFailed, StateInterrupted, StateUnavailable, StateCancelled:
		return true
	}
	return false
}

// cancellable reports whether a pause, a member removal or a room deletion
// cancels a delivery in this state: everything not yet consumed by a turn.
func cancellable(d *deliveryRecord) bool {
	if d.Consumed || terminal(d.State) {
		return false
	}
	switch d.State {
	case StateRecorded, StateAwaitingApproval, StateAdmitted, StateHeld, StateUnknown:
		return true
	}
	return false
}
