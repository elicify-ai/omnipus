// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// FR-023..FR-030, FR-032, FR-032a: Session-end recap pipeline.
//
// CloseSession → runRecap → LLM call → WriteLastSession + AppendRetro + audit.
// BootstrapRecapPass: on gateway start, re-cap sessions missing LAST_SESSION.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/utils"
)

// CloseSession triggers an async session-end recap if AutoRecapEnabled is set
// and this sessionID has not already been claimed. Idempotent: duplicate calls
// for the same sessionID are silently dropped (FR-027).
func (al *AgentLoop) CloseSession(sessionID, trigger string) {
	if !al.cfg.Agents.Defaults.AutoRecapEnabled {
		return
	}

	// Idempotency gate (FR-027): only one goroutine wins the claim.
	if _, loaded := al.claimedCloseSessions.LoadOrStore(sessionID, true); loaded {
		// Emit an audit entry so operators can see which trigger arrived second.
		al.auditRecap(sessionID, "", trigger, "skipped_already_claimed")
		return
	}

	// Cancel any outstanding idle ticker for this session.
	al.cancelIdleTicker(sessionID)

	go al.runRecap(sessionID, trigger)
}

// runRecap performs the session-end LLM summarisation and persists the result.
// Runs in a goroutine; a top-level recover() prevents a panic in any
// subsystem (provider, JSON parse, file I/O) from killing the gateway process.
func (al *AgentLoop) runRecap(sessionID, trigger string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("session_end: runRecap panic recovered",
				"session_id", sessionID,
				"trigger", trigger,
				"panic", r,
			)
			al.auditRecap(sessionID, "", trigger, "panic_recovered")
		}
	}()

	agentInst, err := al.AgentForSession(sessionID)
	if err != nil {
		// Heuristic fallback: agent deleted or session meta unavailable.
		al.writeHeuristicFallbackRetro(sessionID, trigger, "agent_deleted", nil)
		return
	}

	// Read the session transcript from the shared store.
	store := al.sharedSessionStore
	if store == nil {
		al.writeHeuristicFallbackRetro(sessionID, trigger, "no_session_store", agentInst)
		return
	}

	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		al.writeHeuristicFallbackRetro(sessionID, trigger, fmt.Sprintf("transcript_read_error: %v", err), agentInst)
		return
	}

	// FR-028: filter to user-role, non-empty, non-SubTurn, non-interrupt-hint messages.
	const subTurnPrefix = "[SubTurn Result]"
	const interruptHintLiteral = "Interrupt requested. Stop scheduling tools and provide a short final summary."
	var userTurns []string
	toolCallCount := 0
	for _, e := range entries {
		if e.Role == "user" {
			content := strings.TrimSpace(e.Content)
			if content == "" {
				continue
			}
			if strings.HasPrefix(content, subTurnPrefix) {
				continue
			}
			if content == interruptHintLiteral {
				continue
			}
			userTurns = append(userTurns, content)
		}
		toolCallCount += len(e.ToolCalls)
	}

	// Build the conversation text, truncate to 2000 tokens (~8000 runes).
	// FR-030: "truncate oldest" — we keep the tail (most recent turns) because
	// the recap's value is summarizing what just happened, not what happened
	// 40 turns ago.
	const tokenBudget = 2000
	combined := strings.Join(userTurns, "\n\n")
	runes := []rune(combined)
	prefix := ""
	if len(runes)/4 > tokenBudget {
		budgetRunes := tokenBudget * 4
		combined = string(runes[len(runes)-budgetRunes:])
		prefix = "[history truncated for summarisation]\n\n"
	}
	historyText := prefix + combined

	// Resolve recap model (FR-029): prefer LightModel when AutoRecapEnabled.
	recapModel := ""
	if al.cfg.Agents.Defaults.Routing != nil && al.cfg.Agents.Defaults.AutoRecapEnabled {
		recapModel = al.cfg.Agents.Defaults.Routing.LightModel
	}
	if recapModel == "" {
		recapModel = agentInst.Model
	}

	// Build recap prompt.
	recapPrompt := `Summarize this conversation in ≤ 150 words. Then list up to 5 wins, up to 5 needs-improvement items, and up to 5 items worth remembering long-term. Respond ONLY with valid JSON: {"recap":"...", "went_well":[...], "needs_improvement":[...], "worth_remembering":[...]}`

	msgs := []providers.Message{
		{Role: "user", Content: historyText + "\n\n" + recapPrompt},
	}

	// Cost-guard options (FR-029a): hard output cap + no thinking/reasoning tokens.
	opts := map[string]any{
		"max_tokens":        250,
		"extended_thinking": false,
		"extra_body": map[string]any{
			"reasoning": map[string]any{"exclude": true},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, llmErr := agentInst.Provider.Chat(ctx, msgs, nil, recapModel, opts)

	if llmErr != nil {
		slog.Warn("session_end: llm call failed",
			"session_id", sessionID,
			"agent_id", agentInst.ID,
			"error", llmErr.Error(),
		)
		// SF1: emit two distinct audit entries so operators can see both outcomes:
		// (1) the LLM call failed, (2) the heuristic fallback was written.
		al.auditRecap(sessionID, agentInst.ID, trigger, "llm_failed:"+classifyLLMError(llmErr))
		al.writeHeuristicFallbackRetroWithCount(
			sessionID,
			trigger,
			classifyLLMError(llmErr),
			agentInst,
			len(entries),
			toolCallCount,
		)
		return
	}

	// Parse LLM JSON response.
	type recapJSON struct {
		Recap            string   `json:"recap"`
		WentWell         []string `json:"went_well"`
		NeedsImprovement []string `json:"needs_improvement"`
		WorthRemembering []string `json:"worth_remembering"`
	}

	var parsed recapJSON
	responseText := resp.Content
	if parseErr := json.Unmarshal([]byte(responseText), &parsed); parseErr != nil {
		// Model returned something that is not the expected JSON envelope.
		// The raw body can still be useful for debugging recap-prompt regressions,
		// so log a truncated preview before discarding it and falling back.
		slog.Warn("session_end: recap JSON parse failed",
			"session_id", sessionID,
			"agent_id", agentInst.ID,
			"parse_error", parseErr.Error(),
			"response_preview", utils.Truncate(responseText, 500),
		)
		al.writeHeuristicFallbackRetroWithCount(
			sessionID,
			trigger,
			"json_parse_error",
			agentInst,
			len(entries),
			toolCallCount,
		)
		return
	}

	// Persist last-session summary.
	memory := agentInst.ContextBuilder.Memory()
	if memory == nil {
		al.writeHeuristicFallbackRetroWithCount(
			sessionID,
			trigger,
			"no_memory_store",
			agentInst,
			len(entries),
			toolCallCount,
		)
		return
	}

	if err := memory.WriteLastSession(parsed.Recap); err != nil {
		slog.Warn("session_end: failed to write LAST_SESSION.md",
			"session_id", sessionID,
			"agent_id", agentInst.ID,
			"error", err,
		)
	}

	retro := Retro{
		Timestamp:        time.Now().UTC(),
		Trigger:          RecapTrigger(trigger),
		Fallback:         false,
		Recap:            parsed.Recap,
		WentWell:         parsed.WentWell,
		NeedsImprovement: parsed.NeedsImprovement,
	}
	if err := memory.AppendRetro(sessionID, retro); err != nil {
		slog.Warn("session_end: failed to append retro",
			"session_id", sessionID,
			"agent_id", agentInst.ID,
			"error", err,
		)
	}

	al.auditRecap(sessionID, agentInst.ID, trigger, "success")
	// Claim stays for the process lifetime: idempotency is provided by
	// agentSessionHasRetro at the file level. Re-recap on a subsequent
	// bootstrap pass will be blocked by the file check, not this map.

	slog.Info("session_end: recap complete",
		"session_id", sessionID,
		"agent_id", agentInst.ID,
		"trigger", trigger,
		"recap_len", len(parsed.Recap),
	)
}

// writeHeuristicFallbackRetro writes a fallback retro entry when recap fails
// without the transcript pre-read available. Prefer the _WithCount variant
// when the caller has already computed turn + tool-call counts.
func (al *AgentLoop) writeHeuristicFallbackRetro(sessionID, trigger, fallbackReason string, agentInst *AgentInstance) {
	turnCount, toolCallCount := 0, 0
	if agentInst != nil {
		if store := al.sharedSessionStore; store != nil {
			if entries, err := store.ReadTranscript(sessionID); err == nil {
				turnCount = len(entries)
				for _, e := range entries {
					toolCallCount += len(e.ToolCalls)
				}
			}
		}
	}
	al.writeHeuristicFallbackRetroWithCount(sessionID, trigger, fallbackReason, agentInst, turnCount, toolCallCount)
}

// writeHeuristicFallbackRetroWithCount is the preferred fallback writer when
// the transcript has already been parsed — avoids re-reading the file. The
// recap body matches the format pinned in Ambiguity #2:
//
//	"Session <id> ended. Turns: N. Tool calls: M. Fallback reason: <reason>."
func (al *AgentLoop) writeHeuristicFallbackRetroWithCount(
	sessionID, trigger, fallbackReason string,
	agentInst *AgentInstance,
	turnCount, toolCallCount int,
) {
	slog.Warn("session_end: recap fallback",
		"session_id", sessionID,
		"trigger", trigger,
		"fallback_reason", fallbackReason,
	)

	if agentInst == nil {
		// No agent — can't write the retro anywhere.
		al.auditRecap(sessionID, "", trigger, "fallback:"+fallbackReason)
		return
	}

	memory := agentInst.ContextBuilder.Memory()
	if memory == nil {
		al.auditRecap(sessionID, agentInst.ID, trigger, "fallback:"+fallbackReason)
		return
	}

	recap := fmt.Sprintf("Session %s ended. Turns: %d. Tool calls: %d. Fallback reason: %s.",
		sessionID, turnCount, toolCallCount, fallbackReason)

	if err := memory.WriteLastSession(recap); err != nil {
		slog.Warn("session_end: fallback: failed to write LAST_SESSION.md",
			"session_id", sessionID,
			"error", err,
		)
	}

	retro := Retro{
		Timestamp:      time.Now().UTC(),
		Trigger:        RecapTrigger(trigger),
		Fallback:       true,
		FallbackReason: fallbackReason,
		Recap:          recap,
	}
	if err := memory.AppendRetro(sessionID, retro); err != nil {
		slog.Warn("session_end: fallback: failed to append retro",
			"session_id", sessionID,
			"error", err,
		)
	}

	al.auditRecap(sessionID, agentInst.ID, trigger, "fallback:"+fallbackReason)
	// Claim stays for the process lifetime: file-level idempotency via
	// agentSessionHasRetro prevents duplicate fallback writes.
}

// auditRecap logs a memory.auto_recap audit event if audit logging is enabled.
func (al *AgentLoop) auditRecap(sessionID, agentID, trigger, outcome string) {
	if al.auditLogger == nil {
		return
	}
	// agent_id / session_id are top-level Entry fields; only event-specific
	// context goes into Details.
	if err := al.auditLogger.Log(&audit.Entry{
		Event:     "memory.auto_recap",
		Decision:  audit.DecisionAllow,
		AgentID:   agentID,
		SessionID: sessionID,
		Details: map[string]any{
			"outcome": outcome,
			"trigger": trigger,
		},
	}); err != nil {
		slog.Warn("session_end: failed to write audit entry",
			"session_id", sessionID,
			"error", err,
		)
	}
}

// AgentForSession resolves the AgentInstance responsible for the given session.
// FR-026.
func (al *AgentLoop) AgentForSession(sessionID string) (*AgentInstance, error) {
	if al.sharedSessionStore == nil {
		return nil, fmt.Errorf("meta_not_found: no shared session store")
	}
	meta, err := al.sharedSessionStore.GetMeta(sessionID)
	if err != nil {
		return nil, fmt.Errorf("meta_not_found: %w", err)
	}

	// Prefer ActiveAgentID (v2 multi-agent) over AgentID (legacy).
	agentID := meta.ActiveAgentID
	if agentID == "" {
		agentID = meta.AgentID
	}
	if agentID == "" {
		return nil, fmt.Errorf("agent_not_found: session %s has no agent_id in meta", sessionID)
	}

	agentInst, ok := al.registry.GetAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent_not_found: %s", agentID)
	}
	return agentInst, nil
}

