package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// handoffTranscriptWriteFailures counts how many times SwitchAgentTool's
// audit-trail AppendTranscriptStrict write failed (ADR-057 FR-002: every
// AppendTranscript invocation site must surface the now-possible
// strict-write error as a counter increment plus a WARN naming the session
// id — see the spec's "conversion boundary" table, handoff.go row). The
// write itself stays best-effort by design (Execute's audit step doc
// comment): a failed audit entry must never fail the switch operation, so
// this counter is the only durable, operator-visible signal that it
// happened. Exposed via HandoffTranscriptWriteFailures() for tests and
// operator tooling. Name retained across the ADR-071 D4 merge (hand_off +
// return_to_default -> switch_agent) — it is an internal metric name, not a
// tool identity, and renaming it has no user-facing effect.
var handoffTranscriptWriteFailures atomic.Uint64

// HandoffTranscriptWriteFailures returns the current value of the
// omnipus_handoff_transcript_write_failures_total counter (FR-002).
func HandoffTranscriptWriteFailures() uint64 {
	return handoffTranscriptWriteFailures.Load()
}

// ErrAlreadyActive is returned by SessionStore.SwitchAgent when the session is
// already assigned to the requested agent. Treat as a no-op success.
// (Aliased from session.ErrAlreadyActive — both packages share the same sentinel.)
var ErrAlreadyActive = session.ErrAlreadyActive

// AgentRegistryReader is a minimal interface for looking up agents by ID.
// It is satisfied by *agent.AgentRegistry — using an interface here avoids
// an import cycle (tools → agent → tools).
type AgentRegistryReader interface {
	// GetAgentName returns the display name and a boolean indicating whether
	// the agent exists. Used by SwitchAgentTool to validate target and build
	// user-facing messages.
	GetAgentName(agentID string) (string, bool)

	// IsWorker reports whether the agent identified by agentID is a sub-agent
	// worker (the delegation-only labor tier). SwitchAgentTool uses it to
	// reject a switch to a worker — workers are invoked via delegation, not
	// hand-off, so a worker must never become a session pin / live chat
	// target. Returns false for an unknown agent.
	IsWorker(agentID string) bool
}

// HandoffEvent carries all context fields for a handoff or return-to-default event.
// Using a struct avoids brittle positional arguments and makes call sites self-documenting.
type HandoffEvent struct {
	// Channel is the turn's channel (e.g. "webchat", "whatsapp", "telegram").
	// Load-bearing for routing: channel inbound messages carry no SessionID, so
	// their handoff override is keyed by chat scope ("chat:<channel>:<chatID>"),
	// not "session:<id>". Without Channel a channel handoff is silently dropped
	// and routing falls back to ResolveRoute (the "agent stays" bug).
	Channel   string
	ChatID    string
	SessionID string
	AgentID   string
	AgentName string
}

// HandoffFunc is the callback signature for handoff notifications.
type HandoffFunc func(HandoffEvent)

// HandoffSessionStore is the subset of *session.UnifiedStore that
// SwitchAgentTool requires. Defining it as an interface decouples the tools
// package from the concrete store type, which is being refactored by the
// session-store subagent in parallel.
//
// *session.UnifiedStore satisfies this interface once SwitchAgent is added to it.
type HandoffSessionStore interface {
	// SwitchAgent atomically changes the active agent on the session.
	// Returns ErrAlreadyActive (session.ErrAlreadyActive) when the session is
	// already on the requested agent — callers MUST treat this as success.
	SwitchAgent(sessionID, newAgentID string) error

	// ReadTranscript returns all transcript entries for the session.
	ReadTranscript(sessionID string) ([]session.TranscriptEntry, error)

	// AppendTranscriptStrict appends a single entry to the session
	// transcript, returning a non-nil error (and creating nothing on disk)
	// when sessionID does not name a real, store-backed session (ADR-057
	// FR-001/FR-002 — pkg/session/unified_api.go:69). Converted from the
	// lenient AppendTranscript (ADR-057 U22, W3c): both call sites below
	// already had an error branch, so this is a runtime-behavior change at
	// an existing check, not a new one.
	AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error
}

// SwitchAgentDefaultTarget is the reserved switch_agent `target` value that
// always means "the configured default agent" (ADR-071 §5.1.3) — exact
// match, case-sensitive, and it always wins over any real agent whose id
// happens to be the literal string "default"; there is no fallback to an
// id-matched agent and no lookup-order subtlety. Exported so callers outside
// this package that must reason about the same reservation — agent
// create/update boundary rejection, the upgrade-time boot WARN for a
// pre-existing agent literally named "default" — check against the same
// literal rather than re-declaring it.
const SwitchAgentDefaultTarget = "default"

