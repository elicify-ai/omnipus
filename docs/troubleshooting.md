# Troubleshooting

Common operator issues, grouped by symptom. If the gateway started, [debug.md](debug.md) covers how to read its logs. If the gateway never starts, this page is the right place.

## The gateway exits 0 or 1 immediately

### "no providers configured. Please add entries to model_list in your config"

You're booting a fresh `~/.omnipus/` with no provider yet. Two paths forward:

- **Drive the web onboarding wizard** (recommended) — start with `--allow-empty` and visit `http://localhost:5000`:

  ```bash
  omnipus gateway --allow-empty
  ```

  The wizard at `/onboarding` walks Welcome → Provider → API Key → Model → Admin Account → Done, then the gateway is fully provisioned.

- **Use the interactive CLI wizard** — same end result, no browser:

  ```bash
  omnipus onboard
  ```

  Prompts for provider, API key (hidden input), model, and admin username/password. Writes the encrypted API key into `credentials.json`, the provider entry + admin user into `config.json`, and marks onboarding complete in `system/state.json`.

### Gateway exits silently — nothing on stdout

Check `$OMNIPUS_HOME/logs/gateway_panic.log` for the startup error. The runtime panic handler always writes there, even when the rest of the boot path can't log. Once you've fixed the underlying problem, you can delete the file (it's recreated on the next crash).

`$OMNIPUS_HOME/logs/gateway.log` carries the running gateway's normal log stream — useful when the gateway *did* start but a request fails.

### Exit code 78 ("EX_CONFIG")

The kernel sandbox failed to apply on a kernel that claims Landlock support. This is a **fail-closed** path — the HTTP listener never binds. Inspect `gateway.log` for `sandbox apply failed`, then either:

- Lower the kernel's expectations: `omnipus gateway --sandbox=permissive` to log violations without blocking, or `--sandbox=off` for development.
- Set `OMNIPUS_ENV=production` to nothing — production mode prints a recurring stderr nag banner when the sandbox is off or permissive, by design.

Full sandbox configuration: [operations/sandbox-config.md](operations/sandbox-config.md). Known limitations (macOS, Windows, older kernels): [operations/sandbox-limitations.md](operations/sandbox-limitations.md).

## "Port conflict" — gateway appears to start but you can't reach it

Default ports are **5000** (SPA + API) and **5001** (preview iframes). If another process is bound to either, the gateway either falls through to a different port or fails silently — Linux behaviour depends on the listening socket.

Check what's bound:

```bash
lsof -i :5000 -i :5001 | grep LISTEN
```

If you see another process, change the gateway's ports in `~/.omnipus/config.json`:

```json
{
  "gateway": {
    "host": "127.0.0.1",
    "port": 5500,
    "preview_port": 5501
  }
}
```

Restart, then visit `http://localhost:5500`.

## "401 Unauthorized" on every request

The auth decision tree in `pkg/gateway/auth.go::checkBearerAuth` is:

1. No `Authorization: Bearer …` header on a protected route → **401**.
2. `cfg.gateway.users` populated → token must match a registered user.
3. `OMNIPUS_BEARER_TOKEN` env set → token must constant-time-equal the env value.
4. No users **and** no env token → `gateway.dev_mode_bypass: true` lets the caller through as admin; `false` returns 401 *"no users configured, complete onboarding first"*.

The most common failure is **(4)** with `dev_mode_bypass: false` on a fresh install before you've completed onboarding. The web onboarding wizard works with bypass off (those endpoints use `withOptionalAuth`, not `withAuth`), but if you're hitting `/api/v1/agents` or `/api/v1/sessions` directly, you need either an admin user (run onboarding) or a temporary `OMNIPUS_BEARER_TOKEN=<your-token>` env variable.

For local dev, set `dev_mode_bypass: true` in `config.json` — but **never in production**. The flag triggers a one-time stderr `WARN` at boot and a `503` on a hand-picked allow-list of admin-only routes (e.g. `PUT /api/v1/security/sandbox-config`) as defence in depth.

## LLM call returns 404 *"No endpoints found that support tool use"*

You picked a model that doesn't support tool calling. Omnipus sends a tool list with every LLM request, so the model must support OpenAI-style function calling.

Known offenders on OpenRouter: `google/gemma-2-9b-it`, most small open-source models without explicit tool-use training.

Known-good defaults:
- `z-ai/glm-5v-turbo` (the project's standard demo model)
- `anthropic/claude-3.5-haiku`
- `google/gemini-2.5-flash`
- `openai/gpt-4o`

Change the default in Settings → Providers, or edit `agents.defaults.model_name` in `config.json` to a `model_name` that resolves to one of these in the `providers` array.

## Provider model name issues

Two common shapes:

### "provider X is not a known protocol"

The `provider` field in your `providers[]` entry doesn't match a registered protocol. Run `omnipus providers list` (if available) or check `pkg/providers/factory_provider.go` for the canonical list — OpenRouter is `openrouter`, Anthropic is `anthropic`, etc.

### Provider returns 400 "invalid model ID"

The `model` field in your provider entry is what gets sent to the LLM API verbatim. OpenRouter, for example, expects the **full** model ID including the provider prefix:

- Wrong: `"model": "claude-3.5-haiku"` → OpenRouter rejects.
- Right: `"model": "anthropic/claude-3.5-haiku"` → OpenRouter routes correctly.

Sample working entry:

```json
{
  "providers": [
    {
      "model_name": "fast",
      "provider": "openrouter",
      "model": "z-ai/glm-5v-turbo",
      "api_key_ref": "openrouter_api_key"
    }
  ],
  "agents": {
    "defaults": { "model_name": "fast" }
  }
}
```

`api_key_ref` points to the encrypted credential under that name in `credentials.json`. To set it without editing the file directly:

```bash
omnipus credentials set openrouter_api_key sk-or-v1-...
```

Or use the SPA at **Settings → Security → Credential Vault**.

## "credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets"

The credential store can't be unlocked. Modes tried in priority order:

1. `OMNIPUS_MASTER_KEY` — 64-char hex 256-bit key in the env.
2. `OMNIPUS_KEY_FILE` — path to a 0600 file with the hex key.
3. `$OMNIPUS_HOME/master.key` — auto-loaded if it exists with mode 0600.
4. Auto-generate on a fresh install — fires only when no env key, no key file, and no existing `credentials.json` (otherwise we'd strand the encrypted data).
5. Interactive passphrase prompt — needs a TTY.

If you saw the warning *"Omnipus generated a new master key on fresh install. Key file: …/master.key. BACK THIS FILE UP."* on first boot — heed it. Losing `master.key` loses every credential in `credentials.json`. There is no recovery.

Full credential model: [credential_encryption.md](credential_encryption.md).

## SPA shows stale UI / "old" buttons after a source build

When you build from source, the Go binary embeds the SPA from `pkg/gateway/spa/` via `go:embed`. **That directory is not the Vite output.** You must sync them before rebuilding:

```bash
npm run build                       # builds to dist/spa/
rm -rf pkg/gateway/spa/assets
cp -r dist/spa/* pkg/gateway/spa/   # sync to embed dir
make build                          # rebuilds the Go binary
```

`make build` runs the `spa-embed` target first, so the canonical workflow is just `make build` — skip manual `go build` unless you know why.

To verify the embedded SPA matches the source, grep the hashed bundle:

```bash
grep -c "<YOUR_NEW_STRING>" pkg/gateway/spa/assets/index-*.js   # must be >0
```

## Tasks fail with "priority must be 1-5"

The OpenAPI schema says `priority: 0-100`, but the underlying `taskstore` validator caps at 1-5. Use a priority in that range. (Open contract drift, separate issue.)

## Cross-device rename in skills installer (test environments)

Symptom: `failed to move downloaded file: rename /tmp/omnipus-dl-* /your/workspace/...: invalid cross-device link`.

Cause: the skills installer downloads to `/tmp` then `os.Rename`s into the target workspace. When `/tmp` and the workspace are on different filesystems (common in CI containers with mounted volumes), the rename fails.

Fix: set `TMPDIR` to a path on the same filesystem as the workspace before starting the gateway. `make`/`go test` users should also set `GOTMPDIR` to match.

```bash
export TMPDIR=/your/workspace/tmp
mkdir -p $TMPDIR
```

## Still stuck?

- Read `gateway_panic.log` first, `gateway.log` second.
- Run with `--debug` and `--no-truncate` to see the full LLM request payloads — see [debug.md](debug.md).
- Search [open issues](https://github.com/elicify-ai/omnipus/issues); file a new one with the log excerpt + your config minus secrets.
