# ADR-051 Wave 1+2 — Persona UAT Report

**Test type:** Focused persona UAT against the real embedded Go binary (no programmable stub harness).
**UAT plan reference:** `docs/internal/regression/adr-051-media-and-error-uat.md` (Rev 3)
**Implementation under test:** commit `2f4c8d8d` on `sendfile-fix` (head of branch — includes ADR-051 Rev 3 / Wave 1+2 changes per `git status`).
**Built binary:** `/tmp/omnipus-uat` (CGO_ENABLED=0, tags `goolm,stdjson`).
**Report date:** 2026-07-22 (UTC).
**Tested by:** Playwright MCP + curl, against the live SPA on the devpod.

---

## 0. Headline result

**All four chat-driven personas are blocked by a single pre-existing defect unrelated to ADR-051 (DEFECT-P1 below). The 30-case stub-driven matrix from the UAT plan requires programmable provider profiles that this environment does not have; the real-LLM subset (UAT-024/025) cannot run because the chat thread never opens. Two non-chat personas (Verbose Chat settings, REST upload) were exercised and PASS.**

| Persona | Status | Notes |
|---|---|---|
| 1. Non-technical chat user | **BLOCKED — DEFECT-P1** | Chat composer sends; no chat thread ever opens; no assistant response possible. |
| 2. Power user + Verbose Chat + provider error | **BLOCKED — DEFECT-P1** | Cannot trigger an error because the chat thread never opens. Verbose Chat toggle itself works. |
| 3. Log auditor | **PARTIAL — trivially PASS** | No raw provider text in `gateway.log`, `gateway_panic.log`, console, or DOM (trivially, because no LLM call completed). UAT-008/011/017 invariants not directly verifiable in this environment. |
| 4. Archive/document user (REST path) | **PASS** (with env-block caveat) | All three fixtures uploaded and retrieved byte-perfect via REST; **not via the chat composer** (blocked by DEFECT-P1). |

---

## 1. Environment setup and known env-blocks

### 1.1 What was built / started
- Built `/tmp/omnipus-uat` from `2f4c8d8d` (`CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus-uat ./cmd/omnipus/`).
- Isolated `OMNIPUS_HOME=/tmp/adr-051-uat-810` (clean; new install auto-mints `master.key`).
- Config file at `$OMNIPUS_HOME/config.json`:
  ```json
  {
    "version": 1,
    "gateway": { "host": "0.0.0.0", "port": 8081, "dev_mode_bypass": true, "public_url": "https://pod-omnipus.fly.dev" }
  }
  ```
  - **`version: 1` is required** — a config without it aborts boot with `error loading config: unsupported config version: 0` (`gateway_panic.log`). The UAT plan does not document this; the embedded schema requires it.
- Gateway launched on `0.0.0.0:8081`; SPA is the embedded build from `pkg/gateway/spa/`.
- Onboarding completed via REST: `POST /api/v1/onboarding/complete` with `provider: {id: "openrouter", api_key: $OPENROUTER_API_KEY, model: "deepseek/deepseek-chat"}` and `admin: {username: "uat", password: "UATpass-12345!"}`. CSRF requires both `csrf` cookie (seeded via `GET /api/v1/state`) and `X-Csrf-Token` header echoing the cookie value. Response: `{"token":"omnipus_24d4b39d_…","username":"uat"}`.
- Env-var fail-if-unset check: `bash -c ': "${OMNIPUS_E2E_NO_VISION_MODEL:?…}"'` aborts with the expected message; readiness is intact.

### 1.2 Environment blocks (not regressions)

