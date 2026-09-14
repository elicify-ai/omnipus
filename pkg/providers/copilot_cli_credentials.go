// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CopilotSignInState is the sign-in state of the GitHub Copilot CLI's own
// login, as ADR-068 FR-009 defines it, plus the one state that is about this
// machine rather than the account: the CLI is not installed at all.
type CopilotSignInState string

const (
	// CopilotCLIMissing — the `copilot` binary is not on this machine. The
	// gateway reports the provider row as `disconnected` with an operator hint;
	// it is never a sign-in state on the wire.
	CopilotCLIMissing CopilotSignInState = "cli_missing"
	// CopilotNotSignedIn — the CLI is installed and finds no credential.
	CopilotNotSignedIn CopilotSignInState = "not_signed_in"
	// CopilotSignedIn — the CLI holds a usable credential.
	CopilotSignedIn CopilotSignInState = "signed_in"
	// CopilotSignInExpired — the CLI has a credential and the vendor rejected
	// it (expired, revoked or otherwise invalid).
	CopilotSignInExpired CopilotSignInState = "expired"
)

// CopilotCLIMissingHint is the operator-facing reason shown on the provider row
// when the Copilot CLI is absent (ADR-068 FR-009).
const CopilotCLIMissingHint = "`copilot` not found on this machine"

// CopilotSignInResult is what a sign-in check learned about the Copilot CLI's
// login.
type CopilotSignInResult struct {
	State CopilotSignInState
	// AccountLabel is the GitHub login when the CLI reports one. The shipped
	// CLI reports no account in its scriptable output, so this is normally
	// empty — FR-009 allows `account_label` to be absent.
	AccountLabel string
	// ExpiresAt is the credential's expiry when the CLI reports one. The
	// Copilot CLI never does — Omnipus does not hold or decode its token — so
	// this is nil in practice.
	ExpiresAt *time.Time
	// Detail is the CLI's own message for a failed check, for logging. It is
	// never returned to the operator verbatim.
	Detail string
}

// copilotSignInProbePrompt is the smallest prompt that still forces the CLI
// through its authentication path. See CopilotSignIn for the cost note.
const copilotSignInProbePrompt = "Reply with only the word: ok"

// --- verified CLI behaviour, @github/copilot 1.0.80 (2026-08-24) -------------
//
// The task flagged the Copilot CLI's auth surface as UNVERIFIED. Confirmed
// against the published 1.0.80 binary (`copilot --help`, `copilot login
// --help`, `copilot help environment`) rather than guessed:
//
//   - There is NO auth-status command. The subcommand set is exactly
//     completion / help / init / login / mcp / plugin / plugins / skill /
//     update / version, and `login` has only --device-code, --web-flow and
//     --host. There is no `logout` either.
//   - There is NO readable login state file in the general case: `copilot login
//     --help` states the token "will be stored securely in the system
//     credential store", falling back to a plain-text file under ~/.copilot
//     only when no credential store is available. Reading $COPILOT_HOME would
//     therefore report "not signed in" for most signed-in operators.
//   - Argument parsing happens BEFORE the authentication check (an unknown flag
//     fails with `unknown option`, a known flag set reaches the auth error), so
//     a prompt run is a sound auth oracle.
//   - With no credential, `copilot -p …` exits 1, prints nothing on stdout, and
//     writes `Error: No authentication information found.` to stderr.
//
// So the only sound status oracle the shipped CLI offers is one minimal prompt
// run. That costs one premium request WHEN THE OPERATOR IS SIGNED IN, which is
// why this is wired to the explicit *Check sign-in* action and MUST NOT be put
// on a poll or a page-load path.
//
// Still unverified, because it needs a live Copilot subscription: the exact
// stderr wording of an EXPIRED or revoked session. So the patterns below accept
// only PHRASE-LEVEL evidence — a sign-in noun next to "expired" or "revoked",
// GitHub's own "Bad credentials", or 401 in an HTTP-status form — and an
// unrecognised failure degrades to `not_signed_in` with a warning (the state
// SignInStatus.yaml prescribes for an unreadable login) rather than to a
// confident wrong answer.
//
// A bare "401" or "expired" never counts. Both turn up in text that has nothing
// to do with the login — a file path (the Omnipus home is passed with -C), a
// request id, a duration, "certificate has expired" — and answering those with
// "expired" sends the operator to run `copilot login`, which fixes none of them.
// A random "401" in a test's temp folder path did exactly that on CI.

