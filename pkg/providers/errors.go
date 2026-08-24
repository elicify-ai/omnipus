// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "errors"

// ErrUnknownProvider is the typed sentinel every construction path returns
// when a configured provider id is neither a catalog id nor a valid custom
// row (ADR-067 FR-015, US-5.AC6).
//
// The wrapped message names the id the operator typed and NOTHING else — no
// canonical alternative, no "did you mean", no alias table. That prohibition
// is the point of the error: the greenfield rule (ADR-067 §1 header) removed
// every rename path, so a hint would be the one place a retired spelling
// could still be resurrected. SC-010 asserts the absence of the canonical id
// in the log, in `GET /providers` and in `GET /agents` — never on the echoed
// user-supplied id, which callers are free to quote back.
var ErrUnknownProvider = errors.New("unknown provider")

// ErrUnsupportedProvider is returned for a catalog row the runtime cannot
// construct at all — `tier: unsupported` (cloud-IAM providers such as
// amazon-bedrock, deployment-URL providers such as azure). The reason comes
// from the catalog row's `unsupported_reason`, so it is data, never a Go
// list (FR-019).
var ErrUnsupportedProvider = errors.New("unsupported provider")
