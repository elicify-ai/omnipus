// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Configure's failure codes and apply outcome (channels-spec-r8 T-6, T-6b,
// FR-050, FR-052, FR-056, FR-057).

import (
	"errors"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Normative texts of the ChannelConfigureError codes (T-6b, FR-056/FR-057).
const (
	configureCouldNotSaveText       = "could not save config"
	configureSavedNotRefreshedText  = "settings saved, but the running configuration could not be refreshed; the connection was not restarted"
	configureCredentialSavedNotText = "The credential was saved, but the configuration change was not: nothing else was applied. " +
		"The connection keeps its previous token until it next restarts; whether that restart picks the new value up " +
		"depends on the credential reference the saved configuration holds (see below). Save again to retry — " +
		"a successful re-save is the general recovery."
	// configureApplyNote is T-6's apply_note: an attempted, never a
	// completed, restart (FR-052, R2-MAJ-006).
	configureApplyNote = "A connection restart was attempted; other connections and scheduled services may have paused briefly while it ran."
	// configureReloadFailedReason is apply_reason for a failed reload: the
	// reload error itself can carry sensitive text, so a fixed phrase.
	configureReloadFailedReason = "the configuration reload failed; details are in the server log"
	// configureDisposalFailedReason is apply_reason for a failed reload that
	// left this connection's previous adapter unfinished (S-52 teardown row).
	configureDisposalFailedReason = "the previous connection did not finish shutting down; the next save retries it"
)

// configureStoreCredential is Configure's per-secret credential-store step.
// Test seam (S-47 shape 2, R3-MAJ-004): a test replaces it to fail exactly
// the N-th store of a multi-secret request. Never reassign it outside tests.
var configureStoreCredential = func(a *restAPI, refName, secret string) error {
	_, err := a.storeCredential(refName, secret)
	return err
}

// writeConfigureFailure writes a coded ChannelConfigureError 500 (FR-056).
// detail is a short fixed phrase; err goes to the server log only.
func writeConfigureFailure(w http.ResponseWriter, channelID string, code gen.ChannelConfigureErrorCode, detail string, err error) {
	slog.Error("rest: configure channel failed", "channel", channelID, "code", string(code), "detail", detail, "error", err)
	text := configureCouldNotSaveText
	switch code {
	case gen.ChannelConfigureErrorCodeSavedNotRefreshed:
		text = configureSavedNotRefreshedText
	case gen.ChannelConfigureErrorCodeCredentialSavedNotSaved:
		text = configureCredentialSavedNotText
	}
	body := gen.ChannelConfigureError{Code: code, Error: text}
	if detail != "" {
		body.Detail = &detail
	}
	writeJSON(w, http.StatusInternalServerError, body)
}

// configureWriteFailureCode classifies a failed configuration write by what
// the request durably changed (FR-056): file written but not refreshed →
// saved_not_refreshed; else credentials stored → credential_saved_not_saved;
// else could_not_save.
func configureWriteFailureCode(err error, storedCredentials int) gen.ChannelConfigureErrorCode {
	var refreshErr *configRefreshError
	switch {
	case errors.As(err, &refreshErr):
		return gen.ChannelConfigureErrorCodeSavedNotRefreshed
	case storedCredentials > 0:
		return gen.ChannelConfigureErrorCodeCredentialSavedNotSaved
	default:
		return gen.ChannelConfigureErrorCodeCouldNotSave
	}
}

// markCredentialsPending sets the pending-application marker of channelID
// once per stored credential (FR-050 leg 5 (i)). Nil manager: nothing runs
// that could apply it, so there is nothing to mark.
func (a *restAPI) markCredentialsPending(channelID string, count int) {
	if count == 0 || a.agentLoop == nil {
		return
	}
	mgr := a.agentLoop.GetChannelManager()
	if mgr == nil {
		return
	}
	for range count {
		mgr.MarkCredentialPending(channelID)
	}
}

// configureApplyResult is the apply part of a 200 Configure response (T-6).
type configureApplyResult struct {
	outcome gen.ChannelConfigureResponseApplyOutcome
	reason  string
}

// applyConfiguredChannel decides and performs the restart after a
// successful Configure write (FR-050) and reports the true outcome (T-6).
// before is the live configuration before the write; storedCredential says
// whether this request stored or replaced any credential.
func (a *restAPI) applyConfiguredChannel(channelID string, before *config.Config, storedCredential bool) configureApplyResult {
	after := a.agentLoop.GetConfig()
	inst, exists := after.Channels[channelID]
	if !exists || !inst.Enabled {
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeNoReloadNeeded}
	}
	mgr := a.agentLoop.GetChannelManager()
	revisionChanged := before == nil || channels.ChannelRevision(before, channelID) != channels.ChannelRevision(after, channelID)
	leg := channels.LegCurrent
	if mgr != nil {
		leg = mgr.ApplicationCurrent(channelID, after)
	}
	if !revisionChanged && !storedCredential && leg == channels.LegCurrent {
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeNoReloadNeeded}
	}
	slog.Info("rest: configure restarts channel", "channel", channelID,
		"revision_changed", revisionChanged, "credential_stored", storedCredential, "not_current", string(leg))
	if mgr != nil {
		mgr.RequestRestart(channelID, "", after)
	}
	confirmed, err := a.triggerReloadAndWaitOutcome()
	if mgr == nil {
		// No channel manager is wired (unit-test environments): nothing can
		// report this connection as loaded.
		mgr = a.agentLoop.GetChannelManager()
	}
	switch {
	case err != nil:
		slog.Error("rest: configure reload failed", "channel", channelID, "error", err)
		reason := configureReloadFailedReason
		if mgr != nil && mgr.ApplicationCurrent(channelID, a.agentLoop.GetConfig()) == channels.LegDisposalPending {
			reason = configureDisposalFailedReason
		}
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeFailed, reason: reason}
	case !confirmed:
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeUnconfirmed}
	case mgr == nil:
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeAppliedNotRunning, reason: "not loaded"}
	}
	avail := mgr.Availability(channelID)
	if avail.State == channels.AvailabilityRunning {
		return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeApplied}
	}
	return configureApplyResult{outcome: gen.ChannelConfigureResponseApplyOutcomeAppliedNotRunning, reason: avail.Reason}
}

