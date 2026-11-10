package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// U15 — DEL-16 contract slice (session-core-spec.md §"U15 — Leaf deletion
// inventory", DEL-16 row; BDD-12.1/12.3; T14/T23).
//
// DEL-16 removes the SSE chat-stream transport. Its two halves:
//   1. the gateway source — pkg/gateway/sse.go::SSEHandler/newSSEHandler and
//      the gateway_boot.go backward-compat registration of the `/api/v1/chat`
//      route. That half is asserted by
//      sessioncore_u15_leaf_deletions_test.go::TestSessionCoreU15_SSEHandlerRemoved.
//   2. THIS FILE — the matching contract surface: the `POST /chat` operation
//      (i.e. POST /api/v1/chat, the openapi server base being /api/v1) and its
//      `SseChatRequest` schema in contracts/openapi.yaml, plus every artifact
//      generated from them:
//        - pkg/api/generated/openapi_types.gen.go  (Go: SseChatRequest,
//          PostChatJSONRequestBody),
//        - src/lib/api/generated/openapi-types.ts   (TS types),
//        - src/lib/api/generated/schemas.ts         (Zod schemas + route table),
//        - pkg/gateway/inboundschemas/SseChatRequest.yaml — the copy
//          scripts/gen-contracts.sh step 5 syncs from
//          contracts/components/schemas/ and Makefile `verify-contracts`
//          diff-checks, so it must fall away with the canonical schema.
//
// Canonical replacement: the persistent WebSocket transport at
// `/api/v1/chat/ws` (contracts/asyncapi.yaml), which MUST survive.
//
// Oracle: the spec's DEL-16 row names both the removed surface and the
// replacement — the expected ABSENCE is derived from the spec, not observed
// from the tree. RED on the pre-removal tree (every forbidden token is still
// present); GREEN once the contract-removal lane lands.
//
// Instrument (BDD-12.1 "controlled absence"): a bare grep cannot tell
// "removed" from "never read". Every sweep below therefore carries a
// known-present control (a token that IS in the same file, proving the reader
// read real content) and an injected-forbidden control (the forbidden token
// written to a temp fixture must be detected by the same matcher, proving a
// negative here is meaningful).

// u15Del16MatcherDetectsAll proves the substring matcher these sweeps rely on
// actually fires on a real occurrence of forbidden — the injected-forbidden
// control. Without it a passing "absent" assertion would be vacuous.
func u15Del16MatcherDetectsAll(t *testing.T, forbidden string) {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "injected_fixture.txt")
	if err := os.WriteFile(fixture, []byte("prefix "+forbidden+" suffix"), 0o600); err != nil {
		t.Fatalf("U15/DEL-16 instrument: write injected fixture: %v", err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("U15/DEL-16 instrument: read injected fixture: %v", err)
	}
	if !strings.Contains(string(data), forbidden) {
		t.Fatalf("U15/DEL-16 instrument broken: injected forbidden token %q not detected — a negative sweep cannot be trusted", forbidden)
	}
}

// u15Del16TokenAbsent asserts repoRelative does NOT contain forbidden, guarded
// by a known-present control (presentControl must be found in the same file)
// and the injected-forbidden control.
func u15Del16TokenAbsent(t *testing.T, repoRelative, forbidden, presentControl string) {
	t.Helper()
	path := filepath.Join(u18RepoRoot(t), filepath.FromSlash(repoRelative))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("U15/DEL-16 instrument: read %s: %v", repoRelative, err)
	}
	src := string(data)

	if !strings.Contains(src, presentControl) {
		t.Fatalf("U15/DEL-16 instrument broken: present-control %q not found in %s — the sweep is reading the wrong or empty file", presentControl, repoRelative)
	}
	u15Del16MatcherDetectsAll(t, forbidden)

	if strings.Contains(src, forbidden) {
		t.Errorf("%s still contains %q — DEL-16 requires the SSE /chat contract surface removed (spec §U15 DEL-16)", repoRelative, forbidden)
	}
}

