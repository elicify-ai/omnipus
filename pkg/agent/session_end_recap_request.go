package agent

// session_end_recap_request.go — the recap request: its prompt and limits
// from settings, each candidate's own effort (default Off), and the one
// overall time limit (effort-levels spec FR-070, FR-072, FR-074, "Recap
// limits", "Recap timing rules", S-71, S-78, S-79, S-80, S-97).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

// errRecapDeadline is the recap failure when the overall time limit ends
// before any candidate could answer.
var errRecapDeadline = errors.New("recap deadline exceeded")

// recapCandidate is one model of the recap chain with its own stored effort.
type recapCandidate struct {
	providers.FallbackCandidate
	effort string
}

// callCandidates runs the recap chain with the timing values read from the
// settings getters, converted to durations in this one place (S-97).
func (rr *agentLoopRunRecap) callCandidates() bool {
	d := &rr.al.GetConfig().Agents.Defaults
	return rr.callCandidatesWithTiming(
		time.Duration(d.GetRecapTimeoutSeconds())*time.Second,
		d.GetRecapStreamRetries(),
		time.Duration(d.GetRecapRetryPauseMs())*time.Millisecond,
	)
}

// recapMessages builds the recap prompt: the newest user turns within the
// input budget (recap_input_tokens x recap_chars_per_token characters, oldest
// dropped) and the instruction with the summary and list limits.
func (rr *agentLoopRunRecap) recapMessages(d *config.AgentDefaults) []providers.Message {
	budgetRunes := d.GetRecapInputTokens() * d.GetRecapCharsPerToken()
	combined := strings.Join(rr.userTurns, "\n\n")
	prefix := ""
	if runes := []rune(combined); len(runes) > budgetRunes {
		combined = string(runes[len(runes)-budgetRunes:])
		prefix = "[history truncated for summarisation]\n\n"
	}
	items := d.GetRecapListItems()
	recapPrompt := fmt.Sprintf(
		`Summarize this conversation in ≤ %d words. Then list up to %d wins, up to %d needs-improvement items, and up to %d items worth remembering long-term. Respond ONLY with valid JSON: {"recap":"...", "went_well":[...], "needs_improvement":[...], "worth_remembering":[...]}`,
		d.GetRecapSummaryWords(), items, items, items)
	return []providers.Message{{Role: "user", Content: prefix + combined + "\n\n" + recapPrompt}}
}

// recapCandidates returns [primary, ...recap_fallback_models], each with its
// own effort; an absent effort means Off (FD-19).
//
// The primary's provider: recap_provider when set (#1291); otherwise, with no
// recap model configured, the default (provider, model) pair as-is (ADR-068);
// otherwise the configured provider that owns the model (resolveModelRef), so
// a provider-prefixed model name is stripped before the upstream call.
func (rr *agentLoopRunRecap) recapCandidates(cfg *config.Config) []recapCandidate {
	d := &cfg.Agents.Defaults
	recapModel := strings.TrimSpace(d.RecapModel)
	pinned := ""
	if recapModel != "" {
		pinned = strings.TrimSpace(d.RecapProvider)
	} else if !d.DefaultModel.IsZero() {
		recapModel = d.DefaultModel.Model
		pinned = d.DefaultModel.Provider
	}
	if recapModel == "" {
		recapModel = rr.agentInst.Model
	}
	primary := providers.FallbackCandidate{Model: recapModel, Provider: pinned}
	if pinned == "" {
		if ref, ok := resolveModelRef(cfg, recapModel); ok {
			primary = providers.FallbackCandidate{Model: ref.Model, Provider: ref.Provider}
		}
	}
	out := make([]recapCandidate, 0, 1+len(d.RecapFallbackModels))
	out = append(out, recapCandidate{FallbackCandidate: primary, effort: recapEffortOrOff(d.RecapReasoningEffort)})
	for _, fm := range d.RecapFallbackModels {
		out = append(out, recapCandidate{
			FallbackCandidate: providers.FallbackCandidate{Provider: fm.Provider, Model: fm.Model},
			effort:            recapEffortOrOff(fm.ReasoningEffort),
		})
	}
	return out
}

func recapEffortOrOff(v string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return effortdialect.ValueOff
}

// recapOptions is one candidate's request options: the output limit and the
// candidate's mapped effort. A model with no catalog reasoning data, or one
// that cannot carry the value (Off without a way to turn reasoning off),
// gets no effort key and no note (S-71).
func recapOptions(d *config.AgentDefaults, c recapCandidate) map[string]any {
	opts := map[string]any{"max_tokens": d.GetRecapMaxOutputTokens()}
	if plan := planEffortFor(c.effort, c.Provider, c.Model, false); plan.send != "" {
		opts[effortdialect.EffortOptionKey] = plan.send
	}
	return opts
}

