// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-083 D-C / D9 step 4 — the external video host.
//
// Spec tests 59 (TestSpaCsp_FrameSrcAllowListMatchesServedHeader), 61
// (TestSpaCsp_FrameAncestorsAndImgSrcUnchanged), 62
// (TestSpaCsp_OperatorCanDeclineTheHost) and 114
// (TestSpaPdfWorkerCsp_InheritsTheFrameHost), plus the resolver's own unit
// tests. Test 60 is the extension to TestSpaCsp_DirectiveFloor in
// embed_csp_test.go.
//
// THE THREE FALSE GREENS THIS FILE IS WRITTEN AGAINST, in the spec's words:
//
//  1. "An allow-list equality test that compares a value to itself." If both
//     sides import the same constant the assertion is a tautology. So test 59
//     reads the SERVED Content-Security-Policy header off the real SPA handler
//     and the SERVED JSON off the real state handler, and compares those two —
//     and each install's expected list is ALSO written as a literal in the test
//     body, so the pair cannot agree on a value neither should hold.
//
//  2. "A test that passes because the host was never added." Test 62 therefore
//     carries BOTH halves in one body: the default install's header contains
//     the host AND equals §10.7's literal line; the emptied install's contains
//     no external host AND differs from that line. The negative half alone
//     passes on a build where this whole change was reverted.
//
//  3. A neighbouring directive weakened while nobody was looking. Test 61 pins
//     `frame-ancestors` and `img-src` byte for byte. It passes before this
//     change as well as after — it is a guard, never evidence that step 4
//     landed.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// --- shared helpers --------------------------------------------------------

// cspDirectiveSources returns the source expressions of one directive.
//
// Written here rather than reusing spaCSPFloorViolations' parser because the
// two want different things: the floor wants "is it stated and is it wide
// open", this wants the exact source LIST, in order, to compare against another
// served response.
func cspDirectiveSources(t *testing.T, policy, directive string) []string {
	t.Helper()
	for _, part := range strings.Split(policy, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 || !strings.EqualFold(fields[0], directive) {
			continue
		}
		return fields[1:]
	}
	t.Fatalf("directive %q is absent from the served policy %q — an absent directive is not "+
		"an empty one, and a test that treated it as empty would pass on a deleted policy",
		directive, policy)
	return nil
}

// replaceFrameSrc rewrites (or, with an empty replacement, removes) the whole
// frame-src segment of a policy. Used by the directive-floor mutation table.
func replaceFrameSrc(policy, replacement string) string {
	parts := strings.Split(policy, "; ")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(strings.TrimSpace(part), "frame-src") {
			if replacement != "" {
				out = append(out, replacement)
			}
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, "; ")
}

// videoEmbedTestInstall is one operator configuration, with the allow-list the
// test itself expects both surfaces to report for it.
type videoEmbedTestInstall struct {
	name        string
	configured  *[]string
	wantHosts   []string
	description string
}

// videoEmbedInstalls are the configurations every cross-surface assertion runs
// over.
//
// The middle row exists so nothing here can pass on an implementation that
// hardcodes the shipped default and ignores config: a narrowed list that is
// neither the default nor empty has to survive end to end, in the served
// header AND in the served JSON.
func videoEmbedInstalls() []videoEmbedTestInstall {
	emptied := []string{}
	narrowed := []string{"videos.example.org", "www.youtube-nocookie.com"}
	return []videoEmbedTestInstall{
		{
			name:        "default install",
			configured:  nil,
			wantHosts:   []string{"www.youtube-nocookie.com"},
			description: "the key is absent, so the shipped default applies (EMB-075: exactly one entry)",
		},
		{
			name:        "operator narrowed the list",
			configured:  &narrowed,
			wantHosts:   []string{"videos.example.org", "www.youtube-nocookie.com"},
			description: "a list that is neither the default nor empty must survive to both surfaces",
		},
		{
			name:        "operator declined the external host",
			configured:  &emptied,
			wantHosts:   []string{},
			description: "EMB-081: emptied means no external frame source anywhere",
		},
	}
}

