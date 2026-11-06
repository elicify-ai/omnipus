// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "context"

const DefaultChatHistoryLimit = 40
const DefaultChatHistoryMinRows = 20
const DefaultChatHistorySoftByteBudget int64 = 4 * 1024 * 1024

// Display shortening and the hard page ceiling (chat-history paging spec,
// "Amendment 2026-10-27 (founder, #1452 D2)", A2.1 and A2.3).
const (
	// ChatHistoryInlineResultMaxBytes equals gateway.InlineToolResultMaxBytes:
	// a result whose JSON encoding is at most this size is shown whole.
	ChatHistoryInlineResultMaxBytes = 51200
	// ChatHistoryResultPreviewBytes bounds a shortened result's preview.
	ChatHistoryResultPreviewBytes = 4096
	// ChatHistoryTextDisplayMaxBytes bounds ordinary message content.
	ChatHistoryTextDisplayMaxBytes = 65536
	// DefaultChatHistoryByteCeiling is the server-owned page selection
	// ceiling on the carrier meter. Not an operator setting.
	DefaultChatHistoryByteCeiling int64 = 8388608
)

// ChatHistoryPageOptions is the non-wire input to the bounded history reader.
type ChatHistoryPageOptions struct {
	Limit          int
	MinRows        int
	Before         HistoryPosition
	After          HistoryPosition
	WindowStart    HistoryPosition
	WindowEnd      HistoryPosition
	SoftByteBudget int64
	Weigh          ChatHistoryWeigher
}

// ChatHistoryEntry identifies one whole source group in the core projection.
// Gateway callers must map it to generated public types, not marshal it.
type ChatHistoryEntry struct {
	Position HistoryPosition
	Entry    TranscriptEntry
	RowCount int
	// Actual retained delegate/spawn/create_task parents of this entry's
	// nested calls, resolved at this cut. Internal replay context only;
	// neither their transcript bodies nor this field cross the wire.
	NestedDelegateParents []ToolCallID
}

type ChatHistoryClear struct {
	EntryID  string
	Position HistoryPosition
}

type ChatHistorySpan struct {
	SpanID  string
	Start   *ChatHistoryEntry
	State   *ChatHistoryEntry
	Message *ChatHistoryEntry
	End     *ChatHistoryEntry
	HasRun  *bool
}

type ChatHistoryDuplicate struct {
	Kind              string
	ID                string
	CanonicalPosition HistoryPosition
	OccurrenceCount   int
}

type ChatHistoryContext struct {
	Spans        []ChatHistorySpan
	DuplicateIDs []ChatHistoryDuplicate
}

type ChatHistoryPageInfo struct {
	Mode                string
	Limit               int
	MinRows             int
	RowCount            int
	SourceGroupCount    int
	SoftByteBudget      int64
	ByteCeiling         int64
	ProjectedBytes      int64
	ByteBudgetExceeded  bool
	StopReason          string
	BudgetOverride      string
	FirstPosition       HistoryPosition
	LastPosition        HistoryPosition
	StoppedAtClear      bool
	LatestClear         *ChatHistoryClear
	RetentionGeneration string
	Context             ChatHistoryContext
}

// ChatHistoryPage is a non-wire core result, not a REST or WebSocket payload.
type ChatHistoryPage struct {
	SessionID  string
	Entries    []ChatHistoryEntry
	HasEarlier bool
	HasLater   bool
	NextBefore HistoryPosition
	NextAfter  HistoryPosition
	PageInfo   ChatHistoryPageInfo
	Cut        ChatHistoryReadCut
}

// ChatHistoryReadCut is opaque outside session. It identifies the immutable
// published root and retained set used by one bounded reader.
type ChatHistoryReadCut struct {
	sessionID           string
	generation          string
	retentionGeneration string
}

func (c ChatHistoryReadCut) SessionID() string {
	return c.sessionID
}

func (c ChatHistoryReadCut) Generation() string {
	return c.generation
}

func (c ChatHistoryReadCut) RetentionGeneration() string {
	return c.retentionGeneration
}

type ChatHistoryWeight struct {
	RESTBytes      int64
	WSBytes        int64
	ProjectedBytes int64
}

type ChatHistoryWeigher func(context.Context, ChatHistoryPage) (ChatHistoryWeight, error)

// ReadChatHistoryPage selects whole source groups at one immutable read cut.
// The caller supplies the public-carrier meter; no guessed default is installed.
func (us *UnifiedStore) ReadChatHistoryPage(
	ctx context.Context,
	sessionID string,
	opts ChatHistoryPageOptions,
) (ChatHistoryPage, error) {
	o, err := historyNormalizeOptions(opts)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	if err = validateSessionID(sessionID); err != nil {
		return ChatHistoryPage{}, err
	}
	return us.readChatHistoryPage(ctx, sessionID, func(t *chatHistoryIndexTree, dir string, cp historyCheckpoint) (ChatHistoryPage, error) {
		return historySelect(t, dir, cp, o)
	})
}

// ReadChatHistoryEntry returns the full public projection after effects of one
// occurrence at the current read cut: the same reduction, cancellation and
// disposition as ReadChatHistoryPage, with display shortening off. It runs
// through the same bounded read attempt (cut capture, retention re-validation
// and cleanup precedence) as the page reader. A well-formed position unknown
// in this session is ErrHistoryPositionNotFound; one whose retention
// tombstone proves expiry is ErrHistoryEntryExpired.
func (us *UnifiedStore) ReadChatHistoryEntry(ctx context.Context, sessionID string, position HistoryPosition) (ChatHistoryEntry, error) {
	if _, err := ParseHistoryPosition(string(position)); err != nil {
		return ChatHistoryEntry{}, err
	}
	if err := validateSessionID(sessionID); err != nil {
		return ChatHistoryEntry{}, err
	}
	p, err := us.readChatHistoryPage(ctx, sessionID, func(t *chatHistoryIndexTree, dir string, cp historyCheckpoint) (ChatHistoryPage, error) {
		return historySelectEntry(t, dir, cp, position)
	})
	if err != nil {
		return ChatHistoryEntry{}, err
	}
	if len(p.Entries) != 1 {
		return ChatHistoryEntry{}, historyUnavailable("projection_error", ErrHistoryProjection)
	}
	return p.Entries[0], nil
}
