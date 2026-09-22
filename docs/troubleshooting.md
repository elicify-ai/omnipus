# Troubleshooting

Fixes for problems, grouped by symptom. For a running-but-odd gateway (the Omnipus server process), [debugging](operations/debug.md) covers reading its logs in depth.

## What it is

A symptom-first fix list for startup failures, unreachable gateways, login and API errors, model-provider rejections, credential lockouts, and stale builds. It assumes Omnipus is installed — if not, start with [getting-started.md](getting-started.md).

## How to start diagnosing

1. Open `$OMNIPUS_HOME/logs/gateway_panic.log` (default: `~/.omnipus/logs/`). A startup crash always writes there, even when nothing appears on screen.
2. Open `$OMNIPUS_HOME/logs/gateway.log` — the running gateway's log stream, and the reason behind most failed requests.
3. Run `omnipus doctor`, which checks your configuration for known unsafe settings.
4. Still unexplained? Restart with `omnipus start --debug` — see [debugging](operations/debug.md).

## The gateway exits during startup

If it exits with no output at all, read `gateway_panic.log`, as above.

### Exit code 78: the sandbox failed on a capable kernel

Exit 78 now happens only when a kernel that claims to support Landlock fails for a reason other than a right the kernel does not know — a malformed rule, a port rule the kernel rejects, or the step that activates the policy. Omnipus refuses to start rather than listen half-protected. Look for `sandbox.apply_failed` in `gateway.log`, then start with `omnipus start --sandbox=permissive` (violations logged, not blocked) or, for development only, `--sandbox=off`. Valid values are `enforce`, `permissive`, and `off`; a typo exits with code 2.

A kernel that is too old for a right Omnipus asks for is different: Omnipus starts anyway, at application-level enforcement, and logs `sandbox.degraded` instead of exiting — see [operations/sandbox-limitations.md](operations/sandbox-limitations.md). `OMNIPUS_ENV=production` with the sandbox weakened prints a repeating warning banner — deliberate, not a fault. Full detail: [operations/sandbox-config.md](operations/sandbox-config.md).

### It exits with "bind: address already in use"

The gateway serves everything — web app, API, and agent-built previews — on a single listener, `gateway.port`, default **5000**. There is no second preview port. If another process holds the port, the gateway retries the bind twice, then exits with a clear boot error; it never falls back to a different port. Check what holds it:

```bash
lsof -i :5000 | grep LISTEN
```

Stop that process, or set another port in `~/.omnipus/config.json` and start again:

```json
{
  "gateway": { "host": "127.0.0.1", "port": 5500 }
}
```

The port is read at startup; a running gateway does not move its listener.

## Every request returns 401 Unauthorized

The web app signs you in once, then rides a session cookie. Scripts must present a Bearer token belonging to a configured account or the gateway's CLI token, or match `OMNIPUS_BEARER_TOKEN`.

| Message you see | Why it happens | What to do |
|---|---|---|
| `unauthorized: no users configured, complete onboarding first` | Fresh install: no account, no `OMNIPUS_BEARER_TOKEN` | Complete onboarding — next section |
| `unauthorized: missing Bearer token` | A script sent no token, or your browser session expired | Sign in again, or send a Bearer token |
| `unauthorized: invalid Bearer token` | The token matches no account and no CLI token | Re-issue the token, or fix `OMNIPUS_BEARER_TOKEN` |

For local development only, `"dev_mode_bypass": true` under `gateway` lets requests through with no token; the gateway warns at boot and answers 503 on admin routes while it is on. Never enable it on a machine others can reach.

## Chat replies with a limited-mode message

On a fresh install the gateway boots into **limited mode**: it serves the web app but has no model, and every chat message returns the reason, such as `no default model configured; gateway started in limited mode`. The same happens when the API key behind your default model stops resolving; each reply then names the credential at fault.

Finish onboarding to fix it. Open `http://localhost:5000` — on a fresh install every screen leads to the wizard (your name, a password, then a provider and its API key). Without a browser, `omnipus onboard` prompts for provider, key, model, and account.

## The provider rejects your model requests

### 404 "No endpoints found that support tool use"

