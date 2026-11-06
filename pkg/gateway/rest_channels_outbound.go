package gateway

// The one outbound writer and its REST routes (channels-spec-r8 FR-012,
// FR-013, FR-016, FR-017, FR-019, FR-020, FR-021, FR-022, FR-027, T-1, T-6b,
// T-9): GET/PUT /api/v1/channels/{id}/outbound.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	// outboundMaxTargets is the stored-list limit over distinct canonical
	// targets (FR-012).
	outboundMaxTargets = 20
	// outboundMaxRawTargets and outboundMaxRawTargetLen are the request
	// schema bounds (ChannelOutboundUpdateRequest), enforced here too so
	// they hold with inbound validation off.
	outboundMaxRawTargets   = 64
	outboundMaxRawTargetLen = 256
	// outboundMaxTargetErrors is ChannelOutboundRejection.target_errors'
	// maxItems; outboundErrorMaxLen its error maxLength.
	outboundMaxTargetErrors = 10
	outboundErrorMaxLen     = 500
	// outboundReasonMaxLen is target_errors[].reason's maxLength.
	outboundReasonMaxLen = 120
	// outboundHintLen is the number of safe-echo characters a target hint
	// keeps before "...".
	outboundHintLen = 20

	outboundSaveFailedText = "could not save the setting; nothing was changed"
	outboundConnectFirst   = "connect the channel first to add Discord ids"
	outboundSavedLiveText  = "The saved and running configurations for this connection differ because an earlier " +
		"change did not finish applying. Nothing was saved. Finish applying the earlier change first (any " +
		"successful save of this connection, or disable/enable), then save the list again."
)

// errOutboundNotConfigured is applyOutboundChange's answer for an instance
// with no configuration entry (REST 404).
var errOutboundNotConfigured = errors.New("channel is not configured")

// outboundRejectionError is applyOutboundChange's refusal before anything was
// written: Status is the HTTP status of the contract (400, 404, 409, 422,
// 502) and Body the contract ChannelOutboundRejection (already sanitized to
// the safe-echo set). Any other non-nil error from applyOutboundChange is a
// save failure: before the file write → 500 "could not save the setting;
// nothing was changed".
type outboundRejectionError struct {
	Body   gen.ChannelOutboundRejection
	Status int
}

func (e *outboundRejectionError) Error() string {
	return string(e.Body.Code) + ": " + e.Body.Error
}

func outboundRejection(status int, code gen.ChannelOutboundRejectionCode, text string) *outboundRejectionError {
	return &outboundRejectionError{Status: status, Body: gen.ChannelOutboundRejection{
		Code: code, Error: truncateSafeEcho(text, outboundErrorMaxLen),
	}}
}

// isSafeEcho reports whether r is in the contract's safe-echo set: printable
// ASCII except the five characters json.Marshal HTML-escapes.
func isSafeEcho(r rune) bool {
	return r >= 0x20 && r <= 0x7e && !strings.ContainsRune("\"\\<>&", r)
}

