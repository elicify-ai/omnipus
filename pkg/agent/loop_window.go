// loop_window.go: Sliding window and model switch

package agent

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// selectCandidates returns the model candidates and resolved model name to use
// for a conversation turn. When model routing is configured and the incoming
// message scores below the complexity threshold, it returns the light model
// candidates instead of the primary ones.
//
// The returned (candidates, model) pair is used for all LLM calls within one
// turn — tool follow-up iterations use the same tier as the initial call so
// that a multi-step tool chain doesn't switch models mid-way.
func (al *AgentLoop) selectCandidates(
	agent *AgentInstance,
	userMsg string,
	history []providers.Message,
) (candidates []providers.FallbackCandidate, model string, usedLight bool) {
	if agent.Router == nil || len(agent.LightCandidates) == 0 {
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	_, usedLight, score := agent.Router.SelectModel(userMsg, history, agent.Model)
	if !usedLight {
		logger.DebugCF("agent", "Model routing: primary model selected",
			map[string]any{
				"agent_id":  agent.ID,
				"score":     score,
				"threshold": agent.Router.Threshold(),
			})
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	logger.InfoCF("agent", "Model routing: light model selected",
		map[string]any{
			"agent_id":    agent.ID,
			"light_model": agent.Router.LightModel(),
			"score":       score,
			"threshold":   agent.Router.Threshold(),
		})
	return agent.LightCandidates, resolvedCandidateModel(agent.LightCandidates, agent.Router.LightModel()), true
}

// assembleMessages is the single consistent helper that reads the archive,
// builds the breadcrumb, splices the recall span, and calls BuildMessages —
// deduplicating the four former call sites (CRITICAL 2).
//
// It reads the archive under ONE consistent snapshot so the breadcrumb and
// the window see the same archive state (avoids the turn-number race). On a
// ReadArchive error it logs at ERROR with a stable error id and emits a
// FALLBACK breadcrumb stub so the recall path stays discoverable.
//
// Callers pass fresh history (post-trim when called after windowTrim), the
// current user message + media, and active skill names. The recall span is
// read from al.recallSpans for the given sessionKey.
func (al *AgentLoop) assembleMessages(
	ctx context.Context,
	ts *turnState,
	history []providers.Message,
	userMsg string,
	media []string,
	skillNames []string,
) []providers.Message {
	archive, archErr := ts.agent.Sessions.ReadArchive(ctx, ts.sessionKey)
	var breadcrumb string
	if archErr != nil {
		logger.ErrorCF("agent", "archive-read-error: could not read archive for breadcrumb; recall may be impaired",
			map[string]any{
				"session_key": ts.sessionKey,
				"error":       archErr.Error(),
			})
		// Fallback stub so the model knows earlier turns exist and how to page them.
		breadcrumb = "## Earlier in this conversation\n" +
			"Earlier turns exist but could not be indexed due to a storage error. " +
			"Use the recall_conversation tool with a turn_range to retrieve them."
	} else {
		breadcrumb = buildBreadcrumb(archive, history, breadcrumbTokenCap)
		// ADR-066 FR-019: apply the persisted projection state so the
		// window the provider sees here (turn start, post-trim, reload) is
		// byte-identical to what the choke point / the D5 emptying pass
		// produced live. Pure; no-op when nothing was capped or emptied.
		if pm := ts.agent.Sessions.Projection(ts.sessionKey); len(pm.Entries) > 0 {
			var cs config.ContextSettings
			if cfg := al.GetConfig(); cfg != nil {
				cs = cfg.Context
			}
			history = projectMessages(history, archiveLineResolver(archive, history), pm.Entries, projectionContext{
				policy:  capPolicyFor(cs, agentContextBudget(ts.agent)),
				archive: archive,
			})
		}
	}
	// Review B3 (reworded for the one-claim-path model, founder decision
	// 2026-09-14): a task run's goal_claim instruction (buildPrompt,
	// task_executor.go) lives only in the run's first user turn — a long,
	// tool-heavy run can trip windowTrim (ADR-028) and evict that turn
	// entirely, silently dropping the instruction for the rest of the run.
	// Piggyback a terse reminder onto the SAME breadcrumb block that already
	// fires exactly when (and only when) something has been evicted
	// (breadcrumb != ""), scoped to native task runs only (ts.opts.IsTaskRun;
	// an external-CLI worker never reaches assembleMessages at all).
	if ts.opts.IsTaskRun && breadcrumb != "" {
		breadcrumb += "\n\nReminder (task run): when the work is verified, report completion by calling goal_claim (status \"met\", with your one-line evidence) — not by writing a status yourself."
	}
	span := al.activeRecallSpan(ts.sessionKey)
	// ADR-066 D5.4 (FR-043): this from-scratch assembly includes the active
	// span exactly once (BuildMessages, after the pinned core). Record it by
	// identity so the tool-result site never splices it a second time, and
	// so a same-turn replacement (E20) can find the block to remove.
	recordAssembledRecallSpan(ts, span, history)
	return ts.agent.ContextBuilder.BuildMessages(
		history,
		userMsg,
		media,
		ts.opts.WorkspaceID,
		ts.channel,
		ts.chatID,
		ts.opts.SenderID,
		ts.opts.SenderDisplayName,
		breadcrumb,
		span.Messages(),
		skillNames...,
	)
}

// sentToolSurfaceTokens estimates the tokens the tool surface ACTUALLY costs on
// the wire for this agent+session — the non-evictable overhead history has to
// fit around.
//
// This is deliberately NOT agent.Tools.ToProviderDefs(): that returns a full
// JSON schema for EVERY registered tool, but under a compressed manifest only
// three groups are sent (buildCompressedToolDefs, tool_manifest.go):
//
//   - ManifestFull  — full schema, every turn
//   - ManifestInfra — full schema (ToolSearch is always callable)
//   - ManifestLazy  — full schema ONLY while loaded this session; otherwise it
//     is one line in the compact manifest block, ~25x cheaper
//
// Charging every lazy tool a full schema made the budget shrink with the size
// of the CATALOG rather than the size of the REQUEST. With ~15 MCP servers
// connected (150-450 lazy tools, none of them sent) the over-count exceeds the
// whole context window, driving the history budget negative: the trimmer would
// evict every turn, still not fit, and stop at its FR-003 floor keeping only
// the last user message — silently, on every turn. TestSwitchTime_EndToEnd
// caught the small-scale version of this at a 20k window.
//
// The manifest block is MEASURED via the same builder the turn uses, not
// approximated by a per-entry constant, so the two cannot drift.
//
// When the compressed manifest is off every tool really is sent, so the whole
// registry is the correct answer and we fall back to it.
//
// transcriptID/sessionKey are the same two inputs manifestBucketKey takes
// everywhere else (ADR-071 D3 §4.6) — callers that have a *turnState in
// scope MUST pass ts.opts.TranscriptSessionID and ts.sessionKey (or, more
// directly, thread ts.manifestBucket() down to whichever caller owns this
// call). Passing a bare sessionKey with no agentID/transcriptID component
// (the pre-fix bug here) can never match a bucket written by markToolsLoaded,
// so the lookup below always saw an empty loaded set.
func (al *AgentLoop) sentToolSurfaceTokens(agent *AgentInstance, transcriptID, sessionKey string) int {
	if agent == nil || agent.Tools == nil {
		return 0
	}
	all := agent.Tools.GetAll()

	cfg := al.GetConfig()
	if cfg == nil || !cfg.Tools.Manifest.Compressed {
		// Uncompressed: every tool is sent as a full def.
		return estimateToolDefsTokens(agent.Tools.ToProviderDefs())
	}

	bucket := manifestBucketKey(agent.ID, transcriptID, sessionKey)
	loaded := al.sessionLoadedTools(bucket)

	sent := make([]tools.Tool, 0, len(all))
	for _, t := range all {
		switch tools.ToolManifestTier(t.Name()) {
		case tools.ManifestFull, tools.ManifestInfra:
			sent = append(sent, t)
		case tools.ManifestLazy:
			if loaded[t.Name()] {
				sent = append(sent, t)
			}
		}
	}

	total := estimateToolDefsTokens(tools.ToolsToProviderDefs(sent))
	// The compact block for the lazy tools that are NOT loaded. Measured with
	// the real builder: same input shape the turn passes, same filtering.
	if note := tools.BuildCompressedManifest(all, loaded); note != "" {
		total += estimateMessageTokens(providers.Message{Role: "system", Content: note})
	}

	// Loud, non-fatal: if the surface alone rivals the window, the trimmer will
	// bottom out on its floor and the agent will look like it lost its memory
	// for no visible reason. Name the numbers so that is diagnosable from logs.
	if window := agent.ContextWindow; window > 0 && total > window/2 {
		logger.WarnCF("agent", "tool definitions occupy over half the context window; "+
			"history retention will be severely reduced", map[string]any{
			"agent_id":          agent.ID,
			"tool_surface_toks": total,
			"context_window":    window,
			"tools_registered":  len(all),
			"tools_sent":        len(sent),
		})
	}
	return total
}

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
	// NothingToTrim is set (alongside ok=false) when windowTrim declined to
	// run because the live window had nothing eligible to evict (e.g. a
	// fresh turn with a single-message window). Callers that treat ok=false
	// as "compaction failed, abandon retry" should check this flag first: a
	// no-op trim is not a failure of the compaction mechanism and should
	// not, on its own, be grounds for giving up on a retry triggered by an
	// unrelated transient error.
	//
	// windowTrim's other ok=false path — TruncateHistory was attempted but
	// the window did not actually shrink — is a genuine failure and leaves
	// this flag unset; the window is still over budget in that case, so
	// retrying against it is unlikely to help.
	//
	// A third windowTrim outcome exists but is deliberately NOT reported
	// through this flag: dropping the active recall span alone (FR-019) can
	// bring the window back under budget without evicting any window Turns.
	// That is a real, successful eviction, so windowTrim reports it the same
	// way as a normal window eviction — ok=true with DroppedMessages==0 —
	// rather than ok=false+NothingToTrim, so callers rebuild the assembled
	// messages (dropping the now-stale recall-span content) before retrying.
	NothingToTrim bool
}

// windowTrim replaces forceCompression (FR-001/002/003/004). It evicts the
// oldest whole Turn(s) from the live window by advancing meta.Skip until the
// assembled token budget fits, deleting zero bytes from disk.
//
// Algorithm (FR-001):
//  1. Read the current post-Skip live window via GetHistory.
//  2. If len(window) <= 2, nothing to trim — return false.
//  3. Drop the recall span first (FR-019) if active and over budget; re-check.
//  4. Build the 5%-headroom budget target.
//  5. Walk Turn boundaries from oldest-first; pick the smallest b such that
//     window[b:] + toolDefs + recallSpanTokens fits in budget.
//  6. keepLast = len(window) - b; call TruncateHistory (window-relative).
//  7. FR-003 floor: if no boundary fits (single huge Turn), keep only the
//     most-recent user Turn and terminate.
//
// Returns a compressionResult with DroppedMessages/RemainingMessages for
// event emission, and ok=true when eviction actually occurred.
//
// The tool-surface term is what the turn ACTUALLY SENDS, not the whole
// registry — see sentToolSurfaceTokens.
//
// transcriptID is forwarded to sentToolSurfaceTokens unchanged — pass
// ts.opts.TranscriptSessionID when a *turnState is in scope, "" otherwise
// (manifestBucketKey then falls back to sessionKey alone for the loaded-tool
// bucket, same as an agent with no transcript session).
func (al *AgentLoop) windowTrim(agent *AgentInstance, transcriptID, sessionKey string) (compressionResult, bool) {
	return al.windowTrimForce(agent, transcriptID, sessionKey, false)
}

// evictionTotal counts successful windowTrim evictions (FR-018,
// context_eviction_total). Exported for test assertions.
var evictionTotal atomic.Int64

// skipAdvanceTotal counts TruncateHistory calls that actually advanced Skip
// (FR-018, context_skip_advance_total). Exported for test assertions.
var skipAdvanceTotal atomic.Int64

// agentLoopWindowTrimForce carries the shared state of windowTrimForce across its stages.
type agentLoopWindowTrimForce struct {
	agent            *AgentInstance
	sessionKey       string
	window           []providers.Message
	toolDefsTokens   int
	measured         []providers.Message
	recallSpanTokens int
	budget           int
	cutIdx           int
}

// windowTrimForce is windowTrim with the "the window already fits, do
// nothing" guard optionally disabled.
//
// force=false (windowTrim, every proactive caller): a caller that measured
// the budget itself and decided to trim can be WRONG — the pre-turn check
// historically charged the whole tool registry while this function charges
// only the sent surface — and without the guard a mis-fired call silently
// evicted the oldest turn every turn.
//
// force=true: the PROVIDER rejected the request with its own context error.
// Our estimate said it fit and the provider says otherwise, so the estimate
// is what is wrong; refusing to trim here would leave the retry identical to
// the call that just failed. This is the documented reactive fallback for
// "the estimate undershoots reality".
func (al *AgentLoop) windowTrimForce(
	agent *AgentInstance, transcriptID, sessionKey string, force bool,
) (compressionResult, bool) {
	aw := &agentLoopWindowTrimForce{agent: agent, sessionKey: sessionKey}

	if r0, r1, stop := aw.checkEligibility(); stop {
		return r0, r1
	}

	aw.toolDefsTokens = al.sentToolSurfaceTokens(aw.agent, transcriptID, aw.sessionKey)

	// ADR-066 FR-019: measure the window AS THE PROVIDER SEES IT. GetHistory
	// returns the archive's raw tail; results the choke point capped or an
	// earlier pass emptied are projected only at assembly. Counting their
	// full content here would over-evict (and, on the floor path, re-empty
	// results that are already marks). One archive read serves the
	// projection, the floor-path emptying below and the M5 stat.
	archive, archErr := aw.agent.Sessions.ReadArchive(context.Background(), aw.sessionKey)
	if archErr != nil {
		logger.DebugCF("agent", "windowTrim: ReadArchive failed; window measured unprojected, no floor emptying",
			map[string]any{"session_key": aw.sessionKey, "error": archErr.Error()})
	}
	aw.measured = aw.window
	lineOf := func(int) int { return -1 }
	if archErr == nil {
		lineOf = archiveLineResolver(archive, aw.window)
		if pm := aw.agent.Sessions.Projection(aw.sessionKey); len(pm.Entries) > 0 {
			var cs config.ContextSettings
			if cfg := al.GetConfig(); cfg != nil {
				cs = cfg.Context
			}
			aw.measured = projectMessages(aw.window, lineOf, pm.Entries, projectionContext{
				policy:  capPolicyFor(cs, agentContextBudget(aw.agent)),
				archive: archive,
			})
		}
	}

	// Recall span tokens — updated after a potential drop below.
	recallSpan := al.activeRecallSpan(aw.sessionKey)
	aw.recallSpanTokens = 0
	if recallSpan != nil {
		aw.recallSpanTokens = recallSpan.Tokens
	}

	// The ONE budget B (ADR-066 FR-028): W − max_tokens − ceil(0.05·W) −
	// pinnedCoreOverhead, resolved by the same helper the pre-turn and
	// timeout-recovery checks call, so the suffix fit-check below and the
	// checks that decide to invoke it can never disagree. The 5 % headroom
	// keeps a just-trimmed window from re-trimming on the very next turn;
	// the pinned-core term (M3 fix) is what stops under-eviction on
	// small-window models — the system prompt and breadcrumb are sent every
	// turn but are not part of `window`.
	aw.budget = agentContextBudget(aw.agent)

	// FR-019 drop-span-first: if an active span exists and we're over budget,
	// drop it and re-check. Only evict real window Turns if still over budget.
	currentWindowTokens := sumMessageTokens(aw.measured)

	// Nothing to do: the window already fits. Without this, a caller that
	// mis-fired (historically the pre-turn check, which charged the whole
	// tool registry while this function charges only the sent surface) would
	// still reach the boundary walk below, take the first non-zero boundary
	// whose suffix fits, and evict the oldest turn — every turn, silently,
	// on a conversation that never came close to the budget.
	//
	// Skipped under force: there the provider itself rejected the request, so
	// it is our estimate that is wrong, not the window.
	if !force && currentWindowTokens+aw.toolDefsTokens+aw.recallSpanTokens <= aw.budget {
		return compressionResult{NothingToTrim: true, RemainingMessages: len(aw.window)}, false
	}

	if recallSpan != nil && (currentWindowTokens+aw.toolDefsTokens+aw.recallSpanTokens > aw.budget) {
		al.dropRecallSpan(aw.sessionKey, "pressure")
		aw.recallSpanTokens = 0
		// Re-check against the same budget B used for the suffix fit-check
		// below. Using the raw window here would pass cases that the suffix
		// walk would still reject, causing unnecessary evictions on the next call.
		if currentWindowTokens+aw.toolDefsTokens <= aw.budget {
			// The recall span alone was the problem: dropping it brought the
			// window back under budget without evicting any window Turns.
			// This IS a real, successful eviction — FR-019 names the span
			// drop as step 3 of the documented algorithm — so report
			// ok=true, the same as a normal window eviction, rather than
			// ok=false. That makes the caller rebuild the assembled
			// messages (dropping the now-stale recall-span content) instead
			// of treating a useful eviction as a compaction failure.
			return compressionResult{RemainingMessages: len(aw.window)}, true
		}
	}

	aw.findCut()

	// Determine how many messages to keep (tail of the live window).
	// cutIdx >= 0: normal path — keep window[cutIdx:].
	// cutIdx < 0 (FR-003 floor): keep from the most-recent user message onward
	//   (the last complete Turn in the live window).
	//
	// Both paths call TruncateHistory (Skip-advancing, archive-preserving; zero
	// bytes deleted from the JSONL file). SetHistory is NEVER used — it would
	// overwrite the entire JSONL and reset Skip=0, permanently destroying evicted
	// turns (SC-001). The floor keeps window[lastUserIdx:] — the user message and
	// any following assistant/tool messages — not just the bare user message.
	var droppedCount int
	var emptiedCount int
	if aw.cutIdx >= 0 {
		// Normal path: tail-of-window keeps are handled by TruncateHistory.
		// TruncateHistory advances meta.Skip (archive-preserving; zero bytes
		// deleted from the JSONL file). SetHistory is NOT used here.
		keepLast := len(aw.window) - aw.cutIdx
		droppedCount = len(aw.window) - keepLast
		aw.agent.Sessions.TruncateHistory(aw.sessionKey, keepLast)
	} else {
		// FR-003: emergency floor — no boundary fits (single huge Turn).
		// Keep the messages from the most-recent user message onward. This is
		// the last complete Turn in the window (user message + any following
		// assistant/tool messages). TruncateHistory advances meta.Skip to
		// exactly that position — archive-preserving, no SetHistory.
		lastUserIdx := -1
		for i := len(aw.window) - 1; i >= 0; i-- {
			if aw.window[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		keepStart := lastUserIdx
		if keepStart < 0 {
			// Degenerate: no user message at all — keep last message.
			keepStart = len(aw.window) - 1
		}
		keepLast := len(aw.window) - keepStart
		droppedCount = len(aw.window) - keepLast
		if droppedCount > 0 {
			aw.agent.Sessions.TruncateHistory(aw.sessionKey, keepLast)
		}

		// ADR-066 D5, register #3 / B-21b (FR-017): the floor kept an
		// oversized turn whole. Its tool results — every one whose call is
		// in the kept slice, EXCEPT the floor set (the results of its last
		// assistant step) — are emptied oldest-first until the kept turn
		// fits B, or none is left. Skip does not move again: this is an
		// empty, not a cut. The pass persists (id, line) → emptied; the
		// caller's post-trim assembleMessages re-applies it, so the
		// in-memory copy mutated here is only the fit measurement.
		if archErr == nil {
			kept := append([]providers.Message(nil), aw.measured[keepStart:]...)
			keptLineOf := func(i int) int { return lineOf(keepStart + i) }
			fits := func(m []providers.Message) bool {
				return sumMessageTokens(m)+aw.toolDefsTokens+aw.recallSpanTokens <= aw.budget
			}
			emptiedCount = len(al.emptyInPlace(
				al.getActiveTurnState(aw.sessionKey), aw.agent, aw.sessionKey, kept, keptLineOf, archive, fits, emptyingSitePreTurn))
		}
	}

	if droppedCount == 0 && emptiedCount == 0 {
		// The window is already a single turn whose results are all in the
		// floor set (or already marks): nothing this site may do. Not an
		// error — D6's clamp keeps that set under B (CRIT-002).
		return compressionResult{NothingToTrim: true, RemainingMessages: len(aw.window)}, false
	}

	if saveErr := aw.agent.Sessions.Save(aw.sessionKey); saveErr != nil {
		logger.ErrorCF("agent", "windowTrim: failed to persist trimmed session",
			map[string]any{"session_key": aw.sessionKey, "error": saveErr.Error()})
	}

	// M4 fix: verify the window actually shrank after the TruncateHistory call.
	// The backends' TruncateHistory is fire-and-forget (errors are logged, not
	// returned). Re-read GetHistory and compare: if the window is the same size
	// as before, the trim silently failed — log the error and return ok=false so
	// the caller does not misreport a successful eviction. An empty-only pass
	// (droppedCount == 0) shrinks bytes, not the message count, so it is
	// exempt from the count check.
	postWindow := aw.agent.Sessions.GetHistory(aw.sessionKey)
	if droppedCount > 0 && len(postWindow) >= len(aw.window) {
		logger.ErrorCF("agent", "windowTrim: TruncateHistory did not shrink the window (backend write may have failed)",
			map[string]any{
				"session_key": aw.sessionKey,
				"before":      len(aw.window),
				"after":       len(postWindow),
			})
		return compressionResult{}, false
	}

	evictionTotal.Add(1)
	// skipAdvanceTotal counts Skip-advancing TruncateHistory calls. Both the
	// normal path and the FR-003 floor path now use TruncateHistory (Skip-
	// preserving), so increment whenever droppedCount > 0.
	if droppedCount > 0 {
		skipAdvanceTotal.Add(1)
	}

	keptCount := len(postWindow) // use the verified post-trim window size

	// M5 / FR-018: emit context_archive_lines from the archive read above.
	// Eviction never deletes bytes from the JSONL file, only advances Skip,
	// and emptying never touches it either (ADR-028 / ADR-066 B-23), so the
	// pre-trim read is the post-trim truth. Actual byte stat requires
	// fs.Stat — not exposed through SessionStore; the line count is the
	// proxy observable alongside the real skip value. -1 = unavailable.
	archiveBytes := int64(-1)
	if archErr == nil {
		archiveBytes = int64(len(archive))
	}

	logger.WarnCF("agent", "windowTrim: evicted oldest Turns from live window",
		map[string]any{
			"session_key":            aw.sessionKey,
			"turns_evicted":          droppedCount,
			"results_emptied":        emptiedCount,
			"kept_msgs":              keptCount,
			"budget":                 aw.budget,
			"context_archive_lines":  archiveBytes, // FR-018 context_archive_bytes proxy
			"context_eviction_total": evictionTotal.Load(),
		})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: keptCount,
	}, true
}

// checkEligibility rejects exempt providers and windows that cannot be shrunk further.
func (aw *agentLoopWindowTrimForce) checkEligibility() (compressionResult, bool, bool) {
	if aw.agent.budgetChecksExempt() {
		// FR-005: an exempt provider manages its own context; there is no
		// budget to fit against, so there is nothing to trim.
		return compressionResult{NothingToTrim: true}, false, true
	}
	aw.window = aw.agent.Sessions.GetHistory(aw.sessionKey)
	if len(aw.window) <= 1 {
		// Nothing to evict: a single-message window cannot be shrunk further.
		return compressionResult{NothingToTrim: true}, false, true
	}
	return *new(compressionResult), false, false
}

// findCut finds the smallest whole-turn boundary whose suffix fits the context budget.
func (aw *agentLoopWindowTrimForce) findCut() {
	// Walk Turn boundaries (oldest first) to find the smallest cut that fits.
	boundaries := parseTurnBoundaries(aw.window)

	// Find the smallest boundary index b such that window[b:] fits in budget.
	aw.cutIdx = -1 // -1 means no boundary fits
	for _, b := range boundaries {
		if b == 0 {
			// Boundary at 0 keeps everything — not a useful cut.
			continue
		}
		suffix := aw.measured[b:]
		suffixTokens := sumMessageTokens(suffix)
		if suffixTokens+aw.toolDefsTokens+aw.recallSpanTokens <= aw.budget {
			aw.cutIdx = b
			break
		}
	}
}

// SwitchAction is the result of decideSwitchCompressAction: should we
// compress the conversation at model-switch time, or no-op?
//
// (FR-011, spec §11 Dataset 3.)
type SwitchAction int

const (
	// SwitchActionNoop means the new window fits the current conversation;
	// no re-window needed.
	SwitchActionNoop SwitchAction = iota
	// SwitchActionCompress means the new window is smaller than the current
	// conversation; handleModelSwitch MUST invoke windowTrim before the next
	// LLM call (FR-011).
	SwitchActionCompress
)

// String returns a stable name for logging/metrics.
func (a SwitchAction) String() string {
	switch a {
	case SwitchActionNoop:
		return "noop"
	case SwitchActionCompress:
		return "compress"
	default:
		return "unknown"
	}
}

// decideSwitchCompressAction decides whether the loop must run switch-time
// compression (FR-011) given the current conversation size and the new
// model's context window. The decision is pure — it has no side effects and
// no LLM call — so the call site can gate the expensive summary path.
//
// Decision matrix (spec §11 Dataset 3):
//
//	currentConvTokens == 0           → Noop
//	newContextWindow  <= 0           → Noop (graceful — see handleModelSwitch)
//	currentConvTokens <= newWindow    → Noop
//	currentConvTokens >  newWindow    → Compress
//
// Tested by TestSwitchTimeCompress_*.
func decideSwitchCompressAction(currentConvTokens, newContextWindow int) SwitchAction {
	if currentConvTokens <= 0 || newContextWindow <= 0 {
		return SwitchActionNoop
	}
	if currentConvTokens <= newContextWindow {
		return SwitchActionNoop
	}
	return SwitchActionCompress
}

// sumMessageTokens sums estimateMessageTokens across a message slice.
// It is the single consistent call site for token-counting a []providers.Message
// so that the heuristic (chars*2/5) is applied uniformly across windowTrim,
// estimateHistoryTokens, and any future callers.
func sumMessageTokens(msgs []providers.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessageTokens(m)
	}
	return total
}

// estimateHistoryTokens is a small helper that sums estimateMessageTokens
// across a history slice. The loop's switch-time compress path uses it to
// compute the "current conversation size" fed into decideSwitchCompressAction.
func estimateHistoryTokens(history []providers.Message) int {
	return sumMessageTokens(history)
}

// handleModelSwitch is the switch-time re-window path (FR-011). It is invoked
// when an incoming bus message carries a model_name metadata that differs from
// the agent's current Model.
//
// Behavior:
//  1. Resolve the new model's ContextWindow.
//  2. Estimate the current conversation size.
//  3. If decideSwitchCompressAction says Compress (downsize only), call
//     windowTrim with the new budget — no LLM call, no summary written.
//     windowTrim inherits FR-003's last-user-Turn floor (MAJ-06).
//  4. Upsize (Noop): Skip stays forward-only (US-6.2). Extra room is used by
//     new turns; evicted turns are reachable via recall_conversation.
//  5. Update agent.Model to the new model and return the agent.
//
// The function is intentionally side-effect-light on the agent: it does NOT
// touch agent.Provider / agent.Candidates — those are resolved by the
// existing ApplyAgentModel path (FR-004). handleModelSwitch is the
// conversation-shape half of the switch; ApplyAgentModel is the
// provider/credential half. The turn flow wires both.
//
// Returns the (possibly mutated) agent so callers can keep using the same
// pointer.
func (al *AgentLoop) handleModelSwitch(
	ctx context.Context,
	agent *AgentInstance,
	transcriptID string,
	sessionKey string,
	newModel string,
	_ bus.InboundMessage,
) (*AgentInstance, error) {
	if agent == nil {
		return nil, fmt.Errorf("handleModelSwitch: nil agent")
	}
	if newModel == "" {
		return agent, nil
	}

	oldModel := agent.Model
	if oldModel == newModel {
		return agent, nil
	}

	history := agent.Sessions.GetHistory(sessionKey)
	currentConvTokens := estimateHistoryTokens(history)

	// ADR-066 D2: the new model's window comes from the ONE resolver, keyed
	// by the new primary's (provider, model) and this agent's id — the same
	// call NewAgentInstance and ApplyAgentModel make, so the switch-time
	// re-window, the pre-turn trim and the mid-turn check compare against
	// one budget B (B-06). An unresolvable model keeps the agent's current
	// window (the "unknown model = no-op switch" behaviour — ApplyAgentModel
	// below will fail and the turn continues on the previous model), but
	// the miss MUST be surfaced: discarding it would let a typo'd
	// `metadata.model_name` silently route the next call through the
	// agent's PRIMARY model — the exact FR-007 failure mode.
	newContextWindow, _, _ := agent.windowSnapshot()
	al.mu.RLock()
	cfg := al.cfg
	al.mu.RUnlock()
	if cfg != nil {
		if modelCfg, resolveErr := ResolveModelCfg(cfg, newModel, agent.Home); resolveErr != nil {
			logger.WarnCF("agent", "handleModelSwitch: requested model did not resolve; keeping the current window",
				map[string]any{
					"requested_model": newModel,
					"agent_id":        agent.ID,
					"resolve_error":   resolveErr.Error(),
				})
		} else {
			candidates := resolveModelCandidatesForAgent(cfg, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model, agent)
			windowProvider, windowModel := primaryWindowPair(candidates, cfg.Agents.Defaults.DefaultModel.Provider, modelCfg.Model)
			newContextWindow = ResolveWindow(cfg, windowProvider, windowModel, agent.ID).Window
		}
	}

	// FR-011: Re-window via windowTrim when the new model's window is
	// smaller than the current conversation. windowTrim uses the agent's
	// current ContextWindow; temporarily override it with newContextWindow
	// so the trim targets the new model's budget.
	//
	// UPSIZE: Skip stays forward-only (US-6.2). Extra room goes to new
	// turns; evicted turns are reachable via recall_conversation.
	// DOWNSIZE: windowTrim inherits FR-003's last-user-Turn floor (MAJ-06).
	// No summary is written — breadcrumb in BuildMessages is the only clue.
	// An exempt or unknown new window (0) is a Noop: nothing to trim against.
	action := decideSwitchCompressAction(currentConvTokens, newContextWindow)
	if action == SwitchActionCompress {
		// Temporarily set the agent's context window to the new model's window
		// so windowTrim computes the correct budget. We restore it after the
		// trim; ApplyAgentModel (below) sets the canonical value under the
		// instance lock together with the rest of the model identity.
		agent.mu.Lock()
		oldContextWindow := agent.ContextWindow
		agent.ContextWindow = newContextWindow
		agent.mu.Unlock()

		if _, trimOK := al.windowTrim(agent, transcriptID, sessionKey); !trimOK {
			logger.DebugCF("agent", "handleModelSwitch: windowTrim returned false (history too small to trim)",
				map[string]any{"session_key": sessionKey})
		} else {
			logger.InfoCF("agent", "handleModelSwitch: re-windowed via windowTrim (no summary)",
				map[string]any{
					"session_key":        sessionKey,
					"old_model":          oldModel,
					"new_model":          newModel,
					"new_context_window": newContextWindow,
				})
		}

		agent.mu.Lock()
		agent.ContextWindow = oldContextWindow
		agent.mu.Unlock()
	}

	// 5. Orchestrate the full in-memory model swap under the agent mutex.
	//    ApplyAgentModel resolves Model + Provider + Candidates atomically
	//    (FR-004 / FR-011), which is what the next LLM call will read. We
	//    run the whole swap inside agent.mu so concurrent runTurn readers
	//    never observe a torn (new Model, old Provider) state — that's the
	//    race the Go race detector was flagging on the bare `agent.Model =`
	//    write.
	//
	//    ApplyAgentModel takes its own agent.mu lock internally, so we
	//    dispatch it and rely on its serialization rather than double-locking.
	if _, applyErr := al.ApplyAgentModel(agent.ID, newModel); applyErr != nil {
		// ApplyAgentModel failed — leave the in-memory state untouched and
		// surface the error. We do NOT fall back to the bare Model write
		// because that would re-introduce the torn-state race.
		return agent, fmt.Errorf("handleModelSwitch: ApplyAgentModel(%q): %w", newModel, applyErr)
	}

	return agent, nil
}