// videoEmbedTestAPI builds a real restAPI over a real AgentLoop carrying cfgHosts.
func videoEmbedTestAPI(t *testing.T, cfgHosts *[]string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:            "127.0.0.1",
			Port:            8080,
			VideoEmbedHosts: cfgHosts,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{agentLoop: al, homePath: tmpDir}
}

// servedReaderHosts drives the REAL GET /api/v1/state handler and returns the
// allow-list the browser would actually receive.
//
// It reads the field back out of the encoded JSON rather than out of a Go
// struct the test built, because the encoding is where an "always present, `[]`
// when declined" promise is kept or broken: a field dropped by `omitempty`
// reaches the reader as `undefined`, which it cannot tell from a failed fetch.
func servedReaderHosts(t *testing.T, api *restAPI) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	api.HandleState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/state", nil))
	require.Equal(t, http.StatusOK, rec.Code, "GET /api/v1/state: %s", rec.Body.String())

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	raw, present := body["video_embed_hosts"]
	require.True(t, present,
		"CW-3: video_embed_hosts must always be present on the state payload. Absent reaches "+
			"the reader as undefined, which it cannot tell from a fetch failure — and the "+
			"reader has to know the difference to say the host is not allowed rather than "+
			"drawing a play control that does nothing")

	var hosts []string
	require.NoError(t, json.Unmarshal(raw, &hosts))
	require.NotNil(t, hosts,
		"a JSON null is not an empty list — the declined state must serialise as []")
	return hosts
}

// servedFramePolicy drives the REAL SPA handler over api's live config and
// returns the served document policy.
func servedFramePolicy(t *testing.T, api *restAPI) string {
	t.Helper()
	rec := spaResponseWithHosts(t, "/", func() []string {
		return ResolveVideoEmbedHosts(api.agentLoop.GetConfig())
	})
	require.Equal(t, http.StatusOK, rec.Code)
	policies := rec.Header().Values("Content-Security-Policy")
	require.Len(t, policies, 1, "exactly one Content-Security-Policy header — two are INTERSECTED")
	return policies[0]
}

// --- test 59 — EMB-080 -----------------------------------------------------

// TestSpaCsp_FrameSrcAllowListMatchesServedHeader is spec test 59.
//
// EMB-080: "The allow-list the reader uses MUST equal the allow-list in the
// served policy, asserted by a test that reads the served header."
//
// If the two ever drift, the symptom is one of the two shapes this project has
// banned outright: a blank frame with nothing naming the cause, or a play
// control that does nothing when pressed. Neither produces an error anywhere.
func TestSpaCsp_FrameSrcAllowListMatchesServedHeader(t *testing.T) {
	for _, install := range videoEmbedInstalls() {
		t.Run(install.name, func(t *testing.T) {
			api := videoEmbedTestAPI(t, install.configured)

			readerHosts := servedReaderHosts(t, api)
			policy := servedFramePolicy(t, api)
			frameSrc := cspDirectiveSources(t, policy, "frame-src")

			// The test's OWN oracle first, so the two surfaces cannot agree on
			// a value neither should hold. Without this, an implementation that
			// returned the same wrong list to both would pass.
			assert.Equal(t, install.wantHosts, readerHosts,
				"the reader's allow-list — %s", install.description)

			// Then the equality EMB-080 actually asks for, built from what the
			// STATE endpoint served rather than from any shared constant. The
			// hostname→origin mapping is written out here rather than borrowed
			// from production, so a bug in that mapping fails this test instead
			// of being applied identically to both sides of it.
			want := []string{"'self'"}
			for _, host := range readerHosts {
				want = append(want, "https://"+host)
			}
			assert.Equal(t, want, frameSrc,
				"EMB-080: frame-src must be 'self' plus exactly the hosts the reader was "+
					"given. Served policy: %s", policy)
		})
	}
}

// --- test 61 — the neighbours that must not move ---------------------------