// safeEcho drops every character outside the safe-echo set (never escapes
// or substitutes).
func safeEcho(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isSafeEcho(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncateSafeEcho(s string, maxLen int) string {
	s = safeEcho(s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

// outboundTargetHint is the contract's target hint: the first 20 safe-echo
// characters of the raw entry, plus "..." when more survived.
func outboundTargetHint(raw string) string {
	s := safeEcho(raw)
	if len(s) > outboundHintLen {
		return s[:outboundHintLen] + "..."
	}
	return s
}

// outboundTargetReasons are the fixed catalog reasons, one per target error
// code (all in the safe-echo set, at most 120 characters): every refused
// entry reaches the operator with its reason (S-90).
var outboundTargetReasons = map[channels.TargetErrorCode]string{
	channels.TargetErrInvalidID:          "not a valid id for this platform; use the numeric id",
	channels.TargetErrWildcardNotAllowed: "a wildcard is not allowed; list each person or group by its id",
	channels.TargetErrUsernameNotAllowed: "a message cannot be sent to a name; use the numeric id",
	channels.TargetErrWrongPlatform:      "this id names a different platform than this connection",
	channels.TargetErrNotFound:           "no person, channel or thread with this id was found",
	channels.TargetErrUnsupportedChannelKind: "this kind of Discord channel cannot receive a posted message; " +
		"use a text channel, announcement channel or thread id",
	channels.TargetErrCannotMessagePeople: "this platform can only answer, not start a direct message",
	channels.TargetErrLookupUnavailable:   "the platform could not be reached to check this id; try again",
}

// invalidTargetsRejection builds the 400 invalid_targets body: the first 10
// refused entries in request order, omitted_count for the rest.
func invalidTargetsRejection(errs []channels.TargetError) *outboundRejectionError {
	rej := outboundRejection(http.StatusBadRequest, gen.ChannelOutboundRejectionCodeInvalidTargets,
		"some entries are not valid targets; nothing was saved")
	type item = struct {
		Code   gen.ChannelOutboundRejectionTargetErrorsCode `json:"code"`
		Index  int                                          `json:"index"`
		Reason *string                                      `json:"reason,omitempty"`
		Target string                                       `json:"target"`
	}
	items := make([]item, 0, min(len(errs), outboundMaxTargetErrors))
	for _, e := range errs[:min(len(errs), outboundMaxTargetErrors)] {
		it := item{Code: gen.ChannelOutboundRejectionTargetErrorsCode(e.Code), Index: e.Index, Target: outboundTargetHint(e.Raw)}
		if reason := truncateSafeEcho(e.Reason, outboundReasonMaxLen); reason != "" {
			it.Reason = &reason
		} else if reason, ok := outboundTargetReasons[e.Code]; ok {
			it.Reason = &reason
		}
		items = append(items, it)
	}
	rej.Body.TargetErrors = &items
	if omitted := len(errs) - len(items); omitted > 0 {
		rej.Body.OmittedCount = &omitted
	}
	return rej
}

// lookupUnavailableRejection builds the 502 body listing the unavailable ids
// in request order within the 500-character bound, then "+<N> more".
func lookupUnavailableRejection(ids []string) *outboundRejectionError {
	const prefix, suffix = "Discord could not be reached to check ", "; nothing was saved; try again"
	listed := ""
	for i, id := range ids {
		next := listed
		if next != "" {
			next += ", "
		}
		next += safeEcho(id)
		more := ""
		if rest := len(ids) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d more", rest)
		}
		if len(prefix+next+more+suffix) > outboundErrorMaxLen {
			listed += fmt.Sprintf(" +%d more", len(ids)-i)
			break
		}
		listed = next
	}
	return outboundRejection(http.StatusBadGateway, gen.ChannelOutboundRejectionCodeLookupUnavailable, prefix+listed+suffix)
}

// outboundPermissions is a T-6b permission set: (binding, target) pairs in
// force under one state; empty unless the state's policy is agent_may_start.
type outboundPermissions map[[3]string]bool

func permissionsOf(inst config.ChannelInstanceConfig, outbound *config.ChannelOutboundConfig) outboundPermissions {
	set := outboundPermissions{}
	if outbound == nil || channels.StartPolicy(outbound.Policy) != channels.StartPolicyAgentMayStart {
		return set
	}
	workspace, agent := strings.TrimSpace(inst.WorkspaceID), ""
	if inst.Identity != nil {
		agent = strings.TrimSpace(inst.Identity.ID)
	}
	for _, target := range outbound.Targets {
		set[[3]string{workspace, agent, target}] = true
	}
	return set
}

func (p outboundPermissions) subsetOf(other outboundPermissions) bool {
	for perm := range p {
		if !other[perm] {
			return false
		}
	}
	return true
}

// outboundExtending is T-6b's classification: the candidate is extending iff
// it can put in force — through the saved binding (the file) or the live
// binding (the narrow publish) — a permission that the saved or the live
// state does not already carry.
func outboundExtending(saved, live config.ChannelInstanceConfig, candidate *config.ChannelOutboundConfig) bool {
	grantsSaved, grantsLive := permissionsOf(saved, saved.Outbound), permissionsOf(live, live.Outbound)
	for _, perms := range []outboundPermissions{permissionsOf(saved, candidate), permissionsOf(live, candidate)} {
		if !perms.subsetOf(grantsSaved) || !perms.subsetOf(grantsLive) {
			return true
		}
	}
	return false
}

// canonicalInstanceBytes is the saved/live correspondence form of T-6b: the
// loader's canonical instance (config.CanonicalChannelInstance), whole,
// outbound included.
func canonicalInstanceBytes(id string, inst config.ChannelInstanceConfig) string {
	canonical, _ := config.CanonicalChannelInstance(id, inst)
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "unencodable:" + err.Error()
	}
	return string(raw)
}

// savedChannelInstance reads instance id from the saved config file (the
// state the common save step starts from). ok is false when absent.
func (a *restAPI) savedChannelInstance(id string) (config.ChannelInstanceConfig, bool, error) {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return config.ChannelInstanceConfig{}, false, fmt.Errorf("read config: %w", err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return config.ChannelInstanceConfig{}, false, fmt.Errorf("parse config: %w", err)
	}
	saved, err := decodeChannelsDoc(doc)
	if err != nil {
		return config.ChannelInstanceConfig{}, false, err
	}
	inst, ok := saved.Channels[id]
	return inst, ok, nil
}

// channelAvailability is Manager.Availability (T-7), or not_loaded with no
// manager.
func (a *restAPI) channelAvailability(id string) channels.Availability {
	if a.agentLoop != nil {
		if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
			return mgr.Availability(id)
		}
	}
	return channels.Availability{State: channels.AvailabilityNotLoaded, Reason: "not loaded"}
}

// lookUpDiscordTargets runs the T-9 look-ups for added Discord targets
// through the running adapter, outside configMu. It returns the refused
// entries (not_found / unsupported_channel_kind, with their raw indices) and
// the unavailable ids (lookup_unavailable).
func (a *restAPI) lookUpDiscordTargets(ctx context.Context, id string, added []string, rawIndex map[string]channels.TargetError) (refused []channels.TargetError, unavailable []string) {
	var resolver channels.OutboundTargetResolver
	if a.agentLoop != nil {
		if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
			if ch, ok := mgr.GetChannel(id); ok {
				resolver, _ = ch.(channels.OutboundTargetResolver)
			}
		}
	}
	for _, target := range added {
		if resolver == nil {
			// A running connection that cannot resolve targets cannot
			// validate them: fail closed, nothing saved.
			unavailable = append(unavailable, channels.BareTargetID(target))
			continue
		}
		res := resolver.ResolveOutboundTarget(ctx, target)
		switch res.Code {
		case "":
		case channels.TargetErrLookupUnavailable:
			unavailable = append(unavailable, channels.BareTargetID(target))
		default:
			refusal := rawIndex[target]
			refusal.Code = res.Code
			refusal.Reason = res.Reason
			refused = append(refused, refusal)
		}
	}
	return refused, unavailable
}