// classifyLLMError returns a short error class string for audit/fallback logging.
// Falling into the "llm_error" default is logged at Warn so operators can see
// the unclassified underlying error rather than just the bucket label.
func classifyLLMError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timeout"):
		return "llm_timeout"
	case strings.Contains(msg, "rate limit") || strings.Contains(msg, "429"):
		return "llm_rate_limit"
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401"):
		return "llm_unauthorized"
	default:
		slog.Warn("session_end: unclassified llm error",
			"error", err.Error(),
		)
		return "llm_error"
	}
}

// BootstrapRecapPass (FR-032, FR-032a): on gateway start, scans the shared
// session store for sessions that lack a retro and are older than 30 minutes,
// and enqueues a CloseSession("bootstrap") for each.
//
// Early-returns if AutoRecapEnabled or BootstrapRecapEnabled is false.
// Rate-limits starts to GetBootstrapRecapMaxPerMinute per minute.
// Caps total estimated cost at GetBootstrapRecapDailyBudgetUSD.
//
// Sessions are a gateway-wide resource, NOT per-agent — so the sessions
// directory is walked exactly once. Each session's owning agent is resolved
// via AgentForSession before auditing so the audit entry reflects reality.
func (al *AgentLoop) BootstrapRecapPass(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("session_end: BootstrapRecapPass panic recovered", "panic", r)
		}
	}()

	defaults := &al.cfg.Agents.Defaults
	if !defaults.AutoRecapEnabled || !defaults.BootstrapRecapEnabled {
		slog.Info("session_end: bootstrap recap skipped",
			"BootstrapRecapEnabled", defaults.BootstrapRecapEnabled,
			"AutoRecapEnabled", defaults.AutoRecapEnabled,
		)
		return
	}

	if al.sharedSessionStore == nil {
		slog.Warn("session_end: bootstrap recap: no shared session store")
		return
	}

	maxPerMinute := defaults.GetBootstrapRecapMaxPerMinute()
	budgetUSD := defaults.GetBootstrapRecapDailyBudgetUSD()

	// Rate limiter: one slot every (60/maxPerMinute) seconds. Guard against
	// sub-second intervals on a high max per minute.
	intervalBetweenStarts := time.Duration(float64(time.Minute) / float64(maxPerMinute))
	if intervalBetweenStarts < time.Second {
		intervalBetweenStarts = time.Second
	}
	ticker := time.NewTicker(intervalBetweenStarts)
	defer ticker.Stop()

	var accumulatedCostUSD float64
	const costPerRecapUSD = 1e-5 // rough estimate: ~1000 tokens × $0.00001/token

	// SF2: counters for the pass-complete summary log.
	var processed, skippedBudget, errored int

	sessionsBaseDir := al.sharedSessionStore.BaseDir()
	entries, err := os.ReadDir(sessionsBaseDir)
	if err != nil {
		slog.Warn("session_end: bootstrap_recap: cannot read sessions dir",
			"dir", sessionsBaseDir,
			"error", err,
		)
		return
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			slog.Info("session_end: bootstrap recap pass complete (context canceled)",
				"processed", processed,
				"skipped_budget", skippedBudget,
				"errored", errored,
			)
			return
		}

		if !entry.IsDir() || entry.Name() == ".context" {
			continue
		}

		sessionID := entry.Name()
		sessionDir := filepath.Join(sessionsBaseDir, sessionID)

		// Resolve the real owning agent so audit + retro land in the right
		// workspace. If the session's meta doesn't resolve, mark it as
		// archived rather than processing it against some arbitrary agent.
		agentInst, agentErr := al.AgentForSession(sessionID)
		if agentErr != nil {
			al.auditRecap(sessionID, "", "bootstrap", "skipped_unresolvable_agent")
			errored++
			continue
		}

		// Canonical "already recapped" signal — scoped to the owning agent's
		// workspace, not scanning every agent's retro dir.
		if al.agentSessionHasRetro(agentInst, sessionID) {
			continue
		}

		// Find the newest JSONL file timestamp to determine session age.
		newestTS, hasJSONL := al.newestTranscriptTimestamp(sessionDir)
		if !hasJSONL {
			// No transcript files — archived or empty.
			al.auditRecap(sessionID, agentInst.ID, "bootstrap", "skipped_archived")
			continue
		}

		age := time.Since(newestTS)
		if age < 30*time.Minute {
			continue
		}

		if accumulatedCostUSD+costPerRecapUSD > budgetUSD {
			al.auditRecap(sessionID, agentInst.ID, "bootstrap", "skipped_budget_exceeded")
			skippedBudget++
			slog.Info("session_end: bootstrap recap: budget exceeded",
				"accumulated_usd", accumulatedCostUSD,
				"budget_usd", budgetUSD,
			)
			// Keep iterating to count remaining budget-skipped sessions.
			continue
		}

		select {
		case <-ctx.Done():
			slog.Info("session_end: bootstrap recap pass complete (context canceled)",
				"processed", processed,
				"skipped_budget", skippedBudget,
				"errored", errored,
			)
			return
		case <-ticker.C:
		}

		accumulatedCostUSD += costPerRecapUSD
		al.CloseSession(sessionID, "bootstrap")
		processed++
	}

	// SF2: emit a single summary so operators can audit the pass at a glance.
	slog.Info("session_end: bootstrap recap pass complete",
		"processed", processed,
		"skipped_budget", skippedBudget,
		"errored", errored,
	)
}

