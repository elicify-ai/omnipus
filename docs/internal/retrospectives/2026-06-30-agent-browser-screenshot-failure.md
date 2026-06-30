# Incident Post-Mortem — "Go to elicify.ai and take a screenshot" failed; the agent then fought the sandbox

**Date:** 2026-06-30
**Reporter:** engineering (grounded in the live transcript + gateway logs + code)
**Severity:** High — a first-class, advertised agent capability (browser automation / screenshots) is **dead-on-arrival** in this deployment, and the failure mode is expensive and user-hostile.
**Affected sessions (workspace `01KWA08EH5B7CZDBRF2DK3C2ME`):**
- `session_01KWBQS3J3BS5NH4E14R7AQHYH` — "please go to eleicify.ai and take a screenshot" (Mia, model `z-ai/glm-5v-turbo`, **136.4k tokens**, 2026-06-30 07:44–07:46)
- `session_01KWA0X386AJ87NS0KM2B9A02Y` — "hi" (2026-06-29 16:13) hit the **identical** error → this is recurring, not a one-off.

---

## 1. What the user experienced (the blunt version)

A trivial request — "screenshot this website" — turned into a ~2.5-minute, **136,000-token** ordeal across **three agents and two hand-offs**, ending in *no screenshot*. Along the way the product:

- told the user it had no browser tools (false),
- leaked raw sandbox internals ("`Cannot fork`", "snap-only stub", "`Permission denied`") into the chat,
- did exactly what its own system prompt **explicitly forbids** (shelling out to `apt`/`npm` to install a browser), and
- only at the very end fell back to the thing it should have done in the first second (fetch the page text and summarize it).

The end answer ("here's what's on elicify.ai…") was actually fine. The path to it was a small disaster.

---

## 2. Timeline (from the transcript + `logs/gateway.log`)

| # | Actor | Action | Result |
|---|-------|--------|--------|
| L0 | user | "please go to eleicify.ai and take a screenshot" | — |
| L1–L2 | **Mia** | `hand_off` → **Ray** ("our Scout… handles browser automation") | ok |
| L7 | **Ray** | `load_tool(["browser_navigate","browser_screenshot"])` | **error** |
| L8 | **Ray** | "I don't have access to browser automation tools… restricted for my agent." → **gives up**, offers to fetch instead | capitulation |
| L9 | user | "why did you stop, hand over to jim" | (frustration) |
| L11 | **Ray** | `hand_off` → **Jim** (Orchestrator) | ok |
| L14 | **Jim** | `load_tool(["browser_navigate","browser_screenshot"])` | **ok** (≠ Ray!) |
| L16 | **Jim** | `browser_navigate("https://eleicify.ai")` | **error**: SSRF DNS `no such host` (user typo) |
| L18 | user | "elicify.ai" (correct spelling) | — |
| L20 | **Jim** | `browser_navigate("https://elicify.ai")` | **error**: *chrome failed to start — `/usr/bin/chromium-browser` requires the chromium snap* |
| L21 | **Jim** | "Chromium isn't available… needs to be installed first. Let me set that up." | ← the fatal misread |
| L22–L26 | **Jim** | `exec`: `which chromium`, `apt-get install chromium`, … | **blocked**: `Could not open lock file /var/lib/apt/lists/lock … Permission denied` |
| L27–L30 | **Jim** | `load_tool(workspace_shell)` → `npm install puppeteer-core` | **blocked**: `Cannot fork` (seccomp) |
| L31 | **Jim** | "this environment has significant restrictions…" (leaks sandbox internals) | — |
| L32 | **Jim** | `fetch_url("https://elicify.ai")` | **success** |
| L33 | **Jim** | Summarizes the site text | done (no screenshot) |

---

## 3. Root cause (the code-level defect)

**`/usr/bin/chromium-browser` on this host is a 2,408-byte Ubuntu snap-redirect *shell stub*, not a browser:**

```sh
#!/bin/sh
if ! [ -x /snap/bin/chromium ]; then
    echo "Command '$0' requires the chromium snap to be installed." >&2
    echo "Please install it with:  snap install chromium" >&2
    ...
```

