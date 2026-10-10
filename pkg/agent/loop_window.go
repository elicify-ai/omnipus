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
	"github.com/elicify-ai/omnipus/pkg/session"
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

func (al *AgentLoop) assembleMessages(
	ctx context.Context, ts *turnState, history []providers.Message,
	userMsg string, media []string, skillNames []string,
) []providers.Message {
	breadcrumb := ""
	if !ts.opts.NoHistory {
		store, ok := ts.agent.Sessions.(session.ContextWindowStore)
		if !ok {
			ts.setContextWindowError(fmt.Errorf("context assembly: session store does not support atomic context checkpoints"))
			return history
		}
		snap, err := store.WindowView(ctx, ts.sessionKey)
		if err != nil {
			ts.setContextWindowError(err)
			return history
		}
		var lines []int
		history, lines = recoveryWindowHistory(snap)
		cs := config.DefaultContextSettings()
		if cfg := al.GetConfig(); cfg != nil {
			cs = cfg.Context
		}
		history, err = projectMessagesChecked(history, func(i int) int { return lines[i] }, snap.State.Projection.Entries, projectionContext{
			policy: capPolicyFor(cs, agentContextBudget(ts.agent)), archive: viewArchive{view: snap},
			sourceRunes: snap.State.Projection.SourceRunes,
		})
		if err != nil {
			ts.setContextWindowError(err)
			return history
		}
		breadcrumb, err = breadcrumbForWindow(ctx, storeSlots{store: store, key: ts.sessionKey}, snap.State.Skip)
		if err != nil {
			ts.setContextWindowError(err)
			return history
		}
	}
	if ts.opts.IsTaskRun && breadcrumb != "" {
		breadcrumb += "\n\nReminder (task run): when the work is verified, report completion by calling goal_claim (status \"met\", with your one-line evidence) — not by writing a status yourself."
	}
	span := al.activeRecallSpan(ts.sessionKey)
	// Same drop-only pass spliceRecallSpan uses. BuildMessages is called with
	// nil history below, so it never sees the span; sanitizing here is what
	// keeps a mid-group freeze from reaching the pre-send validator. The live
	// window stays verbatim: re-filtering it would drop an in-flight group
	// whose results are not all in the slice yet.
	keptSpan, _ := sanitizeHistoryIndexed(span.Messages())
	recordAssembledRecallSpan(ts, span, history, len(keptSpan))
	// Build only the pinned instructions and the current user here. The live
	// window is inserted verbatim; only the recalled span is sanitized.
	base := ts.agent.ContextBuilder.BuildMessages(ts.depth, nil, userMsg, media,
		ts.opts.WorkspaceID, ts.channel, ts.chatID, ts.opts.SenderID,
		ts.opts.SenderDisplayName, breadcrumb, nil, skillNames...)
	out := make([]providers.Message, 0, len(base)+len(history)+len(keptSpan))
	out = append(out, base[0])
	// Keep the original leading user/anchor ahead of injected archived context.
	if len(history) > 0 && history[0].Role == "user" {
		out = append(out, history[0])
		history = history[1:]
	}
	out = append(out, keptSpan...)
	out = append(out, history...)
	out = append(out, base[1:]...)
	return out
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
	// Err is a genuine storage/projection error, distinct from NothingToTrim.
	Err               error
	DroppedMessages   int
	RemainingMessages int
	// NothingToTrim distinguishes already-fit or immutable windows from Err.
	// A committed recall drop or text projection is useful relief even when
	// DroppedMessages is zero; callers rebuild the request whenever ok is true.
	NothingToTrim bool
}

// windowTrim replaces forceCompression (FR-001/002/003/004). It evicts the
// oldest whole Turn(s) from the live window by advancing meta.Skip until the
// assembled token budget fits, deleting zero bytes from disk.
//
// Read one exact archive/window snapshot. Drop recall first, then choose a
// legal whole-turn suffix. If necessary, project retained result text without
// removing newest/incomplete structure, instructions or unconsumed controls.
// Commit cursor, anchor and exact projection metadata before publication.
// Immutable oversized residue is left to the provider, never a local fatal.
//
// Returns message counts for event emission, ok=true for committed relief
// (including recall/text-only changes), and Err for genuine failures.
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

// skipAdvanceTotal counts committed checkpoints that actually advanced Skip
// (FR-018, context_skip_advance_total). Exported for test assertions.
var skipAdvanceTotal atomic.Int64

// windowTrimForce preserves the pre-turn entry point. Real storage errors
// travel in the internal result, never as a guessed success or a size guard.
func (al *AgentLoop) windowTrimForce(
	agent *AgentInstance, transcriptID, sessionKey string, force bool,
) (compressionResult, bool) {
	ctx := context.Background()
	if ts := al.getActiveTurnState(sessionKey); ts != nil && ts.ctx != nil {
		ctx = ts.ctx
	}
	return al.trimWindowChecked(ctx, agent, transcriptID, sessionKey, force)
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

		compression, trimOK := al.windowTrim(agent, transcriptID, sessionKey)
		agent.mu.Lock()
		agent.ContextWindow = oldContextWindow
		agent.mu.Unlock()
		if compression.Err != nil {
			return agent, fmt.Errorf("handleModelSwitch: context checkpoint: %w", compression.Err)
		}
		if !trimOK {
			logger.DebugCF("agent", "handleModelSwitch: context window needs no mutable relief",
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