// copilotExpiredPatterns are checked first: an expired-session message is likely
// to also tell the operator to log in again, so "expired" must win over the
// not-signed-in patterns. They run against the lower-cased detail.
var copilotExpiredPatterns = []*regexp.Regexp{
	// "session has expired", "token expired", "credentials have expired". The
	// noun is required, so "certificate has expired" never matches.
	regexp.MustCompile(`\b(?:session|token|credentials?|login)\s+(?:has\s+|have\s+|is\s+|was\s+)?(?:now\s+)?expired\b`),
	// "expired session", "expired token", "expired credentials".
	regexp.MustCompile(`\bexpired\s+(?:session|token|credentials?|login)\b`),
	// "invalid token", "invalid oauth token".
	regexp.MustCompile(`\binvalid\s+(?:oauth\s+|access\s+|auth\s+|github\s+)?token\b`),
	// GitHub's API message for a rejected credential.
	regexp.MustCompile(`\bbad\s+credentials\b`),
	// "token has been revoked", "credentials were revoked".
	regexp.MustCompile(`\b(?:token|credentials?|session|authori[sz]ation)\s+(?:has\s+been\s+|have\s+been\s+|was\s+|were\s+|is\s+)?revoked\b`),
	// 401 only as an HTTP status: "401 Unauthorized", "HTTP 401",
	// "HTTP/1.1 401", "status 401", "status code: 401".
	regexp.MustCompile(`\b401\s+unauthori[sz]ed\b`),
	regexp.MustCompile(`\b(?:http(?:/\d(?:\.\d)?)?|status(?:\s+code)?|response\s+code)(?:\s*[:=]\s*|\s+)401\b`),
	// An explicit request to authenticate again.
	regexp.MustCompile(`\bre-?authenticate\b`),
}

// copilotNotSignedInPatterns match the verified no-credential message and the
// guidance the CLI prints beside it. They run against the lower-cased detail.
var copilotNotSignedInPatterns = []*regexp.Regexp{
	// The verified @github/copilot 1.0.80 message.
	regexp.MustCompile(`\bno authentication information found\b`),
	regexp.MustCompile(`\bnot authenticated\b`),
	regexp.MustCompile(`\bcopilot login\b`),
	regexp.MustCompile(`\bgh auth login\b`),
	// The '/login' slash command as a word of its own — quoted, back-ticked or
	// space-separated — never a path segment such as /srv/login/omnipus.
	regexp.MustCompile("(?:^|[\\s'\"`])/login(?:$|[\\s'\"`.,;:!?)])"),
}

// CopilotSignIn asks the GitHub Copilot CLI whether it holds a usable login
// (ADR-068 FR-009). command is an explicit override for the Copilot binary
// name/path; empty means the default `copilot` on PATH. It is NOT sourced
// from a catalog row field — catalog.Provider carries no `cli_path`, and
// every call site passes "" unconditionally today, so an operator with
// `copilot` installed off PATH currently has no way to point this at it.
// workspace, when set, is the directory the check runs in.
//
// Cost: one premium request against the operator's Copilot subscription when
// they ARE signed in — see the block comment above. Call it from the operator's
// explicit *Check sign-in*, never from a poll.
func CopilotSignIn(ctx context.Context, command, workspace string) CopilotSignInResult {
	if command == "" {
		command = CopilotCLICommand
	}
	if _, err := exec.LookPath(command); err != nil {
		return CopilotSignInResult{State: CopilotCLIMissing, Detail: err.Error()}
	}

	args := []string{
		"-p", copilotSignInProbePrompt,
		"-s",
		"--allow-all-tools",
		"--no-ask-user",
		"--no-color",
		"--no-custom-instructions",
		"--log-level", "none",
	}
	if workspace != "" {
		args = append(args, "-C", workspace)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	if workspace != "" {
		cmd.Dir = workspace
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return CopilotSignInResult{State: classifyCopilotSignInFailure(detail), Detail: detail}
	}

	return CopilotSignInResult{State: CopilotSignedIn}
}

// classifyCopilotSignInFailure maps the CLI's stderr onto FR-009's states. An
// unrecognised failure is `not_signed_in` with a warning, never a confident
// `signed_in` or `expired`.
func classifyCopilotSignInFailure(detail string) CopilotSignInState {
	if state, matched := MatchCopilotSignInFailure(detail); matched {
		return state
	}
	slog.Warn("copilot cli sign-in check failed with an unrecognised message; reporting not_signed_in",
		"provider", "github-copilot", "detail", detail)
	return CopilotNotSignedIn
}

// MatchCopilotSignInFailure reports the sign-in state a failed Copilot CLI
// invocation EXPLICITLY names, and whether any pattern matched at all. It walks
// the same two pattern sets classifyCopilotSignInFailure does — it is where that
// function's matching now lives — so there is one copy to keep current when the
// vendor rewords an error.
//
// The difference from classifyCopilotSignInFailure is the `matched` half, and
// it is the whole point of exporting this. That function degrades an
// unrecognised message to `not_signed_in`, which is the right default when the
// question being asked IS "is this login usable" — CopilotSignIn's own fixed
// prompt run. The ADR-068 FR-036 sign-in probe asks a different question: it
// has already established that a CLI exists and has just spent one completion
// on the model the OPERATOR picked, so an unrecognised failure there is far
// likelier to be that model or that plan than the login. Answering it with
// "you are not signed in" would send the operator to re-authenticate against a
// problem authentication cannot fix, so that caller takes matched=false and
// uses its own wording.
func MatchCopilotSignInFailure(detail string) (CopilotSignInState, bool) {
	lower := strings.ToLower(detail)
	for _, p := range copilotExpiredPatterns {
		if p.MatchString(lower) {
			return CopilotSignInExpired, true
		}
	}
	for _, p := range copilotNotSignedInPatterns {
		if p.MatchString(lower) {
			return CopilotNotSignedIn, true
		}
	}
	return "", false
}
