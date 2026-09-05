// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package session provides day-partitioned JSONL session storage per
// Appendix E §E.5 and Wave 1 user story US-5.
package session

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// EntryType classifies a transcript entry.
type EntryType string

const (
	// EntryTypeMessage is the default entry type for a user or assistant chat turn.
	EntryTypeMessage EntryType = "message"
	// EntryTypeCompaction marks a context-compaction summary entry.
	EntryTypeCompaction EntryType = "compaction"
	// EntryTypeSystem marks a system-level event entry.
	EntryTypeSystem EntryType = "system"
	// EntryTypeToolCall marks a tool invocation entry.
	EntryTypeToolCall EntryType = "tool_call"
	// EntryTypeTurnCancelled marks the JSONL entry written when a turn is canceled
	// mid-stream. Written once per fired cancel to transcript.jsonl (FR-15).
	EntryTypeTurnCancelled EntryType = "turn_canceled"
	// EntryTypeJudgeVerdict marks a transcript entry carrying a task.JudgeVerdict
	// (ADR-049, spec Part A §C/FR-025). Written alongside the worker's ADR-043
	// completion marker so the two cannot silently disagree — the Judge System
	// Agent's verdict is never inferred from absence (NFR-2).
	EntryTypeJudgeVerdict EntryType = "judge_verdict"
)

// SessionStatus classifies the lifecycle state of a session.
type SessionStatus string

const (
	// StatusActive is a session that is currently in use.
	StatusActive SessionStatus = "active"
	// StatusArchived is a session that has been intentionally closed.
	StatusArchived SessionStatus = "archived"
	// StatusInterrupted is a session that was terminated unexpectedly or canceled.
	StatusInterrupted SessionStatus = "interrupted"
)

// PartitionStore manages day-partitioned JSONL session transcripts per Appendix E §E.5.
type PartitionStore struct {
	mu      sync.Mutex
	agentID string
	baseDir string // ~/.omnipus/agents/<agentID>/sessions/
}

// NewPartitionStore returns a PartitionStore for the given agent workspace.
func NewPartitionStore(agentWorkspaceDir, agentID string) *PartitionStore {
	return &PartitionStore{
		agentID: agentID,
		baseDir: filepath.Join(agentWorkspaceDir, "sessions"),
	}
}