// SwitchAgentTool switches the active agent for a session — either handing
// off to a named target agent, or returning to the configured default.
//
// ADR-071 D4 merges the retired HandoffTool and ReturnToDefaultTool into
// this one tool identity (the same "one capability, one tool, variation
// becomes a parameter" precedent ADR-036 set for bash and delegate).
// target == SwitchAgentDefaultTarget selects the return-to-default branch;
// any other value is a named-agent hand-off. The two branches share every
// step below except the token-budget transcript transfer (named-target
// only — the default agent isn't cold, it's already in this transcript)
// and the audit entry's content/AgentID stamping (asymmetric by design —
// see Execute's own comment on that step).
//
// On a successful switch it:
//  1. Resolves the target agent — the default sentinel, or a registry lookup.
//  2. Rejects a worker target — unconditionally, on both branches.
//  3. Atomically switches the active agent on the session (idempotent).
//  4. Named-target branch only: reads the session transcript and applies a
//     50% token-budget split so the target agent receives recent context
//     without overflowing its context window.
//  5. Appends a system entry to the transcript as an audit-trail record.
//  6. Notifies the frontend so the UI can update its active-agent indicator.
type SwitchAgentTool struct {
	BaseTool
	getRegistry      func() AgentRegistryReader
	sessionStore     HandoffSessionStore
	getContextWindow func(agentID string) int
	getDefaultAgent  func() string
	onHandoff        HandoffFunc
}

// NewSwitchAgentTool creates a SwitchAgentTool.
//
//   - getRegistry is called at Execute time (not construction time) so hot
//     reloads are automatically reflected without rebuilding the tool.
//   - sessionStore provides atomic agent switching and transcript access.
//   - getContextWindow resolves the target agent's context window for
//     budget math (named-target branch only); it should follow:
//     agent-specific → defaults → 8192.
//   - getDefaultAgent resolves the default agent ID from config at call
//     time (target: "default" branch only).
//   - onHandoff notifies the frontend of the agent switch; may be nil.
func NewSwitchAgentTool(
	getRegistry func() AgentRegistryReader,
	sessionStore HandoffSessionStore,
	getContextWindow func(agentID string) int,
	getDefaultAgent func() string,
	onHandoff HandoffFunc,
) *SwitchAgentTool {
	return &SwitchAgentTool{
		getRegistry:      getRegistry,
		sessionStore:     sessionStore,
		getContextWindow: getContextWindow,
		getDefaultAgent:  getDefaultAgent,
		onHandoff:        onHandoff,
	}
}

func (t *SwitchAgentTool) Name() string           { return "switch_agent" }
func (t *SwitchAgentTool) Scope() ToolScope       { return ScopeCore }
func (t *SwitchAgentTool) Category() ToolCategory { return CategoryDelegation }

func (t *SwitchAgentTool) Description() string {
	return "Switch the active agent for this session — hand off to a named agent (target: <agent_id>) or return to the default agent (target: \"default\")."
}

func (t *SwitchAgentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{
				"type": "string",
				"description": "ID of the target agent (e.g. \"ray\", \"ava\", \"jim\"), " +
					"or the literal string \"default\" to return to the default agent",
			},
			"note": map[string]any{
				"type": "string",
				"description": "Optional. When switching to a named agent: context or instructions for " +
					"that agent about this conversation. When returning to the default agent " +
					"(target: \"default\"): a summary of what was accomplished. Strongly recommended " +
					"when handing off to another agent — it is the only context the incoming agent " +
					"gets beyond the transcript.",
			},
		},
		// note is declared optional (ADR-071 §5.1.1): hand_off's equivalent
		// "context" param was schema-required but never enforced in
		// Execute — no validation, no error on a missing/empty value — so
		// this is a deliberate, near-costless relaxation, not a behavior
		// regression. Only target is required.
		"required": []string{"target"},
	}
}

