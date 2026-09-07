// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — FR-019b (the SPA's own policy), FR-006b (the framing
// control), FR-018b (a missing PDF.js runtime asset must fail visibly), and
// spec §13 tests 68 (TestSpaServedWithCSP) and 119 (TestSpaCsp_DirectiveFloor).
//
// WHY TEST 119 EXISTS AS A SEPARATE THING FROM TEST 68, in the spec's own
// words: FR-019b requires "a Content-Security-Policy" and the earlier MV-21
// required the header to exist and contain no 'unsafe-eval'. So
// `Content-Security-Policy: default-src *` satisfied FR-019b, MV-21, test 68
// and AC-15.9 simultaneously — a header that grants everything the policy
// exists to withhold. Round 4 named that and gave MV-21 a directive floor
// (MV-25) that can fail.
//
// Test 119's second half is the point, and this file takes it literally: the
// SAME checker used against the shipped header is fed a mutation table — a
// wide-open policy, the framing control removed, eval added, the worker source
// dropped — and each must be REJECTED. Without that, a checker that returns
// "no violations" unconditionally passes the first half forever. That is not
// hypothetical: it is what the previous version of this requirement was.

package gateway

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- the oracle: §10.7, read from the specification ------------------------

// specSPAPolicy extracts §10.7's fenced policy block from the spec file.
//
// Same reasoning as the §10.3 reader in rest_library_preview_test.go: a test
// carrying its own hand-typed copy of a policy asserts only that two strings
// written by the same person at the same moment agree. Failing to find the
// block is a FAILURE, never a skip — a silently missing oracle is the exact
// false-green this suite is audited against.
func specSPAPolicy(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(specDocRelPath)
	require.NoError(t, err, "the ADR-067 spec must be readable — it is this test's oracle")

	// §10.7's block is the only fenced line in the document beginning with
	// default-src 'self'.
	re := regexp.MustCompile(`(?m)^default-src 'self';.*$`)
	matches := re.FindAllString(string(raw), -1)
	require.Len(t, matches, 1,
		"§10.7 must contribute exactly one literal policy line to the spec; found %d", len(matches))
	return strings.TrimSpace(matches[0])
}

// --- test 68 — TestSpaServedWithCSP ----------------------------------------

// spaResponse drives the real SPA handler for one path.
func spaResponse(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newSPAHandler()
	require.NotNil(t, handler, "no SPA embedded — every assertion below would be vacuous")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// spaFixtureFS is a minimal in-memory SPA embed used to exercise the handler's
// path→CSP routing WITHOUT a built SPA. It carries the one file the worker-CSP
// rule keys on (pdfJSWorkerPath) plus an index and a wasm asset, so the routing
// is tested for real and deterministically on any machine — including the
// root-user, stub-SPA CI worker, where fetching the real embedded worker 404s.
func spaFixtureFS() fs.FS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>fixture</title>")},
		pdfJSWorkerPath: {Data: []byte("// pdf.js worker (fixture)")},
		pdfJSAssetPathPrefix + "wasm/qcms_bg.wasm": {Data: []byte("\x00asm\x01\x00\x00\x00")},
	}
}