// SessionMeta is the meta.json file per Appendix E §E.5.1.
type SessionMeta struct {
	ID          string        `json:"id"`
	AgentID     string        `json:"agent_id"`
	Title       string        `json:"title,omitempty"`
	Status      SessionStatus `json:"status"` // StatusActive | StatusArchived | StatusInterrupted
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Model       string        `json:"model,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	Stats       SessionStats  `json:"stats"`
	WorkspaceID string        `json:"workspace_id,omitempty"`
	TaskID      string        `json:"task_id,omitempty"`
	Channel     string        `json:"channel"`
	// InstanceID is the channel INSTANCE this session belongs to — the key in
	// cfg.Channels, e.g. "whatsapp.eu", not the bare type "whatsapp".
	//
	// Channel alone is not an identity. An install can hold many instances of
	// one platform — a hundred WhatsApp numbers, each bound to its own
	// (workspace, agent) pair under ADR-029 — and every one of their sessions
	// records Channel=="whatsapp". Without this field they are
	// indistinguishable, so anything that acts on "the sessions of this
	// channel" acts on all of them: re-binding one number's workspace would
	// relabel the other ninety-nine.
	//
	// Empty on sessions created before this field existed, and on non-channel
	// sessions. Callers that key on it MUST treat empty as "unknown", never as
	// a match.
	InstanceID string `json:"instance_id,omitempty"`
	// PeerID is the channel-native chat/peer identifier (e.g. Telegram user ID "7236886139").
	// Set for non-webchat sessions to allow per-peer session resumption.
	PeerID     string   `json:"peer_id,omitempty"`
	Partitions []string `json:"partitions"`

	LastCompactionSummary string `json:"last_compaction_summary,omitempty"`

	// Owner is the username of the authenticated user who created this session.
	// Empty means unowned/shared (agent-created or legacy sessions). Internal
	// use only — not serialized to any REST response or wire format.
	Owner string `json:"owner,omitempty"`

	// v2 multi-agent fields (joined session model)
	AgentIDs            []string          `json:"agent_ids,omitempty"`
	ActiveAgentID       string            `json:"active_agent_id,omitempty"`
	CompactionSummaries map[string]string `json:"compaction_summaries,omitempty"` // per-agent compaction

	// ParentSessionID names the DIRECT parent of a delegated child session
	// (ADR-057 FR-008/W2). Empty for a root (non-delegated) session. This is
	// the write side of the FR-097 in-memory parent index (pkg/session/
	// unified.go's u4IndexAddChild/ChildCount) — every writer that persists
	// this field (currently the identity-group targeted writer,
	// u5WriteIdentityLocked in unified_meta_files.go) MUST also call
	// u4IndexAddChild(ParentSessionID, ID) so the index stays in sync with
	// disk; skipping that call leaves ChildCount permanently 0 for real
	// children, a silent failure of exactly this ADR's governing shape. See
	// FR-091 for the sole reader (GET /api/v1/sessions' roots-only + nested
	// listing) — without a reader this field could ship write-only with
	// every test green (spec note on W2).
	ParentSessionID string `json:"parent_session_id,omitempty"`

	// Goal loop state (ADR-049 D6/D7, spec Part B US-8, `/goal <condition>`),
	// following the TaskID precedent above: session-scoped, not a wire type
	// (the `goal_status` WS frame and `/goal status` reply are the wire/UX
	// surfaces). GoalCondition == "" means no active goal — replace-on-set
	// (FR-068) simply overwrites all five fields together.
	// GoalID is the stable identifier for the CURRENT goal generation
	// (ADR-053 R§8.11's `goal_id`, UAT S3 fix): minted once when a goal
	// activates from empty (fresh `/goal <condition>` or a confirmed fresh
	// pending goal), held constant across every round-advance/pause/judging
	// frame for that generation (an amendment via `/goal confirm` keeps it —
	// it is the SAME goal being refined), and cleared together with the rest
	// of the Goal* fields on `/goal clear`. This is what lets the SPA's
	// GoalPillTray key one pill per goal generation instead of collapsing
	// every goal this session ever carried into a single `_default` bucket.
	// Never regenerated mid-lifecycle — a fabricated per-frame id would be
	// worse than no id at all.
	GoalID           string `json:"goal_id,omitempty"`
	GoalCondition    string `json:"goal_condition,omitempty"`
	GoalRoundsUsed   int    `json:"goal_rounds_used,omitempty"`
	GoalMaxRounds    int    `json:"goal_max_rounds,omitempty"`
	GoalLatestReason string `json:"goal_latest_reason,omitempty"`
	// GoalStartedAt is an RFC 3339 UTC timestamp, used for `/goal status`'s
	// elapsed wall-clock (FR-069).
	GoalStartedAt string `json:"goal_started_at,omitempty"`
	// GoalLastActivityAt is the FR-064/D7 idle-expiry calendar-brake clock for
	// `/goal` (review r1, mirrors plan_engine.go's Plan.LastActivityAt
	// semantics exactly): an RFC 3339 UTC timestamp bumped on genuine goal
	// activity (goal set, or a judge round that actually ran) but
	// deliberately NOT on a judge-unavailability pause (R9/m4) — a
	// permanently-unavailable judge must still end the loop via this
	// calendar brake, never via a fabricated verdict or a clock that never
	// expires.
	GoalLastActivityAt string `json:"goal_last_activity_at,omitempty"`
	// GoalCriteriaJSON is the ADR-053 Phase-2 compiled criteria ladder
	// (FR-110/FR-113): the engine-invoked SMART compiler's output — a JSON-encoded
	// []task.AcceptanceCriterion (the S1 unified goal/criteria record, reusing the
	// SAME AcceptanceCriterion type tasks/plans use, DoD-11 — never a second
	// criteria type). Empty when no goal is active OR when a legacy pre-Phase-2
	// goal carries only GoalCondition (checkGoalLoopAfterTurn falls back to a
	// single prose criterion from GoalCondition in that case, preserving
	// back-compat). Cleared together with GoalCondition on /goal clear (FR-114).
	// Immutable once the user confirms the echo (D9); a re-statement AMENDS by
	// minting a fresh JSON via a diffed, confirmed amendment (N-6).
	GoalCriteriaJSON string `json:"goal_criteria,omitempty"`
	// GoalPendingJSON holds a PROPOSED-but-unconfirmed CompiledGoal JSON during
	// a re-statement amendment flow (N-6/D11: a `/goal <new intent>` issued while
	// a goal is ALREADY active is diffed as an amendment and confirmed via
	// `/goal confirm`, never silently recompiled). While pending, the ACTIVE
	// goal's Condition + GoalCriteriaJSON are untouched; on confirm the pending
	// goal mints a new generation (GoalCondition + GoalCriteriaJSON take the
	// proposed values, GoalPendingJSON clears). Ephemeral-ish: cleared on
	// /goal clear and on a fresh `/goal <intent>` that supersedes it.
	GoalPendingJSON string `json:"goal_pending,omitempty"`
	// PendingAskJSON is the AskUserQuestion tool's durable pending question
	// set (askuserquestion-tool-spec.md §0.4, M-R2-1): a JSON-encoded
	// askuser.PendingSet, written by pkg/askuser's Registry when a question
	// set parks the owner session's turn, updated on every server-side state
	// change (a default-safe auto-resolution, the answered/cancelled
	// terminal record §0.6), and re-read on boot to re-arm the 30-minute
	// default-safe timers (US-6 S1). Empty means no set was ever asked on
	// this session; a terminal-status record (status answered/cancelled)
	// means "not pending" — the collapsed card record renders from it on
	// history reload. Persisted in the goal.json field group alongside
	// GoalPendingJSON (its closest sibling: session-scoped pending
	// interaction state that must not bump the session's composed recency).
	PendingAskJSON string `json:"pending_ask,omitempty"`

	// GoalClarificationJSON holds the ADR-074 D4a pending-clarification record
	// (judgment-first spec US-3 S7): when the prose `/goal` LLM compile returns
	// a clarifying question instead of criteria, the original intent + the
	// question persist here (JSON, pkg/agent's goalClarificationRecord shape)
	// until the user's next ordinary chat message answers it (feeding ONE
	// resumed compile), or `/goal clear`/a fresh `/goal <intent>` discards it.
	// Mutually exclusive with GoalPendingJSON in practice (a question round has
	// produced no pending criteria yet). Covered by the same idle-expiry sweep
	// as active goals (US-3 S10).
	GoalClarificationJSON string `json:"goal_clarification,omitempty"`

	// Loop state (ADR-049 D6/D7, spec Part B US-9, `/loop`). LoopMode == ""
	// means no active loop. LoopMode is "interval" (cron `every` + `continue`)
	// or "self_paced" (agent-chosen one-shot `at` jobs, FR-072).
	LoopMode     string `json:"loop_mode,omitempty"`
	LoopPrompt   string `json:"loop_prompt,omitempty"`
	LoopRunCount int    `json:"loop_run_count,omitempty"`
	LoopMaxRuns  int    `json:"loop_max_runs,omitempty"`
	// LoopIntervalMS is set for interval mode (the fixed cadence); 0 for
	// self_paced mode (which instead carries LoopNextDelayMS per run).
	LoopIntervalMS int64 `json:"loop_interval_ms,omitempty"`
	// LoopNextDelayMS is the self-paced mode's most recently agent-chosen
	// next-run delay (FR-072), surfaced by `/loop status`. 0 until the first
	// self-paced run completes.
	LoopNextDelayMS int64 `json:"loop_next_delay_ms,omitempty"`
	// LoopJobID is the owning cron.CronJob.ID (pkg/agent's dedicated
	// LoopScheduler store) so /loop stop and the run-fired callback can find
	// and remove/reschedule the job.
	LoopJobID string `json:"loop_job_id,omitempty"`
	// LoopStartedAt is an RFC 3339 UTC timestamp, used for `/loop status`'s
	// elapsed wall-clock.
	LoopStartedAt string `json:"loop_started_at,omitempty"`
	// LoopLastActivityAt is the FR-064/D7 idle-expiry calendar-brake clock for
	// `/loop` (review r1): an RFC 3339 UTC timestamp bumped on genuine loop
	// activity (loop set, or a scheduled run that actually fired).
	LoopLastActivityAt string `json:"loop_last_activity_at,omitempty"`
}

// PostLoad backfills v2 multi-agent fields from the legacy AgentID field.
// Call after every JSON unmarshal of SessionMeta.
func (m *SessionMeta) PostLoad() {
	if len(m.AgentIDs) == 0 && m.AgentID != "" {
		m.AgentIDs = []string{m.AgentID}
	}
	if m.ActiveAgentID == "" && m.AgentID != "" {
		m.ActiveAgentID = m.AgentID
	}
}

// ModelTokens holds per-model token breakdown for a session.
// All fields are additive across turns that used the same model name.
type ModelTokens struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Total      int `json:"total"`
}

// SessionStats aggregates usage across all partitions.
type SessionStats struct {
	// TokensIn is the running total of uncached prompt (input) tokens.
	// Two sources feed it (see accumulateEntryStats in entry_stats.go):
	// the provider's PromptTokens split on ASSISTANT entries — this is the
	// live, populated path in production, wired from turn.go's assistant
	// TranscriptEntry construction, which is what closed the RC-7 defect
	// this field used to describe — and entry.Tokens on any NON-assistant
	// (user/system) entry. That second source remains structurally
	// unpopulated: no production caller sets Tokens on a user/system-role
	// entry before appending it, so it never contributes in practice. See
	// TestSessionStatsAggregation and TestAppendMessage_TokensIn_
	// UnpopulatedByRealisticCaller in daypartition_test.go, and
	// TestAppendTranscriptStrict_RecordsInputOutputSplit /
	// TestAppendTranscriptStrict_InOutReconcilesWithTotal in
	// token_split_test.go for the assistant-side coverage.
	TokensIn     int     `json:"tokens_in"`
	TokensOut    int     `json:"tokens_out"`
	TokensTotal  int     `json:"tokens_total"`
	Cost         float64 `json:"cost"`
	ToolCalls    int     `json:"tool_calls"`
	MessageCount int     `json:"message_count"`
	// Cache token breakdown (Wave 1). Omitted from JSON when zero so legacy
	// meta.json files without these fields round-trip cleanly.
	TokensCacheRead  int `json:"tokens_cache_read,omitempty"`
	TokensCacheWrite int `json:"tokens_cache_write,omitempty"`
	// ByModel maps model name → per-model token breakdown.
	// nil on old sessions; the reader must treat nil as empty.
	ByModel map[string]ModelTokens `json:"by_model,omitempty"`
}

// TranscriptEntry represents one line in a partition JSONL file.
type TranscriptEntry struct {
	ID          string       `json:"id"`
	Type        EntryType    `json:"type,omitempty"` // EntryTypeMessage | EntryTypeCompaction | EntryTypeSystem; empty = message
	Role        string       `json:"role,omitempty"` // "user" | "assistant" | "system"
	Content     string       `json:"content,omitempty"`
	Summary     string       `json:"summary,omitempty"` // for compaction entries
	Timestamp   time.Time    `json:"timestamp"`
	Tokens      int          `json:"tokens,omitempty"`
	Cost        float64      `json:"cost,omitempty"`
	Status      string       `json:"status,omitempty"` // "ok" | "error" | "interrupted"
	Attachments []Attachment `json:"attachments,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	AgentID     string       `json:"agent_id"` // which agent produced this entry (FR-002)
	// Model records which model produced this assistant message. Populated
	// on every assistant message written via pkg/agent/turn.go (FR-013).
	// Empty for legacy turns written before this field existed; the UI
	// omits the model span entirely for those (FR-014) — there is no
	// "(model not recorded)" placeholder string rendered.
	Model string `json:"model,omitempty"`
	// ErrorCode is the translated LLMError.code (per ADR-051 Rev 3).
	// Populated by appendErrorTranscript and consumed by the replay path so
	// the SPA can render the same code/message regardless of the live wire.
	ErrorCode string `json:"error_code,omitempty"`
	// ErrorRetryable is the translated LLMError.retryable flag.
	ErrorRetryable bool `json:"error_retryable,omitempty"`
	// CacheReadTokens and CacheWriteTokens carry the provider cache split for
	// this assistant turn. Both are 0 for non-assistant entries and for legacy
	// entries written before Wave 1 token tracking. Used to accumulate
	// SessionStats.TokensCacheRead/Write and ByModel per-model breakdown.
	// PromptTokens and CompletionTokens carry the provider's input/output
	// split for this entry. Tokens (above) is the collapsed total and cannot
	// express the split, which is why SessionStats.TokensIn and the per-model
	// In/Out fields were structurally always 0 before these existed.
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`

	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`

	// For compaction entries.
	MessagesCompacted int `json:"messages_compacted,omitempty"`

	// Truncated is set to true on the last assistant entry when a turn is
	// canceled mid-stream. Only serialized when true (FR-14). Only written to
	// transcript.jsonl; context.jsonl is never mutated (FR-14a).
	Truncated bool `json:"truncated,omitempty"`

	// Cancel-specific fields — only populated for EntryTypeTurnCancelled entries
	// (FR-15). All are omitempty so they are invisible on other entry types.
	TurnID               string   `json:"turn_id,omitempty"`
	CancelledByUser      string   `json:"canceled_by_user,omitempty"`
	CancelledByChannel   string   `json:"canceled_by_channel,omitempty"`
	CancelMethod         string   `json:"cancel_method,omitempty"` // "graceful" | "hard"
	DescendantsCancelled []string `json:"descendants_canceled,omitempty"`

	// ParentSpawnCallID marks this entry as an assistant-text entry produced
	// by a CHILD delegation sub-turn, not a genuine top-level turn. It equals
	// the spawning "delegate"/"spawn" ToolCall.ID in the PARENT's own turn —
	// the same correlation anchor ToolCall.ParentToolCallID already uses to
	// nest child tool calls under a subagent span (FR-H-001) — mirrored here
	// for assistant TEXT entries, which have no ToolCall wrapper to carry
	// ParentToolCallID.
	//
	// Populated only by pkg/agent/turn.go's appendIntermediateAssistantTranscript
	// / appendAssistantTranscript and pkg/gateway/websocket.go's
	// wsStreamer.Finalize, from turnState.parentSpawnCallID — non-empty ONLY
	// for a child turnState created by pkg/agent/subturn.go's spawnSubTurn.
	// Empty (and omitted from JSON) for every entry produced by a root
	// (non-delegated) turn, so existing/legacy transcripts round-trip
	// unchanged.
	//
	// [ADR-057 FR-037 doc correction] The paragraph that used to sit here
	// described the PRE-ADR-057 world and is retained below only as history,
	// because two of its load-bearing claims are now FALSE:
	//
	//   (1) "a delegated child sub-turn shares its parent's
	//       transcriptSessionID (spawnSubTurn sets
	//       TranscriptSessionID: parentTS.transcriptSessionID … there is no
	//       separate per-sub-turn transcript file)" — FALSE since FR-007.
	//       spawnSubTurn now sets TranscriptSessionID: childID
	//       (pkg/agent/subturn.go), so a delegated child owns its OWN
	//       store-backed session and its OWN transcript.jsonl. The parent's
	//       transcript is deliberately EMPTY of the child's writes. (What a
	//       child DOES inherit verbatim from its parent is the separate
	//       routingSessionID field — the cancel/interrupt reachability key,
	//       subturn.go:1130 — which is a different concept from this one and
	//       must not be conflated with it.)
	//
	//   (2) "replay.go uses it to withhold the entry from replay entirely" —
	//       FALSE since FR-034/FR-038. That skip is DELETED, not replaced,
	//       at all four former read sites. Because the child's narration no
	//       longer lands in the parent's transcript at all, there is nothing
	//       left for a read boundary to withhold, and FR-038 explicitly
	//       FORBIDS reintroducing a transcript visibility filter at any read
	//       boundary. See pkg/gateway/replay.go's streamReplay contract.
	//
	// THIS FIELD IS NOT DEAD — do not delete it on discovering that replay.go
	// no longer reads it. Post-ADR-057 it is written by three sites
	// (pkg/agent/turn.go's appendIntermediateAssistantTranscript :1328 and
	// appendAssistantTranscript :1394, plus pkg/gateway/websocket.go's
	// wsStreamer.Finalize :4471) and READ by pkg/tools/delegate.go's
	// recentActivityLines (:2173, ADR-057 FR-043), which reads the CHILD's
	// own durable session and filters to ParentSpawnCallID == spawnCallID so
	// a `delegate` status poll reports only THIS delegate call's activity.
	// Its post-057 job is therefore per-spawn-call attribution WITHIN a
	// child's own transcript, not parent/child separation ACROSS one shared
	// transcript. (Distinct from the identically-named ParentSpawnCallID on
	// the ToolExec*/SubTurnSpawn/SubTurnEnd event payloads —
	// pkg/agent/events.go:326/372/479/512, type session.ToolCallID — which
	// drives subagent span framing in websocket.go. Same name, different
	// field; changing one does not change the other.)
	//
	// HISTORICAL root cause (pre-ADR-057, retained because pkg/agent/turn.go
	// :896 and :1325 still cite this comment for it): when the child DID
	// share the parent's transcript, its narration landed in the same
	// transcript.jsonl as the delegator's real top-level messages,
	// indistinguishable by any OTHER field (Role, Content, AgentID, TurnID
	// were all populated normally). Live rendering never showed it as a chat
	// bubble — wsStreamer.Update withheld the live TokenFrame for a child
	// sub-turn's streaming (the "shadow-stream" ownership gate) while still
	// persisting via Finalize — so without this field replay.go had no signal
	// to replicate that suppression on reload: it flattened every entry with
	// non-empty Content into a top-level replay_message frame, doubling the
	// visible bubble count and leaking the delegate's raw internal report
	// text as if each were a separate top-level turn. The own-session split
	// (FR-007) removes that whole failure mode at the source.
	//
	// Backend-only: never serialized onto a wire frame.
	ParentSpawnCallID string `json:"parent_spawn_call_id,omitempty"`
}