| Block | Symptom | Owner | Status |
|---|---|---|---|
| **EB-1: Port 8080 occupied** | `ss -ltnp` shows `omnipus-release` (PID 19491, started 2026-07-21) bound to `*:8080`. Our binary tried to bind 8080 first; we re-bound to **8081** and updated `config.json`. Playwright targets `http://127.0.0.1:8081/`. The stale binary is owned by another session — **NOT killed** (would disrupt that session). | The other session that owns PID 19491. | Documented; not a defect in the build under test. |
| **EB-2: No programmable stub provider** | The UAT plan §4.4 requires a programmable OpenAI-compatible stub with 13 response profiles (accept-capture, xai-format-then-ok, pdf-reject-then-ok, …). No such harness exists in this devpod. Only the real OpenRouter + `deepseek/deepseek-chat` is available. So the 28 stub-driven cases (UAT-001…UAT-023 + UAT-026…UAT-028) are **not runnable** in this environment. UAT-024/025 are runnable in principle but blocked by DEFECT-P1. | UAT harness / lead agent to decide whether to add a stub harness or accept the 4-persona smoke. | Documented. |
| **EB-3: SPA-embed rebuild not performed** | Plan §4.3 step 2 calls for `npm run build` + sync to `pkg/gateway/spa/` + Go rebuild. The `2f4c8d8d` tree already has `pkg/gateway/spa/` populated; the binary was rebuilt but the SPA was not resynced. No SPA delta was observed during the UAT, so the embedded SPA is consistent with the source tree. | This session. | Documented; not material. |

---

## 2. Defect filed

### DEFECT-P1 — Chat composer can type, but the chat thread never opens; the chat WebSocket authentication handshake times out (BLOCKER for all chat-driven personas)

**Severity:** Blocker (operator-blocking for any chat-driven UAT or live user on this build).
**Test surfaces:** All chat-driven personas (1, 2, 4-via-chat); also blocks UAT-024/025/026/027/028/030 in the full plan.
**Repro:**
1. Fresh install via the embedded binary.
2. `POST /onboarding/complete` (real LLM provider + admin) — succeeds.
3. Navigate to `http://127.0.0.1:8081/#/workspaces/<id>/chat`.
4. Type into the composer, click Send (or press Enter, or call `form.requestSubmit()`).
5. **Observed:** no `/api/v1/sessions` POST, no WS upgrade to `/api/v1/chat/ws`, no chat thread loaded. "Welcome to omnipus.ai. Your agent is ready. Start a conversation below." remains unchanged.
6. `gateway_panic.log` shows repeated `WARN ws: auth read failed error="read tcp 127.0.0.1:8081->127.0.0.1:NNNNN: i/o timeout"` every ~10s (one per WS reconnection attempt).

**Direct WS evidence** (Python WebSocket client with the same cookie state as the SPA):
```
Cookie header: ['Cookie: csrf=yjmBZFd9TZSPTsg6fGEs7xvXGzJkNkmi11D87vgdBq0']
SPA-style recv error: Connection timed out        # no frame sent — server waits 10s
# after sending a bogus auth frame:
RECV after-auth: {"message":"unauthorized: invalid token", …}   # falls through to frame path
```

**Root cause (per project memory + observed behaviour):** `POST /api/v1/auth/login` (and the path that follows) does **not** set the `omnipus-session` HttpOnly cookie. The SPA's `document.cookie` is only `csrf=…`. The WS auth path (`pkg/gateway/websocket.go:719-738`) first tries `middleware.ResolveUserFromCookie`; with no `omnipus-session` cookie, that returns an error and the server falls through to the 10s `conn.ReadMessage()` frame-read path. The SPA no longer sends the legacy `{"type":"auth","token":…"}` frame (per the comment at `websocket.go:700-707`), so the read times out and the session never opens. **This matches the project memory entry "Session-Cookie Auth Half-Wired: omnipus-session HttpOnly cookie built+issued at login but SPA + main checkBearerAuth still bearer-only; ADR-044 US-5 finishes it."**

**Not in scope for ADR-051:** this defect is pre-existing on the Wave 1+2 changes — the persona UAT cannot complete until the session cookie is wired. The lead agent should coordinate the fix wave separately (or confirm the half-wired state is acceptable and update the persona UAT plan to require an alternative auth path).

**Status for ADR-051 acceptance:** the persona UAT is **environment-blocked** for all chat-driven cases. The 30-case stub-driven matrix remains unrunnable until a stub harness exists.

---

## 3. Per-persona results