// applyOutboundChange is the one outbound writer (FR-020), shared by PUT
// /api/v1/channels/{id}/outbound and the set_channel_outbound tool:
// (1) without a lock, read the live configuration, fast-fail on
// expectedRevision, canonicalize targets and run the bounded Discord
// look-ups for added Discord targets (ctx carries the request's
// cancellation; each look-up adds its own 5-second deadline); (2) take
// configMu; (3) recheck the live revision (409 stale_revision); (3b)
// classify extending vs non-extending over the saved and live snapshots
// (409 saved_live_mismatch); (4) save through the common save step with
// GuardOutbound(dedicated = true); (5) on a live-refresh failure publish the
// guard-accepted outbound value alone and set Warning; (6) return the
// contract ChannelOutboundWriteResponse. actor names the writer for logs
// ("rest" or the calling agent id); it grants nothing.
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
	var none gen.ChannelOutboundWriteResponse
	live := a.agentLoop.GetConfig()
	inst, configured := live.Channels[id]
	if !configured {
		return none, errOutboundNotConfigured
	}
	if config.ChannelConfigRevision(live, id) != expectedRevision {
		return none, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeStaleRevision,
			"these settings changed since they were read; read them again and retry; nothing was saved")
	}
	platform := channels.InstancePlatform(id, inst)
	canonical, errs := channels.CanonicalizeTargets(platform, targets)
	if len(errs) > 0 {
		return none, invalidTargetsRejection(errs)
	}
	if len(canonical) > outboundMaxTargets {
		return none, outboundRejection(http.StatusBadRequest, gen.ChannelOutboundRejectionCodeTooManyTargets,
			fmt.Sprintf("at most %d people and groups can be listed; nothing was saved", outboundMaxTargets))
	}
	if canonical == nil {
		canonical = []string{}
	}
	added := addedTargets(platform, inst.Outbound, canonical)
	rule := channels.PlatformStartRuleFor(platform)
	if !rule.StartSupported && (channels.StartPolicy(policy) == channels.StartPolicyAgentMayStart || len(added) > 0) {
		if len(added) > 0 && rule.PersonRefusal == string(gen.ChannelOutboundRejectionCodePlatformCannotMessagePeople) {
			return none, outboundRejection(http.StatusUnprocessableEntity,
				gen.ChannelOutboundRejectionCodePlatformCannotMessagePeople,
				"this platform can only answer, not start a direct message")
		}
		return none, outboundRejection(http.StatusUnprocessableEntity, gen.ChannelOutboundRejectionCodePlatformCannotStart,
			"starting messages is not supported yet on "+rule.DisplayName)
	}
	if platform == "discord" && len(added) > 0 {
		if a.channelAvailability(id).State != channels.AvailabilityRunning {
			return none, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeConnectFirst, outboundConnectFirst)
		}
		rawIndex := firstRawEntries(platform, targets)
		refused, unavailable := a.lookUpDiscordTargets(ctx, id, added, rawIndex)
		if len(unavailable) > 0 {
			return none, lookupUnavailableRejection(unavailable)
		}
		if len(refused) > 0 {
			return none, invalidTargetsRejection(refused)
		}
	}
	if err := ctx.Err(); err != nil {
		return none, err // a cancelled request never commits later (FR-019)
	}
	desired := &config.ChannelOutboundConfig{Policy: string(policy), Targets: canonical}
	return a.commitOutboundChange(id, desired, expectedRevision, actor)
}

