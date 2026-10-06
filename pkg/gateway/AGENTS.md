# pkg/gateway — HTTP shell

Mux, auth, WebSocket, SPA embed, `/preview`. Domain REST lives in its module.

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
  `state`, `onboarding/*`, `auth/*`, `providers` are optional-auth; `uploads`
  and workspace media require auth since #716, while legacy
  `/api/v1/media/{uuid}` stays optional (122-bit id). `RequireNotBypass` 503s
  high-blast-radius admin routes while bypass is on; never remove it without
  an ADR.

## Default agent — one singleton, two ladders (ADR-054 D6.4)

`config.Agents.Defaults.DefaultAgentID` is the ONLY input default-agent
resolution consults. Registry's `GetDefaultAgent()` and
`routing::resolveDefaultAgentID` carry DIFFERENT Priority-2 fallbacks by
design; they agree only via Priority 1. The wire `default` field is derived
(`ac.ID == DefaultAgentID`), never read from the per-entity
`AgentConfig.Default` bool (once nothing wrote the singleton: PUT toasted
success, changed no routing). Regression:
`rest_agents_update_test.go::TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree`.

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
RegisterSensitiveValues → NewManager → Start` — any failure aborts boot (ADR-004).