// u15Del16PathAbsent asserts repoRelative does not exist. Controls: a
// known-present file (presentFile must be statable, proving the repo root and
// Stat are live) and an injected control (a temp file we create reads as
// present, then absent after removal — proving Stat distinguishes the two).
func u15Del16PathAbsent(t *testing.T, repoRelative, presentFile string) {
	t.Helper()
	root := u18RepoRoot(t)

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(presentFile))); err != nil {
		t.Fatalf("U15/DEL-16 instrument broken: present-control file %s not statable (repo root wrong?): %v", presentFile, err)
	}

	fixture := filepath.Join(t.TempDir(), "injected_present.txt")
	if err := os.WriteFile(fixture, []byte("x"), 0o600); err != nil {
		t.Fatalf("U15/DEL-16 instrument: write injected present fixture: %v", err)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("U15/DEL-16 instrument broken: created fixture not statable: %v", err)
	}
	if err := os.Remove(fixture); err != nil {
		t.Fatalf("U15/DEL-16 instrument: remove injected fixture: %v", err)
	}
	if _, err := os.Stat(fixture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("U15/DEL-16 instrument broken: removed fixture still statable (err=%v)", err)
	}

	path := filepath.Join(root, filepath.FromSlash(repoRelative))
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s still exists — DEL-16 requires the SSE /chat contract surface removed (spec §U15 DEL-16)", repoRelative)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("U15/DEL-16 instrument: stat %s: %v", repoRelative, err)
	}
}

// TestSessionCoreU15_DEL16_OpenAPI_SseChatRemoved asserts the OpenAPI half: the
// `POST /chat` operation and its `SseChatRequest` schema are gone from
// contracts/openapi.yaml, and the schema component file is deleted. RED on the
// pre-removal tree.
func TestSessionCoreU15_DEL16_OpenAPI_SseChatRemoved(t *testing.T) {
	const openapi = "contracts/openapi.yaml"
	// Present control: the neighbouring /activity operation (getActivity) stays.
	u15Del16TokenAbsent(t, openapi, "operationId: postChat", "operationId: getActivity")
	u15Del16TokenAbsent(t, openapi, "SseChatRequest", "operationId: getActivity")
	u15Del16PathAbsent(t, "contracts/components/schemas/SseChatRequest.yaml", "contracts/openapi.yaml")
}

// TestSessionCoreU15_DEL16_GeneratedArtifacts_SseChatRemoved asserts the
// generated-artifact half: the Go openapi types, the TS openapi types + Zod
// schemas, and the gen-contracts.sh-synced inbound-schema copy no longer carry
// the SSE /chat surface. RED on the pre-removal tree.
func TestSessionCoreU15_DEL16_GeneratedArtifacts_SseChatRemoved(t *testing.T) {
	u15Del16TokenAbsent(t, "pkg/api/generated/openapi_types.gen.go", "SseChatRequest", "type Session struct")
	u15Del16TokenAbsent(t, "pkg/api/generated/openapi_types.gen.go", "PostChatJSONRequestBody", "type Session struct")
	u15Del16TokenAbsent(t, "src/lib/api/generated/openapi-types.ts", "SseChatRequest", `export type Session = components["schemas"]["Session"]`)
	u15Del16TokenAbsent(t, "src/lib/api/generated/schemas.ts", "SseChatRequest", "export const ErrorResponse = z")
	u15Del16PathAbsent(t, "pkg/gateway/inboundschemas/SseChatRequest.yaml", "pkg/gateway/inboundschemas/schemas.go")
}

// TestSessionCoreU15_DEL16_CanonicalWebSocketSurvives is the canonical positive
// control (BDD-12.3): DEL-16's replacement transport — the persistent WebSocket
// session at /api/v1/chat/ws — is untouched in contracts/asyncapi.yaml. It is
// GREEN before and after the cut and proves the contract reader (strings
// Contains over a real contract file) can see a present token.
func TestSessionCoreU15_DEL16_CanonicalWebSocketSurvives(t *testing.T) {
	path := filepath.Join(u18RepoRoot(t), "contracts", "asyncapi.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("U15/DEL-16: read contracts/asyncapi.yaml: %v", err)
	}
	src := string(data)
	// Both tokens are in the asyncapi channels block for the WS transport.
	for _, want := range []string{"pathname: /api/v1/chat/ws", "address: /api/v1/chat/ws"} {
		if !strings.Contains(src, want) {
			t.Errorf("contracts/asyncapi.yaml must keep the canonical WS transport %q — the DEL-16 replacement", want)
		}
	}
}