func (t *SwitchAgentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// Overall 10-second timeout — unconditional across both branches
	// (ADR-071 §5.1.2, a deliberate widening from hand_off's original
	// handoff-only wrapper). The default-return branch does no transcript
	// I/O so the ceiling costs it nothing, and one context lifetime for a
	// tool that mutates live session state is the safer default.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	target, ok := args["target"].(string)
	if !ok || target == "" {
		return ErrorResult("target is required")
	}
	note, _ := args["note"].(string)

	toDefault := target == SwitchAgentDefaultTarget

	// Step 1: Resolve the target agent. The sentinel always wins
	// (ADR-071 §5.1.3 part 1) — no fallback to a real agent literally
	// named "default", no lookup-order subtlety.
	reg := t.getRegistry()
	var agentID, agentName string
	if toDefault {
		agentID = t.getDefaultAgent()
		if agentID == "" {
			return ErrorResult("no default agent configured")
		}
		// Matches ReturnToDefaultTool's original behavior exactly: the
		// default agent's own id is used as its display name in the audit
		// entry / result text, with no registry display-name lookup.
		agentName = agentID
	} else {
		var exists bool
		agentName, exists = reg.GetAgentName(target)
		if !exists {
			return ErrorResult(fmt.Sprintf("agent %q not found — check the agent ID", target))
		}
		agentID = target
	}

	// Step 2: Reject a worker target — unconditional on both branches
	// (ADR-071 §5.1.2, a deliberate small improvement over the retired
	// return_to_default, which had no worker check at all). A hand-edited
	// config can point the default-agent singleton at a worker; without
	// this check that misconfiguration would silently pin the session to
	// it instead of producing the clear error hand_off already gave for
	// the named-target case.
	if reg.IsWorker(agentID) {
		return ErrorResult(
			fmt.Sprintf(
				"cannot switch to %q — it is a worker; workers are invoked via delegation, not switch_agent",
				agentName,
			),
		)
	}

	// Step 3: Get session ID from context.
	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return ErrorResult("switch_agent is not available in this context (no session key)")
	}

	// Step 4: Atomic switch (idempotent via ErrAlreadyActive) (MAJ-005, MAJ-006, FR-005, FR-015).
	if err := t.sessionStore.SwitchAgent(sessionID, agentID); err != nil {
		if errors.Is(err, ErrAlreadyActive) {
			return NewToolResult(fmt.Sprintf("Already connected to %s.", agentName))
		}
		return ErrorResult(fmt.Sprintf("failed to switch agent: %v", err))
	}

	// Step 5: Token-budget-aware context transfer (FR-004, FR-011) — SKIPPED
	// when target == "default" (ADR-071 §5.1.2): the transfer exists to
	// brief a COLD agent, one that hasn't been in this conversation. The
	// default agent isn't cold — it was in the conversation before any
	// handoff, its own entries are already in this same transcript, and it
	// hydrates them on its next turn regardless. Paying a full
	// ReadTranscript + budget split to re-brief an agent that already has
	// the context is pure cost for no information; skipping it here
	// preserves today's return_to_default behavior exactly.
	var summaryLine string
	var recent []session.TranscriptEntry
	if !toDefault {
		contextWindow := t.getContextWindow(agentID)
		budget := int(float64(contextWindow) * 0.50)

		transcript, err := t.sessionStore.ReadTranscript(sessionID)
		if err != nil {
			slog.Warn("switch_agent: could not read transcript for context transfer", "session", sessionID, "error", err)
			// Proceed with empty context rather than failing the switch.
			transcript = nil
		}
		var older []session.TranscriptEntry
		recent, older = splitByTokenBudget(transcript, budget)
		if len(older) > 0 {
			summaryLine = fmt.Sprintf("[%d earlier messages not shown]", len(older))
		}
	}

	// Step 6: Log the switch as an audit-trail entry (FR-016). Content AND
	// AgentID stamping stay asymmetric by branch, DELIBERATELY (ADR-071
	// §5.1.2) — do not "clean up" what looks like an inconsistency:
	//   - Named target: stamps the TARGET agent, because transcript
	//     hydration on a fresh turn surfaces this entry under the
	//     INCOMING agent's own history (e.g. Ray sees the brief on his
	//     first turn after Mia hands off to him).
	//   - Default: stamps the CURRENT (outgoing) agent, because this entry
	//     is a record of what the outgoing agent did.
	// The "Handoff: " content prefix is FROZEN (ADR-071 §5.2.2a) —
	// pkg/gateway/replay.go matches it via strings.HasPrefix with no
	// shared constant between producer and consumer; do not rewrite it
	// without introducing one first. The default branch's
	// "Returned to default agent (...)" text does NOT share that prefix
	// and so — matching today's return_to_default exactly — is not
	// replayed into an agent_switched frame; that pre-existing gap is
	// documented, not fixed, by this ADR (§5.2.2a).
	currentAgentID := ToolAgentID(ctx)
	var content, auditAgentID, forUser, forLLMHeadline string
	if toDefault {
		content = fmt.Sprintf("Returned to default agent (%s).", agentID)
		if note != "" {
			content = fmt.Sprintf("Returned to default agent (%s). Summary: %s", agentID, note)
		}
		auditAgentID = currentAgentID
		forUser = "Returning to default agent."
		forLLMHeadline = content
	} else {
		content = fmt.Sprintf("Handoff: %s → %s. Context: %s", currentAgentID, agentName, note)
		auditAgentID = agentID
		forUser = fmt.Sprintf("Connecting you with %s...", agentName)
		forLLMHeadline = fmt.Sprintf("Handoff complete. %s is now active.", agentName)
	}
	appendErr := t.sessionStore.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("switch-agent-%d", time.Now().UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		AgentID:   auditAgentID,
		Content:   content,
		Timestamp: time.Now().UTC(),
	})
	if appendErr != nil {
		// FR-002: surface the now-possible strict-write error as a counter
		// increment plus a WARN naming the session id — this remains
		// best-effort (the switch itself already succeeded above), but the
		// failure must no longer be invisible.
		handoffTranscriptWriteFailures.Add(1)
		slog.Warn("switch_agent: could not write audit entry to transcript", "session", sessionID, "error", appendErr)
	}

	// Step 7: Notify frontend (so the UI can update its active-agent indicator).
	if t.onHandoff != nil {
		chatID := ToolChatID(ctx)
		if chatID == "" {
			slog.Warn("switch_agent: empty chatID; UI agent_switched will not target a specific connection",
				"session_id", sessionID)
		}
		t.onHandoff(HandoffEvent{
			Channel:   ToolChannel(ctx),
			ChatID:    chatID,
			SessionID: sessionID,
			AgentID:   agentID,
			AgentName: agentName,
		})
	}

	// Step 8: Return context for the caller.
	forLLMParts := []string{forLLMHeadline}
	if summaryLine != "" {
		forLLMParts = append(forLLMParts, summaryLine)
	}
	if len(recent) > 0 {
		forLLMParts = append(forLLMParts, "Recent context:")
		forLLMParts = append(forLLMParts, formatRecentMessages(recent))
	}

	return &ToolResult{
		ForUser: forUser,
		ForLLM:  strings.Join(forLLMParts, "\n"),
	}
}

