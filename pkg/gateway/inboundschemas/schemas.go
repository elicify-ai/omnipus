//go:build !cgo

// Package inboundschemas embeds OpenAPI component schema YAML files for
// server-side inbound request body validation.
//
// The *.yaml files in this directory are machine-copied from
// contracts/components/schemas/ by scripts/gen-contracts.sh (Step 5).
// They are the single source of truth for the embedded validator.
//
// Do not edit the *.yaml files in this directory — they are canonical copies.
// To update them, modify contracts/components/schemas/ and re-run:
//
//	make gen-contracts
package inboundschemas

import "embed"

// FS holds all OpenAPI component schema YAML files.
//
//go:embed *.yaml
var FS embed.FS