// TestSpaCsp_FrameAncestorsAndImgSrcUnchanged is spec test 61, and it is a PIN:
// both directives already hold these values, so it passed before this change.
// It is worth nothing as evidence that step 4 landed, and everything as a guard
// that step 4 did not disturb the two directives it sits next to.
//
// WHY THESE TWO AND NOT OTHERS.
//
//   - `frame-ancestors 'none'` (FR-006b) is the compensating control for the
//     isolated preview policy's own `frame-src 'self'`: a previewed page may
//     embed any gateway page, so the control belongs on the framed resource,
//     which is the SPA. It shares a string with the directive this change edits
//     and NOTHING else — `frame-src` governs what the SPA frames OUTWARD,
//     `frame-ancestors` what may frame the SPA INWARD. No browser derives one
//     from the other and permitting an outward source grants an embedder
//     nothing. That is the argument; this assertion, and E2E test A6 re-run
//     verbatim, are the evidence.
//   - `img-src` is D-C's other half. No external images means no provider
//     thumbnail, which is why the click-to-play placeholder is drawn locally.
//     Adding the provider's image host would put back exactly the per-embed
//     contact click-to-play exists to prevent, and it would look like a
//     cosmetic improvement while doing it.
//
// Asserted on the SERVED header for every install, because a policy that is
// correct by default and wrong once an operator edits the allow-list is still
// wrong.
func TestSpaCsp_FrameAncestorsAndImgSrcUnchanged(t *testing.T) {
	for _, install := range videoEmbedInstalls() {
		t.Run(install.name, func(t *testing.T) {
			policy := servedFramePolicy(t, videoEmbedTestAPI(t, install.configured))

			assert.Contains(t, policy, "frame-ancestors 'none'",
				"FR-006b: the framing control must survive the frame-src edit byte for byte")
			assert.Equal(t, []string{"'none'"}, cspDirectiveSources(t, policy, "frame-ancestors"),
				"frame-ancestors must be exactly 'none' — a source added here would let a "+
					"previewed page render genuine Omnipus chrome inside attacker-authored content")

			assert.Contains(t, policy, "img-src 'self' data: blob:;",
				"ADR-083 D-C: no external image host. There is no provider thumbnail, which is "+
					"why the placeholder is drawn locally")
			assert.Equal(t, []string{"'self'", "data:", "blob:"},
				cspDirectiveSources(t, policy, "img-src"),
				"an external image source here re-creates the per-embed provider contact that "+
					"click-to-play exists to prevent")
		})
	}
}

// --- test 62 — EMB-081 -----------------------------------------------------

// TestSpaCsp_OperatorCanDeclineTheHost is spec test 62.
//
// BOTH HALVES IN ONE BODY, and that is the whole design of this test. "Empty
// setting → no external host in the served policy" is satisfied by a build
// where the host does not exist at all — i.e. by this entire change reverted.
// So the default install's header is asserted to CONTAIN the host and to equal
// ADR-067 §10.7's literal line in the same test, and the emptied install's to
// contain no external host and to DIFFER from that line.
func TestSpaCsp_OperatorCanDeclineTheHost(t *testing.T) {
	// The oracle is the specification document, read at runtime — the same
	// oracle TestSpaServedWithCSP uses, for the same reason: a literal typed
	// into this file would assert only that two strings written by one person
	// at one moment agree.
	defaultLine := specSPAPolicy(t)

	emptied := []string{}
	declinedPolicy := servedFramePolicy(t, videoEmbedTestAPI(t, &emptied))
	defaultPolicy := servedFramePolicy(t, videoEmbedTestAPI(t, nil))

	t.Run("the default install carries the host and matches §10.7", func(t *testing.T) {
		assert.Contains(t, defaultPolicy, "https://www.youtube-nocookie.com",
			"the shipped default must actually add the host — without this clause the "+
				"emptied-install assertions below pass on a build that never added it")
		assert.Equal(t, defaultLine, defaultPolicy,
			"the default install's served policy must equal ADR-067 §10.7's literal line. "+
				"That line is edited in the same change as this policy, or CI fails on an "+
				"oracle whose source is a Markdown file")
	})

	t.Run("the emptied install carries no external host and differs from §10.7", func(t *testing.T) {
		assert.NotContains(t, declinedPolicy, "youtube",
			"EMB-081: an operator who empties gateway.video_embed_hosts must get a policy "+
				"with no external frame host at all")
		assert.Equal(t, []string{"'self'"}, cspDirectiveSources(t, declinedPolicy, "frame-src"),
			"frame-src must fall back to 'self' alone, not to an absent directive: absent "+
				"would inherit default-src and be indistinguishable from a dropped policy")
		assert.NotEqual(t, defaultLine, declinedPolicy,
			"the declined install must serve a DIFFERENT string from the default. If these "+
				"were equal the config key would be inert while appearing to save")

		// The rest of the policy is not collateral damage of the removal.
		assert.Contains(t, declinedPolicy, "frame-ancestors 'none'")
		assert.Empty(t, spaCSPFloorViolations(declinedPolicy),
			"the declined install's policy must still pass MV-25's directive floor")
	})

	t.Run("and the reader is told, so it does not draw a dead control", func(t *testing.T) {
		// The failure mode this clause exists for: a served policy that blocks
		// the frame while the browser still renders a play control, because
		// nobody told the browser. Pressing it would do nothing at all — the
		// undetectable-fallback shape this project removed from the live
		// browser view once already.
		assert.Equal(t, []string{}, servedReaderHosts(t, videoEmbedTestAPI(t, &emptied)),
			"an emptied allow-list must reach the reader as an empty list, so it renders the "+
				"embed as a link and says the host is not allowed")
	})
}

