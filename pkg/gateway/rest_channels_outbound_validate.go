package gateway

// The one outbound writer's validation step, shared by the writer
// (applyOutboundChange) and the read-only preview set_channel_outbound runs
// before its approval card (Channels Q2-B, previewOutboundChange), so the
// checks the person's card relies on and the checks the save runs cannot
// drift.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// outboundValidation is validateOutboundChange's result: the live instance
// it checked, its platform, the canonical requested list, the targets that
// list adds to the stored one and the added Discord targets confirmed by a
// look-up (in this call, or before the approval card under the same
// revision).
type outboundValidation struct {
	inst      config.ChannelInstanceConfig
	platform  string
	canonical []string
	added     []string
	verified  []string
}

// validateOutboundChange is the writer's step 1 on one live configuration
// snapshot, without a lock: the instance exists, expectedRevision is still
// current (409 stale_revision), the targets canonicalize (400
// invalid_targets / too_many_targets), the platform can start messages (422
// platform_cannot_start / platform_cannot_message_people) and every added
// Discord target exists on a running connection (409 connect_first, 502
// lookup_unavailable, 400 invalid_targets; ctx carries the request's
// cancellation, each look-up adds its own 5-second deadline).
//
// preVerified lists the Discord targets an approval card's preview already
// looked up and confirmed. Founder Q1 B: when every added target is in it,
// the connection and look-up checks are not repeated — the revision check
// above has just proved the settings are the ones the card was built from.
// Nil (REST, a call without a card) always runs them.
func (a *restAPI) validateOutboundChange(
	ctx context.Context,
	live *config.Config,
	id string,
	policy gen.ChannelOutboundUpdateRequestPolicy,
	targets []string,
	expectedRevision string,
	preVerified []string,
) (outboundValidation, error) {
	var v outboundValidation
	inst, configured := live.Channels[id]
	if !configured {
		return v, errOutboundNotConfigured
	}
	if config.ChannelConfigRevision(live, id) != expectedRevision {
		return v, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeStaleRevision,
			"these settings changed since they were read; read them again and retry; nothing was saved")
	}
	platform := channels.InstancePlatform(id, inst)
	canonical, errs := channels.CanonicalizeTargets(platform, targets)
	if len(errs) > 0 {
		return v, invalidTargetsRejection(errs)
	}
	if len(canonical) > outboundMaxTargets {
		return v, outboundRejection(http.StatusBadRequest, gen.ChannelOutboundRejectionCodeTooManyTargets,
			fmt.Sprintf("at most %d people and groups can be listed; nothing was saved", outboundMaxTargets))
	}
	if canonical == nil {
		canonical = []string{}
	}
	added := addedTargets(platform, inst.Outbound, canonical)
	rule := channels.PlatformStartRuleFor(platform)
	if !rule.StartSupported && (channels.StartPolicy(policy) == channels.StartPolicyAgentMayStart || len(added) > 0) {
		if len(added) > 0 && rule.PersonRefusal == string(gen.ChannelOutboundRejectionCodePlatformCannotMessagePeople) {
			return v, outboundRejection(http.StatusUnprocessableEntity,
				gen.ChannelOutboundRejectionCodePlatformCannotMessagePeople,
				"this platform can only answer, not start a direct message")
		}
		return v, outboundRejection(http.StatusUnprocessableEntity, gen.ChannelOutboundRejectionCodePlatformCannotStart,
			"starting messages is not supported yet on "+rule.DisplayName)
	}
	v = outboundValidation{inst: inst, platform: platform, canonical: canonical, added: added}
	if platform != "discord" || len(added) == 0 {
		return v, nil
	}
	if containsAllTargets(preVerified, added) {
		v.verified = append([]string{}, added...)
		return v, nil
	}
	if a.channelAvailability(id).State != channels.AvailabilityRunning {
		return v, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeConnectFirst, outboundConnectFirst)
	}
	rawIndex := firstRawEntries(platform, targets)
	refused, unavailable := a.lookUpDiscordTargets(ctx, id, added, rawIndex)
	if len(unavailable) > 0 {
		return v, lookupUnavailableRejection(unavailable)
	}
	if len(refused) > 0 {
		return v, invalidTargetsRejection(refused)
	}
	v.verified = append([]string{}, added...)
	return v, nil
}

