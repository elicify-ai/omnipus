// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// copilotProviderID is the catalog id of the GitHub Copilot subprocess
// provider (ADR-068 §2.1, spec S68 X-14).
const copilotProviderID = "github-copilot"

// EventProviderSignInStatusChecked is the audit event emitted once per Copilot
// sign-in probe (M5). Registered in pkg/audit.IsValidEventName so the first
// emission never trips the unknown-event warn-once.
const EventProviderSignInStatusChecked = "provider.sign_in_status_checked"

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

// copilotProbeCacheTTL is how long a COST-BEARING Copilot probe result is
// reused (C2). Only signed_in / expired are cached: those are the outcomes
// that spend a premium request, and they are stable — an operator who is
// signed in stays signed in. not_signed_in and cli_missing are never cached,
// so the one transition an operator actually waits on (run `copilot login`,
// click Check sign-in) is always answered by a fresh probe, and that probe
// costs nothing because there is no session to bill against.
//
// The only staleness this introduces is the reverse transition (signed in →
// signed out), which no UI flow waits on.
const copilotProbeCacheTTL = 5 * time.Minute

// copilotProbeRetryAfterSeconds is the Retry-After a caller gets when another
// probe is already running. Bounded by copilotSignInCheckTimeout above.
const copilotProbeRetryAfterSeconds = 5

// copilotProbeGuard bounds the cost of the Copilot sign-in probe (C2).
//
// The probe execs the vendor CLI with `--allow-all-tools --no-ask-user` and a
// 60s ceiling, and CopilotSignIn's own doc comment states it "costs one
// premium request when the operator is signed in ... and MUST NOT be put on a
// poll or a page-load path". It nonetheless sat on an FR-050 pre-auth route
// behind signInStatusLimiter's 60/min PER-IP ceiling — a limit chosen to
// accommodate polling. An unauthenticated caller during onboarding could
// therefore loop it for 3,600 premium requests/hour billed to the operator,
// multiplying linearly across source IPs, and leave up to ~60 concurrent
// `copilot` child processes alive if the CLI hung to its timeout.
//
// Two controls, each closing one half:
//
//   - inFlight — at most ONE probe runs at a time, process-wide. A caller that
//     arrives while one is running gets 429, not a queued goroutine holding a
//     request open for 60s. This caps child processes at one regardless of how
//     many IPs attack.
//   - cached/cachedAt — collapses repeated probing of a signed-in operator
//     onto one premium request per copilotProbeCacheTTL, so the billing cost
//     is bounded by TIME rather than by request rate, and is therefore
//     independent of the number of source IPs.
//
// It is a restAPI FIELD, not a package var, so the bound is per gateway
// instance. In production there is exactly one restAPI, which makes it
// process-wide; in tests each case builds its own restAPI and so gets a clean
// guard, with no cross-test cache bleed and no global budget to exhaust.
// The zero value is a ready, empty guard.
type copilotProbeGuard struct {
	mu       sync.Mutex
	inFlight bool
	cached   *gen.SignInStatus
	cachedAt time.Time
}

// hit returns a cached probe result when one is live, and whether there was one.
func (g *copilotProbeGuard) hit() (gen.SignInStatus, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cached == nil || time.Since(g.cachedAt) >= copilotProbeCacheTTL {
		return gen.SignInStatus{}, false
	}
	return *g.cached, true
}

// acquire claims the single probe slot, reporting false when it is taken.
func (g *copilotProbeGuard) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight {
		return false
	}
	g.inFlight = true
	return true
}

// release returns the probe slot.
func (g *copilotProbeGuard) release() {
	g.mu.Lock()
	g.inFlight = false
	g.mu.Unlock()
}

// store caches a probe result if and only if it is one of the cost-bearing
// outcomes. Caching not_signed_in would make the operator's post-login Check
// sign-in click answer stale, and would save nothing: that outcome spends no
// premium request.
func (g *copilotProbeGuard) store(st gen.SignInStatus) {
	if st.State != gen.SignInStatusStateSignedIn && st.State != gen.SignInStatusStateExpired {
		return
	}
	g.mu.Lock()
	cached := st
	g.cached = &cached
	g.cachedAt = time.Now()
	g.mu.Unlock()
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
//
// C2: "must never be put behind a poll" is now ENFORCED here rather than
// merely asserted in prose — a. by the copilotProbeGuard cache, which makes
// repeat probing of a signed-in operator free, and b. by its single-slot
// concurrency claim. Note the route is also 401 for an anonymous caller once
// onboarding completes (FR-050, and fail-closed on unknown state after M3),
// so the residual anonymous window is an install mid-onboarding.
func (a *restAPI) handleCopilotSignInStatus(w http.ResponseWriter, r *http.Request) {
	if st, ok := a.copilotProbe.hit(); ok {
		a.auditCopilotProbe(r, st, true)
		jsonOK(w, st)
		return
	}
	if !a.copilotProbe.acquire() {
		w.Header().Set("Retry-After", strconv.Itoa(copilotProbeRetryAfterSeconds))
		jsonErr(w, http.StatusTooManyRequests,
			fmt.Sprintf("a Copilot sign-in check is already running, retry after %d seconds",
				copilotProbeRetryAfterSeconds))
		return
	}
	defer a.copilotProbe.release()
	// Re-check under the slot: a probe that finished between hit() and
	// acquire() has a fresh answer, and spending a second premium request to
	// re-derive it is exactly what this guard exists to prevent.
	if st, ok := a.copilotProbe.hit(); ok {
		a.auditCopilotProbe(r, st, true)
		jsonOK(w, st)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), copilotSignInCheckTimeout)
	defer cancel()

	res := providers_pkg.CopilotSignIn(ctx, "", a.copilotCheckWorkspace())
	status := copilotSignInStatusResponse(res)
	a.copilotProbe.store(status)
	a.auditCopilotProbe(r, status, false)
	jsonOK(w, status)
}

// auditCopilotProbe records one Copilot sign-in check in the audit log (M5).
//
// The FR-050 sign-in routes are reachable pre-auth, and this one drives
// outbound vendor traffic that the operator PAYS for, so the abuse C2
// describes has to be visible after the fact. The entry carries the actor
// (empty for an anonymous pre-auth caller — audit.Entry.User's documented
// default, never guessed) and the source_ip, matching the shape the CSRF
// mismatch reporter already uses (gateway.go). `cached` distinguishes the
// calls that actually spent a premium request from the ones the C2 guard
// absorbed, so the log answers "what did this cost?" and not just "who
// called?".
//
// Best-effort: a write failure is logged and the request proceeds. An audit
// write must never fail a request.
func (a *restAPI) auditCopilotProbe(r *http.Request, status gen.SignInStatus, cached bool) {
	if a.auditor == nil {
		return
	}
	entry := &audit.Entry{
		Event:    EventProviderSignInStatusChecked,
		Decision: audit.DecisionAllow,
		User:     auditActor(r),
		Details: map[string]any{
			"provider":  copilotProviderID,
			"source_ip": a.clientIPWithLiveFallback(r),
			"state":     string(status.State),
			"cached":    cached,
		},
	}
	if err := a.auditor.Log(entry); err != nil {
		slog.Warn("audit write failed", "event", EventProviderSignInStatusChecked, "error", err)
	}
}

// auditActor returns the authenticated gateway principal's username for an
// audit entry, or "" when the request is anonymous — which is exactly what an
// FR-050 pre-auth call is. Empty is audit.Entry.User's documented default for
// an unauthenticated path and is never filled with a guess.
func auditActor(r *http.Request) string {
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && user != nil {
		return user.Username
	}
	return ""
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