// --- test 114 — the second, derived policy ---------------------------------

// TestSpaPdfWorkerCsp_InheritsTheFrameHost is spec test 114.
//
// A SECOND policy string is served on exactly one path — the PDF.js worker —
// and it is DERIVED from the document policy. Adding a frame host propagates
// into it automatically, which is intended and inert (a worker frames nothing),
// but "exactly the allow-listed hosts and nothing else" is a claim about BOTH
// strings and nothing asserted it. Both are read off SERVED responses here
// rather than from one string assumed to cover the other.
func TestSpaPdfWorkerCsp_InheritsTheFrameHost(t *testing.T) {
	fixture := spaFixtureFS()

	for _, install := range videoEmbedInstalls() {
		t.Run(install.name, func(t *testing.T) {
			hosts := install.wantHosts
			accessor := videoEmbedHostsFunc(func() []string { return hosts })

			workerRec := spaResponseFromFSWithHosts(t, fixture, "/"+pdfJSWorkerPath, accessor)
			require.Equal(t, http.StatusOK, workerRec.Code,
				"the worker path must serve the worker file, not fall through to a 404")
			workerPolicies := workerRec.Header().Values("Content-Security-Policy")
			require.Len(t, workerPolicies, 1,
				"still exactly one header on the worker path: two are INTERSECTED")
			workerPolicy := workerPolicies[0]

			shellRec := spaResponseFromFSWithHosts(t, fixture, "/", accessor)
			shellPolicy := shellRec.Header().Get("Content-Security-Policy")

			assert.Equal(t,
				cspDirectiveSources(t, shellPolicy, "frame-src"),
				cspDirectiveSources(t, workerPolicy, "frame-src"),
				"the derived worker policy must carry the SAME frame-src as the document it "+
					"was derived from — a second string that drifts is the defect this "+
					"derivation exists to prevent")

			// The derivation still adds exactly one token and changes nothing
			// else, now that its input varies per install.
			assert.Equal(t, shellPolicy,
				strings.Replace(workerPolicy, " 'wasm-unsafe-eval'", "", 1),
				"the worker policy must be the served document policy plus 'wasm-unsafe-eval' "+
					"and nothing else")
			assert.Contains(t, workerPolicy, "script-src 'self' 'wasm-unsafe-eval';",
				"PDF.js's qcms module has no JavaScript fallback; without this the symptom is "+
					"silently wrong colour, not an error")
			assert.Empty(t, spaCSPFloorViolations(workerPolicy),
				"the worker policy must still pass MV-25's directive floor for every install")
		})
	}
}

// --- the resolver ----------------------------------------------------------