func containsAllTargets(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, t := range have {
		set[t] = true
	}
	for _, t := range want {
		if !set[t] {
			return false
		}
	}
	return true
}

// applyOutboundChange is the one outbound writer (FR-020), shared by PUT
// /api/v1/channels/{id}/outbound and the set_channel_outbound tool:
// (1) without a lock, read the live configuration and run
// validateOutboundChange (revision fast-fail, canonical targets, platform,
// bounded Discord look-ups); (2) take configMu; (3) recheck the live
// revision (409 stale_revision); (3b) classify extending vs non-extending
// over the saved and live snapshots (409 saved_live_mismatch); (4) save
// through the common save step with GuardOutbound(dedicated = true); (5) on
// a live-refresh failure publish the guard-accepted outbound value alone and
// set Warning; (6) return the contract ChannelOutboundWriteResponse. actor
// names the writer for logs ("rest" or the calling agent id); it grants
// nothing.
//
// Errors: *outboundRejectionError for every contract refusal,
// errOutboundNotConfigured for an instance with no config entry; any other
// error is a save failure before the file was written.
func (a *restAPI) applyOutboundChange(
	ctx context.Context,
	id string,
	policy gen.ChannelOutboundUpdateRequestPolicy,
	targets []string,
	expectedRevision, actor string,
) (gen.ChannelOutboundWriteResponse, error) {
	resp, _, err := a.applyOutboundChangeVerified(ctx, id, policy, targets, expectedRevision, actor, nil)
	return resp, err
}

// applyOutboundChangeVerified is applyOutboundChange with the approval
// card's pre-verified Discord targets (see validateOutboundChange). It also
// returns the added Discord targets that were confirmed by a look-up.
func (a *restAPI) applyOutboundChangeVerified(
	ctx context.Context,
	id string,
	policy gen.ChannelOutboundUpdateRequestPolicy,
	targets []string,
	expectedRevision, actor string,
	preVerified []string,
) (gen.ChannelOutboundWriteResponse, []string, error) {
	var none gen.ChannelOutboundWriteResponse
	v, err := a.validateOutboundChange(ctx, a.agentLoop.GetConfig(), id, policy, targets, expectedRevision, preVerified)
	if err != nil {
		return none, nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return none, nil, ctxErr // a cancelled request never commits later (FR-019)
	}
	desired := &config.ChannelOutboundConfig{Policy: string(policy), Targets: v.canonical}
	resp, err := a.commitOutboundChange(ctx, id, desired, expectedRevision, actor)
	return resp, v.verified, err
}

// previewOutboundChange is set_channel_outbound's read-only run of the
// writer's checks before the approval card (Channels Q2-B §2/§3): the same
// validateOutboundChange on one live snapshot plus a lock-free saved/live
// check (re-checked under configMu at commit). It returns the card facts
// and the revision they were read at. An omitted expected_revision means
// "the current one"; a supplied one must still be current. Every refusal is
// audited here as channel.outbound.write, outcome refused_before_approval,
// and returned in the tool's neutral error types. It writes nothing.
func (a *restAPI) previewOutboundChange(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundPreview, error) {
	preview, err := a.previewOutboundChangeChecked(ctx, change)
	if err != nil {
		a.auditOutboundRefusalBeforeApproval(ctx, change, outboundRefusalReason(err))
		return systools.ChannelOutboundPreview{}, outboundToolError(err)
	}
	return preview, nil
}