The browser manager resolves the Chrome binary like this (`pkg/tools/browser/manager.go:294`):

```go
for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
    if path, err := exec.LookPath(name); err == nil {
        return path, nil          // ← returns the snap STUB; never validates it launches
    }
}
installRoot := ...                // ← managed Chrome-for-Testing auto-installer
return EnsureChromium(ctx, installRoot)   // ← NEVER REACHED
```

`exec.LookPath("chromium-browser")` succeeds (the stub is executable and on `$PATH`), so `resolveExecPath` returns it and **short-circuits before the managed auto-installer** (`EnsureChromium`, which downloads Chrome-for-Testing on first use). chromedp then launches the stub, which immediately exits with the snap nag → `chrome failed to start`.

**The "Chromium installs on first use" safety net is real and would have worked** (machine is `x86_64` → `cftPlatform()` returns `linux64`, a supported build), **but a found-yet-non-functional binary defeats it.** No `chromium/` install dir was ever created under the data dir — confirming the auto-installer never ran.

> This is the single most important finding: **the bug is not "no browser is installed." The bug is that a fake browser on `$PATH` is trusted over the auto-installer.**

---

## 4. Everything that went wrong — critical analysis

### 4.1 — [Critical] Browser capability is dead-on-arrival on this host
A headline agent capability (`browser_navigate`, `browser_screenshot`) cannot run at all here, and has failed identically across at least two sessions on two different days. Any user who asks any agent to screenshot/scrape a page hits this. **Impact:** the feature is advertised but non-functional; every attempt is a guaranteed, expensive failure.
**Fix (P0):** after `exec.LookPath` finds a candidate, **validate it is a real browser** before trusting it — e.g. probe `<bin> --version` and require a `Chromium`/`Google Chrome` version string, or detect the snap-wrapper signature (`#!/bin/sh` + "requires the chromium snap"). On validation failure, **fall through to `EnsureChromium`**. Belt-and-braces: set `OMNIPUS_BROWSER_FORCE_MANAGED=1` in the devpod image (the code already honors it, `manager.go:289`), and/or ship a real headless Chrome in the base image.

### 4.2 — [Critical] The managed auto-installer was silently bypassed
The whole point of `EnsureChromium` is to make the tool self-provisioning. The `$PATH`-wins ordering means a distro stub permanently masks it. **Fix:** same as 4.1 — the `$PATH` lookup must yield only *validated* binaries; an invalid hit should be treated as "not found," not "found."

### 4.3 — [Critical] The agent did exactly what its own prompt forbids — and burned 136k tokens doing it
The core agent prompt is unambiguous (`pkg/coreagent/core.go:1108`, `:1325`):
> *"NEVER use `workspace_shell` or `exec` to run chromium / google-chrome / puppeteer / a CLI screenshot utility, and never npm-install a browser package — the `browser_*` tools already do this for you, sandboxed."*

Jim ignored this and ran `apt-get install chromium`, then `npm install puppeteer-core`. Both are categorically blocked by the deny-by-default sandbox (apt lock = `Permission denied`; fork = seccomp `Cannot fork`). **This is the bulk of the 136k tokens** — a doomed self-provisioning loop the prompt explicitly prohibits.
**Why it happened:** the tool's error string ("…requires the chromium snap to be installed. Please install it with: `snap install chromium`") is an *instruction to install*, and the model (a mid-tier `glm-5v-turbo`) followed the error over the system prompt. **Fixes (P1):** (a) the tool must never surface the raw snap message — return one clean, *non-actionable-by-the-agent* error (see 4.5); (b) strengthen the contract so a missing-dependency error self-describes "do not attempt to install system software; fall back to `fetch_url`."

### 4.4 — [High] Capability inconsistency: the *browser specialist* couldn't load the browser tools
Ray — literally described as the agent who "handles web research and **browser automation**" — got an **error** on `load_tool(["browser_navigate","browser_screenshot"])` (L7) and then **hallucinated** an explanation ("restricted for my agent") and gave up. Jim loaded the same tools fine (L14). So either Ray's tool policy genuinely excludes the browser tools (a routing/seed bug — the wrong agent is the "browser" specialist), or `load_tool` failed for a non-policy reason and Ray invented a story. Both are bad. **Fix (P1):** audit which agents have the browser tools in policy; the Scout/browser agent must actually possess them. Separately, an agent should **report the real `load_tool` error**, not fabricate a capability restriction.