// agentSessionHasRetro returns true if the owning agent's workspace already
// contains a retro file for sessionID. Scoping the scan to one workspace keeps
// the bootstrap pass O(sessions × date_dirs) rather than O(sessions × agents ×
// date_dirs).
func (al *AgentLoop) agentSessionHasRetro(agentInst *AgentInstance, sessionID string) bool {
	if agentInst == nil {
		return false
	}
	sessionsDir := filepath.Join(agentInst.Workspace, "memory", "sessions")
	dateDirs, err := os.ReadDir(sessionsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("session_end: read retro dir failed",
				"dir", sessionsDir,
				"agent_id", agentInst.ID,
				"error", err,
			)
		}
		return false
	}
	for _, dateDir := range dateDirs {
		if !dateDir.IsDir() {
			continue
		}
		retroPath := filepath.Join(sessionsDir, dateDir.Name(), sessionID+"_retro.md")
		if _, statErr := os.Stat(retroPath); statErr == nil {
			return true
		}
	}
	return false
}

// newestTranscriptTimestamp finds the newest timestamp in transcript.jsonl files
// within a session directory. Returns the timestamp and true if any JSONL was
// found. Read errors other than "not found" are logged so a silent permission
// regression is visible.
func (al *AgentLoop) newestTranscriptTimestamp(sessionDir string) (time.Time, bool) {
	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("session_end: cannot read transcript",
				"path", transcriptPath,
				"error", err,
			)
		}
		return time.Time{}, false
	}

	var newest time.Time
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if !entry.Timestamp.IsZero() {
			if !found || entry.Timestamp.After(newest) {
				newest = entry.Timestamp
				found = true
			}
		}
	}
	return newest, found
}
