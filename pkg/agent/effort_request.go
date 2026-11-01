package agent

// effort_request.go — per-candidate effort resolution, request-time mapping,
// the one-retry rule and the per-call effort record (effort-levels spec
// "Resolution per candidate", DE-5, FR-021, FR-057, FR-058).

import (
	"encoding/json"
	"log/slog"
	"maps"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

// Effort record reasons (EffortRecord.reason).
const (
	effortReasonMapped          = "mapped"
	effortReasonNotSupported    = effortdialect.ReasonNotSupported
	effortReasonRejected        = "rejected"
	effortReasonRejectedEarlier = "rejected_earlier"
)

// effortRejections remembers the (provider, model) pairs that rejected an
// effort field and accepted the resend without it (FR-057): later requests
// to the pair send no effort until the process restarts. A catalog refresh
// does not reset it.
type effortRejections struct {
	pairs sync.Map // key: provider + "\x00" + model
}

func effortPairKey(provider, model string) string { return provider + "\x00" + model }

func (r *effortRejections) remember(provider, model string) {
	r.pairs.Store(effortPairKey(provider, model), struct{}{})
}

func (r *effortRejections) rejected(provider, model string) bool {
	_, ok := r.pairs.Load(effortPairKey(provider, model))
	return ok
}

// effortPlan is what one candidate request will try to send.
type effortPlan struct {
	chosen string // the resolved stored setting ("" = nothing chosen)
	send   string // the mapped value handed to the adapter ("" = nothing)
	reason string // a reason already known before the adapter runs
}

// effortAttempt ties one provider call to its plan and the adapter's report
// of what the final body carried.
type effortAttempt struct {
	provider, model string
	plan            effortPlan
	sent            *effortdialect.Sent
	rejected        bool // this call is the resend after a rejection
}

// record builds the per-call effort record (FR-058). nil when nothing was
// chosen for the candidate.
func (a *effortAttempt) record() *generated.EffortRecord {
	if a == nil || a.plan.chosen == "" {
		return nil
	}
	chosen := a.plan.chosen
	if chosen == EffortProviderDefault {
		return &generated.EffortRecord{Chosen: &chosen}
	}
	rec := &generated.EffortRecord{Chosen: &chosen, Provider: effortStrPtr(a.provider), Model: effortStrPtr(a.model)}
	reason := a.plan.reason
	sent := ""
	if a.rejected {
		reason = effortReasonRejected
	} else if a.sent != nil {
		sent = a.sent.Value
		if a.sent.Reason != "" {
			reason = a.sent.Reason
		} else if sent != "" && !sameEffort(chosen, sent) {
			reason = effortReasonMapped
		}
	}
	if sent != "" {
		rec.Sent = &sent
	}
	if reason != "" {
		rec.Reason = &reason
	}
	return rec
}

func effortStrPtr(s string) *string { return &s }

// cloneReasoningDetails deep-copies the OpenRouter reasoning carrier (every
// item byte-for-byte); nil stays nil.
func cloneReasoningDetails(in []json.RawMessage) []json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(in))
	for i, item := range in {
		out[i] = append(json.RawMessage(nil), item...)
	}
	return out
}

// sameEffort reports whether a sent value is the chosen setting itself
// (Off sent as the level "none" is the same setting).
func sameEffort(chosen, sent string) bool {
	if chosen == sent {
		return true
	}
	return isEffortOff(chosen) && isEffortOff(sent)
}

// planEffort maps a turn candidate's chosen setting onto the served
// capability of its (provider, model) pair (DE-5). A pair with no catalog row
// is handed to the encoder unmapped; the encoder table still decides what the
// route can carry.
func planEffort(chosen, provider, model string) effortPlan {
	return planEffortFor(chosen, provider, model, true)
}

// planEffortFor is planEffort with the no-catalog-row rule chosen by the
// caller: unknownSends hands the value to the encoder unmapped; otherwise a
// pair without catalog reasoning data sends nothing (the recap, S-71).
func planEffortFor(chosen, provider, model string, unknownSends bool) effortPlan {
	plan := effortPlan{chosen: chosen}
	if chosen == "" || chosen == EffortProviderDefault {
		return plan
	}
	cat := providers.ProviderCatalog()
	var h catalog.Handle
	if cat != nil {
		h = cat.Resolve(provider, model)
	}
	if cat == nil || !h.Found() {
		if unknownSends {
			plan.send = chosen
		}
		return plan
	}
	levels, sw, reasons := h.ServedReasoning()
	m := MapEffort(chosen, EffortTarget{Levels: levels, Switch: sw, Reasons: reasons})
	switch {
	case m.Blocked, m.Value == "":
		plan.reason = effortReasonNotSupported
	case m.Value == EffortProviderDefault:
		if m.NoteKind == "nothing_sendable" {
			plan.reason = effortReasonNotSupported
		} else {
			plan.reason = effortReasonMapped
		}
	default:
		plan.send = m.Value
		if !sameEffort(chosen, m.Value) {
			plan.reason = effortReasonMapped
		}
	}
	return plan
}