### 4.5 — [High] Sandbox internals leaked into the user-facing chat
The user was shown "`Cannot fork`", "snap-only stub", "`Permission denied`", "no permissions for `apt`/`snap`". This frames the *product* as broken and exposes the security posture. **Fix:** the browser tool should return a single, branded, actionable message — e.g. *"Browser screenshots aren't available in this deployment (no Chromium runtime). I can fetch and summarize the page instead."* — and the agent should relay that, not the kernel-level cause.

### 4.6 — [High] No circuit-breaker on repeated failed self-provisioning
Jim issued ~8 failed `exec`/`workspace_shell` calls in a row (every install path). Nothing stopped the loop; it ran until the model gave up on its own. **Fix (P1):** after *N* consecutive failed exec/provisioning attempts for the same goal, hard-stop and force the fallback path.

### 4.7 — [Important] Delegation churn for a one-call task
Mia → Ray → (capitulate) → user prod → Jim. Two hand-offs, one capitulation, and a required user nudge for "screenshot a page." Each hop adds latency, tokens, and re-establishes context. **Fix (P2):** the router should send a browser task straight to a capable agent; and a capable agent shouldn't bounce on the first `load_tool` hiccup.

### 4.8 — [Important] Token economics
**136,400 tokens** for a task whose happy path is one `browser_navigate` + one `browser_screenshot` (or, on failure, one `fetch_url`). The waste is concentrated in 4.3 (install loop) and 4.7 (handoffs). This is the cost signature of "no graceful degradation + no circuit breaker."

### 4.9 — [Minor] One genuine non-bug muddied the water
The first `browser_navigate` failed on a **real** DNS error (`eleicify.ai`, a user typo → SSRF DNS `no such host`). That's the SSRF guard working correctly — but it sat right next to the Chromium failure, making the whole thing read as one big breakage. The error messages didn't distinguish "the site doesn't exist" from "we have no browser."

---

## 5. The deeper theme

Two correct security mechanisms (deny-by-default sandbox: no `apt`, no `fork`) collided with two product gaps (a stub masking the auto-installer; a leaky, install-suggesting error message), and a mid-tier model that **trusts a tool's error text over its system prompt**. The sandbox did its job perfectly. The product gave the agent every reason to fight it, and no brake to stop. The result is the worst of both worlds: a guardrail that holds, and an agent that exhausts itself (and 136k tokens) discovering the guardrail one failed call at a time.

---

## 6. Recommended fixes (prioritized)

| P | Fix | Where |
|---|-----|-------|
| **P0** | Validate a `$PATH` Chrome candidate (probe `--version`, reject the snap stub) before trusting it; fall through to `EnsureChromium` on failure. | `pkg/tools/browser/manager.go:294` (`resolveExecPath`) |
| **P0** | Make the devpod/deploy work *today*: set `OMNIPUS_BROWSER_FORCE_MANAGED=1` (skips the broken `$PATH` lookup) **or** bake a real `chrome-headless-shell` into the image. | devpod image / env |
| **P1** | Browser tool returns ONE clean, branded error on a launch/init failure; never surface `snap install` / `Cannot fork` / `Permission denied` to the agent or user. | `pkg/tools/browser/manager.go`, tool wrapper |
| **P1** | On any missing-dependency tool error, the agent contract must forbid `apt`/`snap`/`npm` self-install and route to `fetch_url`. Reinforce the existing `core.go` guardrail with an error-shaped "do not install" signal. | tool-error contract + `pkg/coreagent/core.go` |
| **P1** | Circuit-breaker: stop after N consecutive failed `exec`/`workspace_shell` provisioning attempts for one goal. | agent loop |
| **P1** | Fix the Scout/browser-agent (Ray) tool policy so the designated browser agent actually has the browser tools; stop agents from fabricating capability restrictions. | agent seeds / routing |
| **P2** | Reduce handoff churn for single-capability tasks. | router |

