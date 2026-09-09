# Defect list — HTML preview in the Library (2026-09-08)

Reproduced live in Playwright against the UAT instance on port 5177, build
`1bd993220`. Screenshots and the full evidence chain are recorded per defect.
**This file documents; it does not fix.**

---

### HP-1 — HTML preview never renders: the SPA's token minter is hardcoded `null`
**Severity:** high · **Area:** Library SPA · **Status:** Open · **REPRODUCED**

Opening any `.html` file in the Library preview pane shows:

> **Preview unavailable** — Omnipus could not get a preview link for this page.
> Rendering it needs the isolated preview endpoint, which this build does not
> serve yet.

**The message is misleading: the build DOES serve that endpoint.** The backend is
complete and correct end-to-end; only the SPA's client wrapper is missing.

**Verified backend (all observed, not inferred):**

| Step | Result |
|---|---|
| `GET /api/v1/library/{ws}/inline-disposition?path=x.html` | `200` — `renderer:"html"`, `disposition:"inline"`, `requires_sandbox:true` |
| `POST /api/v1/library/preview-token` (`scope:"file"`) | `201` — real token, 900s TTL, `url:/library-preview/{token}/{path}` |
| `GET /library-preview/{token}/x.html` | `200`, correct `Content-Type`, `Content-Disposition: inline`, full §10.3 CSP, correct bytes |
| Rendered in a real browser | **Renders perfectly** — styling, layout, colours all correct |

**Root cause — one line.** `src/components/library/LibraryPreviewPane.tsx:95`:

```ts
const PREVIEW_TOKEN_MINTER: MintLibraryPreviewToken | null = null
```

Its own doc comment states the reason plainly: *"THE MINT CLIENT DOES NOT EXIST
YET... its request/response schemas are already in `contracts/` and generated
above, but no `src/lib/api.ts` wrapper calls them, and this file does not own
`api.ts`."* It is deferred as wave-3 work (spec FR-003f).

**Why this shipped looking finished.** The comment ends: *"Tests inject their own
via the `mintPreviewToken` prop."* So every test supplies a working minter and
passes, while production passes nothing and gets `null` — the feature is green in
CI and dead for every user. This is the false-green shape `false-green-patterns.md`
describes: the test never exercises the production wiring.

**Credit where due:** the failure is HONEST. It renders an explicit, readable
"preview unavailable" state rather than a blank frame — exactly what FR-003c/
FR-003n require. The defect is that the state is permanent, and its text blames
the build for something the build actually does.

**Fix shape.** Add the `api.ts` wrapper for `POST /api/v1/library/preview-token`
(the generated types already exist) and assign it to `PREVIEW_TOKEN_MINTER`. The
comment confirms no consumer changes: *"nothing else in the SPA changes: no
consumer passes it."* The test that would have caught this is one that asserts
the PRODUCTION default is non-null.

---

### HP-2 — the isolation CSP emits an IPv6 source every browser rejects
**Severity:** low-medium · **Area:** pkg/gateway · **Status:** Open · **REPRODUCED**

Every HTML preview logs **six** console errors, one per directive:

> The source list for the Content Security Policy directive 'script-src'
> contains an invalid source: 'http://[::1]:5177'. It will be ignored.

**Verified cause.** `pkg/gateway/library_isolation_policy.go`'s
`libraryIsolationSources` expands a loopback origin to three aliases —
`127.0.0.1`, `localhost`, `::1` — bracketing the IPv6 literal. A bracketed IPv6
literal is not accepted in a CSP host-source, so browsers discard it.

**Two consequences:**
1. **Noise that hides signal.** Six errors on every preview. A real CSP violation
   arrives in the same console and is now much easier to miss — which is exactly
   what happened here: the genuine blocked-resource errors sat below six
   decoy errors.
2. **A real gap for IPv6 loopback users.** Someone reaching the gateway at
   `http://[::1]:5177` is NOT allowlisted, so the gateway's own subresources are
   blocked. Not the default path, but the alias exists precisely to support it,
   and it does not work.

**Fix shape.** Drop `::1` from the alias list, or emit it in whatever form the
CSP grammar accepts. Either way the byte-oracle test (MV-13) must be updated in
the same change, since it asserts the policy string exactly.

---

### Not a defect — the CSP itself behaves correctly

Tested with three purpose-built files, rendered through the real preview endpoint:

