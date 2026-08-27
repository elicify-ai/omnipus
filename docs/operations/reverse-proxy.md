# Reverse Proxy Configuration — Single-Listener Gateway

## Architecture (ADR-044)

The Omnipus gateway serves everything on **one** TCP listener: `gateway.port` (default **5000**). That single origin carries:

- the embedded SPA (`GET /`, static assets),
- the authenticated REST/WebSocket/SSE API (`/api/v1/*`),
- and agent-generated dev/static previews at `/preview/<agent>/<token>/…` — the surface produced by the unified `web_serve` tool.

There is **no separate preview port, host, or origin**. The config keys that used to configure a second listener — `gateway.preview_port`, `preview_host`, `preview_origin`, `preview_listener_enabled` — were **deleted with no back-compat**; a `config.json` still carrying them has those keys silently ignored (the fields no longer exist on `GatewayConfig`).

`/preview/` is registered bare on the main mux — no `withAuth`/session wrapping, and it is the one path prefix exempt from CSRF and the Origin-mismatch check, because the URL path token *is* the credential (`middleware.PreviewPathPrefix`, referenced by both the CSRF and Origin code so the exemption can't drift). Everything else on the mux — the SPA, `/api/v1/*` — is behind the normal HttpOnly-cookie session auth plus CSRF double-submit.

**Preview is a link, not an iframe.** The SPA never embeds preview content in an `<iframe>`; it renders a clickable link that opens in the user's own browser tab (or the agent's built-in browser live panel). There is no "two-port origin isolation" model to reason about here — that design was superseded by ADR-044. The operative security boundary is now the HttpOnly session cookie (unreadable by JS, including any JS the previewed app serves), CSP `frame-ancestors`, and the reserved-cookie-stripping the proxy applies to previewed-app responses — not port separation.

`gateway.preview_enabled` (default `true`) is a **live** toggle: read fresh on every request, no restart required. Flipping it to `false` 404s `/preview/` and makes `serve_web` refuse immediately; it does not tear down already-running dev servers (those idle-TTL out on their own).

---

## Default deployment (no proxy, bare IP)

When running on a VPS without TLS termination, expose the one port in the firewall:

```bash
ufw allow 5000/tcp
```

No further configuration is needed. The gateway binds `gateway.port` on the host specified by `gateway.host` (default `0.0.0.0`). Clients reach the SPA, the API, and previews alike at `http://<host>:5000/...`.

---

## nginx (single server block)

```nginx
# /etc/nginx/sites-available/omnipus

server {
    listen 443 ssl http2;
    server_name omnipus.example.com;

    ssl_certificate     /etc/letsencrypt/live/omnipus.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/omnipus.example.com/privkey.pem;

    # SSE and streaming (chat) and previews of arbitrary size — disable
    # buffering so events/bytes reach the client immediately.
    proxy_buffering off;
    proxy_read_timeout 3600s;

    location / {
        proxy_pass http://127.0.0.1:5000;

        # Forward the real Host and client info — the gateway's CORS/CSP/WS
        # origin checks and audit logging depend on these being accurate.
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP         $remote_addr;

        # WebSocket upgrade — used by the chat stream AND by the built-in
        # browser live panel's WS frames. Both share this one listener now.
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}

# Redirect plain HTTP → HTTPS
server {
    listen 80;
    server_name omnipus.example.com;
    return 301 https://$host$request_uri;
}
```

Obtain the certificate with `certbot certonly --nginx -d omnipus.example.com` (a single hostname — there is no preview subdomain to cover).

Enable and reload:

```bash
ln -s /etc/nginx/sites-available/omnipus /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

---

## Caddy alternative

Caddy handles TLS automatically via its built-in ACME client.

```caddyfile
# /etc/caddy/Caddyfile