---

## 7. What went right
- The **SSRF guard** correctly caught the typo domain.
- The **deny-by-default sandbox** blocked every unsafe install attempt exactly as designed.
- The **`fetch_url` fallback** ultimately produced a useful, accurate summary of the real site — it was just reached last and expensively.

---

## Appendix — primary evidence

**Gateway log (`logs/gateway.log`), the defining error (seen 2026-06-29 *and* 2026-06-30):**
```
tool=browser_navigate error="browser_navigate: browser: failed to initialize tab: chrome failed to start:
Command '/usr/bin/chromium-browser' requires the chromium snap to be installed. Please install it with: snap install chromium"
```
```
tool=exec error="E: Could not open lock file /var/lib/apt/lists/lock - open (13: Permission denied)" [exit 100]
```

**The stub itself:**
```
$ file /usr/bin/chromium-browser → POSIX shell script, 2408 bytes (Sep 18 2020)
$ /usr/bin/chromium-browser --version → "Command '...' requires the chromium snap to be installed."
```

**Resolution order that trusts it:** `pkg/tools/browser/manager.go:289-302` (`resolveExecPath`) — `$PATH` lookup returns before `EnsureChromium`.
**Prompt the agent violated:** `pkg/coreagent/core.go:1108`, `:1325` ("NEVER … npm-install a browser package").

> **Correction to §4.4 (added after code review):** Ray's `LoadToolPolicy` **does** allow the browser tools (`pkg/coreagent/core.go:376-381`, asserted by `pkg/coreagent/constructor_seed_test.go:77`) — identical to Jim's (`:469-478`). So Ray's "restricted for my agent" was **not** a real policy gap; it was a hallucinated paraphrase of whatever `load_tool` actually returned (the transcript only kept the paraphrase). The browser tools are `ManifestLazy`, so both agents must `load_tool` them first; why Ray's load returned an error while Jim's succeeded ~50s later is unconfirmed and needs the raw error captured. See FIX-7.

---

## 8. Remediation design — one fix per surfaced issue

Each fix: **root → change (file:line) → trade-off → effort/risk → test → priority**. Fixes are grounded in a code read of `pkg/tools/browser/{manager,installer,tools}.go`, `pkg/tools/tools_tool.go`, `pkg/agent/loop.go`, `pkg/tools/workspace_shell.go`, `pkg/tools/handoff.go`, and `pkg/coreagent/core.go`.

### FIX-1 — Validate the resolved Chrome binary; reject the snap stub *(P0, the keystone)*
- **Root:** `resolveExecPath` (`manager.go:293-298`) returns the first `exec.LookPath` hit unvalidated; the Ubuntu `chromium-browser` snap stub is a hit, so `EnsureChromium` (the auto-installer at `:300-302`) never runs.
- **Change:** after a `LookPath` success, probe `exec.CommandContext(<bin> --version)` with a ~2s timeout; require stdout to match `Chromium|Google Chrome`. On non-match / non-zero / the snap-wrapper signature, `continue` (treat as not-found) so resolution falls through to `EnsureChromium`. A detection harness already exists in `browser_e2e_test.go:52-58` — reuse it. Cache the validated path so the probe runs once.
- **Trade-off:** one extra ~tens-of-ms exec at first browser use. Negligible. Alternative (read the file and string-match the snap shim) is cheaper but more brittle than `--version`.
- **Effort/Risk:** ~25 LOC + test / **Low**.
- **Test:** put a fake `chromium-browser` stub (exit 1, prints the snap message) on a temp `PATH`; assert `resolveExecPath` skips it and invokes the managed-install path.
- **Why first:** this alone makes the capability *work* on the devpod and removes the trigger for the entire cascade (4.3, 4.5, 4.6).

