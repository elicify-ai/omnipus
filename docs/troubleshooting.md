# Troubleshooting

Common operator issues, grouped by symptom. If the gateway started, [debug.md](debug.md) covers how to read its logs. If the gateway never starts, this page is the right place.

## The Gateway Exits 0 or 1 Immediately

### "no providers configured. Please add entries to model_list in your config"

You're booting a fresh `~/.omnipus/` with no provider yet.

#### Drive the Web Onboarding Wizard (recommended)

Start Omnipus and visit `http://localhost:5000`:

```bash
omnipus start
```

On a fresh install the gateway boots into limited mode automatically (no flag needed). The wizard at `/onboarding` walks Welcome → Provider → API Key → Model → Account → Done, then the gateway is fully provisioned.

#### Use the Interactive CLI Wizard

Same end result, no browser:

```bash
omnipus onboard
```

This prompts for provider, API key (hidden input), model, and username/password. It writes the encrypted API key into `credentials.json`, the provider entry and your account into `config.json`, and marks onboarding complete in `system/state.json`.

### Gateway Exits Silently — Nothing on stdout

Check `$OMNIPUS_HOME/logs/gateway_panic.log` for the startup error. The runtime panic handler always writes there, even when the rest of the boot path can't log. Once you've fixed the underlying problem, you can delete the file — it's recreated on the next crash.

`$OMNIPUS_HOME/logs/gateway.log` carries the running gateway's normal log stream, useful when the gateway *did* start but a request fails.

### Exit Code 78 ("EX_CONFIG")

The kernel sandbox failed to apply on a kernel that claims Landlock support. This is a **fail-closed** path — the HTTP listener never binds. Inspect `gateway.log` for `sandbox apply failed`, then either lower the kernel's expectations with `omnipus start --sandbox=permissive` (log violations without blocking) or `omnipus start --sandbox=off` for development. The valid values for `--sandbox` are `enforce|permissive|off` (parsed by `sandbox.ParseMode` at `cmd/omnipus/internal/gateway/command.go:62-71`; an invalid value exits 2 with a usage error). The exit-code contract is documented in the command's `Long` help text (`cmd/omnipus/internal/gateway/command.go:45-49`) and the sentinel error path is at `cmd/omnipus/internal/gateway/command.go:100-107`.

Setting `OMNIPUS_ENV=production` causes a recurring stderr nag banner when the sandbox is off or permissive, by design.

Full sandbox configuration: [operations/sandbox-config.md](operations/sandbox-config.md). Known limitations (macOS, Windows, older kernels): [operations/sandbox-limitations.md](operations/sandbox-limitations.md).

## "Port Conflict" — Gateway Appears to Start but You Can't Reach It

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

## "401 Unauthorized" on Every Request

The auth decision tree in `pkg/gateway/auth.go::checkBearerAuth` is:

1. No `Authorization: Bearer …` header on a protected route → **401**.
2. `cfg.gateway.users` populated → token must match a registered user.
3. `OMNIPUS_BEARER_TOKEN` env set → token must constant-time-equal the env value.
4. No users **and** no env token → `gateway.dev_mode_bypass: true` lets the caller through; `false` returns 401 *"no users configured, complete onboarding first"*.

The most common failure is **(4)** with `dev_mode_bypass: false` on a fresh install before you've completed onboarding. The web onboarding wizard works with bypass off (those endpoints use `withOptionalAuth`, not `withAuth`), but if you're hitting `/api/v1/agents` or `/api/v1/sessions` directly, you need either a configured account (run onboarding) or a temporary `OMNIPUS_BEARER_TOKEN=<your-token>` env variable.

For local dev, set `dev_mode_bypass: true` in `config.json` — but **never in production**. The flag triggers a one-time stderr `WARN` at boot and a `503` on a hand-picked allow-list of high-blast-radius routes (e.g. `PUT /api/v1/security/sandbox-config`) as defence in depth.

## LLM Call Returns 404 "No Endpoints Found That Support Tool Use"

You picked a model that doesn't support tool calling. Omnipus sends a tool list with every LLM request, so the model must support OpenAI-style function calling.

Known offenders on OpenRouter: `google/gemma-2-9b-it`, most small open-source models without explicit tool-use training.

Known-good defaults are `z-ai/glm-5v-turbo` (the project's standard demo model), `anthropic/claude-3.5-haiku`, `google/gemini-2.5-flash`, and `openai/gpt-4o`.

Change the default in Settings → Providers, or edit `agents.defaults.default_model` in `config.json` to the exact `{"provider": ..., "model": ...}` pair of one of these entries in the `providers` array.

## Provider Model Name Issues

### "provider X is not a known protocol"

The `provider` field in your `providers[]` entry doesn't match a registered protocol. Run `omnipus providers list` (if available) or check `pkg/providers/factory_provider.go` for the canonical list — OpenRouter is `openrouter`, Anthropic is `anthropic`, etc.

### Provider Returns 400 "invalid model ID"

The `model` field in your provider entry is what gets sent to the LLM API verbatim. OpenRouter, for example, expects the **full** model ID including the provider prefix.

`"model": "claude-3.5-haiku"` is wrong — OpenRouter rejects it. `"model": "anthropic/claude-3.5-haiku"` is right — OpenRouter routes correctly.

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

The credential store can't be unlocked. The unlock modes are tried in priority order:

| Priority | Mode | Description |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` | 64-char hex 256-bit key in the env |
| 2 | `OMNIPUS_KEY_FILE` | Path to a 0600 file with the hex key |
| 3 | `$OMNIPUS_HOME/master.key` | Auto-loaded if it exists with mode 0600 |
| 4 | Auto-generate | Fires only on a fresh install when no env key, no key file, and no existing `credentials.json` |
| 5 | Interactive prompt | Needs a TTY |

If you saw the warning *"Omnipus generated a new master key on fresh install. Key file: …/master.key. BACK THIS FILE UP."* on first boot — heed it. Losing `master.key` loses every credential in `credentials.json`. There is no recovery.

Full credential model: [credential_encryption.md](credential_encryption.md).

## SPA Shows Stale UI / "Old" Buttons After a Source Build

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

## Tasks Fail with "priority must be 1-5"

The OpenAPI schema says `priority: 0-100`, but the underlying `taskstore` validator caps at 1-5. Use a priority in that range. (Open contract drift, separate issue.)

## Cross-Device Rename in Skills Installer (Test Environments)

**Symptom:** `failed to move downloaded file: rename /tmp/omnipus-dl-* /your/workspace/...: invalid cross-device link`.

**Cause:** The skills installer downloads to `/tmp` then `os.Rename`s into the target workspace. When `/tmp` and the workspace are on different filesystems (common in CI containers with mounted volumes), the rename fails.

**Fix:** Set `TMPDIR` to a path on the same filesystem as the workspace before starting the gateway. `make`/`go test` users should also set `GOTMPDIR` to match.

```bash
export TMPDIR=/your/workspace/tmp
mkdir -p $TMPDIR
```

## Still Stuck?

Read `gateway_panic.log` first, then `gateway.log`. Run with `--debug` and `--no-truncate` to see the full LLM request payloads — see [debug.md](debug.md). Search [open issues](https://github.com/elicify-ai/omnipus/issues) and file a new one with the log excerpt and your config minus secrets.
