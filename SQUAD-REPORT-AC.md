# Squad AC Report — issue #785

## Outcome

Static `serve_web` previews now permit their own linked stylesheets, JavaScript bundles, and self-hosted fonts without permitting third-party resource origins. The policy keeps workers explicitly blocked so the widened script rule cannot silently authorize Worker, SharedWorker, or ServiceWorker scripts through CSP fallback.

- **Code correct and tested:** the direct CSP-header regression test passes on the final tree, fails against the original policy, and the size-budget gate passes.
- **Reachable by a user/agent:** `serve_web` is already registered and allowed for General Purpose; its tokenized URL reaches `/preview/...`, whose static handler applies this policy. The regression test exercises that real registered static-preview handler path rather than testing the policy builder alone.

## Reproduction — RED before product change

Test: `TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet`

Invocation: package `github.com/elicify-ai/omnipus/pkg/gateway`, build tags `goolm,stdjson`, one process, and the exact test-name filter `^TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet$`.

Receipt:

```text
exit=1
--- FAIL: TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet
Error: "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; ..." does not contain "style-src 'self' 'unsafe-inline'"
Messages: same-origin linked stylesheets must be allowed by the static preview CSP
FAIL github.com/elicify-ai/omnipus/pkg/gateway
```

This reproduces the browser defect at the controlling boundary: the HTML and CSS may both return HTTP 200, but the document response's `style-src` does not authorize the browser to apply the linked stylesheet.

## Root cause

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ac-preview-csp/pkg/gateway/rest_workspace.go::buildWorkspaceCSP` emitted `style-src 'unsafe-inline'` with no same-origin source. Browsers therefore fetched linked CSS successfully and then rejected it under the document's policy.

The same audit found two related silent failures:

- `font-src` was absent, so `default-src 'none'` blocked relative `.woff2` files.
- `script-src` allowed inline code only, so realistic Vite/Next static exports could not load their own external bundles.

GitNexus impact analysis rated the change **LOW** risk: 4 direct dependants, 10 total affected symbols, 1 module (`Gateway`), and no indexed execution process outside that module.

## Fix

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ac-preview-csp/pkg/gateway/rest_workspace.go::buildWorkspaceCSP` now emits:

```text
default-src 'none';
script-src 'self' 'unsafe-inline';
worker-src 'none';
style-src 'self' 'unsafe-inline';
img-src 'self' data: blob:;
font-src 'self';
connect-src 'self';
...
```

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ac-preview-csp/pkg/gateway/serve_web_real_test.go::TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet` exercises a registered static preview and parses the actual response header. It asserts exact source lists, so both removing required sources and adding a wildcard, scheme source, data source, or third-party host fail.

## Security-boundary audit

The current architecture serves previews on the same gateway origin as the application. `buildWorkspaceCSP` receives an origin for `frame-ancestors`, but no agent/token path from which it could build a narrower resource source. Therefore `'self'` is the narrowest source available without redesigning the handler inputs and route isolation; it covers the whole gateway origin, not only the tokenized preview path.

| Directive | Final policy | What it permits | Decision and rationale |
|---|---|---|---|
| `style-src` | `'self' 'unsafe-inline'` | Linked CSS from the gateway origin, plus already-permitted inline CSS | **Loosened.** Required for the reported linked stylesheet. No third-party stylesheet origin is admitted. |
| `font-src` | `'self'` | Fonts from the gateway origin only | **Loosened from implicit deny.** Required for relative self-hosted `.woff2` exports. Data, blob, and third-party fonts remain blocked. |
| `script-src` | `'self' 'unsafe-inline'` | Script files from the gateway origin, plus already-permitted inline scripts | **Loosened.** Required for realistic static bundles. Agent-authored arbitrary script execution already existed through `'unsafe-inline'`; this enables the same code split into same-origin files and admits no third-party script origin. |
| `worker-src` | `'none'` | No Worker, SharedWorker, or ServiceWorker scripts | **Added as a tightening.** Prevents the widened `script-src` from becoming the fallback worker permission. |
| `img-src` | `'self' data: blob:` | Same-origin and embedded/generated images | **Unchanged.** Sufficient for realistic static exports; remote image hosts remain blocked. |
| `connect-src` | `'self'` | Same-origin fetch/XHR/WebSocket connections | **Unchanged.** Supports local data/hydration while external network origins remain blocked. The pre-existing shared-origin API residual is not expanded by this fix. |
| `default-src` | `'none'` | Nothing for resource types without a narrower directive | **Unchanged.** The deny-by-default floor remains in place. |

No external CDN origin, `https:` wildcard, `*`, `data:` script/font source, or `blob:` script/font source was added.

## GREEN receipt

Final invocation: package `github.com/elicify-ai/omnipus/pkg/gateway`, `-count=1 -v`, build tags `goolm,stdjson`, one process, and the exact test-name filter `^TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet$`.

Receipt:

```text
exit=0
=== RUN   TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet
--- PASS: TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet (0.12s)
PASS
ok github.com/elicify-ai/omnipus/pkg/gateway 7.300s
```

Additional gates:

```text
make lint-budgets: exit=0
make lint-guards: exit=0
guards run: 27
GUARD RUNNER: all 27 guards passed
```

The budget gate's own mutation self-checks passed before the gate passed. Per repository policy, no full Go suite was run locally.

## Revert-proof receipt

I restored the original production policy while keeping the strengthened test unchanged, then ran the same uncached test.

```text
exit=1
=== RUN   TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet
expected style-src: ["'self'", "'unsafe-inline'"]
actual style-src:   ["'unsafe-inline'"]
expected script-src: ["'self'", "'unsafe-inline'"]
actual script-src:   ["'unsafe-inline'"]
directive "worker-src" is absent from the served policy
--- FAIL: TestServeWeb_RealStaticSite_CSPAllowsOwnStylesheet
FAIL github.com/elicify-ai/omnipus/pkg/gateway
```

The fix was then restored. The final uncached GREEN receipt above is from the restored tree.

## Independent review

- Simplification review: no change recommended; the diff is already explicit and minimal.
- Code review after security revisions: no high-confidence findings; exact directive assertions and `worker-src 'none'` address accidental widening and CSP fallback.
- Security review: initially found the weak positive-only assertions and worker fallback. Both were fixed before the final test and gate run.

## Honest gaps — real browser confirmation required

The Go test proves what header the real static-preview handler emits and proves the test fails if the policy fix is removed. It does **not** prove browser enforcement or visual rendering.

A real browser must still open a tokenized static `serve_web` preview and confirm all of the following:

1. A linked stylesheet changes a known computed style, and same-origin `document.styleSheets[n].cssRules` is readable.
2. A real `.woff2` request succeeds and `document.fonts.check(...)` returns true.
3. A same-preview external JavaScript bundle executes a sentinel while `worker-src 'none'` continues to block worker creation.
4. DevTools shows no CSP violations for the same-origin CSS, font, and script resources.
5. Negative probes to a third-party stylesheet, font, script, and connection remain blocked.

Font rendering must be checked with a valid font file; an HTTP 200 alone cannot prove that the browser accepted or applied it. No real-browser run was performed in this squad worktree.