### FIX-2 — Unblock the deployment *today* *(P0, ops stopgap)*
- **Root:** even before FIX-1 ships, the pod has no working browser.
- **Change (pick one):** (a) export `OMNIPUS_BROWSER_FORCE_MANAGED=1` in the devpod env — `manager.go:292` makes this skip the `$PATH` lookup entirely and go straight to `EnsureChromium`; (b) bake `chrome-headless-shell` into the base image and point `browser.exec_path` at it; (c) pre-run the managed install at image-build time.
- **Verify first:** `EnsureChromium` downloads from `googlechromelabs.github.io` + the CfT CDN via pure-Go HTTP (`installer.go:74-112`, no fork) into `<ProfileDir>/../chromium`. Confirm (i) the sandbox egress allow-list permits that HTTPS host (v0.2 internal-CIDR blocking shouldn't, but check) and (ii) the install dir is Landlock-writable. If either is denied, only options (b)/(c) work.
- **Effort/Risk:** trivial→small / **Low**. This is the bridge until FIX-1 lands.

### FIX-3 — Clean, branded, *classified* browser-tool errors *(P1)*
- **Root:** `tools.go` returns the raw error to the model — Session/launch errors pass through unchanged (`:61`, `:229`) and page-load errors are prefixed but otherwise raw (`:72-73`). DNS-not-found and chrome-failed-to-start are indistinguishable, and the snap message ("Please install it with: snap install chromium") is what *invited* the install loop.
- **Change:** add `classifyBrowserError(err) (userMsg string)` and apply at the three return sites. Map: *cannot-locate/launch-failed* → "Browser screenshots aren't available in this deployment (no working Chromium runtime)."; *SSRF DNS no-such-host* → "That URL didn't resolve — check the domain."; *SSRF blocked* → "That URL is blocked by the egress policy." Never echo `snap`, `fork`, or `Permission denied`.
- **Trade-off:** classification by substring is slightly fragile; keep a default that's still scrubbed of install hints.
- **Effort/Risk:** small-medium / **Low**.
- **Test:** feed synthetic errors → assert branded text + absence of `snap`/`fork`/`Permission denied`. Covers issues 4.5 and 4.9.

### FIX-4 — Make the dependency error a *"do not install, fall back"* directive *(P1)*
- **Root:** the static prompt forbids self-install (`core.go:1106-1108`) but a mid-tier model followed the tool's install-suggesting error instead. There is no tool-error channel to carry positive guidance.
- **Change:** add an optional `Guidance string` (or `FallbackHint`) field to `ToolResult` (`pkg/tools/result.go`). The browser tool's unavailable-Chromium error sets it to: *"Browser is unavailable here. Do NOT try to install one — apt/snap/npm are blocked by design. Use `fetch_url` to read the page instead."* The loop appends `Guidance` to the tool-result content the model consumes (`loop.go:6984-7061`).
- **Trade-off:** one new optional field; opt-in per tool, no behavior change elsewhere.
- **Effort/Risk:** medium / **Low**. Pairs with FIX-3 (remove the bad suggestion) to fully neutralize the loop.
- **Test:** unit-assert the browser error carries the directive; loop-test that it lands in the model-visible content.

### FIX-5 — Circuit-breaker on consecutive failed `exec`/`workspace_shell` *(P1)*
- **Root:** the existing synthetic-error floor (FR-084, `loop.go:7257-7319`) counts **policy denials**, not execution **failures**; nothing stops ~8 failing shell calls. `turnState` (`turn.go`) tracks iteration count but not per-tool failures.
- **Change:** add `consecutiveExecFailures int` + `lastTool string` to `turnState`; after tool execution (`loop.go ~6833`, where `toolResult.IsError` is known), increment for `exec`/`workspace_shell` errors, reset on any success/other-productive tool. At threshold (≈3) inject a synthetic guidance turn ("repeated shell failures — the sandbox blocks installs; stop and use built-in tools or report the limitation") via the existing `recordSyntheticDeny`/abort plumbing.
- **Trade-off:** must not break legitimate retries — scope to `exec`/`workspace_shell`, conservative threshold, reset on success.
- **Effort/Risk:** medium / **Medium** (touches the hot loop; needs careful reset logic).
- **Test:** drive N failing exec results → assert the breaker fires and guidance is injected; assert a success resets the counter.

### FIX-6 — Sanitize shell errors before model + user *(P1)*
- **Root:** `workspace_shell.go:257` / `result.go:222-229` embed raw stderr (`Cannot fork`, apt lock `Permission denied`) into `ForLLM` **and** `ForUser`; only *web/untrusted* results get prompt-guard scrubbing (`loop.go:6948-6982`) — shell errors don't.
- **Change:** classify common sandbox-denial stderr (seccomp fork, apt-lock, snap) into a concise "blocked by sandbox policy" summary for `ForLLM`, and ensure shell errors never go to the **user** channel verbatim (`ForUser`). Keep enough signal for the model to know it's a hard block (so it stops, see FIX-5), but drop kernel strings.
- **Effort/Risk:** small-medium / **Low**. Directly fixes the user-facing leak (4.5) on the shell path.
- **Test:** failing `apt-get`/fork command → assert `ForUser` empty/branded and `ForLLM` scrubbed.

### FIX-7 — Truthful capability reporting (the Ray puzzle) *(P1, partly investigative)*
- **Root:** Ray's policy already allows browser tools (verified), so "restricted for my agent" was invented. Why Ray's `load_tool` errored while Jim's succeeded is unconfirmed (only the paraphrase survives). `load_tool` returns precise reasons (`tools_tool.go:305-312`; `loop.go:1921/1927/1929`: "denied by this agent's policy" / "unknown tool name" / "unknown tool (did you mean…)").
- **Change:** (a) **Capture/repro:** add a test that Ray *and* Jim both load `["browser_navigate","browser_screenshot"]` successfully against a real registry — if it reproduces a divergence, there's a registration/timing bug to chase (e.g. browser tools not yet registered for the first agent); if not, it was a one-off. (b) **Behavior:** add prompt guidance — *"If a tool fails to load, relay the exact error; never assume it's a permission restriction"* — so the model stops fabricating. (c) Ensure the `load_tool` error string is unambiguous enough that a model can't paraphrase "unknown/registration error" into "restricted."
- **Effort/Risk:** small (test + prompt) → medium (if a real registration bug surfaces) / **Low–Medium**.
- **Honesty:** this is the one item not fully root-caused from evidence; the fix is "instrument + reproduce + correct behavior," not a confirmed one-line patch.

### FIX-8 — Delegation churn / capability-aware handoff *(P2, mostly downstream)*
- **Root:** `hand_off` is model-driven (`handoff.go:131-245`); it validates only that the target exists and isn't a worker — no check that the target actually has the tools the task needs. `routing.Router` is model-selection only, not task→agent routing.
- **Change (lightweight):** keep handoff model-driven, but (a) tighten the handoff guidance ("only hand off if you genuinely lack the capability; a missing dependency is not a reason to hand off"), and optionally (b) have `hand_off` add a non-fatal `ForLLM` note when the target's policy doesn't allow an obviously-implied tool. Avoid a heavy task→tool inference engine.
- **Why P2:** with FIX-1 (capability works) and FIX-7 (no capitulation/fabrication), the 3-agent churn largely disappears on its own. This is polish.
- **Effort/Risk:** small / **Low**.

### FIX-9 — Optional boot-time capability preflight *(P2)*
- **Root:** no health gate; failure is deferred to first call (`register.go:33-58`; `loop.go:1800` only logs on register failure).
- **Change:** a cheap "is a usable Chrome resolvable (or installable)?" probe at boot (reuse FIX-1's validator, *without* forcing the full CfT download). If genuinely unavailable, mark the browser tools degraded in the manifest so agents don't advertise/attempt a dead tool, and log a clear WARN.
- **Trade-off:** don't do the heavy managed download at boot; only the cheap resolvability check.
- **Effort/Risk:** small-medium / **Low**. Complements FIX-1/3.

### Sequencing
**Ship FIX-2 now** (env flag, same-day unblock) → **FIX-1 + FIX-3** together (the real bug + clean errors; one PR) → **FIX-4 + FIX-6** (neutralize the install reflex + stop leakage) → **FIX-5** (circuit-breaker, defense-in-depth) → **FIX-7** (instrument the Ray divergence) → **FIX-8/9** (polish). FIX-1 is the keystone; everything after it is hardening so the *next* unforeseen tool gap degrades gracefully instead of burning 100k+ tokens.
