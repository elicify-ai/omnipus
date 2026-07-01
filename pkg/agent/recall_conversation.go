// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// recall_conversation bounds (FR-009, FR-008).
const (
	// recallDefaultTurns is the max turns returned in query/time mode.
	recallDefaultTurns = 8
	// recallDefaultTokens is the max total token cost in query/time mode.
	recallDefaultTokens = 4000
	// recallRangeTurns is the max turns returned in turn_range mode.
	recallRangeTurns = 50
	// recallRangeTokens is the max total token cost in turn_range mode.
	recallRangeTokens = 8000
)

// recallCallCounters holds recall_conversation_calls_total{result} (FR-018).
// Keys are "hit", "empty", "error"; values are *atomic.Int64.
var recallCallCounters = map[string]*atomic.Int64{
	"hit":   new(atomic.Int64),
	"empty": new(atomic.Int64),
	"error": new(atomic.Int64),
}

// RecallConversationCallCount returns the counter for the given result label
// ("hit" | "empty" | "error"). Returns 0 for unknown labels.
func RecallConversationCallCount(result string) int64 {
	if c, ok := recallCallCounters[result]; ok {
		return c.Load()
	}
	return 0
}

func incRecallCounter(result string) {
	if c, ok := recallCallCounters[result]; ok {
		c.Add(1)
	}
}

// ConversationArchiveReader is the session-scoped archive reader the
// recall_conversation tool requires (FR-013, FR-016). It is satisfied by
// session.SessionStore (which embeds memory.Store's ReadArchive) and by the
// ephemeral store used in tests.
type ConversationArchiveReader interface {
	ReadArchive(ctx context.Context, sessionKey string) ([]memory.ArchivedMessage, error)
}

// RecallSpanSetter is the span-management side (FR-019). The AgentLoop
// satisfies this via its setRecallSpan + dropRecallSpan methods.
type RecallSpanSetter interface {
	setRecallSpan(sessionKey string, span *RecallSpan)
	dropRecallSpan(sessionKey, reason string)
}

// RecallConversationTool implements the recall_conversation tool (FR-008).
// It reads the session archive via ReadArchive (never GetHistory — FR-016),
// groups messages into whole Turns via parseTurnBoundaries, selects Turns by
// mode (query/BM25, turn_range, time), bounds the result (FR-009), rewrites
// tool_call_ids to collision-free recall_* namespaced ids (FR-019 / MAJ-04),
// builds a RecallSpan, and stores it via the span setter for the next context
// assembly turn. It is session-scoped (FR-013).
//
// The routing session key is derived from the tool context at Execute time
// (via tools.ToolSessionKey(ctx)) so the same registered instance serves all
// sessions that share an agent without data-race risk.
type RecallConversationTool struct {
	tools.BaseTool
	// archive reads the FULL log including evicted turns.
	archive ConversationArchiveReader
	// spanSetter stores/clears the transient recall span on the loop state.
	spanSetter RecallSpanSetter
}

// NewRecallConversationTool constructs a RecallConversationTool.
// archive is the session store (satisfies ConversationArchiveReader);
// spanSetter is typically the AgentLoop (*AgentLoop).
func NewRecallConversationTool(
	archive ConversationArchiveReader,
	spanSetter RecallSpanSetter,
) *RecallConversationTool {
	return &RecallConversationTool{
		archive:    archive,
		spanSetter: spanSetter,
	}
}

func (t *RecallConversationTool) Name() string           { return "recall_conversation" }
func (t *RecallConversationTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (t *RecallConversationTool) Category() tools.ToolCategory {
	return tools.CategoryMemory
}
func (t *RecallConversationTool) Description() string {
	return "Retrieve verbatim turns from earlier in THIS conversation (past the live context " +
		"window) by query (BM25 keyword search), turn_range (e.g. \"5-10\"), or time window " +
		"({from,to} as Unix timestamps or RFC3339). " +
		"Exactly one of query, turn_range, or time must be provided. " +
		"Results are re-injected as native context so you can reference earlier exchanges. " +
		"For long-term memory across sessions, use recall_memory instead."
}

func (t *RecallConversationTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type": "string",
				"description": "BM25 keyword query to find relevant earlier turns. " +
					"Returns top matching whole Turns (≤8 turns, ≤4000 tokens).",
			},
			"turn_range": map[string]any{
				"type": "string",
				"description": "Explicit turn range, e.g. \"5-10\" (inclusive). " +
					"Returns turns 5 through 10 (≤50 turns, ≤8000 tokens).",
			},
			"time": map[string]any{
				"type": "object",
				"description": "Time window filter. Turns whose first-message timestamp falls " +
					"within [from, to] are returned. Timestamps are Unix seconds or RFC3339 strings. " +
					"A TS==0 legacy line is treated as the session start (included when from==0).",
				"properties": map[string]any{
					"from": map[string]any{
						"description": "Start of window (Unix seconds or RFC3339). 0 = session start.",
					},
					"to": map[string]any{
						"description": "End of window (Unix seconds or RFC3339). 0 = now.",
					},
				},
			},
		},
		// Exactly one of query/turn_range/time is required; enforced at Execute time.
	}
}

