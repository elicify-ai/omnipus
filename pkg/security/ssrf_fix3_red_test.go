// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

// RED regression pack, #798 fix round 3 — finding SL-F2 (gate report F794-F2).
//
// TEST PLAN (elicify-test-writing step 1)
//
// Behaviour under test:
//	The SSRF checker's gateway-origin prologue admits a PORTLESS
//	`<label>.localhost` URL when the wired gateway origin is an implicit-80
//	origin — the exact isolated_url form the web_serve tool itself mints on
//	an `http://localhost` install (middleware.PreviewIsolatedURL emits
//	`http://<label>.localhost/` with no port when the canonical origin has
//	no explicit port).
//
// Specification source (oracle — NOT the implementation):
//	- docs/internal/specs/adr-094-preview-isolation-spec.md, FR-021 + DS-6
//	  edge table: "An absent port equals the canonical origin's port
//	  (implicit 80)". The Mode 1 isolated_url the tool mints on an
//	  implicit-80 install must be reachable by the agent's own browser tool.
//	- Gate report security-lead F-2: the prologue requires an explicit port
//	  (`pkg/security/ssrf.go::isAllowedGatewayOrigin` — extractHostPort
//	  ok=false for a portless URL), so the minted URL fails closed. The fix
//	  admits the portless label host when the wired gwPort is 80.
//
// Unit boundary: the REAL SSRFChecker (no mocks). The gateway origin is
// wired via CloneWithGatewayOrigin — the same call site production uses
// (pkg/agent/loop_wire.go). Label strings come from
// middleware.PreviewLabelForToken so they are registry-truthful, not
// hand-invented grammar.
//
// Case table:
//	| case                                    | input                              | expected | source            |
//	|-----------------------------------------|------------------------------------|----------|-------------------|
//	| RED: portless label host on implicit 80 | http://<label>.localhost/          | admitted | FR-021 + DS-6     |
//	| pin: explicit port 80 still admitted    | http://<label>.localhost:80/       | admitted | DS-6 (unchanged)  |
//	| pin: portless refused when gw is :5000  | http://<label>.localhost/ (gw 5000)| refused  | DS-6 (explicit-port rule intact) |
//	| pin: non-canonical port still refused   | http://<label>.localhost:5173/     | refused  | DS-6 row: port equality enforced |
//
// What would break this (CHECK mutations): fill port 80 only in
// extractHostPort; drop the label branch; admit ANY portless host; admit
// non-http schemes.
//
// Known gaps (deliberate): a portless NON-label gateway URL
// (`http://localhost/` on an implicit-80 install) sits behind the same
// prologue but is outside finding F-2's scope — not asserted here either
// way, so the chosen fix shape is not over-constrained. Scheme
// equivalence for portless https label URLs is likewise unasserted.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/security"
)

func TestFix3SSRF_PortlessLabelHostAdmittedWhenGatewayOnImplicit80(t *testing.T) {
	ctx := context.Background()

	// Registry-truthful label: what the web_serve tool actually mints for a
	// dev token on this install (deterministic SHA-256/base32 derivation).
	label := middleware.PreviewLabelForToken("fix3-red-ssrf-token")
	require.NotEmpty(t, label, "PreviewLabelForToken must yield a non-empty label")

	// The implicit-80 install: canonical origin http://localhost (no port).
	chkr := security.NewSSRFChecker([]string{"https://example.com:8443"}).
		CloneWithGatewayOrigin("localhost", 80)

	// RED (SL-F2/F794-F2): today the prologue refuses every portless URL,
	// so the tool's own minted Mode 1 URL fails closed and the agent's
	// browser panel can never open it. FR-021 + DS-6: an absent port equals
	// the canonical origin's port (implicit 80) — this must be admitted.
	require.NoError(t, chkr.CheckURL(ctx, "http://"+label+".localhost/"),
		"SL-F2: the portless <label>.localhost isolated_url the tool mints on an implicit-80 gateway must pass SSRF")

	// Pins — protections that must survive the fix unchanged.

	// DS-6: explicit port 80 on the label host is already admitted.
	require.NoError(t, chkr.CheckURL(ctx, "http://"+label+".localhost:80/"),
		"the explicit-port form of the label host must stay admitted")

	// DS-6: with an explicit-port gateway origin, portless stays refused —
	// the fix must key the admission on the wired gateway port being 80,
	// not on dropping the port rule altogether.
	require.Error(t, chkr.CloneWithGatewayOrigin("localhost", 5000).CheckURL(ctx, "http://"+label+".localhost/"),
		"a portless label host must stay refused when the gateway origin has an explicit non-80 port")

	// DS-6: a non-canonical explicit port on the label host stays refused.
	require.Error(t, chkr.CheckURL(ctx, "http://"+label+".localhost:5173/"),
		"a label host on a port that is not the wired gateway port must stay refused")
}