// spaResponseFromFS drives newSPAHandlerFor over a caller-supplied filesystem,
// so a test can assert the real routing without depending on the embedded SPA.
func spaResponseFromFS(t *testing.T, fsys fs.FS, target string) *httptest.ResponseRecorder {
	t.Helper()
	handler := newSPAHandlerFor(fsys)
	require.NotNil(t, handler)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// TestSpaServedWithCSP is spec test 68.
func TestSpaServedWithCSP(t *testing.T) {
	want := specSPAPolicy(t)

	// "/index.html" is deliberately absent: http.FileServer answers it with a
	// 301 to "/" (pre-existing stdlib behaviour, unrelated to this feature), so
	// including it would assert a redirect rather than the shell.
	for _, target := range []string{"/", "/library", "/chat/some-session"} {
		t.Run(target, func(t *testing.T) {
			rec := spaResponse(t, target)
			require.Equal(t, http.StatusOK, rec.Code,
				"the shell must be served, or the header assertions below describe an error page")

			policies := rec.Header().Values("Content-Security-Policy")
			require.Len(t, policies, 1,
				"exactly one Content-Security-Policy header. Two are INTERSECTED rather than "+
					"merged, so a duplicate is not an error — it silently makes the effective "+
					"policy something neither string states")
			assert.Equal(t, want, policies[0],
				"FR-019b: the SPA shell must carry ADR-067 §10.7's policy byte for byte")
		})
	}
}

// TestSpaCsp_AbsorbsTheFramingControl pins FR-006b specifically.
//
// It is a separate assertion from the byte comparison above because the two
// fail for different reasons and the failure message should say which: this one
// fires when someone lands a policy that is perfectly reasonable and drops the
// one directive that stops a previewed page rendering genuine Omnipus chrome
// inside attacker-authored content.
func TestSpaCsp_AbsorbsTheFramingControl(t *testing.T) {
	rec := spaResponse(t, "/")
	policy := rec.Header().Get("Content-Security-Policy")

	assert.Contains(t, policy, "frame-ancestors 'none'",
		"FR-006b: §10.3 contains frame-src 'self', so a previewed page may embed any gateway "+
			"page including the real SPA. The control belongs on the FRAMED resource")
	assert.Len(t, rec.Header().Values("Content-Security-Policy"), 1,
		"the framing directive must be ABSORBED into the §10.7 string, not sent as a second header")
}

// --- test 119 — TestSpaCsp_DirectiveFloor ----------------------------------

// spaCSPFloorViolations reports every way policy fails MV-25's directive floor.
//
// The floor, from the round-4 fix that created MV-25: no 'unsafe-eval',
// object-src 'none', base-uri 'none', frame-ancestors 'none', an explicit
// connect-src, and — because FR-019c's worker requirement is satisfied or
// defeated here — an explicit worker-src. A wide-open default-src is rejected
// outright: it is the specific policy that satisfied every earlier form of this
// requirement while granting everything.
//
// Returns a list rather than a bool so a failure names what is missing instead
// of saying "policy invalid".
func spaCSPFloorViolations(policy string) []string {
	directives := map[string]string{}
	for _, part := range strings.Split(policy, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		directives[strings.ToLower(fields[0])] = strings.Join(fields[1:], " ")
	}

	var violations []string
	if strings.Contains(strings.ToLower(policy), "'unsafe-eval'") {
		violations = append(violations,
			"'unsafe-eval' is present — FR-019a excludes PDF.js's scripting interpreter from "+
				"the build precisely so a reintroduced eval path fails loudly here")
	}
	for _, want := range []struct{ directive, value string }{
		{"object-src", "'none'"},
		{"base-uri", "'none'"},
		{"frame-ancestors", "'none'"},
	} {
		if got, ok := directives[want.directive]; !ok || got != want.value {
			violations = append(violations,
				want.directive+" must be exactly "+want.value+"; got "+directiveOrAbsent(directives, want.directive))
		}
	}
	for _, directive := range []string{"connect-src", "worker-src", "script-src", "default-src"} {
		value, ok := directives[directive]
		if !ok {
			violations = append(violations, directive+" is absent — it must be stated explicitly")
			continue
		}
		if strings.Contains(value, "*") {
			violations = append(violations, directive+" is wide open ("+value+")")
		}
	}
	return violations
}

func directiveOrAbsent(directives map[string]string, name string) string {
	if v, ok := directives[name]; ok {
		return "\"" + v + "\""
	}
	return "(absent)"
}

// TestSpaCsp_DirectiveFloor is spec test 119. Two halves, and the second is the
// point.
func TestSpaCsp_DirectiveFloor(t *testing.T) {
	t.Run("the shipped shell passes the floor", func(t *testing.T) {
		rec := spaResponse(t, "/")
		policy := rec.Header().Get("Content-Security-Policy")
		require.NotEmpty(t, policy, "FR-019b: the shell is served with no policy at all")

		assert.Empty(t, spaCSPFloorViolations(policy),
			"MV-21/MV-25: the SPA shell's policy fails the directive floor")
	})

	// THE HALF THAT MAKES THE OTHER HALF MEAN ANYTHING. Every row is a policy
	// that a "there is a policy, and it has no unsafe-eval" check would accept.
	t.Run("the same checker rejects each named mutation", func(t *testing.T) {
		base := specSPAPolicy(t)

		mutations := []struct {
			name   string
			policy string
			why    string
		}{
			{
				name:   "wide open",
				policy: "default-src *",
				why: "the exact policy round 4 named: it satisfied FR-019b, the old MV-21, " +
					"test 68 and AC-15.9 at once while granting everything the policy withholds",
			},
			{
				name:   "framing control removed",
				policy: strings.ReplaceAll(base, "; frame-ancestors 'none'", ""),
				why:    "FR-006b — a previewed page could render the real SPA inside itself",
			},
			{
				name:   "eval added",
				policy: strings.Replace(base, "script-src 'self'", "script-src 'self' 'unsafe-eval'", 1),
				why:    "FR-019a's runtime enforcement: a future PDF.js reintroducing an eval path must fail loudly",
			},
			{
				name:   "worker source dropped",
				policy: strings.Replace(base, "worker-src 'self' blob:; ", "", 1),
				why: "FR-019b and FR-019c defeat each other silently here: if worker-src does not " +
					"permit the built worker URL, PDF.js does not fail — it parses on the main thread",
			},
			{
				name:   "object-src relaxed",
				policy: strings.Replace(base, "object-src 'none'", "object-src 'self'", 1),
				why:    "MV-25 fixes object-src at 'none'; a plugin document is another script host",
			},
			{
				name:   "base-uri relaxed",
				policy: strings.Replace(base, "base-uri 'none'", "base-uri 'self'", 1),
				why:    "MV-25 fixes base-uri at 'none'",
			},
			{
				name:   "connect-src wide open",
				policy: strings.Replace(base, "connect-src 'self'", "connect-src *", 1),
				why:    "MV-25 requires an explicit connect-src; '*' states it and states nothing",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				require.NotEqual(t, base, m.policy,
					"the mutation did not change the policy — the spec string it edits must have "+
						"drifted, so this row is asserting the unmutated value")
				assert.NotEmpty(t, spaCSPFloorViolations(m.policy),
					"the floor checker accepted a policy it must reject. %s", m.why)
			})
		}
	})
}

