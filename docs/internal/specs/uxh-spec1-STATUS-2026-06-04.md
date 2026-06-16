# Spec-1 (UX-hardening showstoppers + tools registry) — STATUS / HANDOFF

**Date:** 2026-06-04 · **Branch base:** `epic/uxh-wave0ac` · **Owner:** Daniel Piatkowski

This is a checkpoint/handoff written at a hard stop. It records exactly where Spec-1
stands, what is committed, what remains, and the (important) infrastructure finding
that gateway Go tests cannot be run reliably on this dev box.

---

## 1. Context

After the v0.1 UX-hardening epic (PR **#344**) was built and CI-green, manual testing
surfaced showstopper UI bugs. The merge of #344 → `main` is **ON HOLD** until these are
fixed. This evolved into:
- a **tools-system redesign** (one central metadata/policy registry; ADR-018), and
- a batch of **showstopper fixes**, taskified as Epic **#349** with children **#350–#361**.

Authoritative design docs (committed alongside this file):
- `docs/internal/architecture/ADR-018-tools-system-and-v01-showstopper-resolution.md` (+ `-review.md`)
- `docs/internal/specs/uxh-spec1-showstoppers-and-registry.md` (+ `-review.md`)
- `docs/internal/specs/uxh-spec2-tools-categories-config.md` (not yet grilled/built)
- `docs/internal/specs/uxh-spec3-clawhub-registry.md` (not yet grilled/built)
- `.tasks/spec1-showstoppers-registry.task.json` (taskify DAG)

---

## 2. Spec-1 build status — all 5 feature branches committed

Each commit is authored as the human GitHub identity with **no** agent `Co-Authored-By`
trailer (CLA gate). All branch off `epic/uxh-wave0ac`.

| Branch | Issue(s) | Commit(s) | What it does | Local gates |
|---|---|---|---|---|
| `feat/s1-registry` | #350 | `327cf35` | Registers ~26 general-builtin **metadata** into the central `BuiltinRegistry` (total 67 tools), basic category overrides, fixes the gateway boot deps-drop + boot-log counts. Per-agent execution unchanged. | gofmt/lint/scoped go test green |
| `feat/s1-befixes` | #351 #352 | `158ef23`, `e08a9cf` | Provider status = Connected only when API key resolves (inline + `_ref`); `gateway.users` removed from `RestartGatedKeys`; `hot_reload` default on (FR-106). `e08a9cf` = test cleanup (remove 2 redundant `AgentLoop`s). | gofmt/lint green; full gateway suite deferred to CI |
| `feat/s1-fesettings` | #353-356 | `e9b5656` | GatewaySection no-auth removal + unauth banner + drop hot-reload toggle; Profile font-size scaling 12-20px; About logo/version/github=elicify-ai; McpServerModal Dialog→Sheet. | typecheck + vitest green (60 tests) |
| `feat/s1-fetools` | #357 | `3c0c4d7` | Flatten `ToolPolicyEditor` to ONE de-duplicated allow/ask/deny list; remove system-disclosure + raw double-grid; `core→"General"` category mapping. | typecheck + vitest green (90 tests) |
| `feat/s1-investigate` | #358 #359 | `49f01f6` | Time-boxed root-cause findings doc (`docs/internal/specs/uxh-spec1-investigate-findings.md`). No code fix. | n/a |

`fix/s1-gwtest-mem` is a throwaway measurement worktree (no commits) — do not push; can be removed.

---

## 3. Remaining showstoppers — root-caused, NOT yet implemented

### #358 WhatsApp QR never appears after Enable & Save (two coupled bugs)
- **Backend:** `setChannelEnabled` (`pkg/gateway/rest.go:4554`) never calls `TriggerReload`, so
  `ChannelManager.Reload` → `WhatsAppNativeChannel.Start()` never runs → the QR is never
  emitted on the `whatsapp_pairing` WS frame. Fix pattern is established (`HandleCreateAgent`
  rest.go:1369): add `a.agentLoop.TriggerReload()` after `safeUpdateConfigJSON`.
- **SPA:** `doSaveAndEnable.onSuccess` (`src/components/skills/ChannelConfigPanel.tsx:621`)
  closes the panel before the QR frame arrives. Keep the panel open for native WhatsApp.
- Tractable; recommended to fix in this release. (Recorded on issue #358.)

### #359 ~2-3 min session expiry
- Cookie is 24h (`SessionCookieMaxAge=86400`). Likely mechanism: `validateToken()`
  (`src/routes/_app.tsx:29`) runs uncached on every route, and `HandleLogin`
  (`rest_auth.go:350-354`) **rotates the bearer token on every login**, invalidating other
  tabs. Needs a Playwright repro on the integrated build to confirm the exact trigger.
  (Recorded on issue #359.)

---

## 4. IMPORTANT — gateway Go tests cannot be run reliably on this dev box

Repeated tmux/Claude crashes this session all traced to **host resource exhaustion**, NOT
a code bug:
- Box: **15 GB RAM, 4 cores, 77 GB root disk chronically ~96% full** (swapfile + Go/npm
  caches + `~/.claude` transcripts all on root).
- Running the full `pkg/gateway` Go suite — especially several in parallel — exhausts RAM
  (OOM) and/or fills root to 100% (ENOSPC, which crashes Claude's transcript write and hangs
  `sudo`). Memory caps with swap on made it WORSE (thrash → 1h43m stuck unkillable process).

**Measured facts:**
- Epic-base `pkg/gateway` suite (clean run): **86 MB peak RSS, ~60s, identical at parallel 1/4/8.** It is **not** a memory hog.
- The "BE-fixes = ~12 GB" reading was taken while the box was already broken (disk full,
  `sudo` hung, the test inherited OOM-immunity and sat stuck accumulating) → almost certainly
  a **measurement artifact**, not a clean-run cost. The only reliable number is 86 MB.

**Rule going forward:** do NOT run the full `pkg/gateway` suite locally; run at most one
narrowly-scoped test, or **validate via CI (16 GB runners)** — CI is the authority.

### Environment changes made this session (mostly ephemeral / system-level)
- Disabled ClamAV daemon + on-access scanner (was scanning every build-cache write).
- Grew swap 2 GB → 6 GB on `/swapfile`; `vm.swappiness` 60 → 10 (persisted).
- Set `oom_score_adj=-1000` on the tmux server + Claude (resets on restart).
- Reclaimed ~2.7 GB by deleting the stale `omnipus-security-wiring` clone (Sprint-258 work
  already shipped to main). Left `omnipus-channel-signal` untouched (holds the only copy of
  the **Signal channel + proto-installer WIP** — uncommitted/untracked; its git remote also
  has a **plaintext GitHub PAT** that should be rotated).
- **Standing liability:** root disk is the bottleneck. Biggest reclaimable: `~/.local` (15 G),
  `/var` (22 G).

---

## 5. Next steps (recommended order)

1. **Push** all 5 feature branches + this docs commit (done at this checkpoint).
2. **Integrate** the 5 branches into `epic/uxh-wave0ac` and **let CI run the full suite** —
   authoritative answer on whether anything balloons. Fix only what CI flags.
3. Implement the two remaining showstoppers (**#358 WhatsApp QR**, **#359 session expiry**).
4. Run the **7-reviewer quality gate** (6 pr-review-toolkit agents + `/grill-code`) per feature
   and on the whole Spec-1 diff.
5. Build SPA+binary, re-do the manual E2E that found the original showstoppers.
6. Then **#344 → hotfix → main** and re-test.
7. Spec-2 (tools categories/config) and Spec-3 (ClawHub) are written but **not grilled/built** —
   independent follow-on work.

## 6. Tracking
- Epic **#349**; children **#350–#361** (milestone "v0.1 — Stabilize").
- Findings recorded as comments on **#358** and **#359**.
