# ADR-073 — Scope the gateway-origin SSRF exception to `/preview/*`, not the whole origin

- **Status:** Accepted (operator decision 2026-09-02; implemented same day, `pkg/security/ssrf.go`)
- **Deciders:** Daniel Piatkowski (operator)
- **Extends:** [[ADR-044]] (preview-on-main-listener) D2 — the gateway-origin SSRF exception this ADR narrows was introduced there.
- **Motivation:** found during a 2026-09-02 full-tool-catalog UAT round's follow-up review of the browser tool. ADR-044 D2 granted the agent's built-in browser an SSRF exception scoped to the gateway's exact `host:port` so it could reach its own preview URL (`http://localhost:<gateway.port>/preview/<agent>/<token>/`) without opening a blanket loopback allowance. That exception is evaluated **first**, before every other SSRF check including the loopback CIDR block. Because it matched on `host:port` only — never the path — it also let the browser tool reach every *other* path the gateway serves on that same origin, including the internal REST API (`/api/v1/config`, `/api/v1/agents`, …). An agent explicitly denied `read_file` or an HTTP-fetch tool could still read that data via `browser_navigate` to the gateway's own address — a side channel around an unrelated tool's access restriction. This is worse than a plain data leak: a human who has logged into the live-preview panel can leave that session's gateway auth standing for up to 24 hours, so a request reaching the gateway origin outside `/preview/` may be served as an authenticated admin request, not merely an anonymous one.

## Decision

### D1 — The exception is host:port **and** `/preview/*` path, not host:port alone

`isAllowedGatewayOrigin` (`pkg/security/ssrf.go`) now requires, in addition to the existing exact `host:port` match (including the documented `localhost` → `127.0.0.1`/`::1` loopback-form equivalence from ADR-044 D2 / r4 OBS-003): the URL's path must start with `/preview/`. A URL matching the gateway's host:port but any other path (`/`, `/api/v1/...`, or a not-actually-`/preview/` prefix like `/previewer/...`) now falls through to the full SSRF path and is blocked exactly as any other loopback address would be.

No other part of the exception's shape changes: it is still evaluated first, still scoped to a single configured `host:port` via `AllowGatewayOrigin`/`CloneWithGatewayOrigin`, still lives on a clone dedicated to the browser tool rather than the shared SSRF singleton (ADR-044 code-review M2), and the scheme-agnostic matching (`http://`/`https://` both accepted) is unchanged.

### D2 — Why this costs nothing for the feature the exception exists for

`web_serve`'s dev mode never needs the browser to reach a dev server's real port directly — `pkg/gateway/rest_preview.go`'s `proxyDevRequest` reverse-proxies `/preview/<agent>/<token>/...` requests to the dev server's loopback port server-side. The browser is only ever pointed at the gateway's own `/preview/` path, by design, for both static and dev-mode previews. There is no legitimate agent-preview use case that needs a path outside `/preview/`.

## Consequences

**Positive:** an agent denied a file/HTTP-read tool can no longer use `browser_navigate` to read the gateway's own internal API as a side channel. The 24-hour-standing-admin-session risk is closed for this specific vector (the browser tool can no longer reach any admin-only page at all, not just a narrower one).

**Accepted limitation:** a human using the live-preview panel to navigate directly to a gateway page *outside* `/preview/` (e.g. manually browsing to the gateway's own REST API or a future admin UI page rendered at the gateway origin) will now be blocked by the same SSRF check, since the panel's browser session is the same agent-scoped `SSRFChecker` clone. This was a known, named tradeoff at decision time (see the chat exchange this ADR records) — accepted because no current legitimate agent-preview or human-navigation use case outside `/preview/*` was found, weighed against a real, actively-exploitable side channel. If a genuine need for broader human navigation through that panel emerges later, it should get its own explicit, narrower exception (e.g. a distinct SSRF checker for human-initiated navigation vs. agent-initiated `browser_navigate` calls) rather than reopening the origin-wide exception this ADR closes.

**Out of scope:** this ADR does not address the credential/session-lifetime side of the finding (a human login persisting gateway auth for up to 24 hours) — only the SSRF-exception breadth that made the persisted session reachable via the browser tool in the first place. Shortening that session lifetime, if wanted, is a separate decision.

## Verification

`pkg/security/ssrf_test.go::TestGatewayOriginException_ScopedToPreviewPath` is the dedicated regression test (host:port match + non-`/preview/` path is blocked; host:port match + `/preview/` path passes, literal and both resolved-loopback forms). Three pre-existing tests (`TestBrowserSSRFAllowsGatewayLocalhostOnly`, `TestCloneWithGatewayOrigin_DoesNotMutateSingleton`) that had asserted the old host:port-only behavior via non-`/preview/` paths were updated to use `/preview/` paths, preserving what they actually test (loopback-form equivalence, singleton non-mutation, port scoping) rather than the incidental old path-blindness. `go test ./pkg/security/... ./pkg/agent/...` (the browser-SSRF wiring test) both pass.
