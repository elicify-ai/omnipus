// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package security — RED tests for ADR-094 preview isolation, orders 6 and 7
// (spec: docs/internal/specs/adr-094-preview-isolation-spec.md, TDD Plan +
// DS-6).
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: the SSRF layer admits exactly the label class
//     FR-021 defines — a grammar-valid single label + exactly `.localhost`,
//     explicit port equal to the wired gateway port, http(s) — and the shared
//     singleton gains nothing from the browser clone's admission.
//   - Specification source: ADR-094 spec FR-021, DS-6 rows 1–15, S-6.1–S-6.3,
//     FR-005 (grammar), F-1/F-2/F-3 rulings. Every expected verdict below is
//     the DS-6 table's Verdict column, not an observed output.
//   - Unit boundary: real SSRFChecker (pkg/security/ssrf.go), real grammar;
//     no mocks — isAllowedGatewayOrigin is pure against a checker whose
//     gateway origin is wired via the production AllowGatewayOrigin /
//     CloneWithGatewayOrigin paths.
//   - Mutations: M-3 (widen the label class — any port / multi-label) must
//     flip rows 8 and 9 red; M-1/M-2 live in pkg/gateway (order 25's file).
//   - RED shape: rows whose post-GREEN verdict is "admitted" (DS-6 rows 1, 4,
//     15) fail TODAY because the label class does not exist in
//     isAllowedGatewayOrigin — a runtime verdict failure, not a compile
//     error. Rows whose post-GREEN verdict is "refused" already return false
//     today and are pins.
//   - Known gaps: none at this layer; DS-6 row 14's listener-vs-canonical
//     port shape is covered against a checker wired with the LISTENER port
//     (FR-021(d)) per DS-6 row 14's own wording.

package security

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// piRedLabel is a grammar-valid, mint-shaped label: 26 lower-case base32
// characters ≈ 130 bits of entropy (DS-1 row 9's normative floor, FR-005).
// DS-6's "registered-label" rows only need a label the grammar admits; the
// gateway's own registry (404 for unknown labels) is a different layer.
const piRedLabel = "k4m2wpxq5ztn7vrj9eh3as8dyg"