// --- FR-018b — the backend half of "a missing asset fails visibly" ---------

// TestSpaEmbed_PdfJsAssetMissingIs404 is the server side of spec test 82.
//
// newSPAHandler answers any unmatched path with index.html and HTTP 200, which
// is right for client-side routes and catastrophic for an asset: PDF.js fetches
// its character maps per DOCUMENT and checks the status, so a 200 carrying an
// HTML page is indistinguishable from success. The document then renders BLANK
// with nothing naming the cause.
//
// The positive control is the second subtest: an ordinary SPA route must STILL
// get index.html and 200, or a handler that 404'd everything would pass the
// first assertion and break the whole application.
func TestSpaEmbed_PdfJsAssetMissingIs404(t *testing.T) {
	// The names below are deliberately IMPOSSIBLE, not merely plausible.
	//
	// An earlier version listed the REAL asset names (cmaps/Adobe-Japan1-UCS2.bcmap,
	// wasm/openjpeg.wasm, pdf.worker.min.mjs). That passed on a developer
	// machine, where pkg/gateway/spa holds only the placeholder index.html, and
	// FAILED IN CI — because CI's test job runs `npm run build` and copies
	// dist/spa into the embed dir, so those assets genuinely ARE embedded there
	// and 200 was the correct answer. The test was asserting "this file is
	// missing" while never establishing that it was missing; it measured which
	// machine it ran on.
	//
	// A name that cannot be produced by any build is absent in both worlds, so
	// this now tests the handler instead of the environment.
	missing := []string{
		"/pdfjs/cmaps/__omnipus_absent_test_asset__.bcmap",
		"/pdfjs/standard_fonts/__omnipus_absent_test_asset__.pfb",
		"/pdfjs/wasm/__omnipus_absent_test_asset__.wasm",
		"/pdfjs/iccs/__omnipus_absent_test_asset__.icc",
		"/pdfjs/__omnipus_absent_test_asset__.mjs",
		"/pdfjs/nested/deeper/__omnipus_absent_test_asset__.json",
	}

	// No runtime "is it really absent?" guard is needed: the sentinel component
	// cannot be produced by vite, by pdfjs-dist, or by the asset-copy plugin, so
	// absence is guaranteed by the name rather than by the environment. The
	// vacuity risk that remains — a handler that 404s EVERYTHING would satisfy
	// every assertion above — is covered by the positive control subtest below,
	// which requires an ordinary client-side route to still get the shell.

	for _, target := range missing {
		t.Run(target, func(t *testing.T) {
			rec := spaResponse(t, target)
			assert.Equal(t, http.StatusNotFound, rec.Code,
				"FR-018b: an un-embedded PDF.js runtime asset must 404. A 200 carrying "+
					"index.html is what makes a blank CJK PDF unattributable — PDF.js receives "+
					"an HTML page, renders nothing, and reports no error")
			assert.NotContains(t, rec.Body.String(), "<title>",
				"the body must not be the SPA shell")
		})
	}

	t.Run("positive control: a client-side route still gets the shell", func(t *testing.T) {
		rec := spaResponse(t, "/library/some/deep/route")
		require.Equal(t, http.StatusOK, rec.Code,
			"the SPA-routing fallback must survive — 404ing everything would pass the "+
				"assertions above and break the application")
		assert.Contains(t, rec.Body.String(), "<",
			"the shell's HTML must be served for a client-side route")
	})
}

