# E2E full-run failures — complete root-cause list

Branch `feat/preview-on-main-listener` @ `97614ed0`. Full suite: **104 passed / 37 failed (3.3h)**.
Method: every failing spec re-run in a short **isolated** batch on the same commit (real Playwright
errors pulled, not summaries) + 4 code-tracing subagents. "Pre-existing vs introduced" is not tracked —
every failure gets a root cause regardless.

**Headline:** only **6** of 37 reproduce in a short isolated run (deterministic). The other **31** pass in
isolation and fail only in the full 3.3h serial run — driven by **two systemic mechanisms** (both real,
both fixable) plus a few structural test/code budget defects. None is an auth regression (the cookie/CSRF
path is added fail-closed ahead of bearer, has no retry-storm/TTL/rate-limit change, and auth rejection
fast-fails — it does not hang).

---

## A. Deterministic failures (reproduce in a short isolated run)

| Test | Root cause | Fix |
|---|---|---|
| `web-serve-canonical.spec.ts:166` (legacy_run_in_workspace_warmup_replay) | **Production regression.** ADR-044 merged `/preview/` onto the SPA mux and removed the legacy `/serve/`+`/dev/` handlers, but the frontend still renders/probes those legacy paths. An unmatched `/serve/…` probe falls through the SPA catch-all (`pkg/gateway/embed.go:57-60`) → **200 index.html** instead of 404, so a dead legacy preview link falsely renders "ready" instead of warmup→error. | Make the retired `/serve/`+`/dev/` prefixes return non-200 (register them on the main mux → 404 / route to `HandlePreview`) so the warmup probe correctly detects the dead link. |
| `settings-gateway.spec.ts:125` (no hot-reload toggle) | **Stale test.** Asserts no `[role="switch"]` except `god-mode-toggle`, but the branch intentionally added the ADR-044 live **"Preview server"** toggle (`GatewaySection.tsx`, `aria-label="Preview server"`, no `data-testid`). | Add a `data-testid` to the Preview switch; exempt it in the `:not(...)` selector alongside `god-mode-toggle`. |
| `settings-gateway.spec.ts:173` (retained/removed controls) | Same "Preview server" toggle trips the same assertion. | Same as above. |
| `iframe-preview-warmup.spec.ts:41` (warmup_default_60s) | **Test bug.** Uses `page.clock.fastForward(62_000)`, which fires a recurring `setInterval` **once**; the warmup countdown needs 30 ticks to reach the `error` phase, so it never does. (The test's own comment even describes `clock.tick()` behavior.) | `fastForward(62_000)` → `runFor(62_000)`. |
| `bug-regression.spec.ts:60` (Bug-1-b Get Started button) | **Broken test.** `getByText('Welcome to Omnipus')` collides with the **ChatScreen empty-state heading** (`ChatScreen.tsx:1622`), not an onboarding welcome step (which doesn't exist — onboarding uses numbered steps + "Continue" + a "Meet your Assistant" completion screen). Under the authenticated e2e storageState, `/onboarding` redirects to the app → chat empty-state → the `if` enters and asserts a "Get Started" button that was never there. | Fix/scope the test to the onboarding container (there is no "Get Started" button; the forward action is "Continue"). |
| `memory-remember-recall.spec.ts:367` | **Fragile / model-dependent** (fails in isolation at 53s). The `remember` tool wrote no file (shared workspace memories dir absent, agent-private dir empty) → glm-5.2 most likely never emitted the `remember` tool call (documented glm tool-calling unreliability), or the write path differs. | Get the chat transcript to disambiguate model-didn't-call vs code-didn't-write; strengthen the prompt to force the tool call and/or verify `pkg/memrooms/memory_file.go WriteMemoryFile` path. |

---

## B. Load-dependent failures (pass in a short isolated run; fail only in the full 3.3h serial run)

The suite runs `workers:1, fullyParallel:false` (`playwright.config.ts`) — one long-lived gateway, one
shared `OMNIPUS_HOME`, one shared rate-limited `glm-5.2` key, ~42 spec files in series, nothing reset
between them.

### Mechanism A — real-LLM latency under cumulative load (documented in-repo as "Group-A variance")
Leaked agent turns (turns intentionally outlive the browser tab — `tests/e2e/fixtures/console-errors.ts:22-36`)
+ **no server-side LLM-call timeout** (`pkg/config/defaults.go:45` `TimeoutSeconds:0`) + one shared
rate-limited key → cumulative/leaked calls congest the provider window; slow (not fast-failing) calls trip
Playwright's per-assertion 30s gates (the "~32s" signature) or blow the outer budget.
`pkg/gateway/websocket.go:2100-2106` explicitly names `subagent (a)-(e)` and `handoff (b)` as this known family.

- `cancel-cross-channel.spec.ts` T21, T22, T23, T25, T26 — 30s time-to-first-token gate
- `cancel-cross-channel.spec.ts` T24a, T24b — 2-leg delegation cascade (delegate + subagent)
- `chat.spec.ts (e)` cancel-mid-reply — 30s gate, no `test.slow()` (least headroom)
- `subagent.spec.ts (a), (b), (e)` — delegation chains, ≥3 sequential model calls
- `replay-fidelity.spec.ts (c), (d)` — live LLM turns
- `handoff.spec.ts (b)` — delegation (delegate → child bash → reply)
- `media.spec.ts (a)`, `open-in-chat.spec.ts (b)`, `whatsapp-qr.spec.ts (D real backend)` — live turn / real-network timing under load
- `chat.spec.ts (b)` multi-turn retention — **Mechanism A + a self-contained budget bug:** the test allocates only a 10s gap per turn transition, but `waitForTurnFullyDone` can legitimately wait **180s/turn** (follow-up tool call); worst case 540s > the test's own 260s margin → the observed **21.8-min hang**.

### Mechanism B — `ListSessions()` O(N) scan under a single global mutex
`pkg/session/unified.go:88` is a plain `sync.Mutex` shared by every store op; `ListSessions()` (`:848-877`)
does `os.ReadDir` + a per-session-dir disk read, O(N) in total sessions, holding that one lock for the whole
scan. Every "Open sessions panel" click fires `GET /sessions` → this scan; it degrades as sessions accumulate
across the run and can be blocked by a lingering background delegate write. (A **real product scalability
bug**, not just a test artifact.) Affects the zero-LLM tests:

- `replay-fidelity.spec.ts (a), (b), (e), (f)` — transcript-seeded; `openSession()` scan (some call it twice)
- `handoff.spec.ts (a)` (Ray→Ava→Jim chain) — transcript-seeded, **zero LLM** (not a live delegation)
- `retention.spec.ts` (both) — zero LLM; pre/post-sweep session-list checks
- `multi-turn-render.spec.ts` — zero LLM; transcript seeded to disk
- `settings-memory.spec.ts` Test 1 (persist across reload) — zero LLM
- `web-serve-malformed.spec.ts:34, :152` — injected results; session-open/list path under gateway load

### Structural test/code budget mismatches exposed under load (real defects)
- `settings-memory.spec.ts` Test 2 (auto-recap) — the test polls **30s** for a recap file, but the recap
  pipeline is allowed **60s** before it writes its fallback (`pkg/agent/session_end.go:252`); a recap taking
  30–60s produces no file at the deadline. **30 < 60 is a real mismatch.**
- `recap-continuity.spec.ts` — the recap **fallback template** (`session_end.go:515`) is
  `"Session X ended. Turns: N…"` and **structurally cannot contain the asserted nonce**; if the recap LLM
  call degrades to the fallback under load, the content assertion fails deterministically.

### Gateway-responsiveness victim (no LLM)
- `idle-no-reconnect.spec.ts` — delayed WS pong under a gateway ~3h in carrying many still-running (possibly
  leaked) turns/goroutines reproduces the exact reconnect this regression test guards against. (Most
  inferential row — no direct trace from the run.)

---

## Fix themes (regardless of pre-existing)

1. **Preview `/serve/`+`/dev/` 404** (production correctness) — the one true regression in the branch.
2. **Test corrections** — `settings-gateway` (data-testid + exempt), `iframe-preview-warmup` (runFor),
   `bug-regression Bug-1-b` (scope selector), `chat (b)` budget, `settings-memory` Test 2 (30s→≥60s poll),
   `recap-continuity` (don't assert nonce content on the fallback path).
3. **Systemic e2e-harness hardening** — bound/cancel leaked turns reliably at teardown, and/or a test-only
   server-side LLM-call ceiling, and/or per-file isolation, to kill Mechanism A "Group-A variance."
4. **`ListSessions()` scalability** (real product bug) — pagination / cache / sharded lock so the session-list
   path is not O(N)-under-one-mutex; fixes Mechanism B and helps real users with many sessions.
5. **`memory-remember-recall`** — confirm model-vs-code from the transcript.
