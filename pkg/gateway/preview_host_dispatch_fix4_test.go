// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — fix-round-4 admission tests for the ADR-094 Mode 1
// preview host dispatch. Oracle: docs/internal/specs/adr-094-preview-
// isolation-spec.md FR-027 (spec line 971) — the preview rate limiter MUST
// apply PER LABEL on Mode 1 hosts; one label's throttle leaves others and
// the main host untouched. Fix4 resolves an unseen label against the
// registries BEFORE admission routing: a resolving label draws only its own
// bucket from its first request, and only non-resolving labels consume the
// shared unknown-label budget.
package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// f4DrainUnknownBudget drains the harness limiter's shared unknown-label
// budget — the junk-label flood an attacker drives, expressed directly
// against the admission primitive the dispatcher routes non-resolving
// labels through (burst plus margin, so the budget is exhausted with the
// refill accounted for).
func f4DrainUnknownBudget(t *testing.T, h *piRedGuardHarness) {
	t.Helper()
	limiter := h.api.previewLabelLimiter()
	for range previewLabelRateLimitBurst + 16 {
		limiter.allowUnknown()
	}
}

// TestFix4Admission_UnknownDrainServesRegisteredLabel pins the fix4
// admission property: with the shared unknown-label budget drained by junk
// labels, a REGISTERED label's first request is still served (FR-027 — its
// throttle budget is its own), while a junk label stays capped by the
// drained budget.
//
// The round-3 design failed the first row by construction: every unseen
// label — junk or registered — drew the shared unknown budget first and
// promoted only later, so a junk flood denied every legitimate label's
// first request (and coupled every test in one process through the
// package-level singleton).
func TestFix4Admission_UnknownDrainServesRegisteredLabel(t *testing.T) {
	h := piRedNewGuardHarness(t)
	label := middleware.PreviewLabelForToken(h.devToken)
	require.NotEmpty(t, label, "fixture binding: the dev token must derive its label")

	f4DrainUnknownBudget(t, h)

	t.Run("registered_label_first_request_still_served", func(t *testing.T) {
		h.upstreamHits.Store(0)
		resp := h.piRedGuardGet(t, label+".localhost:"+h.port, "/", http.MethodGet, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"FR-027 (fix4): a registered label's first request must be served even with the "+
				"unknown-label budget drained — got %d (the pre-fix4 routing 429s it: the "+
				"label's first sight drew the shared junk budget)", resp.StatusCode)
		assert.Equal(t, int32(1), h.upstreamHits.Load(),
			"the dispatched request must reach the dev upstream — admission must not block a resolving label")
	})

	t.Run("junk_label_still_capped_after_drain", func(t *testing.T) {
		resp := h.piRedGuardGet(t, "f4-junk-label.localhost:"+h.port, "/", http.MethodGet, nil)
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
			"the drained unknown budget must still cap a non-resolving label — "+
				"the junk-flood protection is the point of the unknown budget")
	})
}

// TestFix4Admission_LimitersArePerInstance pins the de-globalised shape:
// the dispatcher's admission state is the restAPI instance's own limiter,
// not the package default, so two gateways (or two harnesses) in one
// process never throttle each other through shared state (fix4 — the
// global-state coupling the round-3 design shipped).
func TestFix4Admission_LimitersArePerInstance(t *testing.T) {
	h := piRedNewGuardHarness(t)
	require.NotNil(t, h.api.previewLabelLimiter(),
		"a harness restAPI always carries a limiter")
	require.NotSame(t, previewLabelLimiters, h.api.previewLabelLimiter(),
		"the harness restAPI must own its limiter instance — the package default is the "+
			"zero-value fallback, never the instance a second gateway shares")
}