### Persona 1 — Non-technical chat user
- **Goal:** load the home page, type a query, submit, verify a response.
- **Executed:** Navigated to `http://127.0.0.1:8081/` → routed to `/#/workspaces/<id>/chat` → typed via Playwright `fill`, via the React native-setter, and via the Playwright `submit` helper → clicked `data-testid="chat-send"` (which became enabled when the textarea had a value) → called `form.requestSubmit()` directly.
- **Observed:** no `POST /api/v1/sessions`, no `/api/v1/chat/ws` upgrade, no bubbles. Token counter stuck at "0 tokens".
- **Screenshots:** `01-chat-user-landing.png`, `02-chat-user-typed.png`, `04-chat-user-sending.png`, `05-composer-typed-pre-send.png`.
- **Result:** **FAIL — DEFECT-P1.** A non-technical chat user would see a working-looking composer and be unable to send a message. The "Welcome to omnipus.ai" placeholder never gives way to a thread.

### Persona 2 — Power user + Verbose Chat + provider error
- **Goal:** Settings → Chat → toggle Verbose Chat on → trigger an error → verify translated bubble + "Technical details" disclosure.
- **Executed:** Navigated to `/#/settings`, clicked the 8th tab (`Chat`). Captured `06-settings-chat-verbose-off.png`. Clicked the only `[role="switch"]` (`data-testid="chat-verbose-switch"`). aria-checked flipped from `false` to `true`; dataset.state = `checked`. Captured `07-settings-chat-verbose-on.png`.
- **Observed:** Verbose Chat toggle UI works correctly. **Cannot trigger the error path** because the chat composer is broken (DEFECT-P1), so UAT-023 (Verbose Chat gate off/collapsed/expanded) is not exercised.
- **Result:** **PARTIAL — Settings UI PASS; chat-driven error path BLOCKED.** A "Technical details" disclosure string does not appear anywhere in the rendered DOM (verified by `document.body.innerHTML` regex search for `Verbose|verbose|Technical|technical|Show\s+technical` — only the toggle label matches).

### Persona 3 — Log auditor (raw provider text only in `gateway.log`)
- **Goal:** confirm raw provider JSON, request ids, incident strings never reach the DOM, console, or transcript; they must only be in `gateway.log`.
- **Executed:** Tailed `$OMNIPUS_HOME/logs/gateway.log` and `gateway_panic.log` across all persona interactions. Searched the rendered DOM (via `document.body.innerText` / `innerHTML`) and the browser console (`browser_console_messages` at `error` level) for any of: `valid JPG, PNG, WebP, or ICO image`, `Provider returned error`, `uat-secret-request-400`, `uat-secret-request-413`, `uat-secret-policy`, `uat-secret-429`, `context length exceeded`, `does not support image input`.
- **Observed:** Zero matches in the DOM and console. In the logs the only chat-related entries are the WS-auth-timeout warnings (see DEFECT-P1). No raw provider body / request id appears anywhere — **trivially, because no LLM call completed** in this environment.
- **Result:** **PARTIAL — invariants not violated; invariants not actually verified (no LLM traffic).** This persona can only be fully exercised once DEFECT-P1 is resolved or a stub harness is wired.

### Persona 4 — Archive / document user (upload + normalization)
- **Goal:** upload a real 1×1 PNG, a real 1×1 PDF, a real ZIP; verify each is handled correctly.
- **Executed via the REST upload endpoint** (the chat composer is blocked by DEFECT-P1, so the SPA upload-via-composer path is not exercised).
  - Fixtures (under `docs/internal/regression/adr-051-uat-evidence/fixtures/`):
    - `tiny.png` 69 B, SHA-256 `e7d6db07…3edb16` (valid 1×1 PNG, hand-built, magic bytes `89 50 4e 47`).
    - `tiny.pdf` 295 B, SHA-256 `c043adae…b9758d` (`%PDF-1.4` with one page, valid `xref` table).
    - `tiny.zip` 257 B, SHA-256 `6431a74a…6a879640` (valid ZIP with `hello.txt` + `nested/notes.csv`).
  - `POST /api/v1/upload?session_id=adr-051-uat-session` with multipart `file` field. All three returned `201 Created` with `media://` refs.
  - `GET /api/v1/uploads/adr-051-uat-session/tiny.{png,pdf,zip}` returned the bytes; SHA-256 matched exactly. Content-Type: `image/png`, `application/pdf`, `application/zip` (the ZIP upload was sent as `application/octet-stream` because `python3 zipfile` does not set MIME; the server correctly persisted and served it with the canonical ZIP MIME).
