// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// workspace_setup_offer.go: the server-initiated workspace-setup offer
// (omnipus-system-agent FR-047, K9; ADR-20261011 OSA-9 W8).
//
// A workspace created with an empty team starts with setup_pending=true.
// Once the create response is out, the server claims that flag exactly once
// and posts the offer into Omnipus's existing main chat, with the new
// workspace as target data. The server is the only producer: every client
// kickoff frame is refused at the WebSocket frame seam
// (websocket.go::handleMessageFrame).

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/google/uuid"
)

// clientKickoffRefusal is the error-frame text for a client frame that carries
// metadata.workspace_setup_kickoff=true. The server posts the setup offer
// itself when a workspace is created.
const clientKickoffRefusal = "workspace setup is started by the server when a workspace is created; " +
	"a client kickoff is refused"

// workspaceSetupOfferTimeout bounds the bus publish of one setup offer, the
// same bound every other server-side publish uses.
const workspaceSetupOfferTimeout = 5 * time.Second

// kickoffOutcome is the result of consumeWorkspaceSetupKickoff.
type kickoffOutcome int

const (
	// kickoffConsumed means SetupPending was true and has now been cleared and
	// persisted — the caller posts the offer.
	kickoffConsumed kickoffOutcome = iota
	// kickoffDuplicate means SetupPending was already false (the offer was
	// already claimed) — the caller posts nothing.
	kickoffDuplicate
	// kickoffFailed means the workspace file could not be read, or the cleared
	// flag could not be persisted. SetupPending is left untouched on disk.
	kickoffFailed
)