// resolveSessionID returns the session ID from context, preferring the transcript
// session ID (actual directory name) over the routing session key.
func resolveSessionID(ctx context.Context) string {
	if sid := ToolTranscriptSessionID(ctx); sid != "" {
		return sid
	}
	return ToolSessionKey(ctx)
}

// splitByTokenBudget partitions entries so that the entries in recent fit within
// budgetTokens (counting from the end of the transcript). Entries that do not
// fit are returned as older.
//
// The algorithm walks backward from the newest entry, accumulating estimated
// token counts until the budget is exhausted. All entries before the cutoff
// point are "older"; entries at or after the cutoff are "recent".
func splitByTokenBudget(entries []session.TranscriptEntry, budgetTokens int) (recent, older []session.TranscriptEntry) {
	if len(entries) == 0 {
		return nil, nil
	}
	tokensSoFar := 0
	cutoff := 0
	for i := len(entries) - 1; i >= 0; i-- {
		tokens := estimateEntryTokens(entries[i])
		if tokensSoFar+tokens > budgetTokens {
			cutoff = i + 1
			break
		}
		tokensSoFar += tokens
	}
	return entries[cutoff:], entries[:cutoff]
}

// estimateEntryTokens returns a fast token-count estimate for a transcript entry.
// When the entry carries a pre-computed Tokens value, that is used directly.
// Otherwise, content length is divided by 2.5 chars/token (matching context_budget.go).
func estimateEntryTokens(e session.TranscriptEntry) int {
	if e.Tokens > 0 {
		return e.Tokens
	}
	// ~2.5 chars per token — same heuristic as context_budget.go.
	return len(e.Content)/2 + 1
}

// formatRecentMessages renders a slice of transcript entries as a compact
// text block suitable for injection into the LLM's context.
func formatRecentMessages(entries []session.TranscriptEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, e := range entries {
		agentTag := ""
		if e.AgentID != "" {
			agentTag = fmt.Sprintf(" [%s]", e.AgentID)
		}
		fmt.Fprintf(&sb, "%s%s: %s\n", e.Role, agentTag, e.Content)
	}
	return strings.TrimRight(sb.String(), "\n")
}