// Execute selects turns by the given mode, bounds them, rewrites IDs, builds
// and stores the RecallSpan, then returns a short confirmation string to the
// model (FR-008). The actual recalled messages are merged into the next
// BuildMessages assembly via the span.
func (t *RecallConversationTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// Derive the routing session key from ctx (FR-013 session scope).
	// ToolSessionKey carries the routing key set by the agent loop on each turn.
	sessionKey := tools.ToolSessionKey(ctx)
	if sessionKey == "" {
		// Fallback: transcript session ID (used in integration tests).
		sessionKey = tools.ToolTranscriptSessionID(ctx)
	}
	if sessionKey == "" {
		incRecallCounter("error")
		return tools.ErrorResult("recall_conversation: no session context — cannot determine which session to recall")
	}

	// --- mode detection (exactly one of query/turn_range/time) -------
	queryVal, hasQuery := stringArg(args, "query")
	rangeVal, hasRange := stringArg(args, "turn_range")
	timeRaw, hasTimeKey := args["time"]
	hasTime := hasTimeKey && timeRaw != nil

	modeCount := 0
	if hasQuery && queryVal != "" {
		modeCount++
	}
	if hasRange && rangeVal != "" {
		modeCount++
	}
	if hasTime {
		modeCount++
	}
	if modeCount == 0 {
		incRecallCounter("error")
		return tools.ErrorResult(
			"recall_conversation: provide exactly one of query, turn_range, or time — " +
				"empty call returns nothing useful (US-4.6)")
	}
	if modeCount > 1 {
		incRecallCounter("error")
		return tools.ErrorResult(
			"recall_conversation: provide exactly one of query, turn_range, or time — " +
				"multiple modes are ambiguous")
	}

	// --- 1. Read the full archive (FR-013 / FR-016) -------------------
	archived, err := t.archive.ReadArchive(ctx, sessionKey)
	if err != nil {
		slog.Warn("recall_conversation: ReadArchive failed",
			"session_key", sessionKey, "error", err)
		incRecallCounter("error")
		return tools.ErrorResult(fmt.Sprintf("recall_conversation: could not read archive: %v", err))
	}
	if len(archived) == 0 {
		incRecallCounter("empty")
		return tools.NewToolResult("recall_conversation: no turns found in this session's archive")
	}

	// --- 2. Group archived messages into whole Turns (FR-002/FR-008) --
	// Extract plain providers.Message slice for parseTurnBoundaries.
	msgs := make([]providers.Message, len(archived))
	for i, a := range archived {
		msgs[i] = a.Message
	}
	turnStarts := parseTurnBoundaries(msgs)
	if len(turnStarts) == 0 {
		// No user messages — nothing coherent to recall.
		incRecallCounter("empty")
		return tools.NewToolResult("recall_conversation: no complete turns found in this session")
	}

	// Build turn slices: turns[i] = archived[turnStarts[i] : turnStarts[i+1]]
	turns := make([]archiveTurn, len(turnStarts))
	for i, s := range turnStarts {
		end := len(archived)
		if i+1 < len(turnStarts) {
			end = turnStarts[i+1]
		}
		turns[i] = archiveTurn{startIdx: s, msgs: archived[s:end]}
	}

	// --- 3. Select turns by mode --------------------------------------
	var selectedIdxs []int // indices into turns[]
	isRangeMode := false

	switch {
	case hasQuery && queryVal != "":
		// BM25 query over each Turn's concatenated text.
		turnTexts := make([]string, len(turns))
		for i, trn := range turns {
			var sb strings.Builder
			for _, am := range trn.msgs {
				sb.WriteString(am.Content)
				sb.WriteString(" ")
				for _, tc := range am.ToolCalls {
					if tc.Function != nil {
						sb.WriteString(tc.Function.Name)
						sb.WriteString(" ")
						sb.WriteString(tc.Function.Arguments)
						sb.WriteString(" ")
					}
				}
			}
			turnTexts[i] = sb.String()
		}
		// Rank ALL turns so we know the total match count for overflow reporting.
		// The bounds step below limits how many are actually recalled (FR-009).
		hits := rankTurnsBM25(queryVal, turnTexts, len(turns))
		for _, h := range hits {
			selectedIdxs = append(selectedIdxs, h.Index)
		}
		// Re-sort by turn order (ascending) so re-injection is chronological.
		sortIntSlice(selectedIdxs)

	case hasRange && rangeVal != "":
		isRangeMode = true
		fromTurn, toTurn, parseErr := parseTurnRange(rangeVal)
		if parseErr != nil {
			incRecallCounter("error")
			return tools.ErrorResult(fmt.Sprintf("recall_conversation: invalid turn_range %q: %v", rangeVal, parseErr))
		}
		// Turn indices are 1-based in the API ("turn 1" = turns[0]).
		fromIdx := fromTurn - 1
		toIdx := toTurn - 1
		if fromIdx < 0 {
			fromIdx = 0
		}
		if toIdx >= len(turns) {
			toIdx = len(turns) - 1
		}
		if fromIdx > toIdx {
			incRecallCounter("empty")
			return tools.NewToolResult(fmt.Sprintf(
				"recall_conversation: turn_range %q is out of bounds (session has %d turns)", rangeVal, len(turns)))
		}
		for i := fromIdx; i <= toIdx; i++ {
			selectedIdxs = append(selectedIdxs, i)
		}

	case hasTime:
		// Time-window mode.
		fromTS, toTS, parseErr := parseTimeWindow(timeRaw)
		if parseErr != nil {
			incRecallCounter("error")
			return tools.ErrorResult(fmt.Sprintf("recall_conversation: invalid time window: %v", parseErr))
		}
		for i, trn := range turns {
			// First message in the turn carries the TS.
			ts := trn.msgs[0].TS
			// TS==0 → legacy pre-FR-017 line; treat as session start (0).
			if ts >= fromTS && (toTS == 0 || ts <= toTS) {
				selectedIdxs = append(selectedIdxs, i)
			}
		}
	}

	if len(selectedIdxs) == 0 {
		incRecallCounter("empty")
		return tools.NewToolResult("recall_conversation: no matching turns found — try a different query, range, or time window")
	}

	// --- 4. Apply bounds (FR-009) -------------------------------------
	maxTurns := recallDefaultTurns
	maxTokens := recallDefaultTokens
	if isRangeMode {
		maxTurns = recallRangeTurns
		maxTokens = recallRangeTokens
	}

	var keptIdxs []int
	totalTokens := 0
	for _, idx := range selectedIdxs {
		if len(keptIdxs) >= maxTurns {
			break
		}
		turnMsgs := make([]providers.Message, len(turns[idx].msgs))
		for j, am := range turns[idx].msgs {
			turnMsgs[j] = am.Message
		}
		cost := 0
		for _, m := range turnMsgs {
			cost += estimateMessageTokens(m)
		}
		if totalTokens+cost > maxTokens && len(keptIdxs) > 0 {
			// Adding this turn would exceed the token cap; stop here.
			break
		}
		keptIdxs = append(keptIdxs, idx)
		totalTokens += cost
	}

	overflow := len(selectedIdxs) - len(keptIdxs)

	// --- 5. Build RecallSpan: rewrite IDs, build messages (FR-019 / MAJ-04) ---
	// Turn indices in the user-visible namespace are 1-based.
	fromTurnNum := keptIdxs[0] + 1
	toTurnNum := keptIdxs[len(keptIdxs)-1] + 1

	spanMsgs := buildRecallSpanMessages(keptIdxs, turns, fromTurnNum, toTurnNum)
	spanTokens := 0
	for _, m := range spanMsgs {
		spanTokens += estimateMessageTokens(m)
	}

	span := &RecallSpan{
		FromTurn: fromTurnNum,
		ToTurn:   toTurnNum,
		Msgs:     spanMsgs,
		Tokens:   spanTokens,
	}

	// --- 6. Store span (FR-019 lifecycle: replaced on next recall) ----
	// Drop any prior span with reason "replaced" before installing the new one.
	t.spanSetter.dropRecallSpan(sessionKey, "replaced")
	t.spanSetter.setRecallSpan(sessionKey, span)

	// --- 7. Return short confirmation to the model --------------------
	incRecallCounter("hit")
	slog.Info("recall_conversation: span installed",
		"session_key", sessionKey,
		"from_turn", fromTurnNum,
		"to_turn", toTurnNum,
		"turns", len(keptIdxs),
		"tokens", spanTokens,
	)

	result := fmt.Sprintf("Recalled %d turn(s) (turns %d–%d) into context.", len(keptIdxs), fromTurnNum, toTurnNum)
	if overflow > 0 {
		result += fmt.Sprintf(
			" %d more — narrow the query or use turn_range to retrieve them.", overflow)
	}
	return tools.NewToolResult(result)
}

