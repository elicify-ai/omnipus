# Plan — Wave 6: UI/UX remediation of the `/agents` surface

**Status:** Fifth revision, awaiting user sign-off
**Date:** 2026-06-18
**Scope:** 32 findings from 4 elicify-UI-UX-Design reviewer subagents (visual / accessibility / config-completeness / plausibility)
**Branch target:** `hotfix/v0.1.1` (additive on top of the v0.1.0-foundation hotfix)
**Termination:** **plan stops on `hotfix/v0.1.1` — NO PR to `main`, NO merge to `main`**
**Deployment target:** **standalone desktop app that runs in the browser** (per user, 2026-06-18). Not a cloud service, not a public preview. No Fly app, no `pod-omnipus.fly.dev`, no maintenance-mode flag. The dev cycle is local: build → run → restart on the user's machine.
**Author:** Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>`

---

## 1. Motivation

The v0.1.0-foundation `hotfix/v0.1.1` shipped with 4 elicify-UI-UX-Design reviewers (visual, accessibility, config-completeness, plausibility) producing **32 findings** against the `/agents` surface. Verdict from each:

| Lens | Verdict |
|---|---|
| A — Visual & layout | **FAIL** (3 Critical + 6 Important + 4 Minor) |
| B — Accessibility | **FAIL** (2 Critical + 5 Important + 4 Minor) |
| C — Configuration completeness | **FAIL** (~13 of 25 wire fields exposed; 6 fallback-editor gaps) |
| D — Plausibility / runtime | **FAIL** (3 Critical + 3 Important + 2 Minor) |

This plan remediates every finding without exception. Three-wave parallel fan-out; each wave ends with a local gate + push.

**Deployment context (revised 2026-06-18 per user):** Omnipus is a **standalone desktop app that runs in the browser**. The user is the only operator. There is no public deployment, no Fly app, no `pod-omnipus.fly.dev`, no maintenance-mode flag, no in-flight users to protect. The dev cycle is local:

```bash
npm run build                       # SPA bundle
rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/
OMNIPUS_HOME=/tmp/omnipus-home /tmp/omnipus gateway --allow-empty
# In a separate terminal:
open http://localhost:8080
```

Each wave ends with the user running this cycle locally; the user is the only one affected by a 2-second restart window. **No maintenance-mode flag is needed** (the user knows the app is being restarted and isn't on it for 2 seconds). The "in-flight users lose data" risk row from prior revisions is **N/A** for a desktop app.

---

## 2. The 32 findings (consolidated, severity-ranked)

### CRITICAL (8)

| # | Lens | Finding |
|---|---|---|
| C1 | Visual | `--color-muted` at ~2.6:1 contrast → 50+ strings fail WCAG 1.4.3 (4.5:1) across all Agents pages. One-token fix in brand theme. |
| C2 | Visual | Body text tier at 10–10.5 px (below 12 px floor). Card descriptions, model labels, section descriptions, modal field labels. Bump to 14/12 px. |
| C3 | Visual | Avatar color swatches 24×24 px (just under WCAG 2.5.8 AA 24×24 min). Icon picker 32×32. Bump to 40×40 + 44×44. |
| C4 | Visual | "Set as default" link 78×16 px on mobile, hidden behind `opacity-0 group-hover:opacity-100`. Touch users never see it. Surface persistently, min-h 44×44. |
| C5 | A11y | All `role="dialog"` missing `aria-modal="true"`. WCAG 4.1.2. |
| C6 | A11y | ModelSelector popover dialog has no `aria-label`, no `aria-labelledby`, no `aria-describedby`. WCAG 4.1.2 + 1.3.1 + 2.4.6. |
| C7 | A11y | Form errors not announced — no `aria-invalid`, no `aria-describedby`, no `role="alert"`. WCAG 3.3.1, 3.3.3, 4.1.3. |
| C8 | Functional | **ICON_OPTIONS case-sensitive** — Mia/Jim/Ava/Ray all show Robot instead of their configured icon. Real bug, one-line fix. |

### IMPORTANT (12)

| # | Lens | Finding |
|---|---|---|
| I1 | Visual | 8–10 section accordions in the Edit slide-over exceeds Miller's 7±2. |
| I2 | Visual | Edit slide-over is 47% of viewport. Cap at 32rem/90vw on desktop. |
| I3 | Visual | H2 same font size (12.25 px) as body & card titles — hierarchy collapses. Bump H2 to 14 px / 600. |
| I4 | Visual | Card grid jumps 1→3 cols (no 2-col stage). At 4 cards on 1440 px, the 4th sits alone. |
| I5 | Visual | Form labels in modal at 10.5 px on 32 px input, swamped by input border. |
| I6 | A11y | Nested 13×13 px info button inside Sandbox accordion trigger. WCAG 2.5.8. |
| I7 | A11y | Modal focus not restored to trigger on close. WCAG 2.4.3. |
| I8 | A11y | Required field has visual `*` only, no `aria-required` / `required`. WCAG 1.3.1, 3.3.2, 4.1.2. |
| I9 | Config | **Fallback editor: no way to pick provider independently from model.** FR-007 is dead-letter on single-provider builds. Add provider picker. |
| I10 | Config | **Fallback editor: no reordering controls.** Order is the documented contract semantic. |
| I11 | Config | **Fallback editor: no persistent validation** — `—` provider badge is unexplained. Free-text adds emit warning toast at add time, no persistent indicator. |
| I12 | Plausibility | **External-CLI worker saved without runtime surfacing prereqs** (binary on PATH, auth, worktree). Save → "active" config that fails at first delegation. Auto-test on save. |

### MINOR (12)

| # | Lens | Finding |
|---|---|---|
| M1 | Visual | Two equally-weighted "New agent" CTAs compete (Von Restorff). |
| M2 | Visual | `Mia — Assistant` card name loses to "Core" badge in visual weight. |
| M3 | Visual | Card grid 4th card sits alone at 1440 px (same root as I4, lower-impact variant). |
| M4 | Visual | `Default` star is decorative-only. Add a "Default" word-label for redundant signaling. |
| M5 | Visual | Per-section empty states use sub-floor text (10.5–11 px, 1.85:1 effective). |
| M6 | A11y | No skip-to-content link. WCAG 2.4.1 (A). |
| M7 | A11y | Avatar color buttons labeled with hex codes, not color names. |
| M8 | A11y | Edit Mia auto-focus jumps to Close button (far from heading). |
| M9 | A11y | Toasts as `role="dialog"` instead of `role="status"`/`role="alert"`. |
| M10 | Plausibility | **Slide-over re-opens on `/agents` after reload** (stale `editAgentId` in zustand). |
| M11 | Plausibility | **Native worker renders Tools & Permissions, Skills, Sandbox accordions** — sections that don't affect a non-chat labour agent. Add "delegation-only" context callout. |
| M12 | Plausibility | Legacy/new turn row height jitters in chat list (FR-014/Q6 honor absence but row drops ~10 px). |

### CONFIG-GAP (12)

| # | Finding |
|---|---|
| G1 | `voice` field in `AgentUpdateRequest` has no UI. Add to Identity accordion. |
| G2 | `delegation_policy` fields (`to`, `accept_from`, `modes`, `depth`, `budget`) not editable from the Agent profile. Add summary link to `/agents/trust` editor. |
| G3 | `default` flag has no toggle in the Edit profile — must go back to Agents list. Add a Default toggle to Identity. |
| G4 | Create modal exposes 6 of 25 wire fields. Signpost "many more settings in Edit". |
| G5 | Locked core agent's `sandbox_profile` UI shows "Built-in (locked)" but doesn't show the actual profile name. Show inherited profile. |
| G6 | `fallback_models` is wire-allowed on locked core agents but the UI strips the block via `canEdit`. Operators can't update. Add gated fallback editor. |
| G7 | Locked core agent "name" field looks editable (no `aria-disabled`); onClick does nothing. Replace with clear "Identity is built-in" note. |
| G8 | Worker has 4 tool overrides pre-populated in seed; UI invites user to edit. Collapse to read-only summary for native workers unless `agent.tools_cfg.overrides` is non-empty. |
| G9 | `executor.kind=external-cli` for a non-worker — UI lets user pick it, backend returns 400. Add client-side "core agents run native only" gate. |
| G10 | `executor.kind=external-cli` worker → Omnipus `sandbox_profile` is ignored at runtime. UI doesn't warn. Add "sandbox ignored when external-cli" callout. |
| G11 | `sandbox_profile: off` for locked core agent — UI shows "Built-in (locked)" but should still show the warning that "off" is rejected for core agents. |
| G12 | Model picker doesn't validate bare slug against connected providers (free-text accepts anything). Persist indication that model is unresolved. |

**Totals:** 8 Critical + 12 Important + 12 Minor + 12 Config-Gap = **32 tickets**.

---

## 3. Sequencing — 3 waves, 12 worktrees, 4 in parallel each

| Wave | Focus | Worktrees | Why this order |
|---|---|---|---|
| **W6-A** (foundation) | Tap targets + text scale + dialog a11y + form errors; foundation for B and C | A1, A2, A3, A4 | Most isolated; C1 muted token lands LAST in W6-C to avoid polluting B's typography diffs |
| **W6-B** (form + dialog + identity structure) | Slide-over width, card grid, accordion collapse, form labels, identity section (G1+G3 move here), icon case bug | B1, B2, B3, B4 | Builds on W6-A tokens; G1/G3 are identity-section inputs that need the identity accordion to be stable first |
| **W6-C** (config-gap + plausibility + final tokens) | 12 config-gap split, fallback editor deep, executor gating, model validation, muted token (C1 — moved from A) | C1, C2, C3, C4 | Largest ticket count; some backend contract changes; depends on the typography + identity sections being stable |

### 3.1 Critical file partitioning — push to extracted subcomponents, cap AgentProfile co-tenancy

**Hard rule: at most 2 worktrees per wave may edit `AgentProfile.tsx`.** Push all other edits to extracted subcomponents. The "extraction" itself is a hidden ticket — see §11.1 for ownership.

| File | LoC | Owner of "extracted" subcomponents |
|---|---|---|
| `AgentProfile.tsx` | 1435 | top-level only — Identity, Sandbox accordion, Model Configuration accordion, etc. |
| `AgentFormFields.tsx` | 226 | **`BehaviorFields`** (form) — temperature, maxTokens, topP, rate_limits, default toggle (G3), voice (G1) |
| `ExecutorSelector.tsx` | 228 | executor.kind UI + runner-test wire (I12) |
| `SandboxProfileSelector.tsx` | 285 | sandbox profile + off-gate warning (G11) |
| `ShellDenyPatternsEditor.tsx` | 97 | shell deny patterns |
| `ToolsAndPermissions.tsx` | (small) | Tools & Permissions, native-worker collapse (G8) |
| **`src/components/chat/ModelFooter.tsx`** | **26** | **lives in `chat/`, NOT `agents/`** — synthetic-turn legibility (F3). Plan §3.1 had it in the wrong path; corrected here. |
| **`src/components/shared/IconRenderer.tsx`** | (small) | **C8 (case-insensitive icon lookup) must also fix here**, not just `getIconComponent` in `AgentProfile.tsx`. `AgentCard.tsx:47` and `WorkerCard.tsx:70` use this component, not `getIconComponent`. |
| **`src/store/ui.ts`** | (small) | **No worktree currently owns this**, but A3 (FormError), B3 (slide-over focus restore), B4 (slide-over re-open), C1 (fallback editor) all read/extend the UI store. W6-B1 (slide-over) owns it. |
| **`src/routes/_app/agents.trust.tsx`** | (small) | **No worktree currently owns this**; G2's "delegation summary link" must land on a route that exists. W6-C1 (config-levers) owns it (and may need to extend `validateSearch` for `?agent=&lt;id&gt;`). |

`pkg/gateway/rest.go:920-974` already has `testAgentRunner`; `src/lib/api.ts:685` already exports `testAgentRunner`; `ExecutorSelector.tsx:159` already has `RunnerTestButton`. F1/I12 = wire the EXISTING call into the save flow, NOT new infrastructure.

### 3.2 Per-worktree file-disjoint plan

| Worktree | Branch | Files | Tickets |
|---|---|---|---|
| **W6-A1** | `chore/w6a1-tap-targets-and-icon-picker` | `CreateAgentModal.tsx` (avatar swatches + icon picker sizes) + `AgentProfile.tsx` (info button in Sandbox accordion, 644-655) | C3 + I6 |
| **W6-A2** | `chore/w6a2-text-scale` | `AgentCard.tsx`, `WorkerCard.tsx`, `CreateAgentModal.tsx` (label tier only) | C2 + C4 (label tier + persistent Set-as-default) |
| **W6-A3** | `chore/w6a3-form-errors` | `CreateAgentModal.tsx` (name + executor error) + new shared `FormError` component in `src/components/ui/` | I5 + I7 + I8 + C7 |
| **W6-A4** | `chore/w6a4-dialog-a11y` | `src/components/ui/sheet.tsx`, `popover.tsx`, `toast-container.tsx` (NOT `dialog.tsx` — doesn't exist) + `model-selector.tsx` popover | C5 + C6 + M9 |
| **W6-B1** | `chore/w6b1-slide-over-and-accordion` | `AgentProfile.tsx` (slide-over width + accordion collapse) + `src/components/ui/sheet.tsx` (default width literal) + **`src/store/ui.ts`** (slide-over focus + onOpenChange) | I1 + I2 + I3 + I7 |
| **W6-B2** | `chore/w6b2-card-grid` | `AgentListScreen.tsx` (grid breakpoint) + `AgentCard.tsx` (badge weight) | I4 + M2 + M3 + M4 |
| **W6-B3** | `chore/w6b3-icon-case-and-create-cta` | `AgentProfile.tsx` (`getIconComponent` case-insensitive, 60-83) + **`src/components/shared/IconRenderer.tsx`** (also case-insensitive — required, plan was missing this) + `CreateAgentModal.tsx` (Two-CTA fix) + `AgentListScreen.tsx` (empty states) + `routes/_app/agents.$agentId.tsx` (M10 fix) | C8 + M1 + M5 + M10 |
| **W6-B4** | `chore/w6b4-identity-section-extended` | `AgentFormFields.tsx` (voice input G1, default toggle G3) + `AgentProfile.tsx` Identity strip block (337-345) + new shared `AvatarColorsByName` table in `src/lib/constants.ts` (for M7) | G1 + G3 + M7 |
| **W6-C1** | `chore/w6c1-config-levers` | `AgentProfile.tsx` (delegation summary link G2, sandbox inherited profile display G5, "Native (in-process)" label) + `CreateAgentModal.tsx` (Create → Edit signpost G4) + `WorkerCard.tsx` (native worker Tools/Skills collapse G8) + `routes/_app/agents.trust.tsx` (link target — extend `validateSearch` for `?agent=&lt;id&gt;` if not present) | G2 + G4 + G5 + G8 + M11 |
| **W6-C2** | `chore/w6c2-fallback-editor` | `AgentProfile.tsx` (fallback chip rendering: provider picker, reorder, persistent validation, gated editor for locked core) + `model-selector.tsx` (provider combobox option) + `useMemo` `modelToProvider` + **persist provider ID (not display name) on wire payload** | I9 + I10 + I11 + G6 |
| **W6-C3** | `chore/w6c3-executor-gating-and-synthetic` | `ExecutorSelector.tsx` (gate external-cli for non-worker G9, wire runner-test on save I12 — both fixes the SAME dropdown component) + `AgentProfile.tsx` (Sandbox accordion header warning when external-cli G10) + **`src/components/chat/ModelFooter.tsx`** (synthetic-turn legibility F3) + `WorkerCard.tsx` (update card to show runner-test status badge) + **`src/components/chat/ChatScreen.tsx`** (mount ModelFooter at VirtualAssistantMessageRow) | G9 (= G8 collapse) + G10 + I12 + F3 |
| **W6-C4** | `chore/w6c4-muted-token-and-model-validation` | `src/styles/globals.css` (muted token C1) + `AgentProfile.tsx` (primary model picker free-text validation G12) + `model-selector.tsx` (free-text indicator chip) + **catalog helper in `pkg/agent/model_resolution.go` (NOT a new `pkg/providers/catalog.go`)** + `src/lib/agents/model-validation.ts` (TS twin) | C1 + G12 |

**Estimated LoC per worktree:** 30-300, total ~1100 (revised up from 970 per Reviewer 2's analysis — W6-C4 alone is 200-300 LoC; W6-C2 is 150-200 LoC).

### 3.3 AgentProfile.tsx co-tenancy

After reallocation, **9 worktrees touch `AgentProfile.tsx`**: {A1, A2, A3, B1, B3, B4, C1, C2, C3}. The line-range table at §3.1 is internally consistent for B/C waves but the **A→B carry-over is unanalyzed** — A2 (text-scale) and B1 (slide-over) both touch lines 519, 582, 632, 682, 707, 840, 913, 1060, 1084, 1169 (every AccordionTrigger).

**Resolution:** merge A2's text-scale (C2 ticket) and B1's slide-over work (I1 + I2 + I3 tickets) into ONE worktree. This becomes **W6-A2+ or W6-B1+** — whichever wave ships first absorbs the other. Concretely: Wave A owns "slide-over + text-scale + identity section" as one logical unit; B1, B3, B4, C1, C2, C3 each touch a different file or a different accordion.

**Still a hard cap: at most 2 of {A1, A2-merged, B1, B3, B4, C1, C2, C3} may edit `AgentProfile.tsx` in the same wave.** The merge agent verifies line-disjointness AND shared-prop contracts. C1 + C3 are now explicitly collapsed into one worktree (W6-C1+3) because they share Sandbox accordion state — see Reviewer 1's correction.

### 3.4 Wave-sequenced hidden-ticket ownership

Tickets the plan implicitly requires but doesn't assign. The Wave 0 worktree (or W6-A1 if Wave 0 is skipped) must build them.

| Hidden ticket | Owner | Required before |
|---|---|---|
| ~~`gateway.preview_maintenance` flag + 503 short-circuit on `/agents*`~~ | ~~REMOVED 2026-06-18: not needed for standalone desktop app, user is the only operator.~~ | N/A |
| `FormError` component API contract | W6-A3 (defines) | Wave A merge |
| `/agents/trust` route search-param support (`?agent=&lt;id&gt;`) | W6-C1 | Wave C merge (G2 link target) |
| `pkg/agent/model_resolution.go` catalog helper | W6-C4 (defines) | Wave C merge (G12 validation) |
| `src/components/shared/IconRenderer.tsx` case-insensitive lookup | W6-B3 | Wave B merge (C8 correctness) |
| `useUiStore` cleanup on `/agents` route mount (M10 — after reproduction) | W6-B1 (slide-over owns UI store) | Wave B merge |

If any of these are skipped, downstream worktrees have nothing to integrate with and the wave fails.

### 3.2 Within-wave parallelism

Within a single wave, the 4 worktrees touch disjoint files or disjoint line ranges of the same file. Two scenarios:

- **Different files** — true parallelism, no race.
- **Same file, disjoint line ranges** — true parallelism, but the merge agent must verify the line ranges are actually disjoint in the committed diff (not just in the plan). If overlap, abort the merge, re-plan, fail the wave.

**Critical file co-tenancy: `AgentProfile.tsx`** is touched by 8 of 12 worktrees. Resolution: each agent reads its assigned line range, modifies only that range, commits. The merge agent does a real 3-way merge (NOT `git checkout --ours`).

---

## 4. Per-wave gate

After each wave's 4-worktree merge, run:

| Gate | Definition |
|---|---|
| **G1** | `make verify-contracts` exit 0 (catches Wave 5-style YAML parse errors) |
| **G2** | `npm run typecheck` exit 0 |
| **G3** | `CGO_ENABLED=0 go build -tags goolm,stdjson ./pkg/...` (excluding gateway which needs SPA build) |
| **G4** | `golangci-lint run --build-tags=goolm,stdjson` exit 0 (per CLAUDE.md Quality Gates) |
| **G5** | `go vet -tags goolm,stdjson ./pkg/...` exit 0 |
| **G6** | Scoped vitest on touched test files: `npx vitest run --reporter=dot <touched files>` (don't run full suite per CLAUDE.md) |
| **G7** | Scoped go test on touched packages: `go test -tags goolm,stdjson -run '^TestW6\|^TestNewName' -p 1` (or pattern that matches the new test names) |
| **G8** | **Per-wave local e2e** (Wave 5 lesson): `npx playwright test tests/e2e/<relevant-spec>.spec.ts` (subset, not full matrix). Catches the class of regression scoped gates miss (e.g. Wave 5 aria-label fail). NOT deferred to the end. **No Fly worker, no remote — runs on the user's dev pod.** Wall-clock ~3-5 min per wave. |
| **G9** | Author audit: `git log origin/hotfix/v0.1.1..HEAD --format='%an <%ae>'` is only `Daniel Piatkowski <...>`; no Anthropic trailers |
| **G10** | Visual smoke via Playwright: load `http://localhost:8080/#/agents` (after gateway restart, see §6.2 step 6); drive the relevant flow; check no console errors. The app runs locally on the user's machine; the dev pod Fly URL (`https://pod-omnipus.fly.dev`) is dev-time infra only and is **not** an app smoke-test target. |