func (a *restAPI) previewOutboundChangeChecked(ctx context.Context, change systools.ChannelOutboundChange) (systools.ChannelOutboundPreview, error) {
	var none systools.ChannelOutboundPreview
	live := a.agentLoop.GetConfig()
	if _, configured := live.Channels[change.ID]; !configured {
		return none, errOutboundNotConfigured
	}
	revision := config.ChannelConfigRevision(live, change.ID)
	expected := change.ExpectedRevision
	if expected == "" {
		expected = revision
	}
	policy := gen.ChannelOutboundUpdateRequestPolicy(change.Policy)
	v, err := a.validateOutboundChange(ctx, live, change.ID, policy, change.Targets, expected, nil)
	if err != nil {
		return none, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return none, ctxErr
	}
	savedInst, saved, err := a.savedChannelInstance(change.ID)
	if err != nil {
		return none, err
	}
	if !saved {
		return none, errOutboundNotConfigured
	}
	desired := &config.ChannelOutboundConfig{Policy: change.Policy, Targets: v.canonical}
	if outboundExtending(savedInst, v.inst, desired) &&
		canonicalInstanceBytes(change.ID, savedInst) != canonicalInstanceBytes(change.ID, v.inst) {
		return none, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeSavedLiveMismatch, outboundSavedLiveText)
	}
	preview := systools.ChannelOutboundPreview{
		Platform:        v.platform,
		CurrentPolicy:   string(channels.StartPolicyReplyOnly),
		CurrentTargets:  []string{},
		Revision:        revision,
		NewPolicy:       change.Policy,
		NewTargets:      append([]string{}, v.canonical...),
		Added:           append([]string{}, v.added...),
		Removed:         removedTargets(v.platform, v.inst.Outbound, v.canonical),
		VerifiedTargets: append([]string{}, v.verified...),
	}
	if v.inst.Outbound != nil {
		preview.CurrentPolicy = v.inst.Outbound.Policy
		preview.CurrentTargets = append(preview.CurrentTargets, v.inst.Outbound.Targets...)
	}
	return preview, nil
}

// removedTargets returns the stored targets (canonicalized) that the
// canonical new list no longer holds, in stored order. The list is replaced
// whole, so the card shows who loses permission.
func removedTargets(platform string, stored *config.ChannelOutboundConfig, canonical []string) []string {
	removed := []string{}
	if stored == nil {
		return removed
	}
	keep := make(map[string]bool, len(canonical))
	for _, t := range canonical {
		keep[t] = true
	}
	seen := map[string]bool{}
	for _, t := range stored.Targets {
		c, code := channels.CanonicalizeTarget(platform, t)
		if code != "" {
			c = t
		}
		if !keep[c] && !seen[c] {
			seen[c] = true
			removed = append(removed, c)
		}
	}
	return removed
}

// outboundRefusalReason is the audit reason of a writer error: the
// rejection code, not_configured, or preview_failed for any other error
// (nothing was written either way).
func outboundRefusalReason(err error) string {
	var rejection *outboundRejectionError
	switch {
	case errors.As(err, &rejection):
		return string(rejection.Body.Code)
	case errors.Is(err, errOutboundNotConfigured):
		return "not_configured"
	default:
		return "preview_failed"
	}
}

// outboundToolError translates a writer error to the sysagent tool
// package's neutral types: *outboundRejectionError →
// *ChannelOutboundRefusalError (code and safe text, per-target detail kept);
// errOutboundNotConfigured → ErrChannelOutboundNotConfigured; any other
// error unchanged.
func outboundToolError(err error) error {
	var rejection *outboundRejectionError
	switch {
	case errors.As(err, &rejection):
		refusal := &systools.ChannelOutboundRefusalError{Reason: string(rejection.Body.Code), Message: rejection.Body.Error}
		if rejection.Body.TargetErrors != nil {
			refusal.TargetErrors = *rejection.Body.TargetErrors
		}
		if rejection.Body.OmittedCount != nil {
			refusal.OmittedCount = *rejection.Body.OmittedCount
		}
		return refusal
	case errors.Is(err, errOutboundNotConfigured):
		return systools.ErrChannelOutboundNotConfigured
	default:
		return err
	}
}

// auditOutboundRefusalBeforeApproval records one refusal of a
// set_channel_outbound call before any approval card (Q2-B §3, closes
// #1623 item 1): channel.outbound.write, outcome refused_before_approval,
// the reason, an empty approval id, and the call's agent, tool call,
// session, turn and channel. No target value is recorded.
func (a *restAPI) auditOutboundRefusalBeforeApproval(ctx context.Context, change systools.ChannelOutboundChange, reason string) {
	logger := a.agentLoop.AuditLogger()
	if logger == nil {
		return
	}
	audit.EmitChannelOutboundWrite(context.WithoutCancel(ctx), logger, audit.ChannelOutboundWrite{
		ToolCallID: tools.ToolCallID(ctx), SessionID: tools.ToolTranscriptSessionID(ctx),
		AgentID: change.Actor, TurnID: tools.ToolTurnID(ctx), ChannelID: change.ID,
		Outcome: "refused_before_approval", Reason: reason,
	})
}