// TestPreviewSSRFLabelClass pins the exact DS-6 verdicts (rows 1–15) against
// a checker whose gateway origin is wired as the spec's default loopback
// install: host `localhost`, port 5000.
//
// RED today: the rows marked (RED) expect "admitted" and fail because
// isAllowedGatewayOrigin has no label class — the honest runtime failure, not
// a compile error. All other rows are pins: they hold today and must keep
// holding after GREEN.
func TestPreviewSSRFLabelClass(t *testing.T) {
	sc := NewSSRFChecker(nil)
	sc.AllowGatewayOrigin("localhost", 5000)

	cases := []struct {
		name    string
		url     string
		checker *SSRFChecker // nil = the default localhost:5000 checker
		want    bool
		redRow  bool // true = this row is RED on pre-change code
	}{
		// DS-6 row 1 — label-class admission (RED: refused today).
		{
			name: "row1_registered_label_admitted",
			url:  fmt.Sprintf("http://%s.localhost:5000/", piRedLabel),
			want: true, redRow: true,
		},
		// DS-6 row 2 — trailing dot refused.
		{
			name: "row2_trailing_dot_refused",
			url:  "http://foo.localhost.:5000/",
			want: false,
		},
		// DS-6 row 3 — explicit-port requirement (no port → fail closed).
		{
			name: "row3_no_explicit_port_refused",
			url:  "http://foo.localhost/",
			want: false,
		},
		// DS-6 row 4 — upper-case admitted, routed lower-cased (F-2).
		// (RED: refused today.)
		{
			name: "row4_upper_case_admitted",
			url:  "http://FOO.LOCALHOST:5000/",
			want: true, redRow: true,
		},
		// DS-6 row 5 — non-http(s) out of scope for the label class: the
		// explicit boolean is FALSE (S-6.3 row 5).
		{
			name: "row5_ftp_scheme_refused",
			url:  "ftp://foo.localhost:5000/",
			want: false,
		},
		// DS-6 row 6 — bare gateway host keeps ADR-073's /preview/ path scope.
		{
			name: "row6_bare_host_preview_scope_admitted",
			url:  "http://localhost:5000/preview/some-agent/some-token/",
			want: true,
		},
		// DS-6 row 7 — grammar negatives: empty label part, leading/trailing
		// hyphens.
		{
			name: "row7a_empty_label_part_refused",
			url:  "http://ba..localhost:5000/",
			want: false,
		},
		{
			name: "row7b_hyphen_edge_label_refused",
			url:  "http://-bad-.localhost:5000/",
			want: false,
		},
		// DS-6 row 8 — wrong port refused (M-3's any-port widening target).
		{
			name: "row8_wrong_port_refused",
			url:  "http://foo.localhost:6379/",
			want: false,
		},
		// DS-6 row 9 — two labels refused (M-3's multi-label widening target).
		{
			name: "row9_multi_label_refused",
			url:  "http://a.b.localhost:5000/",
			want: false,
		},
		// DS-6 row 10 — foreign suffix refused.
		{
			name: "row10_foreign_suffix_refused",
			url:  "http://foo.localhost.evil.com:5000/",
			want: false,
		},
		// DS-6 row 11 — no dot: host is not `<label>.localhost`.
		{
			name: "row11_no_dot_refused",
			url:  "http://foolocalhost:5000/",
			want: false,
		},
		// DS-6 row 12 — foreign suffix with matching port refused.
		{
			name: "row12_foreign_suffix_matching_port_refused",
			url:  "http://foo.example.com:5000/",
			want: false,
		},
		// DS-6 row 13 — the loopback literal keeps its ADR-073 /preview/ path
		// scope; /api/v1/agents is refused.
		{
			name: "row13_loopback_literal_out_of_path_scope_refused",
			url:  "http://127.0.0.1:5000/api/v1/agents",
			want: false,
		},
		// DS-6 row 15 — label class with an API path: SSRF-admitted (the host
		// class is admitted for ANY path; the gateway's registry 404s it).
		// Split from row 1 per round-2 MAJ-006. (RED: refused today.)
		{
			name: "row15_label_class_api_path_admitted",
			url:  fmt.Sprintf("http://%s.localhost:5000/api/v1/agents", piRedLabel),
			want: true, redRow: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := sc
			if tc.checker != nil {
				checker = tc.checker
			}
			got := checker.isAllowedGatewayOrigin(tc.url)
			if tc.redRow {
				require.Equal(t, tc.want, got,
					"RED (FR-021, DS-6): isAllowedGatewayOrigin must admit %q — the label class "+
						"(one grammar-valid label + exactly .localhost, wired gateway port, http) is "+
						"not implemented yet; this row is the RED for it", tc.url)
			}
			assert.Equal(t, tc.want, got,
				"SSRF verdict for %q must equal the DS-6 verdict", tc.url)
		})
	}

	// DS-6 row 14 — port-mapped install: the panel's SSRF layer is wired with
	// the LISTENER port (FR-021(d), `pkg/agent/loop_wire.go` browserSSRF), so
	// the canonical-port label URL is refused (accepted limitation,
	// round-2 MIN-004). Pin: refused today and post-GREEN.
	t.Run("row14_port_mapped_listener_port_wiring", func(t *testing.T) {
		panelChecker := NewSSRFChecker(nil)
		panelChecker.AllowGatewayOrigin("localhost", 5000) // the WIRED listener port
		got := panelChecker.isAllowedGatewayOrigin("http://foo.localhost:8080/")
		assert.False(t, got,
			"DS-6 row 14: a label URL on the canonical port (8080) must be refused when the "+
				"panel checker is wired with the listener port (5000) — FR-021(d)")
	})

	// FR-021(b) — exactly one label: a three-label host is not the class
	// either (row 9's widening generalised). Pin.
	t.Run("three_labels_refused", func(t *testing.T) {
		assert.False(t, sc.isAllowedGatewayOrigin("http://a.b.c.localhost:5000/"),
			"FR-021(b): the literal host must be ONE label + exactly .localhost")
	})
}

// TestPreviewSSRF_SingletonIsolation pins S-6.2 (order 7): the shared SSRF
// singleton (the zero-gateway-origin shape that provider base_url and
// skill-installer validation consult) gains NOTHING from the browser clone's
// label-class admission, and the clone admits exactly the class (RED today).
func TestPreviewSSRF_SingletonIsolation(t *testing.T) {
	labelURL := fmt.Sprintf("http://%s.localhost:5000/", piRedLabel)

	// The shared-singleton shape: no gateway origin configured → fail-closed
	// (ssrf.go::gatewayHost zero value). Pin: refuses label URLs today and
	// must keep refusing post-GREEN — "the shared singleton gains nothing".
	singleton := NewSSRFChecker(nil)
	assert.False(t, singleton.isAllowedGatewayOrigin(labelURL),
		"S-6.2: the shared singleton must refuse label-class URLs before and after GREEN")

	// The browser-dedicated clone admits the label class post-GREEN.
	// RED today: the label class does not exist, so the clone refuses too.
	clone := singleton.CloneWithGatewayOrigin("localhost", 5000)
	require.True(t, clone.isAllowedGatewayOrigin(labelURL),
		"RED (FR-021): CloneWithGatewayOrigin's browser clone must admit the label class — "+
			"it does not exist yet; this is the RED for the clone-side admission")

	// And the original is unchanged by the clone's wiring (the layering
	// claim: admission is scoped to the clone).
	assert.False(t, singleton.isAllowedGatewayOrigin(labelURL),
		"S-6.2: after the clone's admission, the original checker must still refuse the "+
			"label class — the singleton gains nothing from CloneWithGatewayOrigin")
}
