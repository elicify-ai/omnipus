// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import "strings"

// OAuthEntryName returns the encrypted-credential-store entry name that
// holds a device-code provider's stored OAuth tokens (ADR-068 FR-007/FR-046,
// T068-32) — a JSON blob {access_token, refresh_token, account_id,
// expires_at} under "<lowercase providerID>_OAUTH". This is Omnipus's OWN
// credential — never config.json, never the vendor's own credential file
// (e.g. ~/.codex/auth.json for codex-cli, which stays read-only/import-only,
// FR-047).
func OAuthEntryName(providerID string) string {
	return strings.ToLower(providerID) + "_OAUTH"
}
