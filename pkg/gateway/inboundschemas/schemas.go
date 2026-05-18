//go:build !cgo

// Package inboundschemas embeds OpenAPI component schema YAML files for
// server-side inbound request body validation.
//
// These files are copied from contracts/components/schemas/ at codegen time
// (scripts/gen-contracts.sh). The embedded FS is consumed by
// pkg/gateway/rest_inbound_validate.go which compiles the schemas once at
// gateway boot and uses them for per-handler JSON validation.
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