The model cannot call tools, and Omnipus sends its tool list with every request, so the request is rejected outright. Small open models often lack tool support. In the model picker, the **Recommended for chat** mark appears only on models that can call tools. Change the default in **Settings → Providers**, or edit `agents.defaults.default_model` in `config.json`.

### 400 "invalid model ID" or a rejected provider

The `model` value reaches the provider exactly as written; on OpenRouter, IDs carry a vendor prefix (`z-ai/glm-5.2`, not `glm-5.2`). The `provider` value must be an ID the catalog knows, such as `openrouter` or `anthropic`. A working entry:

```json
{
  "providers": [
    {
      "name": "fast",
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_key_ref": "openrouter_api_key"
    }
  ],
  "agents": {
    "defaults": {
      "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" }
    }
  }
}
```

`api_key_ref` names an entry in the encrypted credential store. Set it without touching files with `omnipus credentials set openrouter_api_key sk-or-v1-...`, or use **Settings → Security → Credential Vault** in the web app.

## Saving a secret fails with "credential store locked"

The encrypted credential store needs a master key. The gateway looks for one in this order:

| Priority | Source of the key | Notes |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` | 64-character hex key in the environment |
| 2 | `OMNIPUS_KEY_FILE` | Path to a 0600-permission file holding the hex key |
| 3 | `$OMNIPUS_HOME/master.key` | Loaded automatically when present with 0600 permissions |
| 4 | Auto-generate | Only on a fresh install with no key and no `credentials.json` |
| 5 | Interactive prompt | Terminal only |

A fresh install warns you to back up `master.key`. Heed it: losing that file makes every stored credential permanently unreadable, with no recovery. See [Security for users](security.md).

## Credentials stop working after an upgrade

Every stored value is encrypted together with the name it is stored under, so a value cannot be moved from one entry to another and still open. Entries written before that binding existed no longer open, and the gateway reports each one it cannot read. Re-enter them:

1. Open **Settings**, **Security**, **Credential Vault** and enter each value again — or run `omnipus credentials set <name> <value>`.
2. Restart the gateway.

There is deliberately no fallback that reads an old entry: it would let a value be moved between names again. Note the difference when only one entry fails while the others still work — that entry was edited on disk or copied from another entry, not written by an older release. The gateway names the entry at fault in its log.

## The web app looks outdated after a source build

The Go binary embeds the web app from `pkg/gateway/spa/` — a copy of the frontend build output, not the output itself, so building without refreshing that copy serves an old interface. `make build` refreshes it and builds in one step. Confirm a change reached the binary by searching the bundled assets for a string only it contains:

```bash
grep -c "<your new string>" pkg/gateway/spa/assets/index-*.js
```

A count of 0 means the copy is stale.

## Two more errors with short fixes

**"priority must be between 1 and 5"** — task priority runs from 1 (highest) to 5 (lowest); 3 is the default, and anything outside the range is rejected.

**"failed to move downloaded file ... invalid cross-device link"** — the skills installer downloads to the system temp directory, then moves the file into the workspace; the move fails when the two sit on different filesystems, as in containers with mounted volumes. Point `TMPDIR` at a directory on the workspace's filesystem before starting the gateway:

```bash
export TMPDIR=/path/to/workspace/tmp
mkdir -p "$TMPDIR"
```

## Limits and things to watch

- Sandbox strength depends on the operating system: Linux confines the gateway and everything it starts, macOS confines only the processes Omnipus launches, and Windows uses application-level checks alone. Details: [operations/sandbox-limitations.md](operations/sandbox-limitations.md).
- Behind a reverse proxy, set `gateway.public_url` to the address browsers actually reach; preview links and WebSocket origin checks derive from it. See [operations/reverse-proxy.md](operations/reverse-proxy.md).

## Related pages

- [debugging](operations/debug.md) — reading gateway logs and running with `--debug`.
- [getting-started.md](getting-started.md) — install and first-run setup, including onboarding.
- [operator configuration](operations/configuration.md) — the `config.json` areas named here.
- [Security for users](security.md) — how the master key and encrypted credentials affect you.