// consumeWorkspaceSetupKickoff clears workspaceID's SetupPending flag exactly
// once, under the workspace's own per-ID lock (workspace.LockID), so a second
// claim and every other writer of the same workspace file serialize against
// it: a racing REST PUT/DELETE cannot resurrect a just-cleared flag with a
// stale write-back, and a delete-then-claim race fails with
// errWorkspaceNotFound (mapped to kickoffFailed) instead of recreating the
// file as a ghost.
//
// On kickoffConsumed it also returns the workspace's Name and Description,
// read under the same lock from the record that was just consumed.
func consumeWorkspaceSetupKickoff(home, workspaceID string) (outcome kickoffOutcome, name string, description string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(home, workspaceID)
	if err != nil {
		logsafeWarn("workspace setup offer: could not read workspace file",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	if !w.SetupPending {
		logsafeWarn("workspace setup offer: setup was already claimed — not posting again",
			"workspace_id", workspaceID)
		return kickoffDuplicate, "", ""
	}
	w.SetupPending = false
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(home, w); err != nil {
		logsafeWarn("workspace setup offer: could not persist cleared setup_pending",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	return kickoffConsumed, w.Name, w.Description
}

// restoreWorkspaceSetupPending re-sets workspaceID's SetupPending flag to true
// after a successful claim was followed by a failure to post the offer, so the
// workspace still reads as pending and its Team tab keeps offering setup. It
// takes the same per-workspace lock as consumeWorkspaceSetupKickoff.
//
// A read or write failure here is logged at ERROR: the caller has already
// failed, and there is no further fallback. The workspace then reads as not
// pending although no offer was posted; the Team tab's setup button still
// works.
func restoreWorkspaceSetupPending(home, workspaceID string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(home, workspaceID)
	if err != nil {
		logsafeError("workspace setup offer: could not read workspace file to restore setup_pending",
			"workspace_id", workspaceID, "error", err)
		return
	}
	w.SetupPending = true
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(home, w); err != nil {
		logsafeError("workspace setup offer: could not restore setup_pending",
			"workspace_id", workspaceID, "error", err)
	}
}

// workspaceSetupOfferPoster posts the setup offer for one newly created
// workspace into Omnipus's main. restAPI.setupOfferPoster holds it; nil means
// restAPISetupOfferPoster, the real one.
type workspaceSetupOfferPoster interface {
	PostWorkspaceSetupOffer(ctx context.Context, workspaceID, workspaceName string) error
}

// offerPoster returns the configured poster, or the real one.
func (a *restAPI) offerPoster() workspaceSetupOfferPoster {
	if a.setupOfferPoster != nil {
		return a.setupOfferPoster
	}
	return restAPISetupOfferPoster{a: a}
}

// postWorkspaceSetupOffer is the one producer of the setup offer (FR-047). It
// claims workspaceID's setup_pending flag exactly once and posts the offer
// into Omnipus's main. A duplicate claim posts nothing. Any failure after the
// claim restores the flag. user is the account that created the workspace,
// recorded in the audit entry.
func (a *restAPI) postWorkspaceSetupOffer(workspaceID, user string) {
	outcome, name, _ := consumeWorkspaceSetupKickoff(a.homePath, workspaceID)
	switch outcome {
	case kickoffConsumed:
	case kickoffDuplicate:
		return
	default:
		logsafeError("workspace setup offer: setup_pending could not be claimed; no offer was posted and the workspace stays pending",
			"workspace_id", workspaceID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workspaceSetupOfferTimeout)
	defer cancel()
	if err := a.offerPoster().PostWorkspaceSetupOffer(ctx, workspaceID, name); err != nil {
		logsafeError("workspace setup offer: the offer could not be posted into Omnipus's main; setup_pending is restored",
			"workspace_id", workspaceID, "error", err)
		restoreWorkspaceSetupPending(a.homePath, workspaceID)
		return
	}
	if a.auditor == nil {
		return
	}
	details := map[string]any{"workspace_id": workspaceID}
	mainID, mainErr := omnipusMainSessionID(a.homePath)
	if mainErr != nil {
		details["main_session_error"] = mainErr.Error()
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:     "workspace.setup_consumed",
		Decision:  audit.DecisionAllow,
		AgentID:   string(coreagent.IDOmnipus),
		SessionID: mainID,
		User:      user,
		Details:   details,
	}); err != nil {
		logsafeWarn("workspace setup offer: audit write failed",
			"workspace_id", workspaceID, "session_id", mainID, "error", err)
	}
}

// omnipusMainSessionID computes Omnipus's main id: the default workspace's
// main for Omnipus.
func omnipusMainSessionID(home string) (string, error) {
	defaultID, err := workspace.ResolveDefaultID(home)
	if err != nil {
		return "", fmt.Errorf("resolve the default workspace: %w", err)
	}
	return session.MainSessionID(defaultID, string(coreagent.IDOmnipus))
}

// restAPISetupOfferPoster is the real workspaceSetupOfferPoster. It records
// the offer in Omnipus's main and publishes the turn that makes it. Omnipus's
// main is the default workspace's main for Omnipus — the one computed id,
// get-or-created, never a second session (FR-005). The main's own workspace
// stays the default one; the new workspace is target data only (OSA-9.1).
type restAPISetupOfferPoster struct{ a *restAPI }

func (p restAPISetupOfferPoster) PostWorkspaceSetupOffer(ctx context.Context, workspaceID, workspaceName string) error {
	a := p.a
	defaultID, err := workspace.ResolveDefaultID(a.homePath)
	if err != nil {
		return fmt.Errorf("resolve the default workspace: %w", err)
	}
	omnipusID := string(coreagent.IDOmnipus)
	defaultWS, found, err := loadWorkspaceForMain(a.homePath, defaultID)
	if err != nil {
		return fmt.Errorf("read the default workspace: %w", err)
	}
	if !found {
		return errors.New("the default workspace does not exist")
	}
	if !mainSessionPairEligible(a.agentLoop.GetConfig(), defaultWS, omnipusID) {
		return errors.New("omnipus is not an eligible member of the default workspace")
	}
	mainID, err := a.ensureMainSession(defaultID, omnipusID, "")
	if err != nil {
		return fmt.Errorf("prepare Omnipus's main: %w", err)
	}
	store := a.agentLoop.GetSessionStore()
	if store == nil {
		return errNoSessionStore
	}
	if err := store.AppendTranscript(mainID, workspaceSetupOfferEntry(
		fmt.Sprintf("Workspace setup offered for %s.", workspaceName))); err != nil {
		return fmt.Errorf("record the setup offer in Omnipus's main: %w", err)
	}
	if err := a.agentLoop.PostWorkspaceSetupOffer(ctx, agent.WorkspaceSetupOffer{
		MainSessionID:     mainID,
		MainWorkspaceID:   defaultID,
		TargetWorkspaceID: workspaceID,
		TargetName:        workspaceName,
	}); err != nil {
		// The offer entry is already in the chat: say plainly that it did
		// not start, so the chat never shows an offer that never ran.
		note := workspaceSetupOfferEntry(fmt.Sprintf(
			"The setup offer for %s could not be started. Open its Team tab to set up its team.", workspaceName))
		if noteErr := store.AppendTranscript(mainID, note); noteErr != nil {
			slog.Error("workspace setup offer: could not record that the offer did not start",
				"workspace_id", workspaceID, "session_id", mainID, "error", noteErr)
		}
		return fmt.Errorf("publish the setup offer: %w", err)
	}
	return nil
}

// workspaceSetupOfferEntry is a system-role entry in Omnipus's main, attributed
// to Omnipus so replay places it in his conversation.
func workspaceSetupOfferEntry(content string) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:        uuid.New().String(),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		AgentID:   string(coreagent.IDOmnipus),
		Content:   content,
		Timestamp: time.Now().UTC(),
	}
}
