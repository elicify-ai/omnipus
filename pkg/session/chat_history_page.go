// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "context"

const DefaultChatHistoryLimit = 40
const DefaultChatHistoryMinRows = 20
const DefaultChatHistorySoftByteBudget int64 = 4 * 1024 * 1024

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

// ChatHistoryReadCut is opaque outside session. Its private representation is
// limited to identity declarations until the immutable reader is implemented.
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

// ReadChatHistoryPage declares the shared reader seam. Selection, content I/O
// and weighing are not implemented; this stub never returns a successful page.
func (us *UnifiedStore) ReadChatHistoryPage(
	ctx context.Context,
	sessionID string,
	opts ChatHistoryPageOptions,
) (ChatHistoryPage, error) {
	return ChatHistoryPage{}, ErrChatHistoryNotImplemented
}
