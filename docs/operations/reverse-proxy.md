# Reverse Proxy Configuration — Two-Port Gateway

## Why two ports

The Omnipus gateway listens on two TCP ports. The main port (default **5000**) serves the SPA and the authenticated REST/WebSocket/SSE API. The preview port (default **5001**) serves agent-generated HTML, CSS, and JavaScript via time-limited bearer tokens — these are the assets produced by the unified `web_serve` tool (both static directories and dev-server processes) and surfaced in the chat as clickable previews under the `/preview/<agent>/<token>/` route.

The separation exists for browser origin isolation. When both the SPA and the agent-served content share a single origin (scheme + host + port), a malicious build output — an HTML file with inline JavaScript — could read the `Authorization` header stored in the SPA's memory or make credentialed requests to `/api/v1/` in the same origin. Placing agent-served content on a distinct port (and, in production, a distinct subdomain) gives browsers a separate origin, so cross-origin policies block that read. Without this isolation, a compromised agent that writes and then serves an HTML file would have a path to exfiltrate the admin bearer token.

---

## Default deployment (no proxy, bare IP)

When running on a VPS without TLS termination, expose both ports in the firewall:

```bash
ufw allow 5000/tcp
ufw allow 5001/tcp
```

No further configuration is needed. The gateway binds both ports on the host specified by `gateway.host` (default `0.0.0.0`). Clients reach the SPA at `http://<host>:5000` and preview frames at `http://<host>:5001`.

---

## nginx with two server blocks

The following configuration maps:

`omnipus.example.com` (port 443) to the gateway main port at `127.0.0.1:5000`, and `preview.omnipus.example.com` (port 443) to the gateway preview port at `127.0.0.1:5001`.

A single wildcard certificate from Let's Encrypt (obtained with `certbot certonly --nginx -d omnipus.example.com -d preview.omnipus.example.com`) covers both server blocks.

```nginx
# /etc/nginx/sites-available/omnipus

# Main SPA + API
server {
    listen 443 ssl http2;
    server_name omnipus.example.com;

    ssl_certificate     /etc/letsencrypt/live/omnipus.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/omnipus.example.com/privkey.pem;

    # SSE and streaming — disable buffering so events reach the client immediately
    proxy_buffering off;
    proxy_read_timeout 3600s;

    location / {
        proxy_pass http://127.0.0.1:5000;

        # Standard proxy headers
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP         $remote_addr;

        # WebSocket upgrade (used by the SPA live-chat stream)
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}

# Agent-served preview content
server {
    listen 443 ssl http2;
    server_name preview.omnipus.example.com;

    ssl_certificate     /etc/letsencrypt/live/omnipus.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/omnipus.example.com/privkey.pem;

    # Preview responses may be large; still disable buffering for consistency
    proxy_buffering off;
    proxy_read_timeout 300s;

    location / {
        proxy_pass http://127.0.0.1:5001;

        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP         $remote_addr;

        # No WebSocket upgrade needed on the preview port
        proxy_http_version 1.1;
    }
}

# Redirect plain HTTP → HTTPS for both hostnames
server {
    listen 80;
    server_name omnipus.example.com preview.omnipus.example.com;
    return 301 https://$host$request_uri;
}
```

Enable and reload:

```bash
ln -s /etc/nginx/sites-available/omnipus /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx
```

---

## Caddy alternative

Caddy handles TLS automatically via its built-in ACME client — no separate certbot step.

```caddyfile
# /etc/caddy/Caddyfile

omnipus.example.com {
    reverse_proxy 127.0.0.1:5000 {
        header_up Host {host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
        flush_interval -1
    }
}

preview.omnipus.example.com {
    reverse_proxy 127.0.0.1:5001 {
        header_up Host {host}
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
        flush_interval -1
    }
}
```

`flush_interval -1` disables buffering and is equivalent to nginx's `proxy_buffering off`. Caddy automatically provisions TLS for both hostnames using Let's Encrypt or ZeroSSL on first request.

Reload:

```bash
caddy reload --config /etc/caddy/Caddyfile
```

---

## Operator config

With either proxy, the gateway must know its own public-facing URLs so it can set correct `Content-Security-Policy` headers and build preview links that embed in the SPA. Set these in `~/.omnipus/config.json`:

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

With `trust_xff: true` the gateway reads `X-Forwarded-For` for the audit `remote_ip` field, which gives the real client IP rather than the proxy's loopback address.

**Security note:** Only enable `trust_xff` when all traffic to the gateway port passes through your controlled proxy. If any external client can reach the gateway port directly, they can supply a spoofed `X-Forwarded-For` header and insert a fake IP into audit logs.

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

```json
{
  "gateway": {
    "host": "127.0.0.1",
    "port": 5000,
    "preview_port": 5001,
    "preview_host": "127.0.0.1",
    "public_url": "https://omnipus.example.com",
    "preview_origin": "https://preview.omnipus.example.com"
  }
}
```

| Field | Purpose |
|---|---|
| `host` / `preview_host` | The interface the gateway binds. `127.0.0.1` is correct when a reverse proxy handles external traffic; use `0.0.0.0` for bare-IP deployments. |
| `public_url` | The canonical base URL for the SPA. The gateway uses this to construct `frame-ancestors` CSP directives. |
| `preview_origin` | The origin the SPA uses as the `src` of `<iframe>` elements embedding preview content. Must match the `preview_host:preview_port` the browser reaches. |

After editing, the gateway picks up the new values on the next `POST /api/v1/reload` or process restart. Note: `preview_listener_enabled` is **not** hot-reloadable. The preview mux is built once at boot (the runtime hot-flip from `true`→`false` is tracked under issue #155 and explicitly skipped in `pkg/gateway/preview_listener_hot_flip_test.go:93`); changing it requires a process restart.

---

## Local development (no proxy, no TLS)

For local development both ports work out of the box without any configuration. Modern browsers resolve `*.localhost` to `127.0.0.1`, so `localhost:5000` and `localhost:5001` are treated as distinct origins despite sharing an IP. No TLS is needed because browsers do not apply `Secure` cookie restrictions to `localhost`.

Default config values work without modification:

| Setting | Default |
|---|---|
| `gateway.host` | `0.0.0.0` |
| `gateway.port` | `5000` |
| `gateway.preview_host` | `0.0.0.0` |
| `gateway.preview_port` | `5001` |
| `gateway.public_url` | *(unset — falls back to request origin)* |
| `gateway.preview_origin` | *(unset — falls back to `localhost:5001`)* |

Open `http://localhost:5000` in the browser. Preview iframes load from `http://localhost:5001`.

---

## Single port (rollback)

If binding a second port is not possible — for example, on a restricted host where only one port is available or the process does not have permission to bind additional sockets — the preview listener can be disabled:

```json
{
  "gateway": {
    "preview_listener_enabled": false
  }
}
```

When `preview_listener_enabled` is `false`, the iframe-preview feature is disabled entirely. The gateway does not start the second listener, and the `/preview/` path prefix is not registered on the main mux either — any browser request to `<main-host>:<port>/preview/...` will receive a 404 from the SPA catch-all handler. Tool calls to `web_serve` will still register tokens in the in-memory registry, but the URLs they return will not resolve.

This is a full rollback of the iframe-preview feature, not a partial-degradation mode. If you need to restore functionality, re-enable the preview listener and restart the gateway.