**If any gate fails:** the merge is reverted (`git revert -n <merge-sha>`, then commit), the wave is aborted, the offending worktree agent reworks, and the wave re-runs from a clean base. **No "fix forward"** unless the user explicitly approves — pre-staging a revert commit at wave start keeps the rollback cheap.

---

## 5. Risk analysis

| Risk | Likelihood | Mitigation |
|---|---|---|
| **AgentProfile.tsx 8-way co-tenancy conflict** | HIGH | Strict line-range partitioning enforced; real 3-way merge (no `git checkout --ours`); merge agent validates disjointness; abort + re-plan if overlap |
| **W6-A1 brand-theme mutation breaks Wave 4 a11y tests** | LOW | Token change is non-breaking; expected to pass more tests |
| **W6-C3 `pkg/gateway/rest.go` change breaks existing test surface** | MEDIUM | Run integration test for runner-test endpoint before commit |
| ~~**In-flight preview users lose data on gateway restart**~~ | ~~HIGH~~ | ~~REMOVED 2026-06-18: standalone desktop app, single user, no in-flight users to protect. The 2-second restart window is acceptable; the user knows they're restarting.~~ |
| **CI e2e catches something local gates miss (precedent: Wave 5 YAML took out 5 packages, aria-label took out 6 specs)** | MEDIUM | Per-wave local e2e run: `npx playwright test tests/e2e/<relevant-spec>.spec.ts` (subset, not full matrix). Catches the same class of regression. No Fly worker needed. |
| **Authorship violations (12 agents × multiple commits = 30+ checks)** | MEDIUM | G7 author audit in every per-wave gate; reject on first violation, rebase offending commits |
| **`git checkout --ours` strategy that worked for disjoint-file prior waves fails on same-file scenario** | (now removed from plan) | Banned outright per the merge strategy in §6 |
| **3-4h estimate too optimistic** | HIGH | Budget 6-8h. Each wave ends with a re-estimation checkpoint. |
| **Spec drift (W6-C adds config field → contract regenerates) breaks downstream consumers** | LOW | W6-C3 includes `make verify-contracts` per-wave |
| ~~**Live preview crash → no rollback**~~ | ~~HIGH~~ | ~~REMOVED 2026-06-18: standalone desktop app, user is the only operator. Rollback is "git revert -m 1" the merge commit, then restart the local binary. No data loss (state is in `OMNIPUS_HOME` on the user's filesystem).~~ |

---

## 6. Merge & deployment strategy

The deployment target is a **standalone desktop app that runs in the browser** (per user, 2026-06-18). The dev cycle is local: build, run, restart on the user's machine. There is no cloud app, no Fly machine, no public URL, no in-flight users, no maintenance-mode flag.

### 6.1 Merge within a wave

1. **Pre-staged revert of each wave's merge commit** at wave start (DRY-RUN, not commit):
   - `git revert -n -m 1 &lt;expected-merge-sha&gt;` (with `-m 1` to specify `hotfix/v0.1.1` as mainline parent — without `-m 1`, git errors on merge commits)
   - If the dry-run reverts cleanly, abort (`git revert --abort`) and proceed with the real merge
   - If the dry-run fails, the wave is too tangled to ship; abort the wave, re-plan
2. **Real merge** in alphabetical order (X1, X2, X3, X4). Real 3-way conflict resolution. **No `git checkout --ours`** — that strategy was successful for disjoint-file prior waves but does not apply to same-file co-tenancy.
3. **Dedicated merge agent per wave** with authority to call back the original dev agents on conflict. Not the lead's job. If the merge agent needs more than 60 min of conflict resolution, abort and re-plan.
4. **Post-merge dry-run revert (verify):** after the real merge lands, run `git revert -m 1 --no-commit &lt;merge-sha&gt;` again to verify the rollback still works on the actual merged state (some conflicts may have been resolved in a way that makes the revert impossible). If it fails, the wave is not safely shippable.

### 6.2 Local desktop restart procedure (after each wave merge)

Per-wave procedure — done by the user, not an agent:

1. **Prune stale worktrees** — `git worktree prune` (clears the 18 stale `agent-*` from prior waves + any new ones).
2. **Build the new SPA bundle** (per CLAUDE.md "SPA Embed Pipeline"):
   ```bash
   cd /home/dev/omnipus
   npm run build
   rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/
   ```
3. **Build the new Go binary:**
   ```bash
   CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/
   ```
4. **Restart the local gateway:**
   - If an old process is running: `pkill -f /tmp/omnipus`
   - Start the new one: `OMNIPUS_HOME=/tmp/omnipus-home /tmp/omnipus gateway --allow-empty &` (background)
5. **Health check:** `curl -sS -o /dev/null -w "HTTP %{http_code}\n" http://localhost:8080/health` must return 200.
6. **Local smoke via Playwright:** load `http://localhost:8080/#/agents` (or `http://localhost:8080`), drive the relevant flow, check no console errors. (See CLAUDE.md "Two-port preview" — gateway default port is 5000, not 8080; this plan uses 8080 per the user's earlier env request. If 8080 doesn't work, fall back to default 5000.)
7. **State preservation:** `OMNIPUS_HOME=/tmp/omnipus-home` is a persistent directory on the user's filesystem. Master key (`master.key`), `credentials.json`, sessions, and agent configs SURVIVE the restart. The OpenRouter key (or other provider keys) reloaded automatically from `credentials.json` on boot. **No data loss.**
8. **Acceptable restart window:** ~2-5 seconds. The user is the only operator, knows the app is restarting, isn't on the page during the restart. **No 503 page, no maintenance-mode flag, no polite "deploying" message needed.**

### 6.3 ~~Maintenance mode~~ — REMOVED 2026-06-18

Per user (2026-06-18): "we do not need nay of this preview stuff, this is a stand alone desktop app at the end that happen to run in the browser, remove this feature completely" — referring specifically to the **deploy-time maintenance-mode machinery** (`gateway.preview_maintenance` flag + `pkg/gateway/middleware.go` 503 short-circuit + A4-MAINT prerequisite ticket + §6.3b fallback), **all deleted from the plan**. The two unrelated "preview" concepts are **kept untouched**:

- **Level 1 — Dev pod preview port:** `0.0.0.0:8080` proxied publicly via Fly.io + elicify-devpod as `$DEVPOD_PREVIEW_URL` → `https://<pod>.fly.dev`. Platform infrastructure, not an app feature. Provided by the dev pod, never part of the shipped app.
- **Level 2 — Agent `web_serve` preview:** `gateway.preview_port` (default 5001) lets agents serve generated web apps despite sandboxing, on a separate origin for browser isolation. Core v0.1 ship scope (`pkg/tools/web_serve.go`, `src/components/chat/IframePreview.tsx`, etc.).

The desktop app is a single-user local app; the user knows the app is being restarted and is not on the page during the 2-second window.

### 6.4 Branch & worktree cleanup

- **Per-wave:** `git worktree prune` BEFORE creating new worktrees (so the merge agent doesn't see 18 stale `agent-*` from prior waves).
- **End-of-plan:** delete the 12 wave branches: `git branch -D chore/w6-a1 ... chore/w6-c4`. Final push: `hotfix/v0.1.1` HEAD is the final SHA, all gates green.
- **NO PR to `main`.** The plan terminates on `hotfix/v0.1.1`. There is no "open a PR to `main`" step. There is no `--admin`/force-merge step. There is no squash-merge to `main`. The user has explicitly said the plan must stop on the hotfix branch.
- **No interaction with PR #363 (or any other open PR).** Wave 6 is bounded by `hotfix/v0.1.1`. Any other PR the user is managing is the user's concern, not Wave 6's. Wave 6 does not coordinate with that.

### 6.5 Authorship validation at every commit (not just per-wave)

The plan's G9 audit is a per-wave check (after the merge). But CLAUDE.md says "verify before every push" — that's per-commit. **Each of the 12 worktree agents must run:**

```bash
git log -1 --format='%an <%ae>'  # must be Daniel Piatkowski <...>
git log -1 --format='%(trailers:key=Co-authored-by)' | grep -i anthropic  # must be empty
```

…**before every commit, not just before every push**. The per-worktree agent prompt must include this as a hard stop. A single bad commit buried in a 7-commit W6-A1 branch is cheap to rewrite early, expensive to dig out at merge time.

---

## 7. Authorship & commit hygiene (CLAUDE.md hard rule)

Every commit on every worktree branch must:
- **Author:** `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`
- **No `Co-authored-by:` trailers** — no `@anthropic.com` lines
- Verified per-wave in gate G7 (and per-commit in each worktree agent prompt)
- One commit per ticket (within a worktree, multiple tickets = multiple commits)

The 12 worktree agents must each set `git config user.name` + `user.email` in their worktree before any commit. This is in the agent prompt; gate G7 catches violations.

**No commit lands on `main` from this plan.** Wave 6 is bounded by `hotfix/v0.1.1`. If/when `hotfix/v0.1.1` is later merged to `main` by the user (outside this plan), the authorship audit was already done per-wave.

---

## 8. Critical-file line-range partitioning (AgentProfile.tsx)

To make the 8-way co-tenancy tractable, the file is partitioned by render region. Each agent owns a range:

| Worktree | Approximate line range in `AgentProfile.tsx` |
|---|---|
| W6-A2 | 60-200 (text scale constants) + 519, 582, 632, 682, 707, 840, 913, 1060, 1084, 1169 (label font sizes) |
| W6-A3 | 644-655 (Sandbox info button) |
| W6-B1 | 18-23 (slide-over width literal) + 519, 582, 632, 682, 707, 840, 913, 1060, 1084, 1169 (accordion sections) |
| W6-B3 | 343-360 (name error), 357-359 (executor error) |
| W6-B4 | 62-83 (ICON_OPTIONS lookup) |
| W6-C1 | 232-237, 358-372, 731-784 (fallback editor block) |
| W6-C2 | 337-345 (Identity strip), 644-655 (Sandbox display), new accordions for voice + delegation + default |
| W6-C3 | 838 (skills picker) + new Sandbox warning header |
| W6-C4 | primary model picker block |

This partitioning is approximate. The merge agent MUST verify actual disjointness in the committed diffs; any overlap aborts the wave.

---

## 9. Open questions

- **Q1:** Should each wave go through the full local e2e matrix (~30 min)? Plan says no (defers to end). Reviewer 2 says yes. **Recommended compromise:** per-wave contracts + go-test gate (5 min, all local) on the user's dev pod; full e2e only after the final wave.
- **Q2:** Should the app be maintenance-locked during each wave? **REMOVED 2026-06-18** — desktop app, single user, no maintenance-mode needed.
- **Q3:** Should we add a 5th lens (security / brand / RTL / bundle) to the review? **Recommended: defer**; bake a cross-cutting checklist into each ticket's acceptance criteria instead of expanding the wave structure.

---

## 10. Open review

This plan is in **OPEN REVIEW** state. Two independent reviewers have been dispatched:

- **Reviewer 1 (codebase-grounded code review)** — DONE; verdict **"Plan has fundamental problems"**
- **Reviewer 2 (process / risk review)** — DONE; verdict **"Plan has fixable issues — but several are load-bearing"**

### Reviewer 1 (fresh, blind) — top 3 corrections (integrated above)

1. **§3.1 line ranges for `AgentProfile.tsx` are factually wrong.** W6-A2 and W6-B1 conflict on every AccordionTrigger line (519, 582, 632, 682, 707, 840, 913, 1060, 1084, 1169) — text-size vs text-weight touches the same JSX. **Resolution:** merge A2 (text-scale) with B1 (slide-over) into ONE worktree (now W6-A2+ or W6-B1+, whichever wave ships first absorbs the other).
2. **C1 + C3 share Sandbox accordion state** (lines 580-674). Both touch SandboxProfileSelector.tsx. **Resolution:** collapse into W6-C1+3 single worktree.
3. **3 missing tickets:**
   - **`ModelFooter.tsx` path is wrong** — it's `src/components/chat/ModelFooter.tsx`, not `src/components/agents/ModelFooter.tsx`. F3 is a chat-surface concern.
   - ~~**Maintenance-mode flag (`gateway.preview_maintenance`)** — prerequisite for §6.3, no current implementation. Assigned to W6-A4.~~ **REMOVED 2026-06-18 per user.** Desktop app, no maintenance-mode needed.
   - **`pkg/providers/catalog.go` is wrong layer** — put validation in `pkg/agent/model_resolution.go` next to `buildModelListResolver`, and the UI twin in `src/lib/agents/model-validation.ts`. W6-C4 updated.

Plus critical from Reviewer 1:
- **I9 must persist provider IDs, not display names** — current code (`AgentProfile.tsx:235`) already broken, emits display names to wire. Added to W6-C2.
- **C8 needs `IconRenderer.tsx` fix too**, not just `getIconComponent`. Added to W6-B3 file list.
- **G8 is G9 in disguise** (same finding under two numbers). Collapsed in W6-C3.

### Reviewer 2 (fresh, blind) — top 3 corrections (integrated above)

1. ~~**BLOCKER: `gateway.preview_maintenance` flag does not exist in the codebase.** Plan §6.3 assumed a feature that hasn't been built. **Resolution:** W6-A4 (gateway middleware) takes maintenance-mode flag as a prerequisite ticket (A4-MAINT, new). Lands in Wave A.~~ **REMOVED 2026-06-18** — desktop app, no maintenance-mode needed. User is the only operator.
2. **Pre-staged revert is broken** as written. **`git revert -n <merge-sha>` of a `--no-ff` merge commit fails without `-m 1`.** Resolution: `git revert -n -m 1 <merge-sha>`. Added a dry-run revert before the real merge (§6.1 step 1) AND a post-merge dry-run revert verification (§6.1 step 4).
3. **Re-scope `AgentProfile.tsx` co-tenancy.** Actually 9 worktrees, not 5/6/8. Resolution: extract everything to subcomponents; **add hidden worktree ownership for `/agents/trust` route (G2), `pkg/providers/catalog.go` (C4, now in `model_resolution.go`), and `FormError` component contract (A3)**.

Plus critical from Reviewer 2:
- ~~**Gateway restart procedure targets wrong Fly app** — `ci-omnipus` is the test worker, `pod-omnipus.fly.dev` is the preview. Plan steps referenced the wrong binary location. **Resolution:** §6.2 corrected to target `pod-omnipus` (the live preview Fly app).~~ **REMOVED 2026-06-18** — conflated three "preview" concepts. For a standalone desktop app the gateway runs locally on `localhost`; `pod-omnipus.fly.dev` is the dev pod's public proxy (dev-time infra only, not an app deployment) and `ci-omnipus` is the CI test worker (Fly app `ci-omnipus`, separate from the user's machine). §6.2 step 6 already targets `http://localhost:8080/#/agents`.
- **M10 may not be the bug described** — `useUiStore` is non-persisted, reload resets `editAgentId` to `null`; the slide-over cannot re-open on `/agents` after reload. **Resolution:** W6-B1 must reproduce M10 first; if not reproducible, drop the ticket.
- **No worktree owns `src/store/ui.ts`** despite A3, B3, B4, C1 needing to read/extend it. **Resolution:** W6-B1 owns `src/store/ui.ts` (slide-over work + I7 modal focus restore).
- **No coordination with other PRs needed.** Wave 6 is bounded by `hotfix/v0.1.1` per the user's explicit instruction. The user manages any other PRs (including PR #363) separately. Wave 6 does not interact with them.
- **Hidden workstream for hidden tickets** — new §3.4 lists 6 hidden tickets + their owner worktrees.
- **G3 default-flag toggle** — `pkg/routing/route.go::resolveDefaultAgentID` falls back to first ENABLED agent; the UI toggle needs optimistic clear + `['agents']` query invalidation to avoid visible state lag. Added to G3's W6-B4 prompt.
- **G3 wire contract subtlety** — `AgentUpdateRequest.yaml:156-162` says `default: boolean, nullable: true`; PUT semantics must preserve existing value if field is omitted. W6-B4's prompt must verify the PUT path doesn't accidentally clear `default` when patching other fields.
- **Realistic effort: 8-12h wall-clock, not 6-8h** — §H1 in Reviewer 2's report. The 6-8h estimate is still optimistic; bumping to 8-12h with explicit per-wave re-estimation.

### Final status

**Plan revised with all 6 top-3 corrections from the second review cycle integrated.** Two more rounds of independent review produced:
- Reviewer 1: 3 new corrections (line ranges, C1+C3 collapse, 3 missing tickets)
- Reviewer 2: 1 BLOCKER (maintenance-mode doesn't exist — REMOVED 2026-06-18 per user), 1 high-risk (revert procedure broken), 5 material (AgentProfile co-tenancy, gateway restart targets wrong app — REMOVED 2026-06-18 since it's a local app now, M10 mis-diagnosed, no worktree ownership for hidden tickets, no interaction with other PRs — plan is bounded)

**Plan is now in its third revision.** Standing by for user sign-off to start Wave A.

---

**Status:** Third revision → awaiting user sign-off to start Wave A

