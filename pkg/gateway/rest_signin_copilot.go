// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// copilotProviderID is the catalog id of the GitHub Copilot subprocess
// provider (ADR-068 §2.1, spec S68 X-14).
const copilotProviderID = "github-copilot"

// copilotSignInCommand is the command the operator runs themselves. Omnipus
// never performs or stores the Copilot login (FR-008); it only tells the
// operator what to type. Verified against @github/copilot 1.0.80: `login` is a
// real subcommand ("Authenticate with Copilot via OAuth").
const copilotSignInCommand = "copilot login"

// copilotSignInInstructions is the guidance shown beside the command, ending
// with the prompt to click Check sign-in (FR-008).
const copilotSignInInstructions = "Run `copilot login` in a terminal on this machine and complete the GitHub " +
	"sign-in. The Copilot CLI keeps the credential — Omnipus never sees or stores it. Then click Check sign-in."

// copilotSignInCheckTimeout bounds the sign-in check. The check is one run of
// the vendor CLI, so it is bounded generously but never unbounded.
const copilotSignInCheckTimeout = 60 * time.Second

// handleCopilotSignInStart answers POST /api/v1/providers/github-copilot/sign-in
// with the FR-008 `cli_login` variant: the vendor CLI's own login command.
func (a *restAPI) handleCopilotSignInStart(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, gen.SignInStartResponseCliLogin{
		Method:       gen.CliLogin,
		Command:      copilotSignInCommand,
		Instructions: copilotSignInInstructions,
	})
}

// handleCopilotSignInStatus answers GET
// /api/v1/providers/github-copilot/sign-in/status with the FR-009 state of the
// Copilot CLI's own login.
//
// Cost note: the shipped Copilot CLI offers no auth-status command and keeps
// its token in the OS credential store, so the only sound oracle is one minimal
// prompt run — which spends one premium request when the operator IS signed in
// (see providers.CopilotSignIn). This handler is therefore the operator's
// explicit *Check sign-in* action and must never be put behind a poll.
func (a *restAPI) handleCopilotSignInStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), copilotSignInCheckTimeout)
	defer cancel()

	res := providers_pkg.CopilotSignIn(ctx, "", a.copilotCheckWorkspace())
	jsonOK(w, copilotSignInStatusResponse(res))
}

// copilotCheckWorkspace is the directory the sign-in check runs in — the
// Omnipus home, never the gateway's own working directory.
func (a *restAPI) copilotCheckWorkspace() string {
	if a.homePath != "" {
		return a.homePath
	}
	return ""
}

// copilotSignInStatusResponse maps the CLI's state onto the FR-009 wire enum.
//
// `cli_missing` has no wire state of its own: the enum is
// not_signed_in|pending|signed_in|expired, and "the CLI is not installed" is a
// fact about the machine, reported on the provider ROW as `disconnected` with
// the operator hint (see listProviders). On this endpoint it degrades to
// not_signed_in, which is exactly what SignInStatus.yaml prescribes for a login
// that cannot be read.
func copilotSignInStatusResponse(res providers_pkg.CopilotSignInResult) gen.SignInStatus {
	status := gen.SignInStatus{State: gen.SignInStatusStateNotSignedIn}

	switch res.State {
	case providers_pkg.CopilotSignedIn:
		status.State = gen.SignInStatusStateSignedIn
		if res.AccountLabel != "" {
			label := res.AccountLabel
			status.AccountLabel = &label
		}
		if res.ExpiresAt != nil {
			expires := *res.ExpiresAt
			status.ExpiresAt = &expires
		}
	case providers_pkg.CopilotSignInExpired:
		status.State = gen.SignInStatusStateExpired
	case providers_pkg.CopilotCLIMissing:
		slog.Warn("copilot sign-in status requested but the CLI is not installed",
			"provider", copilotProviderID, "hint", providers_pkg.CopilotCLIMissingHint)
	case providers_pkg.CopilotNotSignedIn:
	}

	return status
}

// copilotRowHint returns the operator hint for a github-copilot provider row
// when the vendor CLI is missing from this machine (FR-009), and "" otherwise.
// The row's sign-in STATE is deliberately not computed here: that check costs a
// premium request and belongs to the explicit Check sign-in action.
func copilotRowHint(providerID string) string {
	if providerID != copilotProviderID {
		return ""
	}
	if providers_pkg.CopilotCLIAvailable("") {
		return ""
	}
	return providers_pkg.CopilotCLIMissingHint
}