// TestResolveVideoEmbedHosts_ThreeStates pins the distinction the pointer type
// exists for.
//
// The middle state is the one that matters: a nil pointer (key absent, e.g. an
// install upgraded across this change) must resolve to the shipped default, and
// a non-nil pointer to an empty slice (the operator declined) must resolve to
// nothing. Collapse those two and one of the two failures follows — either an
// upgraded install silently loses the feature, or an operator's explicit "off"
// is silently re-enabled.
func TestResolveVideoEmbedHosts_ThreeStates(t *testing.T) {
	emptied := []string{}
	narrowed := []string{"videos.example.org"}

	t.Run("absent key resolves to the shipped default", func(t *testing.T) {
		assert.Equal(t, []string{"www.youtube-nocookie.com"},
			ResolveVideoEmbedHosts(&config.Config{}),
			"an install upgraded across this change must resolve identically to a fresh one")
		assert.Equal(t, []string{"www.youtube-nocookie.com"}, ResolveVideoEmbedHosts(nil),
			"a nil config is the same absent-key case, not a declined one")
	})

	t.Run("an explicitly empty list is the declined state", func(t *testing.T) {
		got := ResolveVideoEmbedHosts(&config.Config{
			Gateway: config.GatewayConfig{VideoEmbedHosts: &emptied},
		})
		assert.Equal(t, []string{}, got)
		assert.NotNil(t, got, "the result is always non-nil so it serialises as [] and not null")
	})

	t.Run("a configured list is used verbatim", func(t *testing.T) {
		assert.Equal(t, []string{"videos.example.org"}, ResolveVideoEmbedHosts(&config.Config{
			Gateway: config.GatewayConfig{VideoEmbedHosts: &narrowed},
		}))
	})

	t.Run("the shipped default is exactly one entry", func(t *testing.T) {
		// EMB-075's "whose shipped default contains exactly one entry", asserted
		// rather than assumed: a second default host would be a second external
		// party in every install's policy.
		assert.Len(t, DefaultVideoEmbedHosts, 1)
	})
}

// TestResolveVideoEmbedHosts_RejectsUnsafeEntries is the header-integrity test.
//
// Every surviving entry is concatenated into an HTTP response header. An entry
// containing a space, a semicolon or a quote would not look wrong — it would
// silently restructure the Content-Security-Policy around it, and the operator
// who typed it would see no error. `frame-src 'self' evil.example; script-src *`
// is one config edit away from a policy that grants everything.
//
// The final subtest is the control: a legitimate hostname must still survive,
// or "rejects everything" would pass every assertion above it.
func TestResolveVideoEmbedHosts_RejectsUnsafeEntries(t *testing.T) {
	rejected := map[string]string{
		"policy injection with a semicolon": "evil.example; script-src *",
		"policy injection with a space":     "evil.example 'unsafe-inline'",
		"a bare wildcard":                   "*",
		"a wildcard sub-domain":             "*.youtube-nocookie.com",
		"a scheme":                          "https://www.youtube-nocookie.com",
		"a port":                            "www.youtube-nocookie.com:443",
		"a path":                            "www.youtube-nocookie.com/embed",
		"credentials":                       "user@evil.example",
		"a single label":                    "localhost",
		"an IPv4 literal":                   "169.254.169.254",
		"a leading hyphen":                  "-evil.example",
		"a newline":                         "good.example\nframe-src *",
		"a quote":                           "'unsafe-inline'",
		"a backslash":                       "evil.example\\x",
	}
	for name, entry := range rejected {
		t.Run(name, func(t *testing.T) {
			list := []string{entry}
			hosts := ResolveVideoEmbedHosts(&config.Config{
				Gateway: config.GatewayConfig{VideoEmbedHosts: &list},
			})
			assert.Empty(t, hosts, "%q must never reach the allow-list", entry)

			// And the policy built from it must be intact, which is the
			// property the rejection exists for — asserting only "the entry was
			// dropped" would miss a rejection that dropped it AFTER rendering.
			policy := spaCSPForVideoHosts(hosts)
			assert.Equal(t, []string{"'self'"}, cspDirectiveSources(t, policy, "frame-src"))
			assert.Empty(t, spaCSPFloorViolations(policy),
				"the policy built from a refused entry must still pass the directive floor")
		})
	}

	t.Run("control: a legitimate host still survives, alongside a refused one", func(t *testing.T) {
		list := []string{"evil.example; script-src *", "Www.YouTube-NoCookie.com", "  ",
			"www.youtube-nocookie.com"}
		hosts := ResolveVideoEmbedHosts(&config.Config{
			Gateway: config.GatewayConfig{VideoEmbedHosts: &list},
		})
		assert.Equal(t, []string{"www.youtube-nocookie.com"}, hosts,
			"a valid entry must survive (lower-cased, trimmed, de-duplicated) while the "+
				"injection beside it is dropped — a resolver that refused everything would "+
				"pass every assertion above and silently disable the feature")
	})

	t.Run("the list is capped at the wire schema's maxItems", func(t *testing.T) {
		list := make([]string, 0, maxVideoEmbedHosts+4)
		for i := range maxVideoEmbedHosts + 4 {
			list = append(list, "host"+string(rune('a'+i))+".example")
		}
		hosts := ResolveVideoEmbedHosts(&config.Config{
			Gateway: config.GatewayConfig{VideoEmbedHosts: &list},
		})
		assert.Len(t, hosts, maxVideoEmbedHosts,
			"AppState.yaml caps the list at maxItems; a longer list would be served in the "+
				"policy but refused to the reader by its own schema validator")
	})
}