// TestSpaEmbed_PdfJsPrefixMatchesTheBuild ties the Go constant to the build.
//
// The prefix exists in two languages with no shared source: vite.config.ts
// decides where the assets are EMITTED, embed.go decides where the 404 branch
// applies. A rename on either side leaves the guard watching a prefix nothing
// is served under — which silently restores the exact failure FR-018b removes,
// with no compile error and no other test failing.
func TestSpaEmbed_PdfJsPrefixMatchesTheBuild(t *testing.T) {
	raw, err := os.ReadFile("../../vite.config.ts")
	require.NoError(t, err, "vite.config.ts must be readable — it is this test's oracle")

	re := regexp.MustCompile(`PDFJS_ASSET_PREFIX\s*=\s*'([^']+)'`)
	m := re.FindSubmatch(raw)
	require.Len(t, m, 2,
		"PDFJS_ASSET_PREFIX not found in vite.config.ts; a missing oracle is not a pass")

	assert.Equal(t, string(m[1]), pdfJSAssetPathPrefix,
		"embed.go's 404 branch must guard the prefix the build actually emits under")
}

// --- the PDF.js worker's own policy (audited 2026-09-05) -------------------
//
// A dedicated worker loaded from a same-origin URL takes its Content-Security-
// Policy from ITS OWN response, not from the document that created it. That is
// what made the defect this block guards possible: the SPA handler served
// /pdfjs/pdf.worker.min.mjs with `script-src 'self'`, and `script-src 'self'`
// refuses WebAssembly.instantiate — so the pdfjs/wasm/ modules vite.config.ts
// deliberately ships (and fails the build without) could not compile. qcms has
// no JavaScript fallback, so every ICC and DeviceCMYK colour rendered with the
// crude device conversion, silently, in every released build.
//
// Measured in a real browser by tests/e2e/csp-assumptions.spec.ts test A2b,
// which reads the verdict INSIDE the worker's realm. These tests pin the server
// side of that fix so it cannot be undone by an edit that compiles.

// TestSpaCsp_PdfWorkerCarriesTheWasmPolicy is the fix, asserted end to end
// through the real handler.
//
// The second half is the point and is not decoration: the SHELL must still
// carry the unwidened policy. A "simplification" that moves 'wasm-unsafe-eval'
// onto spaContentSecurityPolicy would satisfy the first assertion perfectly
// while re-permitting WebAssembly on the main thread — which is precisely what
// the Shiki entry in embed.go tells the next reader not to do.
func TestSpaCsp_PdfWorkerCarriesTheWasmPolicy(t *testing.T) {
	// Driven over a fixture FS (not the embedded SPA) so the routing is tested
	// deterministically on any machine — the go-test gate embeds a stub SPA with
	// no PDF.js worker, so fetching the real embedded worker would 404. The
	// fixture carries the worker at its real path, so a broken route (missing or
	// mis-scoped header) still fails here. That the REAL build actually embeds
	// the worker is covered where the SPA is built: the embed-build/e2e gates and
	// tests/e2e/csp-assumptions.spec.ts (which reads the verdict in the worker's
	// own realm).
	fixture := spaFixtureFS()

	t.Run("the worker script is served with 'wasm-unsafe-eval'", func(t *testing.T) {
		rec := spaResponseFromFS(t, fixture, "/"+pdfJSWorkerPath)
		require.Equal(t, http.StatusOK, rec.Code,
			"the worker path must serve the embedded worker file, not fall through to a 404")

		policies := rec.Header().Values("Content-Security-Policy")
		require.Len(t, policies, 1,
			"still exactly one header: two are INTERSECTED, which would silently strip the "+
				"very allowance this response exists to carry")
		assert.Contains(t, policies[0], "script-src 'self' 'wasm-unsafe-eval';",
			"PDF.js's qcms/jbig2/openjpeg modules cannot compile without it, and qcms has no "+
				"JavaScript fallback — the symptom is silently wrong colour, not an error")
	})

	t.Run("every other SPA response keeps the unwidened policy", func(t *testing.T) {
		for _, target := range []string{"/", "/library", "/assets", "/" + pdfJSAssetPathPrefix + "wasm/qcms_bg.wasm"} {
			rec := spaResponseFromFS(t, fixture, target)
			assert.NotContains(t, rec.Header().Get("Content-Security-Policy"), "wasm-unsafe-eval",
				"%s must NOT be able to compile WebAssembly: the allowance is scoped to the "+
					"PDF.js worker realm, which is what keeps Shiki's pure-JavaScript engine a "+
					"policy requirement rather than a convention", target)
		}
	})
}