// Attachment represents a file attached to a message.
type Attachment struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}

// ToolCallID is the opaque identifier for a single tool invocation.
// Using a named type prevents accidental mixing with other string IDs
// (e.g., span IDs, session IDs) at Go call sites.
// JSON marshaling is identical to a plain string — the wire format is unchanged.
type ToolCallID string

// ToolCall represents one tool invocation within a message.
type ToolCall struct {
	ID         ToolCallID     `json:"id"`
	Tool       string         `json:"tool"`
	Status     string         `json:"status"` // "success" | "error" | "pending" | "denied"
	DurationMS int64          `json:"duration_ms,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	// ParentToolCallID is set on tool calls that execute inside a sub-turn.
	// It equals the parent spawn tool call's ID, which is the correlation anchor
	// for the subagent span (FR-H-001). Empty for top-level tool calls.
	// span_id = "span_" + ParentToolCallID (derivable, not stored separately).
	ParentToolCallID ToolCallID `json:"parent_tool_call_id,omitempty"`
	// Error is a human-readable failure reason, set when Status is "error" and
	// no richer Result was already attached (media descriptors or a sync
	// delegate result — both already carry their own explanatory text).
	// Mirrors contracts/components/schemas/ToolCall.yaml's `error` property
	// and generated.ToolCall.Error (pkg/api/generated/openapi_types.gen.go).
	//
	// RC-5 (ADR-057 UAT root-cause fix): before this field existed, a failed
	// tool call persisted only {tool, status, duration_ms, parameters} with a
	// nil Result — the failure reason existed solely in the gateway's ERROR
	// log (ephemeral on some deployments) and in the live WS
	// ToolCallResultFrame, never in the durable transcript. The orchestrating
	// agent had no way to learn WHY a call failed and retried blindly. See
	// pkg/agent/loop.go's tcRecord construction (runTurn) for the writer and
	// pkg/gateway/replay.go's buildResult for the reader that restores
	// live/reload parity (RC-5c).
	Error string `json:"error,omitempty"`
	// ContentState is the ADR-066 D4/D5 projection state of this call's
	// result in the model's window — "capped" | "emptied"; empty means full
	// (the contract's default). Written by pkg/agent's choke point
	// (tool_result_admit.go) when it cuts a result at the door and by the
	// D5 emptying pass; mirrors ToolCall.yaml's `content_state`. Result then
	// holds the PROJECTED text the model saw; the full content stays on the
	// window archive line and in the gateway tool_results/ store.
	ContentState string `json:"content_state,omitempty"`
}

// NewSessionID generates a ULID-based session ID prefixed with "session_".
// Returns an error instead of panicking if ULID generation fails.
func NewSessionID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), crand.Reader)
	if err != nil {
		return "", fmt.Errorf("session: generate ULID: %w", err)
	}
	return "session_" + id.String(), nil
}

// NewSession creates a new session directory, writes meta.json, and returns the metadata.
// Implements US-5 AC1.
func (ps *PartitionStore) NewSession(channel, model, provider string) (*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now().UTC()
	sessionID, err := NewSessionID()
	if err != nil {
		return nil, err
	}
	meta := &SessionMeta{
		ID:        sessionID,
		AgentID:   ps.agentID,
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		Provider:  provider,
		Channel:   channel,
	}

	sessionDir := filepath.Join(ps.baseDir, meta.ID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create dir %q: %w", sessionDir, err)
	}

	if err := writeMeta(sessionDir, meta); err != nil {
		return nil, err
	}

	slog.Debug("session: created", "id", meta.ID, "agent", ps.agentID, "channel", channel)
	return meta, nil
}

// AppendMessage appends a transcript entry to the correct day partition and
// updates meta.json stats. Creates a new partition file when the date changes.
// Implements US-5 AC2, AC3, AC4.
func (ps *PartitionStore) AppendMessage(sessionID string, entry TranscriptEntry) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}

	// Determine partition file for entry's timestamp (UTC day boundary).
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
		entry.Timestamp = ts
	}
	partitionName := ts.UTC().Format("2006-01-02") + ".jsonl"
	partitionPath := filepath.Join(sessionDir, partitionName)

	// Register partition in meta if new.
	if !slices.Contains(meta.Partitions, partitionName) {
		meta.Partitions = append(meta.Partitions, partitionName)
	}

	// Append entry to JSONL partition (O_APPEND, atomic per POSIX).
	if err := fileutil.AppendJSONL(partitionPath, entry); err != nil {
		return fmt.Errorf("session: append to partition %q: %w", partitionPath, err)
	}

	// Update aggregated stats. See accumulateEntryStats (entry_stats.go) for
	// the full token-accounting convention this shares with UnifiedStore.
	// AppendTranscript and UnifiedStore.AppendTranscriptStrict.
	//
	// TokensIn is fed from two sources: the provider's uncached-prompt count
	// on ASSISTANT entries carrying the input/output split (turn.go
	// populates TranscriptEntry.PromptTokens/CompletionTokens from the
	// provider's usage response — this is the wiring that landed and is
	// what this whole fix is about), and entry.Tokens on any NON-assistant
	// entry (user/system) that happens to carry a Tokens value. That second
	// source remains structurally unpopulated in production — no caller
	// sets Tokens on a user/system-role entry before appending it, so a
	// session's TokensIn is entirely attributable to the assistant-side
	// split, never to a user/system entry. See
	// TestAppendMessage_TokensIn_UnpopulatedByRealisticCaller in
	// daypartition_test.go, which still pins that narrower gap.
	accumulateEntryStats(&meta.Stats, entry)
	meta.UpdatedAt = ts

	return writeMeta(sessionDir, meta)
}

// UpdateStats atomically updates the session stats (e.g., after a streaming
// response completes with final token counts).
func (ps *PartitionStore) UpdateStats(sessionID string, delta SessionStats) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}

	meta.Stats.TokensIn += delta.TokensIn
	meta.Stats.TokensOut += delta.TokensOut
	meta.Stats.TokensTotal += delta.TokensIn + delta.TokensOut
	meta.Stats.Cost += delta.Cost
	meta.Stats.ToolCalls += delta.ToolCalls
	meta.Stats.MessageCount += delta.MessageCount
	meta.Stats.TokensCacheRead += delta.TokensCacheRead
	meta.Stats.TokensCacheWrite += delta.TokensCacheWrite
	if delta.ByModel != nil {
		if meta.Stats.ByModel == nil {
			meta.Stats.ByModel = make(map[string]ModelTokens)
		}
		for model, d := range delta.ByModel {
			mt := meta.Stats.ByModel[model]
			mt.In += d.In
			mt.Out += d.Out
			mt.CacheRead += d.CacheRead
			mt.CacheWrite += d.CacheWrite
			mt.Total += d.Total
			meta.Stats.ByModel[model] = mt
		}
	}
	meta.UpdatedAt = time.Now().UTC()

	return writeMeta(sessionDir, meta)
}

// SetStatus updates the session status.
func (ps *PartitionStore) SetStatus(sessionID string, status SessionStatus) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.Status = status
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// SetTitle updates the title of an existing session.
func (ps *PartitionStore) SetTitle(sessionID string, title string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.Title = title
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// SetAgentID updates the agent_id on an existing session.
func (ps *PartitionStore) SetAgentID(sessionID string, agentID string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return err
	}
	meta.AgentID = agentID
	meta.UpdatedAt = time.Now().UTC()
	return writeMeta(sessionDir, meta)
}

// GetMeta returns the metadata for sessionID.
func (ps *PartitionStore) GetMeta(sessionID string) (*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return readMeta(filepath.Join(ps.baseDir, sessionID))
}

// readMeta reads meta.json from sessionDir.
func readMeta(sessionDir string) (*SessionMeta, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("session: read meta.json in %q: %w", sessionDir, err)
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("session: parse meta.json in %q: %w", sessionDir, err)
	}
	meta.PostLoad()
	return &meta, nil
}

// writeMeta writes meta atomically to sessionDir/meta.json.
func writeMeta(sessionDir string, meta *SessionMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal meta: %w", err)
	}
	path := filepath.Join(sessionDir, "meta.json")
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("session: write meta.json: %w", err)
	}
	return nil
}

// ListSessions returns all session metas in this store, sorted by UpdatedAt descending.
// Missing or unreadable meta files are skipped with a warning.
func (ps *PartitionStore) ListSessions() ([]*SessionMeta, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list sessions: %w", err)
	}

	var metas []*SessionMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readMeta(filepath.Join(ps.baseDir, entry.Name()))
		if err != nil {
			slog.Warn("session: skipping unreadable session", "dir", entry.Name(), "error", err)
			continue
		}
		metas = append(metas, meta)
	}

	slices.SortFunc(metas, func(a, b *SessionMeta) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})

	return metas, nil
}

// ClearAll removes every session directory from the store.
// Returns the number of sessions removed.
func (ps *PartitionStore) ClearAll() (int, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	entries, err := os.ReadDir(ps.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("session: clear all: read dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(ps.baseDir, entry.Name())
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("session: clear all: remove session dir", "dir", dir, "error", err)
			continue
		}
		removed++
	}
	return removed, nil
}

// ReadMessages returns all transcript entries for sessionID, merged across all
// day partitions in chronological order. Missing partitions are skipped with a warning.
func (ps *PartitionStore) ReadMessages(sessionID string) ([]TranscriptEntry, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sessionDir := filepath.Join(ps.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return nil, err
	}

	var all []TranscriptEntry
	for _, partition := range meta.Partitions {
		entries, err := readPartition(filepath.Join(sessionDir, partition))
		if err != nil {
			slog.Warn(
				"session: skipping unreadable partition",
				"session_id",
				sessionID,
				"partition",
				partition,
				"error",
				err,
			)
			continue
		}
		all = append(all, entries...)
	}

	return all, nil
}

// readPartition reads all TranscriptEntry values from a JSONL file.
// Blank lines and malformed lines are silently skipped.
func readPartition(path string) ([]TranscriptEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session: read partition %q: %w", path, err)
	}

	var entries []TranscriptEntry
	skipped := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry TranscriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			slog.Warn("session: skipping malformed partition line", "path", path, "error", err)
			skipped++
			continue
		}
		entries = append(entries, entry)
	}
	if skipped > 0 {
		slog.Warn("session: partition had skipped malformed lines", "path", path, "skipped", skipped)
	}

	return entries, nil
}