// commitOutboundChange is applyOutboundChange's locked phase (steps 2–6).
func (a *restAPI) commitOutboundChange(id string, desired *config.ChannelOutboundConfig, expectedRevision, actor string) (gen.ChannelOutboundWriteResponse, error) {
	var none gen.ChannelOutboundWriteResponse
	store := a.pairingOrderStore()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	live := a.agentLoop.GetConfig()
	liveInst, configured := live.Channels[id]
	if !configured {
		return none, errOutboundNotConfigured
	}
	if config.ChannelConfigRevision(live, id) != expectedRevision {
		return none, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeStaleRevision,
			"these settings changed while the targets were checked; read them again and retry; nothing was saved")
	}
	savedInst, saved, err := a.savedChannelInstance(id)
	if err != nil {
		return none, err
	}
	if !saved {
		return none, errOutboundNotConfigured
	}
	if outboundExtending(savedInst, liveInst, desired) &&
		canonicalInstanceBytes(id, savedInst) != canonicalInstanceBytes(id, liveInst) {
		return none, outboundRejection(http.StatusConflict, gen.ChannelOutboundRejectionCodeSavedLiveMismatch, outboundSavedLiveText)
	}
	a.outboundDedicatedWrite = true
	writeErr := a.orderedConfigJSONLocked(store, nil, nil, func(doc map[string]any) error {
		return setOutboundInDoc(doc, id, desired)
	})
	a.outboundDedicatedWrite = false
	var refreshErr *configRefreshError
	var warning *string
	switch {
	case writeErr == nil:
	case errors.As(writeErr, &refreshErr):
		// T-6b: the file holds the change; put the guard-accepted value —
		// what was persisted — in force alone.
		accepted, ok, readErr := a.savedChannelInstance(id)
		if readErr != nil || !ok {
			slog.Error("rest: channel outbound saved but could not be read back for the live publish",
				"channel", id, "refresh_error", writeErr, "read_error", readErr)
			return none, fmt.Errorf("outbound saved, live publish impossible: %w", writeErr)
		}
		if err := a.publishOutboundOnly(id, accepted.Outbound); err != nil {
			slog.Error("rest: channel outbound saved but the narrow live publish failed",
				"channel", id, "refresh_error", writeErr, "publish_error", err)
			return none, fmt.Errorf("outbound saved, live publish failed: %w", err)
		}
		slog.Warn("rest: channel outbound saved; other settings could not be refreshed",
			"channel", id, "actor", actor, "error", writeErr)
		text := "Saved and in force for starting messages. Other settings could not be refreshed: " +
			"the running configuration could not be reloaded. Check the server log."
		warning = &text
	default:
		return none, writeErr
	}
	current := a.agentLoop.GetConfig()
	stored := current.Channels[id].Outbound
	if stored == nil {
		stored = desired
	}
	slog.Info("rest: channel outbound saved", "channel", id, "actor", actor,
		"policy", stored.Policy, "targets", len(stored.Targets))
	resp := gen.ChannelOutboundWriteResponse{Revision: config.ChannelConfigRevision(current, id), Warning: warning}
	resp.Outbound.Policy = gen.ChannelOutboundWriteResponseOutboundPolicy(stored.Policy)
	resp.Outbound.Targets = append([]string{}, stored.Targets...)
	return resp, nil
}

