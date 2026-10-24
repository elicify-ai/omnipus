# pkg/gateway — HTTP shell

Mux, auth, WebSocket, SPA embed, `/preview`. Domain REST lives in its module.

## Paged session history and WebSocket attach

REST messages and session detail use the generated paged carriers. Their
addressed-session access checks precede strict history-query validation. Keep
`newHistoryPageRequest` as the one prepared reader/weigher factory for both REST
and snapshot replay; do not restore raw transcript arrays, retired headers or a
lifetime-read fallback. Preparation memoization belongs to one request/cut.

| Path | Invariant |
|---|---|
| REST messages/detail | Prepare and validate selected source groups with the session reader and generated envelopes. Unavailable detail preserves authorized session metadata but omits message/page fields; it is not a successful empty page. |
| Snapshot attach | Resolve access, validate sizing, bind/hold live frames, then prepare history before sending any snapshot/wipe. Successful order is snapshot metadata, current state, source-owned historical frames/replay done, uncovered current projection, then catch-up completion. Held live frames follow the completed boundary. |
| Unavailable history | Authorized current state/projection can complete as `live_only`, without a snapshot, wipe, history-loaded claim or attention acknowledgement. A servable incremental journal remains complete; history sizing never truncates its tail. |
| Shared projection | Meter the larger actual REST/historical-WS encoding, not a guessed weight. Weighing must not offload results, execute actions or mutate stores. Preserve recorded source ownership and generated validation. |
| Completion and usage | Historical replay done is diagnostics, not live turn completion or a fresh token-total observation. Known absolute usage belongs to REST pages, scoped current state and live done; do not sum a loaded page or manufacture zero from unknown accounting. |
| Diagnostics | One content-free history-page summary covers an authorized page attempt. Preparation duration ends before blocked delivery. Never add raw frames, content, parameters, positions, identifiers or storage causes to the history summary. Malformed-frame diagnostics must likewise be sanitized. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_http.go::serveSessionHistory`,
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_projection.go::newHistoryPageRequest`,
and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/gateway/history_page_websocket.go::sendPreparedHistorySnapshot`.

## SPA embed — the stale-build trap

The binary embeds `pkg/gateway/spa/` (`embed.go`, `//go:embed all:spa`), NOT
Vite's `dist/spa/`. After frontend changes:

```bash
npm run build
rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/
```

Skipping the sync ships a stale SPA (verify: `grep -c "<new string>"
pkg/gateway/spa/assets/index-*.js`); the embed also refuses to compile with no
directory — tests/vet need a stub `pkg/gateway/spa/index.html`.

## Running tests here

~3,200 tests — never run the package whole; CI is the authority. Scope to one
symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree$' -p 1
./pkg/gateway/`) — the two-ladder default-agent regression: after a default
toggle, registry and routing must agree. The package refuses to compile
without the SPA-embed directory above, so a local run needs that stub in
place first.

## Running it locally

Run the Go binary, not Vite's dev server (`/api` → `localhost:18790`):

```bash
export OMNIPUS_HOME=/tmp/omnipus-test && rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &
```

- Default port 5000; bind failure → `lsof -i :5000` or set `gateway.port` in
  `$OMNIPUS_HOME/config.json`. Silent exit → `logs/gateway_panic.log`.
- `gateway.dev_mode_bypass` (default false): with no users and no env token,
  true admits callers as admin (one-time WARN). Onboarding does NOT need it —
  `state`, `onboarding/*`, `auth/*`, `providers`, `media`, `uploads` are
  optional-auth. `RequireNotBypass` 503s high-blast-radius admin routes while
  bypass is on; never remove it without an ADR.

## Explicit agent selection — no fallback agent

There is no default agent. Authority: **The Omnipus system agent: one
technical partner, workflows as skills, a narrow ask list and a secure secret
field**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md::Founder answers after correction (FQ-17, FQ-18)`.

New REST sessions require a nonblank `agent_id`;
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/rest_sessions.go::createSessionHTTP`
returns HTTP 400 when it is missing, even with schema validation off. New
WebSocket chats likewise return an error if no agent is named. Existing
sessions use their stored owner; a conflicting agent or an owner no longer
registered is refused. See `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/websocket_chat.go::wsHandlerHandleChatMessage.resolveTargetAgent`.

An agent update carrying the retired `default` field is refused with HTTP 400
before any write by
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/rest_agents_update.go::rejectRetiredDefaultField`.
An obsolete install-wide selector in saved config has no effect and produces
a load-time warning
(`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/config/ignored_default_agent.go::warnIgnoredDefaultAgentKey`);
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/rest_config.go::sanitizeConfigForWire`
removes it from config responses.

Channels require a workspace and a named main agent. Unbound connections and
missing or ineligible main agents drop messages instead of selecting another
agent (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/routing/route.go::RouteResolver.ResolveRoute`).
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/agent/routing_drops.go::AgentLoop.recordRouteDrop`
logs the drop and records its reason;
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/rest_channels.go::applyRoutingDropOverlay`
exposes the channel's degraded status, reason and last-drop time.

## Preview shares the main listener (ADR-044)

`rest.go::registerPreviewEndpoints` registers `/preview/` bare
(token-authenticated) on the main mux — there is no separate preview listener;
the `preview_port` / `preview_host` / `preview_origin` /
`preview_listener_enabled` keys were deleted, do not reintroduce them.
`gateway.preview_enabled` (default true) 404s `/preview/` per-request, no
restart; `gateway.public_url` is restart-gated and drives
`middleware.CanonicalGatewayOrigin` (preview URLs, CSP/CORS, WS `CheckOrigin`).

## Credential boot order

`NewStore → Unlock → LoadConfigWithStore → InjectFromConfig → ResolveBundle →
Store.HealthCheck → RegisterSensitiveValues → NewManager → Start` (ADR-004).
Store-level failures abort boot or reject reload/REST refresh even with only MCP
refs or no refs. A missing entry, or a single entry-auth failure when another
stored entry decrypts, degrades only that integration (#1277).
