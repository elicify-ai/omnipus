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
	"slices"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// CloseSession triggers an async session-end recap if AutoRecapEnabled is set
// and this sessionID has not already been claimed. Idempotent: duplicate calls
// for the same sessionID are silently dropped (FR-027).
func (al *AgentLoop) CloseSession(sessionID, trigger string) {
	// Evict the session's loaded-tool entry unconditionally so the
	// loadedTools map does not grow without bound regardless of whether the
	// recap feature is enabled. The sessionID here is the transcript session ID
	// — the same key that manifestSessionID returns when transcriptID != "".
	al.forgetSession(sessionID)

	// Clear every "Always Allow" tool-approval grant recorded for this
	// session (all agents), unconditionally — same reasoning as
	// forgetSession above: this is bounded per-session cleanup, not gated on
	// the recap feature. al.approvalGrants is always non-nil after
	// NewAgentLoop, but ClearSession is nil-receiver-safe regardless (e.g.
	// AgentLoop literals built directly in tests without NewAgentLoop).
	al.approvalGrants.ClearSession(sessionID)

	// ADR-051 Rev 4 (FR-007a, Wave 3 T9): decrement the manifest refcount
	// for every workspace library file this session referenced. The matching
	// increment fired during the turn's resolveMediaRefs pass; without this
	// decrement orphan GC would never reach refcount==0 on a file that is no
	// longer referenced. Runs unconditionally (same reasoning as
	// forgetSession/ClearSession above) and is nil-receiver-safe.
	// [Merge-review fix 4, ab1c1aad review: release's call site here was
	// dropped when ours won this region — restored verbatim; without it
	// decrementSessionMediaRefcounts was defined but never called.]
	if al != nil {
		al.decrementSessionMediaRefcounts(sessionID)
	}

	// FR-064/FR-033: force any pending in-memory-only stats delta (the
	// FR-061 throttle) to disk, THEN evict this session's metaCache entry —
	// same reasoning as forgetSession/approvalGrants.ClearSession above: this
	// is bounded per-session cleanup, not gated on the recap feature. Order
	// matters: flush BEFORE evict, mirroring FR-064's DeleteSession sequence
	// (flush-then-remove) — evicting first would drop the cache entry
	// FlushSessionStats itself reads to produce stats.json's bytes, losing
	// the pending delta instead of persisting it. Without the flush, counters
	// accumulated since the last periodic tick (up to StatsFlushInterval,
	// default 5s) are silently lost: nothing errors, the session simply
	// reports stale Stats to the next GetMeta/ListSessions caller. Without
	// the evict, a stale composed meta can be served to a caller after this
	// session has "closed" (FR-033) and the cache grows one entry per
	// ever-delegated child for the process lifetime (Ambiguity 14). Eviction
	// is NOT deletion: the session's files stay on disk and it stays in the
	// FR-097 parent index (EvictSessionMeta deliberately does not call
	// u4IndexEvict) — only the process-lifetime cache entry is dropped; the
	// next GetMeta/ListSessions call self-heals it from disk like any other
	// cache miss. al.sharedSessionStore may be nil in tests that build an
	// AgentLoop literal directly without NewAgentLoop (same nil-safety
	// concern documented on approvalGrants above), so both are skipped rather
	// than panicking when there is no store.
	//
	// Bug fix, closed (14-reviewer finding #2): the OLD code called
	// FlushSessionStats and EvictSessionMeta as two SEPARATE shard
	// acquisitions. Running the evict unconditionally, even when the flush
	// failed, had two costs: (1) deterministic — on a flush failure,
	// u6FlushDirtySessionLocked (pkg/session/unified_stats_flush.go)
	// deliberately KEEPS the dirty mark for the next periodic tick or
	// forced-flush point to retry, but that retry only does anything if BOTH
	// the dirty mark AND a live metaCache entry exist, and the unconditional
	// evict removed the one thing the retry needs; (2) racy — a concurrent
	// AppendTranscript landing in the window between the two separate calls
	// re-marks the session dirty with a FRESH cache entry, which the
	// unconditional evict then destroyed before it was ever flushed.
	// FlushAndEvictSessionMeta (pkg/session/unified_stats_flush.go) closes
	// both: it holds sessionID's shard across the flush AND the evict, so no
	// AppendTranscript for this same session id can interleave between them,
	// and it evicts ONLY when the flush actually succeeded — a failed flush
	// leaves the dirty mark and cache entry exactly as
	// u6FlushDirtySessionLocked left them, for the next retry.
	if store := al.sharedSessionStore; store != nil {
		if err := store.FlushAndEvictSessionMeta(sessionID); err != nil {
			slog.Warn("session_end: forced stats flush failed at close",
				"session_id", sessionID,
				"trigger", trigger,
				"error", err,
			)
		}
	}

	// Use GetConfig() (holds al.mu.RLock) so that a PUT /settings/memory that
	// hot-swaps the config via SwapConfig is immediately visible here — a direct
	// al.cfg read races with SwapConfig's write and may see the pre-PUT value.
	if !al.GetConfig().Agents.Defaults.AutoRecapEnabled {
		return
	}

	// Bug fix (unconsidered rider introduced alongside this ADR): skip the
	// recap pipeline entirely for a delegated child session
	// (session.SessionTypeDelegate). subturn.go's cleanup defer calls
	// al.CloseSession(childID, "delegate_terminal") for EVERY delegated
	// child, unconditionally, and until this gate that reached the exact
	// same recap pipeline as a real user chat session closing — but FR-033's
	// own stated scope for that call site is narrow ("clearing its grant
	// set, loadedTools bucket, metaCache entry and recallSpans entries"), all
	// of which already ran unconditionally above; recap is never named as an
	// intended consequence.
	//
	// A delegated child is an ephemeral task fan-out artifact — its
	// transcript is one synthetic user turn (the task prompt) — never a real
	// user conversation. With AutoRecapEnabled seeded true on every fresh
	// install (coreagent.SeedConfig), letting it through runRecap (which has
	// no session-type gate of its own) meant:
	//   - Unbilled spend: runRecap calls p.Chat directly (max_tokens 2048),
	//     bypassing the session entirely, so those tokens land in no
	//     session's Stats and are invisible to Usage/get_usage. A fan-out of
	//     N delegations is N uncounted LLM calls.
	//   - Memory corruption: every delegated child of the SAME agent shares
	//     that one agent's single last-session.md (pkg/agent/memory.go). N
	//     concurrent delegations race N recap goroutines to overwrite it —
	//     the winner is whichever micro sub-task's recap happens to finish
	//     last, clobbering the recap of the user's actual last real
	//     conversation with that agent.
	//
	// Checking store.GetMeta here (rather than trusting the trigger string)
	// catches every current and future call site that might close a delegate
	// session — not just subturn.go's own "delegate_terminal" label — and
	// self-heals from disk exactly like every other GetMeta caller even
	// though EvictSessionMeta may have just dropped the cache entry above.
	// A GetMeta error (nil store, unresolvable session) falls through to the
	// existing behavior unchanged: AgentForSession will fail the same way
	// moments later inside runRecap and route to the heuristic (no-LLM-call)
	// fallback, so failing open here costs nothing.
	//
	// This also closes the companion leak named in the same defect report:
	// returning BEFORE the claimedCloseSessions.LoadOrStore call below means
	// a delegated child's sessionID is never inserted into that sync.Map at
	// all. Post-ADR-057, every delegated child owns a fresh, effectively
	// single-use session id (subturn.go's childID), so without this early
	// return the map gained one permanent entry per ever-delegated child for
	// the life of the process — a far higher-volume source than the
	// pre-existing "one entry per real chat session" growth this map already
	// had (called out as "bounded in practice" and left unchanged: a real
	// chat session still claims normally below).
	if store := al.sharedSessionStore; store != nil {
		if meta, metaErr := store.GetMeta(sessionID); metaErr == nil && meta.Type == session.SessionTypeDelegate {
			al.auditRecap(sessionID, "", trigger, "skipped_delegate_session")
			return
		}
	}

	// Idempotency gate (FR-027): only one goroutine wins the claim.
	if _, loaded := al.claimedCloseSessions.LoadOrStore(sessionID, true); loaded {
		// Emit an audit entry so operators can see which trigger arrived second.
		al.auditRecap(sessionID, "", trigger, "skipped_already_claimed")
		return
	}

	// Cancel any outstanding idle ticker for this session.
	al.cancelIdleTicker(sessionID)

	// Track the recap goroutine so Close() can drain it before shutdown — its
	// LAST_SESSION.md / retro writes must not outlive the gateway (#265). Gate
	// the Add under recapMu against Close()'s drain: once shutdown has begun we
	// skip scheduling (the session is re-recapped on next start by
	// BootstrapRecapPass) rather than risk a WaitGroup Add-after-Wait.
	al.recapMu.Lock()
	if al.closing {
		al.recapMu.Unlock()
		return
	}
	al.recapWG.Add(1)
	al.recapMu.Unlock()
	go func() {
		defer al.recapWG.Done()
		al.runRecap(sessionID, trigger)
	}()
}

