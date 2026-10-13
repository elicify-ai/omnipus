// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "github.com/elicify-ai/omnipus/pkg/providers/catalog"

// HasSignInMechanism reports whether this build can actually exercise a
// sign-in for a catalog row (provider hotfix H1). A row's `auth_methods` is
// registry data mirrored from a separate repository and can declare
// `sign_in` for a row with no way to sign in — the plain `openai` row does,
// while only `openai-chatgpt` can ever source an OpenAI login.
//
// The mechanisms are exactly the three pkg/gateway's probeSignIn dispatches
// on:
//
//   - a `cli_kind` row (a vendor CLI holds the login),
//   - a first-party OAuth owner (OAuthEntryOwner — the only ids the
//     device-code flow writes credentials for),
//   - a `token_source: codex-auth-json` row (reads the Codex CLI's saved
//     token).
//
// No provider id is named here beyond what OAuthEntryOwner already encodes.
// A mechanism added to probeSignIn must be added here too, or that row's
// sign-in stays hidden.
func HasSignInMechanism(row catalog.Provider) bool {
	return row.CLIKind != "" ||
		OAuthEntryOwner(row.ID) ||
		row.TokenSource == catalog.TokenSourceCodexAuthJSON
}
