// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — focused fix-round tests for the ADR-094 preview host
// dispatch (gate round fix3, findings A6/SL-F1/CR4 + CR2). File name carries
// the fix3 marker per the round's dispatch brief.
package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFix3PreviewPortAgreesImplicit80 pins DS-3 row 10 (portless label Host
// on an implicit-80 canonical origin dispatches) and the DS-3 rows 11–12 pins
// (one-sided or mismatched ports never agree). RED before the fix: every
// True row below fails because previewPortAgrees leaves a portless Host port
// as "" while default-filling the origin side to "80".
func TestFix3PreviewPortAgreesImplicit80(t *testing.T) {
	// DS-3 row 10: portless agrees with implicit 80 (http) and 443 (https).
	require.True(t, previewPortAgrees("", "http://localhost"),
		"DS-3 row 10: a portless Host must agree with the implicit-80 origin — "+
			"the mint emits exactly this URL and the browser sends exactly this Host")
	require.True(t, previewPortAgrees("80", "http://localhost"),
		"an explicit :80 Host agrees with the same implicit-80 origin")
	require.True(t, previewPortAgrees("", "https://localhost"),
		"a portless Host agrees with an implicit-443 origin the same way")
	require.True(t, previewPortAgrees("443", "https://localhost"))

	// DS-3 rows 11–12 pins: one-sided or mismatched ports never agree.
	require.False(t, previewPortAgrees("", "http://localhost:5000"),
		"DS-3 row 12: a portless Host against an explicit-port origin falls through")
	require.False(t, previewPortAgrees("9999", "http://localhost:5000"),
		"DS-3 row 11: a wrong-port Host falls through")
	require.False(t, previewPortAgrees("80", "http://localhost:5000"))
	require.True(t, previewPortAgrees("5000", "http://localhost:5000"),
		"an exact-port Host agrees")

	// An unusable canonical origin agrees with nothing.
	require.False(t, previewPortAgrees("80", ""))

	// Browsers omit the default port even when the origin spelled it.
	require.True(t, previewPortAgrees("", "http://localhost:80"),
		"a portless Host agrees with an explicit :80 origin — the browser sends no port")
	require.True(t, previewPortAgrees("", "https://localhost:443"))
}

// TestFix3LimiterMapBounded pins A6/SL-F1/CR4: unseen labels must not each
// allocate a permanent bucket, and a burst of distinct unknown labels must
// share one cap instead of each being admitted on first sight.
// RED before the fix: allow() inserts a full bucket per label and never
// evicts, so both assertions fail (200 buckets, 200 admits).
func TestFix3LimiterMapBounded(t *testing.T) {
	l := &previewLabelLimiter{buckets: make(map[string]*previewLabelBucket)}
	admitted := 0
	for i := 0; i < 200; i++ {
		// Drive the NON-RESOLVING admission path (fix4: allow() is the
		// resolving path and gives each label its own bucket; unknown labels
		// reach admit(label, false) from previewHostDispatchMW).
		if l.admit(fmt.Sprintf("unk%03d", i), false) {
			admitted++
		}
	}
	require.LessOrEqual(t, len(l.buckets), 1,
		"unknown labels must not each allocate a limiter bucket")
	require.LessOrEqual(t, admitted, previewLabelRateLimitBurst+1,
		"unknown labels must share one global cap, not a full burst each")
}

// TestFix3LimiterEvictsIdleAndCaps pins the resolved-label half of the same
// finding: allocation happens in promote (the registry-resolution path), so
// the eviction/cap pins are driven through it — idle buckets are swept, and
// a map of still-fresh buckets stays at the cap when another label arrives.
// (Retargeted from the pre-reboot draft, which drove allow() here and
// contradicted TestFix3LimiterMapBounded: allow() allocates nothing for an
// unseen label, by design.)
func TestFix3LimiterEvictsIdleAndCaps(t *testing.T) {
	l := &previewLabelLimiter{buckets: make(map[string]*previewLabelBucket)}
	idleAt := time.Now().Add(-time.Hour)
	for i := 0; i < 300; i++ {
		l.buckets[fmt.Sprintf("old%04d", i)] = &previewLabelBucket{tokens: 1, last: idleAt}
	}
	l.promote("freshlabel")
	require.Equal(t, 1, len(l.buckets),
		"buckets idle for an hour must be evicted when a label is promoted")
	_, ok := l.buckets["freshlabel"]
	require.True(t, ok, "the label just promoted must get its own bucket")
	require.True(t, l.allow("freshlabel"),
		"a promoted label admits against its own bucket")

	l = &previewLabelLimiter{buckets: make(map[string]*previewLabelBucket)}
	freshAt := time.Now()
	for i := 0; i < 300; i++ {
		l.buckets[fmt.Sprintf("hot%04d", i)] = &previewLabelBucket{tokens: 1, last: freshAt}
	}
	l.promote("onemore")
	require.LessOrEqual(t, len(l.buckets), 256,
		"the limiter map must stay capped when every bucket is still fresh")
	_, ok = l.buckets["onemore"]
	require.True(t, ok, "the newly promoted label must be kept when the cap evicts")
}