- **Observed:** the upload path is fully working at the REST level. **The chat-composer upload + ADR-051 normalization path (PNG-decode, PDF-text-extract, ZIP-manifest, OOXML sniff) is not exercised**, because the chat composer cannot send any turn. UAT-012/013/014/015/016 of the full plan remain unrun.
- **Result:** **PARTIAL — REST upload PASS; chat-driven normalization not exercised.**

---

## 4. Per-case BDD/edge traceability (UAT plan §6)

| Spec BDD / edge case | UAT case | Status | Notes |
|---|---|---|---|
| JPEG normalized to PNG | UAT-001 | NOT RUN | Blocked by DEFECT-P1 (no chat → no outbound LLM call to inspect). |
| Static / animated GIF | UAT-002, UAT-003 | NOT RUN | same. |
| WebP/BMP/TIFF normalization | UAT-004 | NOT RUN | same. |
| AVIF/HEIC/SVG safe fallback | UAT-005 | NOT RUN | same. |
| Corrupt / oversize / missing image | UAT-006A/B/C | NOT RUN | same. |
| One of N images rejected | UAT-007 | NOT RUN | same. |
| xAI incident string → 1 retry | UAT-008 | NOT RUN | same. |
| Capability-absence → 1 retry | UAT-009 | NOT RUN | same. |
| PDF rejection by non-capable model | UAT-010 | NOT RUN | same. |
| Second media rejection terminal | UAT-011 | NOT RUN | same. |
| ZIP/TAR.GZ manifest | UAT-012 | NOT RUN | same. |
| Opaque EXE metadata note | UAT-013 | NOT RUN | same. |
| OOXML wrong extension / renamed | UAT-014A/B | NOT RUN | same. |
| PDF downgraded before call | UAT-015 | NOT RUN | same. |
| Archive safety edges (cap, protected, corrupt) | UAT-016A/B/C | NOT RUN | same. |
| Generic 400 translation | UAT-017 | NOT RUN | same. |
| 413 ≠ context_too_long | UAT-018 | NOT RUN | same. |
| 408 / 5xx → network | UAT-019 | NOT RUN | same. |
| Content-policy not retried as media | UAT-020 | NOT RUN | same. |
| Provider 429 deduped vs RateLimitFrame | UAT-021 | NOT RUN | same. |
| Internal rate-limit uses RateLimitFrame | UAT-022 | NOT RUN | same. |
| Verbose Chat off / on / expanded | UAT-023 | **PARTIAL** | Toggle works; disclosure path not exercised (DEFECT-P1). |
| Real vision-less image fallback | UAT-024 | NOT RUN | same. |
| Real vision-less PDF fallback | UAT-025 | NOT RUN | same. |
| Live error without reload | UAT-026 | NOT RUN | same. |
| Live / replay dedupe by entry id | UAT-027 | NOT RUN | same. |
| Context signal after byte 512 / unparseable | UAT-028A/B | NOT RUN | same. |
| Kickoff reject — duplicate vs real failure | UAT-029 | NOT RUN | same. |
| Cancel ack + cancel background work | UAT-030A/B | NOT RUN | same. |
| `OMNIPUS_E2E_NO_VISION_MODEL` fail-if-unset | (readiness) | **PASS** | `bash -c ': "${OMNIPUS_E2E_NO_VISION_MODEL:?…}"'` aborts with the expected message. |
| Embedded SPA / binary on isolated `OMNIPUS_HOME` | (readiness) | **PASS** | `OMNIPUS_HOME=/tmp/adr-051-uat-810`, fresh `master.key`, clean `config.json`. |
| Verbose Chat toggle round-trips | (readiness) | **PASS** | `data-testid="chat-verbose-switch"`, `aria-checked` flips, `dataset.state` becomes `checked`. |
| REST upload path (PNG / PDF / ZIP) byte-perfect | (extra) | **PASS** | All three SHA-256s match the original fixtures; correct MIME served back. |

