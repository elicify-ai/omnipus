package capabilities

import _ "embed"

// Seed file — FR-024 freeze gate (re-validated against current 2026 provider
// docs before commit; see docs/internal/research/provider-media-format-support-2026-07.md).
//
// The go:embed directive compiles data/providers_capabilities_seed.json into
// the binary. This is the guaranteed-last-resort source-of-truth: even when
// no Store is configured, when the network is unreachable, AND when the
// GitHub Release + raw fallback both fail, the gateway boots with this seed.
//
// Package docs live in catalog.go.

//go:embed data/providers_capabilities_seed.json
var seedJSON []byte

// embeddedSeed returns the compiled-in seed JSON. Exposed for tests and
// diagnostic tooling; NewCatalog takes the seed data as a parameter so
// callers can swap in fixtures.
func embeddedSeed() []byte {
	return seedJSON
}