// TestSpaCSPForVideoHosts_BasePolicyStillCarriesTheGuardedDirective is a
// FIXTURE-SANITY check, not a proof of spaCSPForVideoHosts's panic — despite
// this test's former name (…PanicsIfTheDirectiveVanishes) claiming the
// latter. It never calls spaCSPForVideoHosts in a state that panics, and
// never asserts require.Panics/assert.Panics anywhere. Renamed rather than
// rewritten to actually panic, because it CANNOT be made to: unlike its
// sibling withWasmCompilation(policy string), spaCSPForVideoHosts reads the
// package-level spaBaseContentSecurityPolicy CONSTANT directly rather than
// accepting the base policy as a parameter (see embed.go), so there is no
// legal way from a test in this package to hand it a policy string missing
// spaCSPFrameSrcSelfOnly and observe the panic — doing that would require
// either mutating a Go constant (impossible) or changing spaCSPForVideoHosts's
// signature, which is embed.go, out of scope here.
//
// What this test actually verifies, and why each half still earns its place:
//  1. The REAL spaBaseContentSecurityPolicy constant still contains
//     spaCSPFrameSrcSelfOnly — i.e. the precondition spaCSPForVideoHosts's
//     panic guards is CURRENTLY satisfied. A silent drop of this substring
//     from the constant (a plausible unrelated CSP edit) is caught here
//     first, before any of this file's other tests would surface it as a
//     confusing downstream mismatch.
//  2. replaceFrameSrc — the LOCAL test-only double every other negative-case
//     table in this file uses to simulate "frame-src is gone" — genuinely
//     removes the substring when told to. If it did not, every test in this
//     file that builds a "directive vanished" fixture via replaceFrameSrc
//     would be silently exercising a no-op mutation instead.
//
// TESTABILITY GAP (reported, not fixed here — embed.go is out of scope for
// qa-lead): parameterizing spaCSPForVideoHosts the same way
// withWasmCompilation already is would make the real panic path unit-testable
// directly (call it with a policy string missing the directive, assert
// require.Panics). Until then, the panic branch itself has no direct test in
// this codebase, for either function — the same is true of withWasmCompilation
// today, despite it already being technically testable.
func TestSpaCSPForVideoHosts_BasePolicyStillCarriesTheGuardedDirective(t *testing.T) {
	require.Contains(t, spaBaseContentSecurityPolicy, spaCSPFrameSrcSelfOnly,
		"the base policy must contain the directive the builder edits, or every policy this "+
			"package serves is built by a no-op — and spaCSPForVideoHosts's own panic guard "+
			"exists specifically to catch this if it ever regresses at runtime")

	assert.NotContains(t, replaceFrameSrc(spaBaseContentSecurityPolicy, ""), spaCSPFrameSrcSelfOnly,
		"replaceFrameSrc (this file's own directive-removal double) must actually remove the "+
			"substring, or every OTHER test in this file that uses it to simulate a vanished "+
			"directive is silently exercising a no-op")
}