---

## 5. Console / network / log evidence

- **Console errors during the persona run:** 0 (`browser_console_messages --level error`).
- **WebSocket reconnection warnings:** present in `gateway_panic.log` (logged server-side, not surfaced in the browser console), one per ~10s attempt. This is the cause of DEFECT-P1.
- **Network requests during the persona run:** SPA made only GET `/api/v1/{workspaces, agents, version, commands, skills, sessions}`. No `/api/v1/chat/ws` upgrade, no `POST /api/v1/sessions`, no `/api/v1/upload` from the SPA composer.
- **`gateway.log` raw provider text check:** zero matches for the UAT plan §8.2 sentinel strings. Same for `gateway_panic.log` and the rendered DOM. Log excerpt saved at `docs/internal/regression/adr-051-uat-evidence/gateway_panic_excerpt.log`.

---

## 6. Recommendations to the lead agent (not fixes; just scoping)

1. **Fix DEFECT-P1 first or document it as a release-blocker** for chat-driven UAT. The persona UAT cannot demonstrate the Wave 1+2 work end-to-end without a working chat thread. Options:
   - Wire `omnipus-session` HttpOnly cookie at `/auth/login` (closes the half-wired session-cookie state per ADR-044 US-5).
   - Or add an alternative path: a stub provider harness + dev-mode stub-by-default, so the persona UAT can drive deterministic responses through a working WS.
2. **Add a stub harness** to the devpod (the UAT plan §4.4 calls for 13 response profiles). Without it, only UAT-024/025 are runnable in this environment, and those still require DEFECT-P1 to be resolved.
3. **Document the `version: 1` config requirement** in the UAT plan §4.3 (the plan does not currently show the field).
4. **Move persona UAT to the post-fix branch.** This report is the snapshot of `2f4c8d8d` on `sendfile-fix` with the Wave 1+2 changes; re-run after DEFECT-P1 is resolved to complete UAT-001…UAT-030.

---

## 7. Evidence index

- **Persona screenshots:** `docs/internal/regression/adr-051-uat-evidence/persona-screenshots/01..07-*.png` and copies at the evidence root.
- **Fixtures:** `docs/internal/regression/adr-051-uat-evidence/fixtures/{tiny.png, tiny.pdf, tiny.zip}`.
- **Gateway panic excerpt:** `docs/internal/regression/adr-051-uat-evidence/gateway_panic_excerpt.log` (40 lines around the WS auth timeouts and the 3 file-store INFO entries).
- **Built binary:** `/tmp/omnipus-uat` (151 MB, CGO_ENABLED=0, tags `goolm,stdjson`).
- **Isolated `OMNIPUS_HOME`:** `/tmp/adr-051-uat-810/`.

---

## 8. Sign-off (persona UAT only)

| Metric | Count |
|---|---:|
| Total personas exercised | 4 |
| Personas PASS | 0 (none fully) |
| Personas PARTIAL | 3 (settings toggle, log audit, REST upload) |
| Personas FAIL | 1 (chat-driven; DEFECT-P1) |
| Defects filed | 1 (DEFECT-P1, Blocker) |
| Environment blocks filed | 3 (EB-1 stale 8080, EB-2 no stub harness, EB-3 SPA resync not performed) |
| Stub-driven UAT cases runnable | 0 / 28 (no stub harness) |
| Real-LLM UAT cases runnable | 0 / 2 (blocked by DEFECT-P1) |
| Readiness checks PASS | 3 / 3 (`OMNIPUS_E2E_NO_VISION_MODEL` fail-if-unset; isolated `OMNIPUS_HOME`; binary + SPA on `0.0.0.0:8081` (env-block 8080) ) |

**Persona UAT verdict:** Environment-blocked + 1 Blocker defect. The ADR-051 Wave 1+2 code changes themselves are not refuted by this run (no LLM traffic was generated, so the error-translation, media-normalization, and rate-limit-dedup paths are not exercised). The persona UAT should be re-run on a build where DEFECT-P1 is closed and a stub harness is available.