// Recap transcript-filtering constants, shared by runRecap and the fallback
// writer (writeHeuristicFallbackRetro) so both agree on which transcript
// entries count as real user turns (FR-028).
const (
	recapSubTurnPrefix        = "[SubTurn Result]"
	recapInterruptHintLiteral = "Interrupt requested. Stop scheduling tools and provide a short final summary."
	// carryForwardMaxRunes bounds the verbatim recent-context tail the fallback
	// recap carries forward when summarisation is unavailable (see buildCarryForward).
	// ~1500 runes is ~19% of the summariser's own input budget (2000 tokens ≈ 8000
	// runes at this file's ~4-runes-per-token ratio; see runRecap's tokenBudget),
	// enough to preserve the essential facts a user stated most recently without
	// bloating last-session.md.
	carryForwardMaxRunes = 1500
)

// buildCarryForward returns a compact, verbatim tail of the most recent user
// turns, for the recap FALLBACK path only.
//
// When the summariser LLM is unavailable or degrades (all candidates fail,
// json_parse_error, no_memory_store), a content-free "Session <id> ended.
// Turns: N..." stub is worthless for continuity — last-session.md is injected
// into the next session, so a stub silently drops whatever the user just said
// (a stated codename, a decision, a hand-off note). Carrying the recent user
// turns forward verbatim preserves those facts even without a real summary.
//
// Bounded to maxRunes, keeping the NEWEST turns (a fact stated at the end of
// the conversation is the most likely thing the next session needs). A single
// final turn longer than the budget is tail-truncated so its end still lands.
func buildCarryForward(userTurns []string, maxRunes int) string {
	if len(userTurns) == 0 || maxRunes <= 0 {
		return ""
	}
	var kept []string // accumulated newest→oldest, reversed to chronological below
	total := 0
	for i := len(userTurns) - 1; i >= 0; i-- {
		t := strings.TrimSpace(userTurns[i])
		if t == "" {
			continue
		}
		r := []rune(t)
		if total+len(r) > maxRunes {
			// Budget hit. If we have kept nothing yet, this single turn is
			// larger than the whole budget — keep its tail so a nonce stated
			// at the very end still survives. Otherwise stop here.
			if len(kept) == 0 && len(r) > maxRunes {
				kept = append(kept, string(r[len(r)-maxRunes:]))
			}
			break
		}
		kept = append(kept, t)
		total += len(r)
	}
	slices.Reverse(kept) // accumulated newest→oldest; restore chronological order
	return strings.Join(kept, "\n\n")
}