// TestSpaCsp_WasmPolicyDiffersByExactlyTheKeyword pins that the two strings
// cannot drift apart.
//
// The worker policy is DERIVED from the shell policy, so a future directive
// added to the shell reaches the worker automatically. This test states the
// other half of that contract: the derivation adds one token and changes
// nothing else. Without it, a hand-edited second literal could quietly widen or
// narrow some unrelated directive for the worker alone.
func TestSpaCsp_WasmPolicyDiffersByExactlyTheKeyword(t *testing.T) {
	require.NotEqual(t, spaContentSecurityPolicy, spaPdfWorkerContentSecurityPolicy,
		"the derivation produced an identical string — strings.Replace matched nothing, and "+
			"the worker would silently keep the policy that broke it")
	assert.Equal(t,
		spaContentSecurityPolicy,
		strings.Replace(spaPdfWorkerContentSecurityPolicy, " 'wasm-unsafe-eval'", "", 1),
		"the worker policy must be the shell policy plus 'wasm-unsafe-eval' and nothing else")
}

// TestSpaCsp_WasmKeywordIsNotUnsafeEval is a fact about CSP that this fix rests
// on, asserted rather than believed.
//
// 'wasm-unsafe-eval' permits WebAssembly compilation and nothing else; it does
// not imply 'unsafe-eval', and eval/new Function stay refused. The directive
// floor's own check is a SUBSTRING test for "'unsafe-eval'", so the two
// keywords being distinguishable by it is load-bearing: if they were not, this
// fix would have silently disabled the floor's eval guard for the worker.
func TestSpaCsp_WasmKeywordIsNotUnsafeEval(t *testing.T) {
	assert.NotContains(t, "'wasm-unsafe-eval'", "'unsafe-eval'",
		"the floor checker distinguishes the two by substring; if this ever became true, "+
			"spaCSPFloorViolations would stop catching a real 'unsafe-eval'")

	assert.Empty(t, spaCSPFloorViolations(spaPdfWorkerContentSecurityPolicy),
		"the worker's policy must still pass MV-25's directive floor in full")

	// The control: the floor still rejects the real thing on the worker policy.
	assert.NotEmpty(t,
		spaCSPFloorViolations(strings.Replace(
			spaPdfWorkerContentSecurityPolicy, "'wasm-unsafe-eval'", "'unsafe-eval'", 1)),
		"a checker that accepts 'unsafe-eval' here would make the assertion above vacuous")
}

// TestSpaCsp_PdfWorkerPathIsTheOneVitePublishes ties the Go constant to the
// build, for the same reason TestSpaEmbed_PdfJsPrefixMatchesTheBuild does.
//
// The worker's filename lives in three places with no shared source: the vite
// plugin that copies it, the SPA's `new Worker(...)` call, and this handler's
// policy branch. A rename in the first two would leave the branch guarding a
// path nothing is served under — the worker would silently drop back to
// `script-src 'self'` and ICC colour would silently regress, with no compile
// error and no other test failing.
func TestSpaCsp_PdfWorkerPathIsTheOneVitePublishes(t *testing.T) {
	raw, err := os.ReadFile("../../vite.config.ts")
	require.NoError(t, err, "vite.config.ts must be readable — it is this test's oracle")

	re := regexp.MustCompile("PDFJS_WORKER_FILE\\s*=\\s*`\\$\\{PDFJS_ASSET_PREFIX\\}([^`]+)`")
	m := re.FindSubmatch(raw)
	require.Len(t, m, 2,
		"PDFJS_WORKER_FILE not found in vite.config.ts; a missing oracle is not a pass")

	assert.Equal(t, pdfJSAssetPathPrefix+string(m[1]), pdfJSWorkerPath,
		"the policy branch must name the worker file the build actually emits")

	spa, err := os.ReadFile("../../src/components/library/preview/LibraryPdfPreview.tsx")
	require.NoError(t, err, "the PDF viewer must be readable — it is this test's second oracle")
	assert.Contains(t, string(spa), "pdf.worker.min.mjs",
		"the SPA must still construct its worker from the file this policy branch covers")
}