omnipus.example.com {
    reverse_proxy 127.0.0.1:5000 {
        header_up Host              {host}
        header_up X-Forwarded-For   {remote_host}
        header_up X-Forwarded-Proto {scheme}
        flush_interval -1
    }
}
```

`flush_interval -1` disables buffering (equivalent to nginx's `proxy_buffering off`) and covers both the SSE/chat stream and previews. Caddy's `reverse_proxy` upgrades WebSocket connections automatically — no extra directive needed.

Reload:

```bash
caddy reload --config /etc/caddy/Caddyfile
```

---

## Operator config

With either proxy, the gateway must know the fully-qualified HTTPS origin the browser actually reaches, so it can set correct `Content-Security-Policy` / CORS headers, validate the WebSocket `Origin` on upgrade, and mint absolute `/preview/…` URLs that resolve. Set this in `~/.omnipus/config.json`:

```json
{
  "gateway": {
    "host": "127.0.0.1",
    "port": 5000,
    "public_url": "https://omnipus.example.com"
  }
}
```

| Field | Purpose |
|---|---|
| `host` | The interface the gateway binds. `127.0.0.1` is correct when a reverse proxy handles external traffic; use `0.0.0.0` for bare-IP deployments. |
| `public_url` | The fully-qualified HTTPS origin the browser reaches. Drives the boot-frozen `CanonicalGatewayOrigin` that CSP `frame-ancestors`, CORS `Access-Control-Allow-Origin`, the WebSocket upgrade's `CheckOrigin`, and `web_serve`'s minted preview URLs all use. **Restart-gated** — it is resolved once at boot, so a change here requires a gateway restart (or `POST /api/v1/reload` after a config write) to take effect. Without it set behind a reverse proxy, the gateway falls back to a `host:port` heuristic that will not match the public hostname the browser actually sees, and the origin checks will fail closed. |

### Accurate client-IP audit logging (`gateway.trust_xff`)

By default, the gateway reads the client IP from `r.RemoteAddr` exclusively. This prevents clients from spoofing their audit-log IP on bare-IP / single-binary deployments (where there is no trusted intermediary to set `X-Forwarded-For`).

When a trusted reverse proxy (nginx, Caddy, etc.) sits in front, set:

```json
{
  "gateway": {
    "trust_xff": true
  }
}
```

With `trust_xff: true` the gateway reads `X-Forwarded-For` for the audit `remote_ip` field and for every rate-limit bucket (login brute-force protection and the provider sign-in limiters), which gives the real client IP rather than the proxy's loopback address.

**Which entry is used: the rightmost one.** `X-Forwarded-For` is a list, and only its last entry is written by the proxy the gateway trusts — every entry to the left of it was supplied by whatever sent the request. The nginx line above uses `$proxy_add_x_forwarded_for`, which *appends* the connecting peer to whatever the client already sent: a client that sends `X-Forwarded-For: 6.6.6.6` arrives at the gateway as `6.6.6.6, <its real address>`. Reading the leftmost entry would therefore read a value the client chose, letting it forge its audit IP and get a fresh rate-limit bucket on every request. The gateway reads the **rightmost** entry instead. Caddy's `header_up X-Forwarded-For {remote_host}` *replaces* the header with the peer address, so its single entry is both leftmost and rightmost — that configuration is unaffected either way.

**Multi-hop chains (e.g. a CDN in front of nginx) are not supported by this flag.** The rightmost entry is the innermost proxy's view, so with a CDN in front you will see the CDN edge address, not the origin client. This is deliberate: the gateway never prefers a value it cannot trust over one it can. If you need true client IPs behind a chain, terminate it at a single proxy you control (so the gateway's peer *is* the client's last untrusted hop), or leave `trust_xff` off.

**Security note:** Only enable `trust_xff` when all traffic to the gateway port passes through your controlled proxy. If any external client can reach the gateway port directly, they can connect without going through the proxy at all and supply a fully-forged `X-Forwarded-For` header — the rightmost-entry rule protects against a client *behind* your proxy, not against one that bypasses it.

### Loopback-gated endpoints behind a same-host proxy (added 2026-07-18)

Behind a same-host reverse proxy, every request the gateway sees presents a loopback
`RemoteAddr` (the proxy's own connection to the gateway, not the original client). This
means the live-browser capture-ingest endpoint's loopback gate (`127.0.0.1`-only, by
design — see ADR-047 D6) provides **no additional defense** in that topology: any
request reaching the proxy on that path also reaches the gateway looking like
loopback. This is expected and accounted for — the **per-stream capability token**
(minted by the gateway, injected out-of-band via CDP, never in a URL) is the sole
security boundary for that endpoint, not the loopback check. The same same-host-proxy
caveat applies to any other loopback-only gate; see `gateway.trust_xff` above for the
analogous IP-spoofing consideration on the **login rate limiter**, which keys off the
same `RemoteAddr`/`X-Forwarded-For` resolution.

---

## Disabling previews (`gateway.preview_enabled`)

Previews (`/preview/` and `web_serve`) can be turned off entirely without touching the reverse proxy or restarting the process:

```json
{
  "gateway": {
    "preview_enabled": false
  }
}
```

This is read **live** on every request — no restart, and no reverse-proxy config change needed either way, since `/preview/` was never a separate listener to disable. Setting it `false` 404s every `/preview/…` request immediately (the SPA renders "preview disabled" rather than a link) and makes `serve_web` refuse new registrations with a clear error; it does not kill any dev server already running (those idle-TTL out on their own). Flipping it back to `true` re-enables the surface on the very next request.

---

## Local development (no proxy, no TLS)

For local development the single port works out of the box without any configuration:

| Setting | Default |
|---|---|
| `gateway.host` | `0.0.0.0` |
| `gateway.port` | `5000` |
| `gateway.public_url` | *(unset — falls back to a `host:port` heuristic; fine for `localhost`)* |

Open `http://localhost:5000` in the browser. Chat, the API, and any preview link the agent produces all resolve on that same origin — no second port to reach.