// publishOutboundOnly swaps a copy of the live configuration with only
// instance id's outbound replaced into the agent loop (T-6b narrow publish).
func (a *restAPI) publishOutboundOnly(id string, outbound *config.ChannelOutboundConfig) error {
	next, err := a.agentLoop.GetConfig().Clone()
	if err != nil {
		return err
	}
	inst, ok := next.Channels[id]
	if !ok {
		return errOutboundNotConfigured
	}
	inst.Outbound = outbound.Clone()
	next.Channels[id] = inst
	a.agentLoop.SwapWrittenConfig(next)
	return nil
}

// setOutboundInDoc writes desired as channels.<id>.outbound in the raw config
// document, replacing every key spelling that binds to the field.
func setOutboundInDoc(doc map[string]any, id string, desired *config.ChannelOutboundConfig) error {
	var inst map[string]any
	for _, key := range foldedKeys(doc, "channels") {
		if section, ok := doc[key].(map[string]any); ok {
			inst, _ = section[id].(map[string]any)
		}
	}
	if inst == nil {
		return fmt.Errorf("channels.%s is not in the saved configuration", id)
	}
	for _, key := range foldedKeys(inst, "outbound") {
		delete(inst, key)
	}
	inst["outbound"] = map[string]any{"policy": desired.Policy, "targets": append([]string{}, desired.Targets...)}
	return nil
}