// configureResponse builds the 200 body: the redacted configuration plus
// apply_outcome, apply_reason where T-6 says, and apply_note whenever a
// reload was attempted (FR-052).
func configureResponse(redacted map[string]any, res configureApplyResult) gen.ChannelConfigureResponse {
	out := gen.ChannelConfigureResponse{ApplyOutcome: res.outcome, AdditionalProperties: map[string]any{}}
	for k, v := range redacted {
		switch k {
		case "apply_outcome", "apply_reason", "apply_note":
			continue // reserved response fields, never a stored config key
		}
		out.AdditionalProperties[k] = v
	}
	if res.outcome != gen.ChannelConfigureResponseApplyOutcomeNoReloadNeeded {
		note := configureApplyNote
		out.ApplyNote = &note
	}
	if res.reason != "" {
		reason := res.reason
		out.ApplyReason = &reason
	}
	return out
}

// rejectedSendersReporter is the adapter read accessor of FR-061
// (channels.BaseChannel.RejectedSenders).
type rejectedSendersReporter interface {
	RejectedSenders() (count int, recent []string)
}

// applyRejectedSendersOverlay adds rejected_senders to every entry whose
// running adapter has ignored at least one sender (FR-061).
func applyRejectedSendersOverlay(channelList []gen.ChannelEntry, getChannel func(string) (channels.Channel, bool)) {
	for i := range channelList {
		ch, ok := getChannel(channelList[i].Id)
		if !ok {
			continue
		}
		reporter, ok := ch.(rejectedSendersReporter)
		if !ok {
			continue
		}
		count, recent := reporter.RejectedSenders()
		if count < 1 {
			continue
		}
		channelList[i].RejectedSenders = &struct { // not-wire-format: the generated gen.ChannelEntry.RejectedSenders anonymous field type
			Count  int      `json:"count"`
			Recent []string `json:"recent"`
		}{Count: count, Recent: recent}
	}
}
