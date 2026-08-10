// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
)

// Compile-time capability assertions.
//
// These live in a NON-TEST file deliberately. StreamingProvider is optional
// and consulted by runtime type assertion in the agent loop
// (`activeProvider.(providers.StreamingProvider)`), so a provider that stops
// satisfying it does not fail to build — it silently drops every call to the
// non-streaming path: no partial text, no tool-argument progress, no way to
// tell a model that is still working from one that has hung. Delegated workers
// were killed on exactly that ambiguity.
//
// ADR-059 D1 names the gap these close: "the only compile signal is a
// hand-written `var _ I = (*T)(nil)`, and on this branch that assertion lives
// in a TEST file, so it is not a `make build` error". It was still in a test
// file after W1 landed. It is not now — `go build ./...` fails if any of these
// four stops satisfying the interface.
//
// Rule: assert the type the FACTORY RETURNS, not the delegate it wraps. That
// distinction is not pedantic — this test previously pinned
// *anthropicprovider.Provider, which does satisfy the interface, while
// *ClaudeProvider (the only thing that constructs it, holding it as an
// unexported non-embedded field so nothing is promoted) did not. Every
// Anthropic install silently took the non-streaming path with the assertion
// green.
//
// Satisfying the interface is necessary, not sufficient: a wrapper can accept
// onProgress and forward nil. streaming_forwarding_test.go covers that half.
var (
	_ StreamingProvider = (*ClaudeProvider)(nil)
	_ StreamingProvider = (*HTTPProvider)(nil)
	_ StreamingProvider = (*anthropicprovider.Provider)(nil)
	_ StreamingProvider = (*openai_compat.Provider)(nil)
)