// waitRecapPause waits until the earlier of the scheduled pause end and the
// recap deadline. It reports false when the deadline came first.
func waitRecapPause(pause time.Duration, deadline time.Time) bool {
	end := time.Now().Add(pause)
	if !end.Before(deadline) {
		if wait := time.Until(deadline); wait > 0 {
			timer := time.NewTimer(wait)
			<-timer.C
		}
		return false
	}
	timer := time.NewTimer(time.Until(end))
	<-timer.C
	return true
}

// callCandidatesWithTiming tries the recap candidates in order within one
// overall time limit. Every try's context deadline is the recap deadline
// (each model gets whatever remains); after a transient stream error with
// retries left, retry n waits n x retryPause, cut at the deadline; no
// provider call starts at or after the deadline. It returns true when the
// recap ended on the heuristic fallback.
func (rr *agentLoopRunRecap) callCandidatesWithTiming(timeLimit time.Duration, streamRetries int, retryPause time.Duration) bool {
	deadline := time.Now().Add(timeLimit)
	snapCfg := rr.al.GetConfig()
	d := &snapCfg.Agents.Defaults
	msgs := rr.recapMessages(d)
	candidates := rr.recapCandidates(snapCfg)

	plain := make([]providers.FallbackCandidate, len(candidates))
	for i, c := range candidates {
		plain[i] = c.FallbackCandidate
	}
	// A one-off provider pool for the recap chain (parity with agent turn
	// fallbacks, FR-007). The agent id is passed for the FR-016 WARNs only;
	// the recap chain's primaryUnknown verdict is deliberately not projected
	// onto the instance's needs_provider degrade.
	pool := buildProviderPool(snapCfg, plain, rr.agentInst.ID).pool
	resolve := func(c providers.FallbackCandidate) providers.LLMProvider {
		if pinned := strings.TrimSpace(c.Provider); pinned != "" && pool != nil {
			if p, ok := pool[pinned]; ok && p != nil {
				return p
			}
		}
		if p := rr.agentInst.GetProviderForCandidate(c); p != nil {
			return p
		}
		return rr.agentInst.Provider
	}

	var llmErr error
	stopped := false
	for _, c := range candidates {
		p := resolve(c.FallbackCandidate)
		opts := recapOptions(d, c)
		for attempt := 0; attempt <= streamRetries; attempt++ {
			if !time.Now().Before(deadline) {
				rr.warnRecapDeadline(c.Model)
				stopped = true
				break
			}
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			r, err := p.Chat(ctx, msgs, nil, c.Model, opts)
			cancel()
			if err == nil {
				rr.resp, llmErr = r, nil
				break
			}
			llmErr = err
			if attempt < streamRetries && isTransientStreamError(err) {
				backoff := time.Duration(attempt+1) * retryPause
				slog.Warn("session_end: recap transient stream error, retrying",
					"session_id", rr.sessionID,
					"agent_id", rr.agentInst.ID,
					"model", c.Model,
					"attempt", attempt+1,
					"max_retries", streamRetries,
					"backoff_ms", backoff.Milliseconds(),
					"error", err.Error(),
				)
				if !waitRecapPause(backoff, deadline) {
					rr.warnRecapDeadline(c.Model)
					stopped = true
					break
				}
				continue
			}
			slog.Warn("session_end: recap candidate failed, trying next",
				"session_id", rr.sessionID,
				"agent_id", rr.agentInst.ID,
				"model", c.Model,
				"attempt", attempt+1,
				"error", err.Error(),
			)
			break
		}
		if rr.resp != nil || stopped {
			break
		}
	}
	if rr.resp == nil && llmErr == nil {
		llmErr = errRecapDeadline
	}
	if rr.resp != nil {
		return false
	}
	slog.Warn("session_end: llm call failed",
		"session_id", rr.sessionID,
		"agent_id", rr.agentInst.ID,
		"error", llmErr.Error(),
	)
	// SF1: two distinct audit entries so operators see both outcomes: the LLM
	// call failed, and the heuristic fallback was written.
	rr.al.auditRecap(rr.sessionID, rr.agentInst.ID, rr.trigger, "llm_failed:"+classifyLLMError(llmErr))
	// The writer logs and audits its own failure; nobody here can take it.
	_ = rr.al.writeHeuristicFallbackRetroWithCount(
		rr.sessionID, rr.trigger, classifyLLMError(llmErr), rr.agentInst,
		len(rr.entries), rr.toolCallCount, rr.carryForward,
	)
	return true
}

func (rr *agentLoopRunRecap) warnRecapDeadline(model string) {
	slog.Warn("session_end: recap deadline exceeded, stopping retries",
		"session_id", rr.sessionID,
		"agent_id", rr.agentInst.ID,
		"model", model,
	)
}