// addedTargets returns the canonical targets not in the stored list
// (canonicalized the same way).
func addedTargets(platform string, stored *config.ChannelOutboundConfig, canonical []string) []string {
	have := map[string]bool{}
	if stored != nil {
		for _, t := range stored.Targets {
			if c, code := channels.CanonicalizeTarget(platform, t); code == "" {
				have[c] = true
			}
		}
	}
	var added []string
	for _, t := range canonical {
		if !have[t] {
			added = append(added, t)
		}
	}
	return added
}

// firstRawEntries maps each canonical value to its first raw entry and index
// in the request (for per-target errors found after canonicalization).
func firstRawEntries(platform string, raw []string) map[string]channels.TargetError {
	out := map[string]channels.TargetError{}
	for i, entry := range raw {
		if c, code := channels.CanonicalizeTarget(platform, entry); code == "" {
			if _, seen := out[c]; !seen {
				out[c] = channels.TargetError{Raw: entry, Index: i}
			}
		}
	}
	return out
}

// getChannelOutbound handles GET /api/v1/channels/{id}/outbound (FR-021):
// one live snapshot.
func (a *restAPI) getChannelOutbound(w http.ResponseWriter, id string) {
	live := a.agentLoop.GetConfig()
	inst, configured := live.Channels[id]
	var body gen.ChannelOutboundRead
	body.Outbound.Policy = gen.ChannelOutboundReadOutboundPolicy(channels.StartPolicyReplyOnly)
	body.Outbound.Targets = []string{}
	if configured && inst.Outbound != nil {
		body.Outbound.Policy = gen.ChannelOutboundReadOutboundPolicy(inst.Outbound.Policy)
		body.Outbound.Targets = append(body.Outbound.Targets, inst.Outbound.Targets...)
	}
	body.Revision = config.ChannelConfigRevision(live, id)
	avail := a.channelAvailability(id)
	body.Availability.State = gen.ChannelOutboundReadAvailabilityState(avail.State)
	if avail.State != channels.AvailabilityRunning {
		reason := avail.Reason
		if len(reason) > outboundErrorMaxLen {
			reason = reason[:outboundErrorMaxLen]
		}
		body.Availability.Reason = &reason
	}
	body.StartSupported = channels.PlatformStartRuleFor(channels.InstancePlatform(id, inst)).StartSupported
	jsonOK(w, body)
}

// putChannelOutbound handles PUT /api/v1/channels/{id}/outbound (FR-022);
// the route wraps it in requireAdminAuthz.
func (a *restAPI) putChannelOutbound(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.ChannelOutboundUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidateStrict(w, r, "ChannelOutboundUpdateRequest", &req, validateEnabled) {
		return
	}
	switch {
	case req.Policy != gen.ChannelOutboundUpdateRequestPolicyReplyOnly &&
		req.Policy != gen.ChannelOutboundUpdateRequestPolicyAgentMayStart:
		jsonErr(w, http.StatusBadRequest, "policy must be reply_only or agent_may_start")
		return
	case strings.TrimSpace(req.ExpectedRevision) == "":
		jsonErr(w, http.StatusBadRequest, "expected_revision is required")
		return
	case req.Targets == nil:
		jsonErr(w, http.StatusBadRequest, "targets is required")
		return
	case len(req.Targets) > outboundMaxRawTargets:
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("targets: at most %d entries", outboundMaxRawTargets))
		return
	}
	for _, t := range req.Targets {
		if len([]rune(t)) > outboundMaxRawTargetLen {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("targets: an entry is longer than %d characters", outboundMaxRawTargetLen))
			return
		}
	}
	resp, err := a.applyOutboundChange(r.Context(), id, req.Policy, req.Targets, req.ExpectedRevision, "rest")
	var rejection *outboundRejectionError
	switch {
	case err == nil:
		jsonOK(w, resp)
	case errors.As(err, &rejection):
		writeJSON(w, rejection.Status, rejection.Body)
	case errors.Is(err, errOutboundNotConfigured):
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q is not configured", id))
	default:
		slog.Error("rest: save channel outbound", "channel", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, outboundSaveFailedText)
	}
}
