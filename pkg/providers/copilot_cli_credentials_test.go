// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "testing"

// TestMatchCopilotSignInFailure_PhraseLevelOnly pins what counts as EVIDENCE
// of a sign-in state in a failed Copilot CLI run (ADR-068 FR-009).
//
// The classifier used to report `expired` whenever the text merely contained
// "401" or "expired" anywhere. A file path, a request id, or a certificate
// error then sent the operator to run `copilot login` — which fixes none of
// those — and the onboarding probe reuses the same classifier.
//
// Only phrase-level evidence counts now. Where a row below is a sign-in
// message, it is either the verified @github/copilot 1.0.80 no-credential
// text, a fixture already used by the Copilot tests, or an HTTP-status form
// (RFC 9110 "401 Unauthorized"). The real CLI wording for an expired or
// revoked session is still unverified; nothing here claims it.
func TestMatchCopilotSignInFailure_PhraseLevelOnly(t *testing.T) {
	const realNotSignedInStderr = `Error: No authentication information found.

Copilot can be authenticated with GitHub using an OAuth Token or a Fine-Grained Personal Access Token.

To authenticate, you can use any of the following methods:
  * Start 'copilot' and run the '/login' command
  * Set the COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN environment variable
  * Run 'gh auth login' to authenticate with the GitHub CLI`

	cases := []struct {
		name        string
		detail      string
		wantState   CopilotSignInState
		wantMatched bool
	}{
		// --- must NOT be read as a sign-in state --------------------------
		{
			name:   "a temp path containing 401 (the CI flake's detail)",
			detail: "/cache/tmp/TestSignInStatus_Copilotnot_signed_in2914017730/002/copilot: line 2: cat: command not found",
		},
		{
			name:   "a launch error naming an install folder containing 401",
			detail: "fork/exec /opt/tools-4017/copilot: permission denied",
		},
		{
			name:   "a request id containing 401",
			detail: "Error: request failed (x-github-request-id: 0401:3F2A:1B7C401:2D4E5F6:66E5A401)",
		},
		{
			name:   "a duration containing 401",
			detail: "Error: upstream timed out after 0.401s",
		},
		{
			name:   "a certificate that has expired",
			detail: "Error: unable to verify the first certificate: certificate has expired",
		},
		{
			name:   "Node's certificate-expired error code",
			detail: "Error: request to https://api.githubcopilot.com failed, reason: CERT_HAS_EXPIRED",
		},
		{
			name:   "a folder whose name contains expired",
			detail: "chdir /home/ops/expired-builds/omnipus: no such file or directory",
		},
		{
			name:   "a path segment named login",
			detail: "open /srv/login/omnipus/config.json: permission denied",
		},

		// --- recognised expired / rejected evidence -----------------------
		{
			name:      "session has expired (existing fixture)",
			detail:    "Error: your Copilot session has expired. Run `copilot login` again.",
			wantState: CopilotSignInExpired, wantMatched: true,
		},
		{
			name:      "GitHub's bad-credentials rejection (existing fixture)",
			detail:    "Error: Bad credentials (401)",
			wantState: CopilotSignInExpired, wantMatched: true,
		},
		{
			name:      "HTTP status 401 Unauthorized",
			detail:    "copilot cli error: request failed: 401 Unauthorized",
			wantState: CopilotSignInExpired, wantMatched: true,
		},
		{
			name:      "status code 401",
			detail:    "copilot cli error: request failed with status code 401",
			wantState: CopilotSignInExpired, wantMatched: true,
		},

		// --- recognised not signed in -------------------------------------
		{
			name:      "the verified 1.0.80 no-credential stderr",
			detail:    realNotSignedInStderr,
			wantState: CopilotNotSignedIn, wantMatched: true,
		},
		{
			name:      "the verified error line alone",
			detail:    "Error: No authentication information found.",
			wantState: CopilotNotSignedIn, wantMatched: true,
		},
		{
			name:      "the onboarding probe's wrapped form",
			detail:    "copilot cli error: Error: No authentication information found.",
			wantState: CopilotNotSignedIn, wantMatched: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotState, gotMatched := MatchCopilotSignInFailure(tc.detail)
			if gotMatched != tc.wantMatched || gotState != tc.wantState {
				t.Errorf("MatchCopilotSignInFailure(%q) = (%q, %v), want (%q, %v)",
					tc.detail, gotState, gotMatched, tc.wantState, tc.wantMatched)
			}
		})
	}
}