| Test file | Behaviour | Verdict |
|---|---|---|
| Self-contained markup + inline styles | renders fully | correct |
| Inline `<script>` | **runs** — DOM updated | correct (`'unsafe-inline'` is granted) |
| External CDN script + remote image | **both blocked**, named in console | correct and intended |

So a self-contained HTML page previews perfectly once HP-1 is fixed. A page that
depends on a CDN will still render without its external pieces — that is the
deliberate isolation trade-off (`default-src 'none'`, `connect-src 'none'`), and
loosening it re-opens the exposure ADR-067 was written to close.

## Summary

| ID | Title | Severity | Status |
|---|---|---|---|
| HP-1 | HTML preview dead in production; minter hardcoded `null` | High | Open — reproduced, root cause is one line |
| HP-2 | CSP emits an invalid `[::1]` source; 6 console errors per preview | Low-med | Open — reproduced |

---

### CI-2 — Firefox `mutation control` in preview-isolation fails intermittently, blocking CI
**Severity:** high (blocks a green CI) · **Area:** e2e harness · **Status:** Open · **OBSERVED**

Full CI on `1bd993220` (2026-09-09): **12 gates exit 0**, e2e 16 shards, one hard
failure with `retries: 0`:

```
[isolation-firefox] preview-isolation.spec.ts:854
  mutation control — with NO policy, all seven vectors reach the second origin
  Error: page.goto: NS_BINDING_ABORTED
  navigating to "http://127.0.0.1:42359/m/none/index.html"
```

**Not caused by this branch — verified.** `git diff --stat main...HEAD` for
`tests/e2e/preview-isolation.spec.ts`, `tests/e2e/preview-svg.spec.ts` and
`pkg/gateway/library_isolation_policy.go` is EMPTY. The branch does not touch the
failing test, its harness, or the policy under test.

**Not a product failure.** The aborted navigation targets the test's OWN mutant
origin (an ephemeral harness server on port 42359), not the gateway. The identical
test passes on **chromium** and the shard's other 12 firefox mutation cases all
pass, including every case that DOES apply a policy.

**Most likely mechanism, stated as a hypothesis rather than a conclusion.** This is
the no-policy control, where all seven egress vectors are supposed to fire —
including `window.open` and a top-level navigation. A vector firing while
`page.goto` is still settling would abort that navigation, and `NS_BINDING_ABORTED`
is precisely Firefox's error for "navigation superseded". The control is therefore
racing the very behaviour it exists to provoke. Chromium tolerates the same race;
Firefox does not. **This has not been proven** — the trace.zip on the worker would
settle it and has not been opened.

**Why it still must be fixed, and must NOT be silenced with a retry.** The previous
run on `94bb13e61` passed this test (87 passed), so it is intermittent, and
`retries: 0` is a deliberate choice recorded in `playwright.config.ts`: *"the
top-level `.pdf` case is a type-confusion security assertion, and 'the script did
not run' is not a property a retry establishes."* The same logic applies here — a
mutation control is the evidence that the seven-vector oracle can SEE egress at
all. Adding a retry would convert a loud, honest failure into an absorbed flake
and quietly weaken the proof behind the whole isolation suite.

**Fix direction:** make the control deterministic rather than tolerant — settle the
navigation before the vectors are allowed to fire (or drive the mutant page without
a racing top-level `goto`), so the control proves the oracle works without
depending on browser-specific navigation timing.

### Full CI verdict for `1bd993220`

| Gate | Result |
|---|---|
| cli-verb-guard, npm-ci, gofmt, go-build, go-vet | exit 0 |
| golangci-lint, verify-contracts, typecheck, vitest | exit 0 |
| go-test, go-race, records-no-sqlite | exit 0 |
| e2e — 14 of 16 shards | all passed (~330 tests) |
| e2e — `preview-isolation` | **1 FAILED** (CI-2 above) |
| e2e — `llm-conformance` | 1 flaky (CI-1, the known t3 re-plan case) |
| e2e — `preview-headed` | 2 skipped — both declared PLACEHOLDERs for ADR-067 Wave 3 |

Skip accounting is clean: all 16 shards report `unauthorized skip count (0)`.

**`preview-headed` is worth naming plainly:** the shard exists, is wired into the
plan, and runs — but both its tests are placeholders, so the browser's own PDF
type-confusion handling is currently asserted by nothing. That is declared in the
test names rather than hidden, and it is the SAME ADR-067 Wave 3 that HP-1's
missing mint client belongs to.
