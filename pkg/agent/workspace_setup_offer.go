// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// Inbound metadata keys of the workspace-setup offer (omnipus-system-agent
// FR-047, K9). workspaceSetupOfferMetadataKey marks the turn as the offer;
// setupTargetWorkspaceMetadataKey carries the new workspace as target data.
// The turn's own workspace stays the main's ("workspace_id"), never the
// target (ADR-20261011 OSA-9.1).
const (
	workspaceSetupOfferMetadataKey  = "workspace_setup_offer"
	setupTargetWorkspaceMetadataKey = "setup_target_workspace_id"
)

// WorkspaceSetupOffer is the target data of one setup offer.
type WorkspaceSetupOffer struct {
	// MainSessionID is Omnipus's main session id.
	MainSessionID string
	// MainWorkspaceID is the main's own workspace (the default workspace).
	MainWorkspaceID string
	// TargetWorkspaceID and TargetName name the newly created workspace.
	TargetWorkspaceID string
	TargetName        string
}

// PostWorkspaceSetupOffer publishes the turn in which Omnipus offers to set up
// a team for a newly created workspace, into his existing main
// (omnipus-system-agent FR-047, K9; ADR-20261011 OSA-9 W8). The caller has
// already recorded the offer in the main and claimed the workspace's
// setup_pending flag.
//
// It is a NON-operator, server-generated owner wake, the same class as the
// request reminder: it carries the owner-wake marker, so
// reviveInboundIsHumanTurn treats it as no person's turn and ordinary
// admission can start the next round of an idle chat but never continue a
// stopped one. It is never UserInitiated and never OperatorPrompt.
// scripts/check-operator-prompt-sites.sh counts this publish site.
func (al *AgentLoop) PostWorkspaceSetupOffer(ctx context.Context, offer WorkspaceSetupOffer) error {
	if al == nil || al.bus == nil {
		return errors.New("workspace setup offer: no message bus")
	}
	if strings.TrimSpace(offer.MainSessionID) == "" || strings.TrimSpace(offer.MainWorkspaceID) == "" ||
		strings.TrimSpace(offer.TargetWorkspaceID) == "" {
		return errors.New("workspace setup offer: the main session, its workspace and the target workspace are all required")
	}
	return al.bus.PublishInbound(ctx, workspaceSetupOfferInbound(offer))
}

// workspaceSetupOfferInbound is the one inbound message PostWorkspaceSetupOffer
// publishes, built in one place so the producer and its tests share its exact
// shape.
func workspaceSetupOfferInbound(offer WorkspaceSetupOffer) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:   "webchat",
		ChatID:    "workspace-setup:" + offer.TargetWorkspaceID,
		Sender:    bus.SenderInfo{CanonicalID: "workspace-setup:" + offer.TargetWorkspaceID},
		Content:   workspaceSetupOfferText(offer.TargetWorkspaceID, offer.TargetName),
		SessionID: offer.MainSessionID,
		Metadata: map[string]string{
			"agent_id":                      string(coreagent.IDOmnipus),
			"workspace_id":                  offer.MainWorkspaceID,
			workspaceSetupOfferMetadataKey:  "1",
			setupTargetWorkspaceMetadataKey: offer.TargetWorkspaceID,
			ownerWakeMetadataKey:            "1",
		},
	}
}

// workspaceSetupOfferText is a PLACEHOLDER instruction (WU-10 decision Q2 A);
// the prompt seat replaces its wording. The workspace name is user input, so
// it is quoted, never spliced in as instruction text.
func workspaceSetupOfferText(workspaceID, name string) string {
	return fmt.Sprintf("The user just created the workspace %q (id %s); its team is empty. "+
		"Offer to set up its team by asking: Set up a team for %q? The user may skip. "+
		"If they accept, run the team setup with %s as the target workspace.",
		name, workspaceID, name, workspaceID)
}