// candidateEffortChoice resolves the stored setting for one candidate
// (spec "Resolution per candidate"): the primary uses the turn's resolved
// value (conversation value, else the agent primary's, else the default
// model's when the agent rides it); a fallback uses only its own stored
// effort — a chat or conversation value never reaches it (FD-5).
func (rt *agentLoopRunTurn) candidateEffortChoice(provider, model string, primary bool) string {
	agent := rt.ts.agent
	if primary {
		if v, ok := rt.llmOpts[effortdialect.EffortOptionKey].(string); ok {
			return strings.TrimSpace(v)
		}
		if agent == nil {
			return ""
		}
		return agent.PrimaryEffort
	}
	if agent == nil {
		return ""
	}
	for _, fb := range agent.FallbackModels {
		if strings.TrimSpace(fb.Provider) == provider && strings.TrimSpace(fb.Model) == model {
			return strings.TrimSpace(fb.ReasoningEffort)
		}
	}
	return ""
}

// isPrimaryCandidate reports whether (provider, model) is the turn's first
// candidate.
func (rt *agentLoopRunTurn) isPrimaryCandidate(provider, model string) bool {
	if len(rt.activeCandidates) == 0 {
		return true
	}
	first := rt.activeCandidates[0]
	return (first.Provider == provider || first.Provider == "") && (first.Model == model || model == rt.llmModel)
}

// effortOptions copies the turn options for one request: the shared map is
// never mutated, the candidate's own value (if any) replaces whatever the
// turn carried, and the adapter's report slot is attached.
func (rt *agentLoopRunTurn) effortOptions(send string, sent *effortdialect.Sent) map[string]any {
	opts := make(map[string]any, len(rt.llmOpts)+2)
	maps.Copy(opts, rt.llmOpts)
	delete(opts, effortdialect.EffortOptionKey)
	if send != "" {
		opts[effortdialect.EffortOptionKey] = send
	}
	opts[effortdialect.ReportOptionKey] = sent
	return opts
}

// isEffortRejection reports whether a failed request is the provider
// refusing a request field: only HTTP 400 and 422 trigger the resend (S-28).
func isEffortRejection(err error) bool {
	switch providers.HTTPStatusOf(err) {
	case 400, 422:
		return true
	}
	return false
}

// callWithEffort runs one candidate request with its own effort (FR-021),
// applying the one-retry rule (FR-057): a 400/422 to a request that carried
// effort is resent once without it; when the resend also fails the original
// error is returned. call receives the per-request options.
func (rt *agentLoopRunTurn) callWithEffort(
	provider, model string,
	call func(opts map[string]any) (*providers.LLMResponse, error),
) (*providers.LLMResponse, error) {
	primary := rt.isPrimaryCandidate(provider, model)
	if provider == "" && primary && rt.ts.agent != nil {
		// An unpinned primary candidate (a single native client, no pool
		// entry) still answers for the agent's configured provider: plan and
		// record against that pair, so a reloaded note can name it (FR-058).
		provider = rt.ts.agent.PrimaryEffortProvider
	}
	chosen := rt.candidateEffortChoice(provider, model, primary)
	plan := planEffort(chosen, provider, model)
	if plan.send != "" && rt.al.effortRejected.rejected(provider, model) {
		plan.send, plan.reason = "", effortReasonRejectedEarlier
		if rt.ts.effortRejectedThisTurn(provider, model) {
			// The rejection happened in this turn: the turn's record keeps
			// saying `rejected` (S-24/S-26); later turns say rejected_earlier.
			plan.reason = effortReasonRejected
		}
	}
	sent := &effortdialect.Sent{}
	attempt := &effortAttempt{provider: provider, model: model, plan: plan, sent: sent}
	rt.ts.setEffortAttempt(attempt)
	defer rt.ts.setEffortAttempt(nil)
	booked := rt.ts.callAccountingCount()
	resp, err := call(rt.effortOptions(plan.send, sent))
	if err == nil || sent.Value == "" || !isEffortRejection(err) {
		return resp, err
	}
	if rt.ts.callAccountingCount() == booked {
		// The refused attempt is still one provider call: book it on its
		// own (S-29), never folded into the resend.
		rt.ts.accountProviderCall(provider, model, nil)
	}
	slog.Warn("effort setting rejected; resending once without effort",
		"effort_rejected", true,
		"status", providers.HTTPStatusOf(err),
		"provider", provider,
		"model", model,
		"requested", sent.Value)
	resent := &effortdialect.Sent{}
	retry := &effortAttempt{provider: provider, model: model, plan: plan, sent: resent, rejected: true}
	rt.ts.setEffortAttempt(retry)
	resp2, err2 := call(rt.effortOptions("", resent))
	if err2 != nil {
		return nil, err
	}
	rt.al.effortRejected.remember(provider, model)
	rt.ts.noteEffortRejected(provider, model)
	return resp2, nil
}

// primaryEffortChoice resolves the turn's stored setting for the primary
// candidate: the web chat's per-message value, else the conversation's
// SessionMeta.reasoning_effort (/effort), else the agent primary's (or the
// default model's when the agent rides it). "" = nothing chosen.
func (rt *agentLoopRunTurn) primaryEffortChoice() string {
	if v := strings.TrimSpace(rt.ts.opts.Metadata[effortdialect.EffortOptionKey]); v != "" {
		return v
	}
	if rt.ts.transcriptStore != nil && rt.ts.transcriptSessionID != "" {
		meta, err := rt.ts.transcriptStore.GetMeta(rt.ts.transcriptSessionID)
		if err != nil {
			slog.Warn("effort: could not read the conversation setting; using the agent setting",
				"session_id", rt.ts.transcriptSessionID, "error", err)
		} else if meta != nil && strings.TrimSpace(meta.ReasoningEffort) != "" {
			return strings.TrimSpace(meta.ReasoningEffort)
		}
	}
	if rt.ts.agent == nil {
		return ""
	}
	return rt.ts.agent.PrimaryEffort
}