// filterRecapUserTurns extracts the real user turns from a transcript (FR-028:
// user-role, non-empty, non-SubTurn-result, non-interrupt-hint) and counts tool
// calls across all entries. Shared by runRecap and writeHeuristicFallbackRetro so
// the two can never diverge on what counts as a "user turn" — the reason the
// recapSubTurnPrefix/recapInterruptHintLiteral constants were hoisted to package
// scope. (User-role entries never carry ToolCalls, so the count is identical to a
// version that summed unconditionally.)
func filterRecapUserTurns(entries []session.TranscriptEntry) (userTurns []string, toolCallCount int) {
	for _, e := range entries {
		if e.Role == "user" {
			content := strings.TrimSpace(e.Content)
			if content == "" || strings.HasPrefix(content, recapSubTurnPrefix) || content == recapInterruptHintLiteral {
				continue
			}
			userTurns = append(userTurns, content)
		}
		toolCallCount += len(e.ToolCalls)
	}
	return userTurns, toolCallCount
}

// runRecap performs the session-end LLM summarisation and persists the result.
// Runs in a goroutine; a top-level recover() prevents a panic in any
// subsystem (provider, JSON parse, file I/O) from killing the gateway process.
func (al *AgentLoop) runRecap(sessionID, trigger string) {
	slog.Debug("session_end: runRecap started", "session_id", sessionID, "trigger", trigger)
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
	userTurns, toolCallCount := filterRecapUserTurns(entries)

	// Carry-forward tail for the fallback path: if summarisation fails below,
	// the recent user turns are preserved verbatim so cross-session continuity
	// survives a degraded recap (buildCarryForward / writeHeuristicFallbackRetroWithCount).
	carryForward := buildCarryForward(userTurns, carryForwardMaxRunes)

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

	// Resolve primary recap model.
	// Priority: recap_model config → overall default model → session agent model.
	// Snapshot the config once (under RLock via GetConfig) so all reads below
	// see a consistent view of the config even if SwapConfig races.
	snapCfg := al.GetConfig()
	recapModel := snapCfg.Agents.Defaults.RecapModel
	// When no recap model is configured the default (provider, model) pair is
	// used as-is — pinned to its own provider, never re-resolved (ADR-068).
	var recapPinnedProvider string
	if recapModel == "" && !snapCfg.Agents.Defaults.DefaultModel.IsZero() {
		recapModel = snapCfg.Agents.Defaults.DefaultModel.Model
		recapPinnedProvider = snapCfg.Agents.Defaults.DefaultModel.Provider
	}
	if recapModel == "" {
		recapModel = agentInst.Model
	}

	// Build the fallback candidate list: [primaryRecapModel, ...RecapFallbackModels].
	// Each config.FallbackModel carries its own Provider for cross-provider fallback.
	//
	// Resolve the PRIMARY recap model's Provider the same way the agent turn does
	// (resolveModelRef → the configured provider that owns the credentials). This
	// is load-bearing: the fallback executor calls stripProviderPrefix(model,
	// candidate.Provider) before the upstream API call, so a provider-prefixed
	// model_name (e.g. "openrouter/z-ai/glm-5.2" — the form onboarding writes)
	// is only reduced to the upstream-valid "z-ai/glm-5.2" when the candidate's
	// Provider is set. Without it the prefixed model is sent RAW and OpenRouter
	// rejects it ("... is not a valid model ID"), the recap falls back to a
	// heuristic stub, and last-session.md loses the real summary. The turn path
	// resolves Provider; the recap MUST match it.
	candidates := make([]providers.FallbackCandidate, 0, 1+len(snapCfg.Agents.Defaults.RecapFallbackModels))
	primaryCandidate := providers.FallbackCandidate{Model: recapModel, Provider: recapPinnedProvider}
	if recapPinnedProvider == "" {
		if ref, ok := resolveModelRef(snapCfg, recapModel); ok {
			primaryCandidate = providers.FallbackCandidate{Model: ref.Model, Provider: ref.Provider}
		}
	}
	candidates = append(candidates, primaryCandidate)
	for _, fm := range snapCfg.Agents.Defaults.RecapFallbackModels {
		candidates = append(candidates, providers.FallbackCandidate{Provider: fm.Provider, Model: fm.Model})
	}

	// Build recap prompt.
	recapPrompt := `Summarize this conversation in ≤ 150 words. Then list up to 5 wins, up to 5 needs-improvement items, and up to 5 items worth remembering long-term. Respond ONLY with valid JSON: {"recap":"...", "went_well":[...], "needs_improvement":[...], "worth_remembering":[...]}`

	msgs := []providers.Message{
		{Role: "user", Content: historyText + "\n\n" + recapPrompt},
	}

	// Cost-guard options (FR-029a): output cap + reasoning hints.
	//
	// max_tokens=2048: reasoning models like glm-5.2 on OpenRouter/Fireworks
	// ignore the reasoning:{enabled:false} hint and always generate an internal
	// reasoning trace before emitting content. With max_tokens=512 the reasoning
	// trace itself consumes the entire budget (observed: ~435–566 reasoning tokens)
	// leaving content=null, which JSON-parses to "" and falls into the
	// json_parse_error fallback path — the nonce never lands in last-session.md.
	// Raising to 2048 gives reasoning models ample room: ~500 reasoning + ~500
	// content = 1000 tokens total, still well within the 2048 cap. The extra
	// tokens are cheap (one call per session close, paid only when the LLM
	// actually generates tokens, not just allocated). extended_thinking:false is
	// kept for Anthropic-style providers that honor it and skip the reasoning
	// phase entirely when set. reasoning:{enabled:false} is left in the
	// extra_body because it DOES work on some OpenRouter routes and upstream
	// providers, and is harmlessly ignored elsewhere.
	opts := map[string]any{
		"max_tokens":        2048,
		"extended_thinking": false,
		"extra_body": map[string]any{
			"reasoning": map[string]any{"enabled": false},
		},
	}

	// Build a provider pool for the recap candidates so each fallback routes
	// through its own configured provider (parity with agent turn fallbacks,
	// FR-007). The agent's own providerPool only contains its turn candidates;
	// recap candidates are separate config fields and are never included there.
	// We build a one-off pool here using the same buildProviderPool helper.
	recapProviderPool := buildProviderPool(snapCfg, candidates)

	// resolveRecapProvider mirrors GetProviderForCandidate but consults the
	// one-off recap pool first, then the agent's turn pool (single-passthrough
	// case), and finally falls back to the agent's primary provider.
	resolveRecapProvider := func(candidate providers.FallbackCandidate) providers.LLMProvider {
		pinned := strings.TrimSpace(candidate.Provider)
		if pinned != "" && recapProviderPool != nil {
			if p, ok := recapProviderPool[pinned]; ok && p != nil {
				return p
			}
		}
		// Single passthrough provider (empty Provider on candidate): use the
		// agent's primary provider — same as the pre-fix behavior and the
		// correct path when all recap candidates share the agent's provider.
		p := agentInst.GetProviderForCandidate(candidate)
		if p != nil {
			return p
		}
		return agentInst.Provider
	}

	// Self-bounded overall at 60s. Divide the budget evenly across candidates
	// so a slow primary cannot exhaust the deadline and leave fallbacks with an
	// already-expired context. Each candidate gets its own fresh timeout derived
	// from its share of the overall budget (minimum 10s to avoid starving
	// fallbacks on short budgets with many candidates).
	const overallBudget = 60 * time.Second
	const minPerCandidate = 10 * time.Second
	numCandidates := len(candidates)
	if numCandidates == 0 {
		numCandidates = 1
	}
	perCandidateTimeout := overallBudget / time.Duration(numCandidates)
	if perCandidateTimeout < minPerCandidate {
		perCandidateTimeout = minPerCandidate
	}

	// recapDeadline is the hard wall-clock limit for the entire recap attempt.
	// It is used to guard transient retries so they cannot push individual
	// candidates past the overall 60 s budget even when many retries are needed.
	recapDeadline := time.Now().Add(overallBudget)

	// maxTransientRetries is the number of additional attempts made per candidate
	// when the error is classified as a transient stream reset (http2 body closed,
	// GOAWAY, connection reset, …). The recap output is collected whole and never
	// streamed to the user, so a retry on a fresh connection is safe.
	const maxTransientRetries = 2

	// Attempt the recap against each candidate in order. For each candidate,
	// retry up to maxTransientRetries times on transient stream-reset errors
	// before moving to the next fallback candidate.
	// Non-transient errors (4xx, unauthorized, context-overflow) fall through to
	// the next candidate immediately — retrying them is not safe or useful.
	var resp *providers.LLMResponse
	var llmErr error
	for _, candidate := range candidates {
		p := resolveRecapProvider(candidate)
		for attempt := 0; attempt <= maxTransientRetries; attempt++ {
			// Respect the hard overall deadline: don't start an attempt if we're
			// already past the budget boundary.
			remaining := time.Until(recapDeadline)
			if remaining <= 0 {
				slog.Warn("session_end: recap deadline exceeded, stopping retries",
					"session_id", sessionID,
					"agent_id", agentInst.ID,
					"model", candidate.Model,
				)
				break
			}
			callTimeout := perCandidateTimeout
			if callTimeout > remaining {
				callTimeout = remaining
			}

			candidateCtx, candidateCancel := context.WithTimeout(context.Background(), callTimeout)
			r, err := p.Chat(candidateCtx, msgs, nil, candidate.Model, opts)
			candidateCancel()
			if err == nil {
				resp = r
				llmErr = nil
				break
			}

			// Check whether this is a transient stream reset that is safe to retry.
			if attempt < maxTransientRetries && isTransientStreamError(err) {
				backoff := time.Duration(1+attempt) * 500 * time.Millisecond
				slog.Warn("session_end: recap transient stream error, retrying",
					"session_id", sessionID,
					"agent_id", agentInst.ID,
					"model", candidate.Model,
					"attempt", attempt+1,
					"max_retries", maxTransientRetries,
					"backoff_ms", backoff.Milliseconds(),
					"error", err.Error(),
				)
				time.Sleep(backoff)
				llmErr = err
				continue
			}

			// Non-transient error or retries exhausted — move to the next candidate.
			slog.Warn("session_end: recap candidate failed, trying next",
				"session_id", sessionID,
				"agent_id", agentInst.ID,
				"model", candidate.Model,
				"attempt", attempt+1,
				"error", err.Error(),
			)
			llmErr = err
			break
		}
		if resp != nil {
			break
		}
	}

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
			carryForward,
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

	// Reasoning-model fallback: some providers (e.g. glm-5.2 on OpenRouter/Fireworks)
	// ignore the reasoning:{enabled:false} hint and generate a reasoning trace. When
	// content is empty but resp.Reasoning is non-empty, the model likely drafted its
	// JSON inside the reasoning trace (observed pattern: the trace ends with a JSON
	// block). Fall back to the reasoning trace so the recap succeeds without a retry.
	if strings.TrimSpace(responseText) == "" && resp.Reasoning != "" {
		responseText = resp.Reasoning
	}

	// Unwrap the JSON envelope. glm-5.2 (and other providers) frequently return the
	// JSON inside a ```json fence or with surrounding prose — observed verbatim:
	// content begins "```json\n{...", which a raw json.Unmarshal rejects at char 0.
	// extractJSONFromText backward-scans for the outermost VALID {…} object; only
	// override when it finds one, so a model that already returned bare JSON is
	// unaffected.
	if extracted := extractJSONFromText(responseText); extracted != "" {
		responseText = extracted
	}

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
			carryForward,
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
			carryForward,
		)
		return
	}

	slog.Info("session_end: writing LAST_SESSION.md",
		"session_id", sessionID,
		"agent_id", agentInst.ID,
		"workspace", agentInst.Home,
	)
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
	var userTurns []string
	if agentInst != nil {
		if store := al.sharedSessionStore; store != nil {
			if entries, err := store.ReadTranscript(sessionID); err == nil {
				turnCount = len(entries)
				userTurns, toolCallCount = filterRecapUserTurns(entries)
			}
		}
	}
	al.writeHeuristicFallbackRetroWithCount(
		sessionID,
		trigger,
		fallbackReason,
		agentInst,
		turnCount,
		toolCallCount,
		buildCarryForward(userTurns, carryForwardMaxRunes),
	)
}

