// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — RED test for ADR-094 preview isolation, order 18
// (FR-026, S-7.1; spec: docs/internal/specs/adr-094-preview-isolation-spec.md).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: a Mode 1 label never reaches a log file or the
//     audit chain raw — it is redacted at every recording site exactly as
//     token-bearing preview paths are today.
//   - Specification source: FR-026 verbatim, S-7.1.
//   - Unit boundary: real chain (piRedNewPlantedHarness), real mint, real
//     slog capture (captureRedactionSlog — the same capture the existing
//     eight-site guard test uses).
//   - RED shape: the Mode 1 surface does not exist — the drive REDs at the
//     missing mint; post-GREEN the assertion holds while GREEN's new
//     Host/URL-recording sites (FR-026: audit events for proxied Mode 1,
//     e.g. dev.proxied carrying the redacted label) are live.
//   - Known gap (documented, GREEN/CHECK): the build-breaking inventory
//     guard EXTENSION (expectedRedactionSites gaining the new Host/URL
//     sites, and the Host-recording scanner) cannot be written pre-GREEN —
//     the sites it must enumerate do not exist until GREEN adds label
//     logging. CHECK verifies the extension against GREEN's diff.
//   - Mutations (post-GREEN, CHECK): M-7 (remove Host/full-URL redaction)
//     must flip this drive red — the label must then appear in the capture.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPreviewLabelRedaction drives a Mode 1 label-host request and asserts
// the raw label appears nowhere in anything the gateway logged or audited
// during the drive. RED today at the missing mint.
func TestPreviewLabelRedaction(t *testing.T) {
	h := piRedNewPlantedHarness(t)
	logs := captureRedactionSlog(t)

	label := piRedLifeMint(t, h, "pi-red-redact-agent", "")
	status := piRedLifeLabelGet(t, h, label)
	_ = status // reachability is not this test's claim — redaction is

	assert.NotContains(t, logs.text(), label,
		"RED (FR-026, S-7.1): the Mode 1 label must be redacted at every Host/URL-recording "+
			"site, exactly as token-bearing paths are — once Mode 1 logging lands, the raw "+
			"label must never appear in the capture")
}