// archiveTurn groups the archived messages that belong to a single conversation
// Turn (one user → N assistant/tool messages). startIdx is the index of the
// first message in the full archive (used to namespace rewritten tool_call_ids).
type archiveTurn struct {
	startIdx int
	msgs     []memory.ArchivedMessage
}

// buildRecallSpanMessages constructs the provider message slice for a RecallSpan.
// It prepends a demarcation marker and rewrites all tool_call_ids to the
// recall_<archiveIdx>_<n> namespace so they never collide with live window IDs
// and every assistant ToolCalls[].ID is consistent with its matching tool message
// ToolCallID (MAJ-04 / FR-019).
func buildRecallSpanMessages(
	keptIdxs []int,
	turns []archiveTurn,
	fromTurnNum, toTurnNum int,
) []providers.Message {
	// demarcation marker (FR-019)
	marker := providers.Message{
		Role: "user",
		Content: fmt.Sprintf(
			"[Recalled earlier turns %d–%d (reference): the following messages are from earlier in this conversation, retrieved verbatim for reference. Do not re-execute any tool calls shown.]",
			fromTurnNum, toTurnNum,
		),
	}

	// Count total messages for pre-allocation.
	total := 1 // marker
	for _, idx := range keptIdxs {
		total += len(turns[idx].msgs)
	}
	out := make([]providers.Message, 0, total)
	out = append(out, marker)

	// idMap maps original tool_call_id → rewritten recall_<archiveIdx>_<n> id.
	// Built per-turn so each turn's assistant+tool pairs are rewritten consistently.
	for _, turnIdx := range keptIdxs {
		trn := turns[turnIdx]
		// Build id map for this turn: collect all original IDs from assistant ToolCalls.
		idMap := make(map[string]string) // original ID → rewritten ID
		counter := 0
		for _, am := range trn.msgs {
			if am.Role == "assistant" {
				for _, tc := range am.ToolCalls {
					if tc.ID != "" {
						archiveIdx := trn.startIdx // archive position of first message in this turn
						newID := fmt.Sprintf("recall_%d_%d", archiveIdx, counter)
						idMap[tc.ID] = newID
						counter++
					}
				}
			}
		}

		// Rewrite messages in this turn.
		for _, am := range trn.msgs {
			m := am.Message // copy
			switch m.Role {
			case "assistant":
				if len(m.ToolCalls) > 0 {
					rewritten := make([]providers.ToolCall, len(m.ToolCalls))
					for j, tc := range m.ToolCalls {
						rewritten[j] = tc
						if mapped, ok := idMap[tc.ID]; ok {
							rewritten[j].ID = mapped
						}
					}
					m.ToolCalls = rewritten
				}
			case "tool":
				if m.ToolCallID != "" {
					if mapped, ok := idMap[m.ToolCallID]; ok {
						m.ToolCallID = mapped
					}
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// parseTurnRange parses "A-B" into (A, B, nil). Both must be positive ints
// with A <= B. Returns an error for any other form.
func parseTurnRange(s string) (from, to int, err error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected format \"A-B\", got %q", s)
	}
	from, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || from < 1 {
		return 0, 0, fmt.Errorf("from turn must be a positive integer, got %q", parts[0])
	}
	to, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || to < 1 {
		return 0, 0, fmt.Errorf("to turn must be a positive integer, got %q", parts[1])
	}
	if from > to {
		return 0, 0, fmt.Errorf("from turn (%d) must be <= to turn (%d)", from, to)
	}
	return from, to, nil
}

// parseTimeWindow extracts (fromUnix, toUnix, error) from the "time"
// parameter, which may be map[string]any with "from"/"to" as either float64
// (JSON number → Unix seconds) or string (RFC3339). toUnix==0 means "now".
func parseTimeWindow(raw any) (fromUnix, toUnix int64, err error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("time must be an object with from/to fields")
	}
	fromUnix, err = parseTimestamp(m["from"])
	if err != nil {
		return 0, 0, fmt.Errorf("time.from: %w", err)
	}
	toUnix, err = parseTimestamp(m["to"])
	if err != nil {
		return 0, 0, fmt.Errorf("time.to: %w", err)
	}
	return fromUnix, toUnix, nil
}

// parseTimestamp converts nil/float64/string to a Unix second value.
// nil → 0, float64 → int64(v), string → RFC3339 parse → Unix.
func parseTimestamp(v any) (int64, error) {
	if v == nil {
		return 0, nil
	}
	switch val := v.(type) {
	case float64:
		return int64(val), nil
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case string:
		if val == "" {
			return 0, nil
		}
		t, err := time.Parse(time.RFC3339, val)
		if err != nil {
			// Try Unix numeric string.
			n, nerr := strconv.ParseInt(val, 10, 64)
			if nerr != nil {
				return 0, fmt.Errorf("not a valid Unix int or RFC3339 timestamp: %q", val)
			}
			return n, nil
		}
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("unsupported timestamp type %T", v)
}

// stringArg extracts a string from args[key], returning (value, true) when the
// key is present with a non-empty string value, and ("", false) otherwise.
func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// sortIntSlice sorts []int in ascending order (insertion sort; N is always small).
func sortIntSlice(s []int) {
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