// writeHeuristicFallbackRetroWithCount is the preferred fallback writer when
// the transcript has already been parsed — avoids re-reading the file. The
// recap body opens with the status line pinned in Ambiguity #2:
//
//	"Session <id> ended. Turns: N. Tool calls: M. Fallback reason: <reason>."
//
// When carryForward is non-empty it is appended as a verbatim "Recent context"
// block so cross-session continuity survives a degraded recap (the status line
// alone carries no facts — see buildCarryForward).
func (al *AgentLoop) writeHeuristicFallbackRetroWithCount(
	sessionID, trigger, fallbackReason string,
	agentInst *AgentInstance,
	turnCount, toolCallCount int,
	carryForward string,
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
	if cf := strings.TrimSpace(carryForward); cf != "" {
		recap += "\n\nRecent context (verbatim — automatic summary unavailable):\n" + cf
	}

	slog.Info("session_end: fallback: writing LAST_SESSION.md",
		"session_id", sessionID,
		"agent_id", agentInst.ID,
		"workspace", agentInst.Home,
		"fallback_reason", fallbackReason,
	)
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
// extractJSONFromText scans text for the last outermost JSON object literal
// (a "{...}" block). This is used as a fallback when a reasoning model places
// its recap JSON inside the reasoning trace rather than in the content field.
//
// Strategy: walk backwards from the last '}' to find the matching '{', ignoring
// nested objects. Returns the extracted JSON string, or "" if none found.
func extractJSONFromText(text string) string {
	// Find the last closing brace.
	lastClose := strings.LastIndex(text, "}")
	if lastClose < 0 {
		return ""
	}
	// Walk backwards to find the matching opening brace, tracking nesting depth.
	depth := 0
	for i := lastClose; i >= 0; i-- {
		switch text[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				candidate := text[i : lastClose+1]
				// Quick sanity check: must be parseable as a JSON object.
				var probe map[string]any
				if json.Unmarshal([]byte(candidate), &probe) == nil {
					return candidate
				}
				// Not valid JSON — continue scanning for an earlier match.
				// Update lastClose to look for the next-outermost block.
				lastClose = i - 1
				if lastClose < 0 {
					return ""
				}
				lastClose = strings.LastIndex(text[:lastClose+1], "}")
				if lastClose < 0 {
					return ""
				}
				depth = 0
				i = lastClose + 1 // restart scan from new lastClose (loop will decrement)
			}
		}
	}
	return ""
}

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

	// Snapshot config under RLock so BootstrapRecapPass sees a consistent view
	// even if SwapConfig races (same reasoning as in CloseSession).
	snapCfgBoot := al.GetConfig()
	defaults := &snapCfgBoot.Agents.Defaults
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

	// Rate limiter: one slot every (60/maxPerMinute) seconds. Guard against
	// sub-second intervals on a high max per minute.
	intervalBetweenStarts := time.Duration(float64(time.Minute) / float64(maxPerMinute))
	if intervalBetweenStarts < time.Second {
		intervalBetweenStarts = time.Second
	}
	ticker := time.NewTicker(intervalBetweenStarts)
	defer ticker.Stop()

	// SF2: counters for the pass-complete summary log.
	var processed, errored int

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

		select {
		case <-ctx.Done():
			slog.Info("session_end: bootstrap recap pass complete (context canceled)",
				"processed", processed,
				"errored", errored,
			)
			return
		case <-ticker.C:
		}

		al.CloseSession(sessionID, "bootstrap")
		processed++
	}

	// SF2: emit a single summary so operators can audit the pass at a glance.
	slog.Info("session_end: bootstrap recap pass complete",
		"processed", processed,
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
	// Spec-5: retros now live in <workspace>/.omnipus/retros/<date>/<sessionID>_retro.md
	retrosDir := filepath.Join(agentInst.Home, ".omnipus", "retros")
	dateDirs, err := os.ReadDir(retrosDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("session_end: read retro dir failed",
				"dir", retrosDir,
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
		retroPath := filepath.Join(retrosDir, dateDir.Name(), sessionID+"_retro.md")
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
