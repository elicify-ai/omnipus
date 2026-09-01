# Spec — Browser: the agent-facing capability surface (ADR-072 **D2 only**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md`, **§D2.0–D2.11 only**. D1 (ownership / workspace re-keying) is a **separate spec by a sibling agent** — this document never decides an ownership question and marks every D1 dependency explicitly.
- **Round-1 grill (of the ADR):** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md` (BLOCK, 26 findings).
- **Grill of THIS spec, round 1 (against revision 1):** `docs/internal/specs/browser-agent-capability-spec-review.md` (BLOCK, 30 findings: 5 CRITICAL / 13 MAJOR / 9 MINOR / 3 OBSERVATION). Revision 2 addressed all 30. Disposition: **§14.1**.
- **Grill of THIS spec, round 2 (against revision 2):** `docs/internal/specs/browser-agent-capability-spec-review-round2.md` (BLOCK, 26 findings: 3 CRITICAL / 11 MAJOR / 9 MINOR / 3 OBSERVATION). **This is revision 3 and it addresses all 26** — 25 accepted, **1 rejected on evidence** (m1). Disposition: **§14.2**.
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Revision 1 written at:** `077c5237` · **Revision 2 at:** `5a67157f` or later · **Revision 3 at:** `da6bb665` or later, after the ADR's §8 corrections log landed and D1's §14.2 exemption set was widened (`335d56fe`).
- **Status:** Draft for re-grill → implementation.
- **Verification posture.** Every code claim below was read in this worktree at revision-3 time. Symbols are cited as **`file::symbol`**, not `file:line`, per root `CLAUDE.md`'s rule for churning files. Line numbers survive **only** where they name a test fixture or a CI literal that does not move.
  **A standing rule this revision adopts from ADR-072 §8** (the corrections log recording six claims the ADR asserted from a plausible reading and did not test): a claim marked *verified* in an earlier revision of this document is **not** evidence. Revision 3 re-derived every claim it touched from source, and **one round-2 finding (m1) is rejected because the re-derivation disproved it** — see §14.2. Read the label as a pointer to where to look, never as the finding.

---

## 0a. What changed in revision 3, in one screen

| # | Change | Driven by |
|---|---|---|
| 1 | **The upload policy contradiction is resolved against the code.** `denyAllThenOverride` writes an **explicit agent-level `deny`** for every `allStaticToolNames` member an agent does not override, and resolution is **most-restrictive-wins** — so Mia and Ava resolve `deny`, not `ask`, from the edit sites this spec lists. §11 fn³'s coverage-vs-resolution reasoning is deleted. "`ask` for every agent" is narrowed to **every browser-capable agent**; S-25, US-8/AC3, §10 order 4 and the dataset row follow. Recorded as **overrulable** (§12 A-16) with the two-edit-site alternative named, and an ADR D2.9 erratum filed. | Round-2 C1 |
| 2 | **FR-007's round-trip budget is rescoped to `waitActionable`**, not to the whole `browser_click`. Verified: `chromedp.Click` appends `NodeVisible`, which issues `dom.GetBoxModel`; `MouseClickNode` then issues `ScrollIntoViewIfNeeded` + `GetContentQuads`. The old assertion was falsifiable and false. The `0 getBoxModel / 0 getNodeForLocation` claims move **inside** the gate's scope. | Round-2 C2 |
| 3 | **FR-029's packaging is rebuilt.** Order 0a drops its "assert #659 is open" guard (no Go test can observe a GitHub issue). Order 0b is rewritten against a **generic `ask`-policy tool**, needs no forbidden build, and lands `t.Skip`-ped citing #659 — a gate that is permanently red is not a gate. S-27's Given follows. | Round-2 C3 |
| 4 | **FR-028 names a real operator surface** — Settings → Security → **Audit Log** (`src/components/settings/AuditLogViewer.tsx` ← `GET /api/v1/audit-log`). Two limits found and stated: the event-type dropdown is a hardcoded 10-name list, and the `AuditEntry` contract pattern is `^[a-z_]+$`, so **the event names are `browser_snapshot` / `browser_upload_file`, not dotted** — a dotted name throws `ApiSchemaError` and blanks the whole viewer. | Round-2 M6 |
| 5 | **Two new requirements close SC-003's residual and D1's dangling citation.** **FR-037** translates a post-gate `chromedp` visibility/deadline failure into `ErrNotActionable{CondVisible}`, so "no bare timeout" is achievable. **FR-038** gives `browser_snapshot`'s read-only exemption its own number, so D1 §14.2 can cite it instead of the wrong FR-018. | Round-2 M5, unasked-Q9 |
| 6 | **The lease tombstone is completed.** §1's in-scope bullet, §4's observable promise, §13 holdout 7 and §14's structural bullet no longer carry the lease as live D2 work. "Five new action tools in scope" → **four**, in both places. | Round-2 M4, M2 |
| 7 | **§15's named cross-spec blocker is downgraded, not closed.** D1 §14.2 rule 3 was widened to five in `335d56fe` — but D1's own round-2 review (CRIT-104) found that set omits `browser_list_tabs`, and D1's MAJ-008 disposition row still reads "three". The dependency is now "**D1 §14 must resolve its own exemption set**", not "closed". | Round-2 M1 |
| 8 | **Two dispositions relabelled honestly.** §14.1's M2 is a **WITHDRAWAL** (the ≤150 ms budget was replaced by a weaker claim), M10 a **RELOCATION**. The sheet now reads 28 fixed / 1 withdrawn / 1 relocated. | Round-2 m8 |
| 9 | **`m1` is REJECTED on evidence.** `manifest_test.go:667-681` is the exact extent of the `tier3SearchOnlyToolNames` literal (`{` at 667, `}` at 681); lines 683-693 are the *next* test's doc comment. The spec's range was right. | Round-2 m1 |

---

## 0b. What changed in revision 2, in one screen

| # | Change | Driven by |
|---|---|---|
| 1 | `browser_snapshot` returns form-field **values by default**. The omit-by-default / `include_values` design is **deleted** — it was offered to the operator and declined. | Operator ruling 2026-08-31 (ADR D2.11) · grill C1 |
| 2 | Two mitigations are now real requirements with tests: the sensitive-value replacer (FR-027) and operator-inspectable capture (FR-028). **The ADR's stated mechanism for the second one is wrong** and needs correcting — see FR-028. | Operator ruling · grill C2 |
| 3 | `browser_upload_file` is **`ask` for every agent**, including Explorer and Researcher. The `deny`-for-workers proposal is deleted. | Operator ruling 2026-08-31 (ADR D2.9) · grill C3 |
| 4 | **Issue #659 is a hard prerequisite** (FR-029), gating registration of `browser_upload_file`, with an oracle that fails while #659 is open. | ADR D2.9 · grill C4 |
| 5 | `browser_handle_dialog` is **exempt from both** the write lease and `controlledResult` (FR-035). It is a recovery verb. | Grill C5 |
| 6 | The ≤150 ms wall-clock gate is **replaced** by an assertable CDP round-trip budget (FR-007) plus a *recorded, non-gating* wall-clock measurement (SC-004). | Grill M1/M2 |
| 7 | The snapshot cap is a **byte** budget with node-boundary truncation — explicitly **not** `capGetText` (FR-017). | Grill M3 |
| 8 | Every agent holding the browser surface also gets **`serve_web: allow`** (FR-030), or the new `file://` message is a longer dead end. | Grill M4 |
| 9 | `browser_upload_file` uses **`FSOpWrite`**, not `FSOpSend` + a hand-rolled roots check. | Grill M9 |
| 10 | The actionability gate ships with a **revert switch** (FR-034), an **audit event** on uploads (FR-031), and **failure telemetry** (FR-032). | Grill M12/M13 |
| 11 | The A-5 `Page`-domain spike is **resolved by reading chromedp's source**, not deferred. | Grill M6 |
| 12 | **The write lease is REMOVED from this spec** and relocated to the D1 spec's §14 annex. FR-023, US-14 and S-52…S-54 are tombstoned. FR-035's exemption stays here and needs a matching D1 amendment. | Operator ruling 2026-08-31 (cross-spec duplication) |
| 13 | Two ADR changes absorbed: **D1.2 superseded** (no attended/unattended split — every agent on a workspace shares its browser and logins, delegated work included) and **D1.1a** (isolation is now one Chrome process per workspace with its own `--user-data-dir`, not CDP browser contexts). Neither changes D2's tool surface; §6 records what they do change. | ADR-072 D1.1a, D1.2 |

---

## 1. Overview / Actors / Scope

**Problem.** The browser tool surface lets an agent *read* a page and lets it *fail* on one. It ships eleven tools (`pkg/tools/browser/register.go::RegisterTools`). A `<select>` cannot be operated at all. There is no Enter key, no hover, no file attach, and a page that calls `alert()` stops the tab answering CDP with no way out. Targeting is CSS-or-visible-text only; the CDP Accessibility domain is unused (**verified: zero occurrences of `getFullAXTree`/`queryAXTree`/`cdproto/accessibility` across `pkg/` and `src/`**). And the actionability contract is one quarter built: `ClickTool.Execute` and `TypeTool.Execute` prepend `chromedp.WaitVisible`, `GetTextTool.Execute` uses `WaitReady`; nothing anywhere checks enabled-ness, positional stability, or whether a click would actually land on the element rather than an overlay.

**Solution (ADR-072 D2).** Complete the surface along four axes, all additive:

1. **Find** — role + accessible name as a third locator alongside CSS and visible text, sourced from CDP Accessibility, wired into the *same* resolution seam (`text_selector.go::resolveActionSelector`) so every action tool inherits it.
2. **Wait** — finish the actionability gate (visible → stable → enabled → hit-testable) in one shared pre-action path, with an error that names *which* condition failed, a revert switch, and per-condition telemetry.
3. **Act** — six new tools: `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, `browser_handle_dialog`, `browser_snapshot`.
4. **Route** — the `file://` rejection names the supported alternative in the error **and** the agents that receive it can actually call that alternative.

**Actors.** `resolveActionSelector` / `resolvePseudoOnlySelector` (`pkg/tools/browser/text_selector.go`) — the target-resolution seam every action tool already funnels through; the eleven tool structs (`tools.go`, `tabs.go`); `BrowserManager` (session/tab ownership, listener install); `BrowserManager.ValidateURL`; the tool-policy seeding pair (`pkg/config/defaults.go`, `pkg/coreagent/core.go`); the manifest tier authority (`pkg/tools/manifest.go`); the coverage validator (`pkg/config/validate.go::ValidateToolPolicyCoverage`); `pkg/audit::Emit` (new consumer).

**In scope (v1):**
- Role + accessible-name resolution (D2.1), through the existing marker-attribute seam.
- The four-condition actionability gate (D2.2) with a closed, named failure set, a **CDP round-trip budget**, a revert switch and per-condition telemetry.
- The six new tools (D2.3, D2.4) — **except that `browser_upload_file`'s registration is gated on issue #659** (FR-029).
- The `serve_web` pointer in the `file://` error, **and the policy change that makes it reachable** (D2.5 + FR-030).
- Manifest tier assignment + the drift tests it pins, **including a new test that can actually detect Tier-3 drift** (D2.8, FR-036).
- Tool-policy seeding for all six, on **every** seeded agent, so boot does not abort (D2.9, Hard Constraint #6).
- The snapshot's information-disclosure posture (D2.11 bullet 3) **as ruled**: values by default, plus the two mandated mitigations.
- The **two lease exemptions** — `browser_handle_dialog` (FR-035) and `browser_snapshot` (FR-038). Stating them is D2's only remaining lease-adjacent obligation; see the next bullet.

> **Not in scope, and moved out in revision 2: the per-browsing-context write lease (D2.10).** Revision 2's change 12 relocated it to `browser-workspace-ownership-spec.md` §14 by operator ruling, but the in-scope list, §4's observable contract and §13's holdouts kept describing it as D2 work for a whole revision (round-2 **M4**). It is now removed from all four places. **D2 defines no lease, tests no lease and asserts no lease behaviour** — it only names the two tools that must sit outside it.

**Out of scope, and why:**
- **All of D1** — ownership, workspace re-keying, live-panel manager resolution, the workspace-less fallback, delegated-sub-turn isolation. Sibling spec. **Including D1.0a's `CaptureSharedContext` default question**, which is an operator decision the ADR explicitly does not take; nothing in D2 depends on its answer, because the write lease is keyed by whatever browsing-context key D1 lands on and D2 never reads the key's value.
- **D2.11 bullets 1 and 2** (elevation-of-privilege disclosure in the team-editing UI; the browsing-context-creation audit event). Both act on a *browsing context* whose key D1 decides. Named as an integration boundary in §6. **Note this spec DOES add two audit events of its own** (FR-028, FR-031) — they are keyed by tool invocation, not by browsing context, so they do not fork D1's decision.
- **D2.6's own exclusions**: replacing chromedp with playwright-go (Hard Constraint #1); network interception, frame targeting, drag-and-drop, cookie/storage manipulation.
- **Human-facing dialog UX.** `browser_handle_dialog` is agent-facing. A human who takes the wheel on a tab with an open dialog is wedged today and stays wedged after this work — see §6 and US-12's note. FR-035 removes the *compounding* case the grill found (agent locked out too); it does not give the human a button.

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

Cited as `file::symbol` (grill m2). Line numbers appear only for immovable fixtures.

| Symbol | Role | Context (verified at revision-2 time) |
|---|---|---|
| `text_selector.go::resolveActionSelector` | **modifies** | The seam. Takes `(selector, text)`, returns `(target, cleanup, err)` where `target` is a CSS string — either the caller's own selector, or the internal marker selector `[data-omnipus-tsel="<tok>"]` (`text_selector.go::textMarkerAttr`). Role/name enters **here** and returns the same marker shape, so every downstream `chromedp` `ByQuery` action is untouched. |
| `text_selector.go::resolvePseudoOnlySelector` | **modifies** | The second entry point — `browser_type` uses this, **not** `resolveActionSelector`, because its `text` arg is the value to type, not a locator. Role/name must be added to **both** or `browser_type` silently misses the new locator. |
| `text_selector.go::wrapTextMatch` | **reuses** | Shared `(marker, err) → (target, cleanup, err)` adapter. The AX path returns through it unchanged. |
| `text_selector.go::displayLocator` / `::scrubMarkerFromError` | **modifies** | Error-surface helpers. `displayLocator` must learn to render `role=button name="Submit"`; `scrubMarkerFromError` needs no change (it scrubs the marker attr, which the AX path also uses). |
| `text_selector.go::removeTextMarker` | **reuses** | Cleanup for the stamped marker. The AX branch returns through it unchanged. |
| `text_selector.go::textResolvePollInterval` | **reuses** | `150 * time.Millisecond`. The gate's retry cadence reuses it rather than inventing a second constant. |
| `tools.go::ClickTool.Execute` | **modifies** | `WaitVisible` → the new actionability gate. |
| `tools.go::TypeTool.Execute` | **modifies** | Same; the `clear` arg's `SetValue`+`SendKeys` sequence must run *after* the gate, not before. |
| `tools.go::GetTextTool.Execute` | **no wait change** | Read-only. `WaitReady` (DOM presence, 8 s budget `tools.go::getTextWaitTimeout`) is deliberate — `<title>` is present but never visible. The gate is for **action** tools; forcing it here would reintroduce the documented ~30 s hang. Gains the role/name locator only. |
| `tools.go::WaitTool.Execute` | **modifies (locator only)** | Gains role/name. Its own `WaitVisible` stays — `browser_wait`'s contract *is* visibility, not actionability. |
| `tools.go::controlledResult` | **reuses + extends** | Returns `{"deferred": true, "reason": …}` as a **non-error** result when a human holds the live view (`mgr.Live().IsControlled`). Its doc comment names seven callers (navigate/click/type/evaluate/switch_tab/close_tab/open_tab); read-only tools (`browser_screenshot`/`browser_get_text`/`browser_wait`) are deliberately ungated. The D1 write lease (`browser-workspace-ownership-spec.md` §14) returns the **same shape** — no prompt rewrite. Every new *action* tool must call it, **except `browser_handle_dialog`** (FR-035) and `browser_snapshot` (read-only). |
| `tools.go::capGetText` / `::maxGetTextChars` | **read, NOT reused** | `maxGetTextChars = config.DefaultBuiltinSuccessCap` = 64,000 (`pkg/config/context_settings.go::DefaultBuiltinSuccessCap`). **Verified: `capGetText` is `text[:maxGetTextChars] + getTextTruncationSuffix`, compared against `len(text)` — an arbitrary BYTE cut that can split a UTF-8 rune and always splits mid-node.** `browser_snapshot` obeys the **same constant** but **not the same mechanism** — see FR-017 and grill M3. |
| `manager.go::BrowserManager.ValidateURL` | **modifies** | `manager.go::blockedSchemes` covers `file`, `javascript`, `data`, `chrome`, `chrome-extension`. **One** format string serves all five: `"browser: %s:// URLs are blocked for security reasons"`. Only `file` has a supported alternative, so the pointer must be scheme-specific. |
| `manager.go::installTargetListenerLocked` | **pattern source, NOT modified** | Installs `chromedp.ListenTarget` on **`se.tabs[0]` only**, deliberately — `Target` discovery is browser-global so one listener suffices. Idempotence key: `sessionEntry.listenerTarget`, compared against the root tab's `targetID`; re-armed per ADR-041 fix F3 because "chromedp.ListenTarget's registration is scoped to the ctx it was given, so closing the tab that ctx belongs to silently ends the listener forever unless something re-installs it". **`Page.javascriptDialogOpening` is per-tab, not browser-global**, so the dialog listener is a SEPARATE installer with its own per-tab key — see Stream C and FR-014. |
| `manager.go::handleTargetEvent` | **reuses (pattern)** | Runs synchronously on the CDP dispatch goroutine and must never block or call `chromedp.Run` inline. Its own doc records that `mgr.Session()` runs a blocking `chromedp.Run` to **recreate a dead tab ctx** — the reason FR-014 needs a re-arm rule. The dialog listener inherits this discipline verbatim. |
| Tab-ctx creation sites | **modifies** | Where a tab ctx is born and the per-tab dialog listener must be armed: `manager.go::createFirstTab` (also the target of `Session`'s crash-recovery recreate), `manager.go::OpenTab`, `manager.go::adoptTarget` / `::adoptTargetWithRetry` (ADR-041 D2 adoption). Named exhaustively so FR-014 is implementable without a hunt. |
| `manager.go::ReapIdleSessions` | **modified by D2 — one eviction, added in revision 3** | Tears down idle browsing contexts and tabs under `m.mu`. Its interaction with the **write lease** is the D1 spec's concern, not this one — see the lease relocation note in §3. **But the dialog state is D2's own new state on `sessionEntry`, so its teardown is D2's (round-2 unasked-question 7).** Revision 2's edit site 15 said "evict at tab teardown" and named no site; the answer is **yes, the reaper is one of those sites**. Whatever tears a tab down must evict that tab's `dialogListeners` key, its pending-dialog entry and its `lastActivation` — the reaper included, alongside explicit `browser_close_tab` and the ctx-recreation path. **Missing the reaper is the ADR-041-F3 failure again:** a reaped-then-readopted `target.ID` whose stale key survives makes the re-arm a no-op, and the tab is wedged with no record. A dialog open **at** reap time simply dies with the tab, which is correct — there is no tab left to unwedge. Edit site 15 now names the reaper. |
| `metadata.go::BrowserBuiltinMetadata` | **modifies** | Eleven metadata-only instances, nil `*BrowserManager`. Feeds `pkg/gateway/gateway.go::buildKnownBuiltinToolNames` → the coverage universe. **Six additions required** (all six names, including `browser_upload_file` — see FR-029 on the difference between *seeded* and *registered*). |
| `register.go::RegisterTools` | **modifies** | Five `RegisterReplacing` calls now, six once #659 lands. `RegisterReplacing`, not `Register` — the hot-reload rationale in its own doc applies identically. |
| `register.go` `EvaluateTool{executeEnabled: …}` | **read, must NOT be copied** | `browser_evaluate` is registered unconditionally but **runtime-gated** by `sandbox.browser_evaluate_enabled`, independent of tool policy. It is the one place "seventeen registered" is true and misleading at once. **None of the six may replicate this pattern** — see §5 (grill m1). |
| `core.go::allStaticToolNames` (browser block) | **modifies** | Six additions. **Mandatory**: `core.go::validateOverrideKeys` **panics** on an override key absent from this list. |
| `core.go::denyAllThenOverride` | **reuses** | Starts every name at `deny`, then applies overrides. **This is why Mia and Ava need no per-agent edit** — their deny is automatic once the six names are in `allStaticToolNames`. |
| `core.go::tightenGlobalCeiling` (used by `IDWorker`) | **inherits** | Returns a **sparse** map — everything absent inherits the **global** ceiling. So `IDWorker`'s posture for the six is whatever `defaults.go` says, with no Worker-specific edit. Same route gives Worker `serve_web: allow` today (FR-030). |
| Explorer / Researcher browser grants (`core.go` `case IDExplorer:` / `case IDResearcher:`) | **modifies** | **Two seeded agents the ADR's D2.9 table originally omitted.** **Each grants exactly TEN browser tools**, not eleven: `browser_navigate`, `_click`, `_type`, `_screenshot`, `_get_text`, `_wait`, `_list_tabs`, `_switch_tab`, `_close_tab`, `_open_tab`. `browser_evaluate` is **deliberately excluded**, and Explorer's own inline comment says so: *"(NOT browser_evaluate)"*. Ray's block carries the same carve-out with the reason spelled out: *"(NOT browser_evaluate — arbitrary JS)"*. That carve-out — ten allow, one deliberate deny — is the precedent §11's policy table reasons from (grill M11). |
| Ray browser grant (`core.go` `case IDRay:`) | **modifies** | Also **ten**, same carve-out, same inline reason. |
| Jim browser grant (`core.go` `case IDJim:`) | **modifies** | **Eleven** — the only agent granted `browser_evaluate`, operator-approved, and the only agent granted `serve_web: allow` (FR-030's whole problem). |
| `validate.go::ValidateToolPolicyCoverage` | **no change** | **OR-based per (agent, tool)**: a **global** `sandbox.tool_policies` entry covers **every** agent. A single edit to `defaults.go` therefore closes the boot-abort risk for all agents at once; the per-agent edits are about *posture*, not coverage. |
| Boot / reload / write enforcement | **no change** | Boot abort `gateway.go` (`ValidateToolPolicyCoverage` call site); hot-reload abort; REST 400 in `rest.go`. |
| `manifest.go::ToolManifestTier` | **no change** | **Tier 3 is the residual, not a list** — it returns `ManifestLazy` for any name absent from `infraManifestToolNames` and `fullManifestToolNames`. The six become Tier 3 with **zero production-code edits**. This is also why the existing tier test cannot detect Tier-3 drift — FR-036. |
| `manifest.go::ToolManifestVisibility` / `::previewedLazyToolNames` | **no change (unless A-3 rules Tier 2)** | 7 previewed names, `serve_web` among them. Only a Tier-2 snapshot needs a production edit here. |
| `manifest.go::previewAllLazy` / `::SetPreviewAllLazy` | **pattern source** | The ADR-071 §4.3.1b time-boxed revert switch: an `atomic.Bool` read inside the single chokepoint, set from live config every turn, no restart. **FR-034's revert switch copies this shape exactly.** |
| `manifest_test.go:667-681` `tier3SearchOnlyToolNames` | **modifies** | Hand-maintained test fixture transcribing ADR-071 §4.1. **62 names, not 63** — `write_agent_metadata` retired. Pinned by hard literals in `TestVisibility_TierArithmetic` (`manifest_test.go:694-744`: `17`, `7`, `62`, `1`, union `87`). Line numbers retained: this is a fixture, and it is the thing being edited. **Re-verified at revision 3 (round-2 m1, REJECTED):** the round-2 grill reported this range as wrong, claiming the literal "runs to ~688". It does not. `var tier3SearchOnlyToolNames = []string{` is at **667** and its closing `}` is at **681**; line 682 is blank and **683-693 are the doc comment of the *next* test**, `TestVisibility_TierArithmetic`, which opens at 694. `667-681` was correct and stands. The only correction taken is the arithmetic test's end bound: its closing `}` is at **744**, not 745. |
| `pkg/gateway/gateway.go::buildKnownBuiltinToolNames` | **reads (new test consumer)** | The registered builtin catalog: general metadata ∪ browser metadata ∪ sysagent tools ∪ the four ADR-052 planning names. FR-036's new test asserts the four-tier partition equals this set. |
| `pkg/tools/web_serve.go::ToolNameWebServe` | **reads** | **`= "serve_web"`.** Corroborated by `previewedLazyToolNames` containing `"serve_web"`. The ADR has since corrected itself (D2.5's inline note); root `CLAUDE.md` still says `web_serve` and is a separate doc fix. |
| `pkg/tools/resolvepath.go::ResolvePath` / `::FSOp` | **reuses** | ADR-046 chokepoint. Verified `FSOp` semantics from the type's own doc: `FSOpRead/List/Send` allowed anywhere except the secret carve-out; **`FSOpWrite/Serve` work-dir-or-mount (`policy.AllowedRoots`) only**. See Stream B — FR-012 selects `FSOpWrite`. |
| `pkg/tools/resolvepath.go::FSOpSend` | **read, REJECTED for this use** | Its own doc: *"It carries NO additional path restriction beyond the open-read rule — the operator explicitly rejected a path-based 'publish' gate for send_file … so the real gate is tool policy."* Revision 1 cited the first half and then built the gate the second half records as rejected (grill M9). Resolved in FR-012. |
| `pkg/config/security.go::Config.SensitiveDataReplacer` | **NOW WIRED IN (FR-027)** | Revision 1 evaluated and declined it. The operator ruling makes it mandatory as defence in depth. See §2.3. |
| `pkg/audit::Emit` | **new consumer** | `Emit(ctx, logger, event, sev, fields)` — the existing structured-audit seam (`pkg/audit/events.go::Emit`), already used by `pkg/tools/memory.go` and others via an injected `*audit.Logger`. **`pkg/tools/browser` does not import `pkg/audit` today** (`capture_session.go`'s header says so explicitly), so FR-028/FR-031 add the first import and a `SetAuditLogger`-style injection mirroring `pkg/tools/library_tool.go::LibraryReadTool.SetAuditLogger`. |
| `src/lib/toolVisibility.ts::shouldRenderToolCall` | **read, must stay unchanged** | Takes `verboseChatEnabled` and short-circuits to `true`. **Verified: zero occurrences of the substring `browser` in the file** — so every browser tool call, including a snapshot, renders in the chat thread today. FR-028 depends on that and §5 forbids changing it. |
| `src/hooks/useRunningActivity.ts` | **read — the ADR's claim fails here** | Its own header: the panel aggregates *"subagent delegation spans … and background `bash` sessions"* (plus judge verdicts), capped at `RECENTLY_FINISHED_CAP = 8`. **It has no code path that renders an arbitrary tool call.** So ADR D2.11's "the snapshot must be reachable in the ActivityPanel" is **false on current code** — see FR-028 (round-1 grill C2). |
| `src/components/settings/AuditLogViewer.tsx` | **read — this is the audit half's operator surface** | **New in revision 3 (round-2 M6).** Settings → Security → Audit Log. Fetches `GET /api/v1/audit-log` (`pkg/gateway/rest_settings.go::HandleAuditLog`, which streams raw JSONL lines from `$OMNIPUS_HOME/system/audit.jsonl`, last 100, reverse-chronological). **It renders every entry by default** — `eventFilter` starts at `'all'` and an unrecognised event name falls through to `BADGE_FALLBACK` styling rather than being dropped — so a `browser_snapshot` audit record *is* visible to an operator without a code change. **Two real limits, both stated in FR-028 rather than glossed:** (i) its `EVENT_TYPE_OPTIONS` dropdown is a **hardcoded 10-name list** (`tool_call`, `exec`, `file_op`, `llm_call`, `policy_eval`, `rate_limit`, `ssrf`, `startup`, `shutdown`, `security_setting_change`), so an operator can see the new events but cannot *filter to* them; (ii) see the next row. |
| `contracts/components/schemas/AuditEntry.yaml` → `src/lib/api/generated/schemas.ts::AuditEntry` | **read, CONSTRAINS the event name** | **The contract pins `event` to `pattern: '^[a-z_]+$'`** — lowercase letters and underscores, **no dots**. `src/lib/api.ts::fetchAuditLog` passes `AuditLogResponseSchema` to `request()`, whose `performRequest` **throws `ApiSchemaError` on `safeParse` failure** rather than dropping the row — and `entries` is `z.array(AuditEntry)`, so **one** non-conforming entry blanks the entire viewer. **Therefore FR-028's and FR-031's event names are `browser_snapshot` and `browser_upload_file` — underscore form, never `browser.snapshot`.** *(Pre-existing defect, recorded not fixed: `audit.EventChannelPairing = "channel.pairing"` is emitted at `pkg/agent/loop.go:4357` and `audit.EventCliValidate = "cli.validate"` at `pkg/gateway/rest_clivalidate.go:370`, both into the same `audit.jsonl`. On any install where a channel has been paired or a CLI validated, the Audit Log viewer already throws. That is not D2's to fix — see §12 B-6 — but D2 must not add a third.)* |

### 2.2 CDP / chromedp primitives — all present, no new dependency

`go.mod` pins `chromedp v0.15.1` and `cdproto v0.0.0-20260321001828-e3e3800016bc`. Every primitive D2 needs already ships:

| Need | Primitive | Location |
|---|---|---|
| Role + name query | `accessibility.QueryAXTree()` with `WithRole` / `WithAccessibleName` | `cdproto/accessibility/accessibility.go::QueryAXTree`, `::QueryAXTreeParams.WithRole`, `::WithAccessibleName` |
| Page structure | `accessibility.GetFullAXTree()` | `cdproto/accessibility/accessibility.go::GetFullAXTree` |
| AX → DOM bridge | `accessibility.Node.BackendDOMNodeID` | `cdproto/accessibility/types.go:465` |
| AX ignored flag | `accessibility.Node.Ignored` | `cdproto/accessibility/types.go:455` |
| AX field value | `accessibility.Node.Value` | `cdproto/accessibility/types.go:461` — **note the ADR cites `:206`, which is `AXPropertySource.Value`, a different struct. Minor ADR erratum, recorded so it is not re-derived.** |
| Batched in-page probe | `runtime.Evaluate` via `chromedp.Evaluate` | the gate's fast path (FR-007) |
| Stability (fallback) | `dom.GetBoxModel()` / `dom.GetContentQuads()` | `cdproto/dom/dom.go::GetBoxModel`, `::GetContentQuads` |
| Hit-test (rejected alt.) | `dom.GetNodeForLocation(x, y)` | `cdproto/dom/dom.go::GetNodeForLocation` — see FR-007's rejected-alternative note |
| Scroll-into-view | `dom.ScrollIntoViewIfNeeded()` | `cdproto/dom/dom.go::ScrollIntoViewIfNeeded` |
| File attach | `chromedp.SetUploadFiles(sel, files, opts…)` | `chromedp/query.go::SetUploadFiles` |
| Key events | `chromedp.KeyEvent` / `KeyEventNode` | `chromedp/input.go::KeyEvent`, `::KeyEventNode` |
| Dialog dismiss | `page.HandleJavaScriptDialog(accept)` + `.WithPromptText` | `cdproto/page/page.go::HandleJavaScriptDialog`, `::HandleJavaScriptDialogParams.WithPromptText` |
| Dialog detect | `page.EventJavascriptDialogOpening` | `cdproto/page/events.go::EventJavascriptDialogOpening` |
| Select | `chromedp.SetValue` (+ a dispatched `change`) | `chromedp/query.go::SetValue` |

**The wedge, from CDP's own contract.** `EventJavascriptDialogOpening.HasBrowserHandler` documents it verbatim: *"When browser has no dialog handler for given target, calling alert while Page domain is engaged will stall the page execution. Execution can be resumed via calling Page.handleJavaScriptDialog."* That sentence — not a design intuition — is why acceptance is "the tab still answers CDP", never "the dialog was dismissed".

**Precedent for synthetic key input.** The live-view human path already dispatches real key events (`pkg/tools/browser/live.go`, `input.DispatchKeyEvent`). `browser_press_key` is the agent-facing counterpart, not a new mechanism.

### 2.2a The `Page`-domain question is RESOLVED, not a spike (grill M6, closes A-5)

Revision 1 ordered a spike to find out whether chromedp enables the `Page` domain per target. **It does, and the source says so.** `chromedp@v0.15.1/chromedp.go::Context.attachTarget` builds an action list for every non-worker target and executes `page.Enable()` in it, alongside `log.Enable()`, `network.Enable()`, `inspector.Enable()`, `dom.Enable()`, `css.Enable()`, `target.SetDiscoverTargets(true)`, `target.SetAutoAttach(...)` and `page.SetLifecycleEventsEnabled(true)`. Every tab in `pkg/tools/browser` reaches that path — `createFirstTab` and `OpenTab` via `chromedp.NewContext` + `chromedp.Run`, adopted tabs via `chromedp.NewContext(..., chromedp.WithTargetID(...))` + `chromedp.Run`.

**Three consequences, all load-bearing:**

1. **No per-tab `Page.enable` round trip is needed**, so M6's "no" branch — which would have required an enable inside `installTargetListenerLocked`, a function whose doc states it must be called with `m.mu` held precisely because `ListenTarget` is a lock-free append and never a round trip — **does not arise**. The §5 non-behavior forbidding CDP under `m.mu` is preserved for free.
2. **The listener install stays a lock-free append**, so FR-014's per-tab installer inherits `installTargetListenerLocked`'s discipline exactly.
3. **chromedp does NOT handle dialogs itself.** `chromedp@v0.15.1/target.go`'s page-event switch lists `*page.EventJavascriptDialogOpening` and `*page.EventJavascriptDialogClosed` among its **explicitly ignored** events. So `HasBrowserHandler` is false and the page stalls exactly as CDP documents — this is the mechanism of the wedge, confirmed rather than assumed.

**This is a dependency behaviour, so it must be pinned, not merely noted.** §10 order 0 replaces the spike with `TestChromedpEnablesPageDomainPerTarget` — a real assertion (a fresh tab, an `alert()`, and the assertion that a `ListenTarget` callback observes `EventJavascriptDialogOpening` with no explicit `page.Enable` anywhere in our code) that turns red on a chromedp bump that changes the bring-up list.

### 2.3 The snapshot's disclosure posture — what was verified, and what the operator ruled

**What was verified (unchanged from revision 1, and the ADR now agrees).**

1. **There is no redaction path in this package to inherit.** `RegisterSensitiveValues` / `SensitiveDataReplacer` appear in `pkg/config`, `pkg/tools/list_jobs_row.go`, `pkg/gitevidence`, `pkg/audit/secretscan.go`, `pkg/agent/session_messaging_wire.go` and `pkg/gateway`. **Zero occurrences anywhere in `pkg/tools/browser/`.** `browser_get_text`'s entire output treatment is `capGetText`.
2. **The replacer would not, on its own, do the job the earlier ADR text claimed.** Verified in `pkg/config/security.go::buildAndPopulateSensitiveCache`: it builds a `strings.Replacer` over reflection-walked `SecureString` config fields plus runtime-registered plaintexts, mapping each to `[FILTERED]`, and **skips any value of length ≤ 3**. An account identifier or an arbitrary form value is not a registered secret and passes through untouched.
3. **The risk is a strict widening, not an inheritance.** `browser_get_text` calls `chromedp.Text` — *innerText*, rendered text only. An `<input>`'s value is not innerText. `accessibility.Node.Value` **is** the field's computed value. A snapshot that emits `Value` therefore exposes what a user typed where the existing text tool structurally could not.

**What the operator ruled (ADR D2.11, 2026-08-31, Daniel Piatkowski).**

> **The snapshot returns field values by default.** Omit-by-default with an `include_values` opt-in was **offered and declined**. The rationale: an agent cannot verify a form is correctly filled before submitting it — one of the main things the panel is for — without seeing what is in the fields.

**The accepted risk, stated plainly rather than softened.** A `browser_snapshot` of a signed-in page can carry a **card number, a partially typed password, or an account identifier** into the model's context, into the conversation the operator reads, and into the stored transcript at `sessions/<id>/<YYYY-MM-DD>.jsonl` — which has a **90-day default retention**. Nothing in this design prevents that. It is accepted because the alternative removes the capability's main purpose, and it is recorded here so it is not rediscovered as a surprise.

**The two mitigations the ruling makes non-optional** are specced as first-class requirements, each with a BDD scenario, a TDD entry and a dataset row:

- **FR-027 — the sensitive-value replacer, wired in as defence in depth.** It does not cover form values and this spec does not pretend it does. It closes the *credential-plaintext* case (a provider key pasted into a form field, a registered secret echoed in an accessible name) at essentially no cost.
- **FR-028 — the capture is visible in two places, and the two answer different questions.** **The ADR's stated mechanism is wrong and the ADR sentence needs correcting.** It says the snapshot "must be reachable in the ActivityPanel / verbose-chat surfaces like any other tool call". Verified against `src/hooks/useRunningActivity.ts`: the panel aggregates subagent delegation spans, background `bash` sessions and judge verdicts — capped at 8 recently-finished — and has **no path that renders an arbitrary tool call**. "Reachable in the ActivityPanel" is false today for *every* browser tool, not just the snapshot.

  **Revision 3 says plainly which half does what (round-2 M6).** Revision 2 called the audit half "operator-inspectable" and named no surface, which left the entire mitigation resting on the chat render — and the chat render *is* the disclosure. Corrected:

  - **(a) The chat thread answers "**what** was captured."** The full snapshot result renders inline, by default, for every viewer of the conversation — true today because `src/lib/toolVisibility.ts` contains zero `browser` references, pinned by a regression assertion (S-43) so it stays true. This is the only surface that shows the values, and it shows them *because* they are the tool's output. It is a control in the sense that nothing is hidden; it is **not** a control in the sense of limiting exposure.
  - **(b) The audit event answers "**that** a capture happened, of what shape, from where."** Page origin, node count, output bytes, whether any node emitted a `Value`, whether the output was truncated — and **never the values themselves**, because an audit log that copies the card number has moved the disclosure rather than recorded it. **It is read at Settings → Security → Audit Log** (`src/components/settings/AuditLogViewer.tsx` ← `GET /api/v1/audit-log` ← `$OMNIPUS_HOME/system/audit.jsonl`) — a named, shipped, operator-facing surface, which is what revision 2 owed and did not give. It is also the **durable** half: the chat thread is bounded by the session's retention and by whoever reads that conversation; the audit log is the record that survives and that an on-call operator greps.

  **Two limits on (b), stated rather than glossed** (both verified — see §2.1's two new rows): the viewer's event-type dropdown is a hardcoded 10-name list, so the new events are **visible but not filterable**; and the `AuditEntry` contract pins `event` to `^[a-z_]+$`, so **the names must be `browser_snapshot` / `browser_upload_file`, not dotted** — a dotted name makes `AuditLogResponseSchema.safeParse` fail and `performRequest` throw, blanking the whole viewer.

  **Recommended ADR amendment (needs the ADR's owner, not this spec):** replace "reachable in the ActivityPanel / verbose-chat surfaces" with "rendered in the chat thread (and verbose chat) like any other tool call, plus a metadata-only audit event read at Settings → Security → Audit Log". Filed as an ADR erratum alongside the D2.11 `types.go:206` line-number correction in §2.2.

### 2.4 Impact assessment

| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `resolveActionSelector` / `resolvePseudoOnlySelector` | **HIGH** | click, type, get_text, wait + all six new tools | `text_selector_test.go`, `text_selector_e2e_test.go` (2 files, ~59 KB of assertions). **Cost is deliberately UNBUDGETED in v1 and that is now recorded, not omitted (round-2 M10, §12 B-5).** The AX branch issues `QueryAXTree` + `DOM.setAttributeValue` + a cleanup `removeAttribute` per call; the snapshot path issues `GetFullAXTree`, which B-1 already concedes is unbounded. FR-007 budgets the *gate* — the narrower half — and nothing budgets this, the wider one. Revision 2 simply did not mention it. |
| Click/type pre-action wait | **CRITICAL** | Every agent click and keystroke on every page | `execute_e2e_test.go`, `text_selector_e2e_test.go`, `tab_adoption_e2e_test.go`. **Mitigated by FR-034's revert switch** — revision 1 shipped this unconditionally with no way back, which the grill (M13) correctly called out against the ADR-071 `previewAllLazy` precedent. |
| `allStaticToolNames` | **CRITICAL** | `validateOverrideKeys` **panics** on drift; `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | Every seeded agent's policy map; boot |
| `defaults.go` global `tool_policies` | **CRITICAL** | `ValidateToolPolicyCoverage` for **every** agent (OR-semantics) | Boot abort; reload abort; REST 400 |
| `BrowserBuiltinMetadata` | **HIGH** | `buildKnownBuiltinToolNames`; `GET /api/v1/tools` catalog | Coverage universe — a tool in metadata but absent from `allStaticToolNames` fails the sync test |
| `tier3SearchOnlyToolNames` + tier arithmetic literals | **MEDIUM** | `TestVisibility_TierArithmetic`, `TestVisibility_SearchOnlyToolsRemainInSearchIndex` | Build fails until updated — by design (ADR-071 FR-034), **but only for Tier 1/2; FR-036 adds the assertion that makes it true for Tier 3 too** |
| Per-tab dialog listener (new, alongside `installTargetListenerLocked`) | **HIGH** | Every tab's event plumbing; ADR-041 tab adoption | `tabs_test.go`, `tab_adoption_e2e_test.go`, `navigate_stranded_tab_test.go` |
| `sessionEntry` dialog-state teardown | **MEDIUM** | Tab close / ctx recreation must evict the per-tab dialog-listener key | FR-014's re-arm rule (S-33) |
| `sessionEntry.lastActivation` (new per-tab field, round-2 M8) | **MEDIUM** | The FR-013 suspected-dialog fallback reads it; every action tool writes it | Concurrency: written under `m.mu` by the tool *after* its CDP call returns, never from `handleTargetEvent` (whose doc forbids blocking). **Cleared, not preserved, on ctx recreation** — a recreated tab has no completed command of its own. Contract and its consequence are in Stream C gap 3 |
| `ValidateURL` | LOW | `browser_navigate`, `browser_open_tab` | `blocked_schemes_test.go` (asserts the current message) |
| Ray / Explorer / Researcher `serve_web` grant (FR-030) | **MEDIUM** | Three agents gain a tool that writes files and serves them | `TestCoreAgentSeed_*`; a real posture change, argued in §11 |

---

## 3. Implementation Streams (fan-out for parallel agents)

Six streams. **Stream A is the critical path** — it defines the resolution and actionability interfaces every other stream codes against. **Stream E (policy + tier) is independently mandatory and must land in the SAME commit series as any stream that registers a tool**, because a registered tool with no seeded policy aborts boot.

### Shared interface contract (Stream A's first commit — everyone codes against this)

```go
// pkg/tools/browser/target.go (new) — placement: same package as the existing
// text_selector.go seam. NOT a new package: resolveActionSelector, wrapTextMatch,
// textMarkerAttr and scrubMarkerFromError are all unexported and stay that way.

// Locator is the closed set of ways an agent may name an element. EXACTLY ONE
// of {CSS, Text, Role+Name} may be populated. Supplying two is an ERROR that
// names both populated fields — never a silent precedence rule. (Revision 1
// said "honoured in that documented order", which is a different and weaker
// contract than "exactly one"; grill m3.)
type Locator struct {
    Selector string // CSS, optionally with a trailing :has-text()/:text-is() pseudo
    Text     string // visible-text substring (NOT valid on browser_type or browser_press_key)
    Role     string // ARIA/computed role, e.g. "button", "combobox", "link"
    Name     string // computed accessible name, e.g. "Submit"
    Index    *int   // 0-based disambiguator on multi-match; nil = "must be unique".
                    // A pointer, not (int, bool): the two-field form permitted an
                    // Index:3/HasIndex:false state nothing validated (grill m4).
}

// ErrLocatorConflict is returned when more than one locator kind is populated,
// or when a tool receives a locator kind it does not accept. It names the
// offending fields; it never picks a winner.
type ErrLocatorConflict struct{ Fields []string; Tool string }

// resolveTarget is the SINGLE seam. It supersedes resolveActionSelector and
// resolvePseudoOnlySelector as the entry point; both survive as internal
// branches so the existing CSS/text tests keep exercising the same code.
// Returns a CSS string the caller's existing chromedp ByQuery action uses
// unchanged — for the Role+Name branch this is the SAME data-omnipus-tsel
// marker selector the text branch already produces, set via
// DOM.setAttributeValue on the AX node's BackendDOMNodeID. cleanup MUST be
// deferred immediately; it is always safe to call, including on the error path.
func resolveTarget(tabCtx context.Context, toolName string, loc Locator, timeout time.Duration) (target string, cleanup func(), err error)

// ActionCondition is the CLOSED set the actionability gate reports on failure.
// Criterion 7 of the ADR requires the error to name WHICH condition was unmet;
// a closed set is what makes that testable rather than prose.
type ActionCondition string

const (
    CondVisible     ActionCondition = "visible"      // rendered, non-zero box
    CondStable      ActionCondition = "stable"       // two box reads one animation frame apart are identical
    CondEnabled     ActionCondition = "enabled"      // not [disabled], not aria-disabled=true
    CondHitTestable ActionCondition = "hit-testable" // the point-hit node is this node or a descendant
)

// ErrNotActionable is the ONLY error type the gate returns on timeout. Failed
// names the FIRST condition that never became true within the budget — first,
// not last, because the conditions are evaluated in the order above and a
// later one is meaningless while an earlier one is false.
type ErrNotActionable struct {
    Failed  ActionCondition
    Display string // the user-facing locator (text_selector.go::displayLocator)
    Detail  string // e.g. the occluding element's tag+id for CondHitTestable
}
func (e *ErrNotActionable) Error() string // "browser_click: element %q is not actionable: %s (%s)"

// waitActionable runs the four-condition gate. Called by every ACTION tool
// (click, type, select_option, press_key-with-target, hover, upload_file);
// NEVER by a read-only tool (get_text, screenshot, wait, snapshot) and NEVER
// by browser_handle_dialog (FR-035 — it is a recovery verb).
func waitActionable(tabCtx context.Context, toolName, target, display string, timeout time.Duration) error

// THE WRITE LEASE IS NOT DEFINED HERE. It belongs to the D1 spec.
//
// Operator ruling, 2026-08-31: both specs had independently specced a lease
// over the same seven call sites with incompatible signatures. Had both
// landed, the action tools would have taken two unrelated mutexes and mutual
// exclusion would have been LOST for whichever tool took only one — the
// nondeterministic interleaving ADR §5 calls the most expensive failure class
// for an agent. ADR §4 calls the lease "the largest open risk in D1", and it is
// D1's re-key that creates the contention (before D1, two agents on one
// workspace had two browsers and could not collide), so it is D1's to own.
//
// The normative definition is:
//   docs/internal/specs/browser-workspace-ownership-spec.md
//   §14 "Annex — the write lease (NORMATIVE)", §14.1 API and §14.2 Rules,
//   requirements FR-019…FR-024 and FR-019a of that spec.
//
// What D2 codes against, and must NOT restate:
//   deferred, release := leaseWrite(ctx, mgr, key, agentID, "browser_click")
//   if deferred != nil { return deferred, nil }
//   defer release()
//
// D2's FOUR new ACTION tools are automatically in scope via that annex's
// membership RULE (§14.2 rule 3: every tool in pkg/tools/browser that mutates
// page or tab state takes the lease, enforced against the REGISTRY, not a
// hand-written list) — which is precisely why D2 does not need to enumerate
// them. FOUR, not five (round-2 M2): six new tools minus the two this spec
// exempts. D1 §14.2 rule 3 says "four" too; revision 2 said five in two
// places, each time on the same screen as "two exemptions", which made
// 5 + 2 = 7 out of a set of 6.
//
//   select_option, press_key, hover, upload_file   -> leased (four)
//   snapshot (FR-038, read-only), handle_dialog (FR-035, recovery)
//                                                  -> exempt (two)
//
// D2's obligation is only to state its two EXEMPTIONS and to get them into
// that annex's closed exemption set: see FR-035, FR-038 and §15 item 2.
```

**Per-tool locator matrix — the table that makes the `Locator` abstraction pay for itself (grill m8).** Revision 1 had exactly one entry and the grill was right that a one-entry table does not justify a struct. It has five:

| Tool | `Selector` | `Text` | `Role`+`Name` | Rule |
|---|---|---|---|---|
| `browser_click`, `browser_hover`, `browser_select_option`, `browser_upload_file`, `browser_wait`, `browser_get_text` | ✅ | ✅ | ✅ | any one |
| `browser_type` | ✅ | ❌ | ✅ | `text` is the **value typed**, not a locator — reject by name (FR-004) |
| `browser_press_key` | ✅ | ❌ | ✅ | `key` is a value; `text` collides the same way `browser_type`'s does — reject by name (FR-004, grill O3) |
| `browser_press_key` with **no** locator | — | — | — | legal: dispatches to whatever holds focus, or to the document body when nothing does. Takes the D1 lease (§14), skips `waitActionable` (A-10). |
| `browser_snapshot`, `browser_handle_dialog` | ❌ | ❌ | ❌ | take no locator at all |

**Discipline inherited, not invented.** `waitActionable` and `resolveTarget` issue CDP round trips and therefore **must never run with `m.mu` held** — the ADR-038 rule the manager already documents on `installTargetListenerLocked` and `handleTargetEvent`. Every new tool follows the existing call order exactly: `controlledResult` → `leaseWrite` (D1 §14) → `mgr.Session(...)` → `context.WithTimeout` → `displayLocator` → `resolveTarget` (+`defer cleanup()`) → `waitActionable` → the act. **`browser_handle_dialog` deliberately starts at `mgr.Session(...)`** — see FR-035.

### Stream A — Target resolution + actionability [CRITICAL PATH]
**Owns:** `target.go` (new: `Locator`, `resolveTarget`, AX branch), `actionable.go` (new: `waitActionable`, `ErrNotActionable`, the FR-034 revert switch, the FR-032 counters), the rewiring of click/type/get_text/wait onto `resolveTarget`, the wait replacement in `ClickTool.Execute` and `TypeTool.Execute`, `displayLocator`'s role/name rendering.
**Depends on:** nothing.
**Interface out:** the contract above.

**AX branch mechanics (the part that must not be guessed):**
- Query via `accessibility.QueryAXTree().WithRole(r).WithAccessibleName(n)`. Its own doc states it returns *"those that match the specified attributes, **including nodes that are ignored for accessibility**"* — so the result set **must be filtered on `Node.Ignored == false`** before ordering, or a hidden node wins.
- **Deterministic ordering** is document order of the surviving nodes' `BackendDOMNodeID`s, mirroring the text matcher's existing behaviour. Ordering must be asserted directly (a stable-order test), not inferred from a passing click.
- **Multi-match** with no `index`: error naming the count and the first three candidates' names — the same shape `resolveTextTarget` uses for ambiguity (`text_selector.go::resolvePendingErr`). With `index`: select the nth; out of range is an error naming the count.
- **Ignored-but-wanted (grill m9).** Chrome marks nodes ignored for reasons beyond `aria-hidden` — presentational containers, and layout-dependent cases. An agent that must reach such an element would otherwise get a "not found" indistinguishable from a genuinely absent element. **The no-match error MUST name the number of ignored candidates that matched the role+name**, e.g. `no visible match for role=button name="Next" (3 candidates matched but are ignored for accessibility)`. That one number is what lets the agent tell the two cases apart and fall back to a CSS locator.
- **Empty AX tree** (a page that has not committed a document, or an about:blank tab): a named error, not a nil-deref and not a silent zero-match. Dataset row.
- **Marker, not node handle.** Set `data-omnipus-tsel` on the winner via `DOM.setAttributeValue` against `BackendDOMNodeID`, return `[data-omnipus-tsel="<tok>"]`, reuse `text_selector.go::removeTextMarker` as cleanup. This is what makes the change additive: no downstream `chromedp` action changes at all.
- **Inherited-and-accepted tampering surface (grill STRIDE row 9).** `data-omnipus-tsel` is an ordinary DOM attribute: a hostile page can read it and can stamp it on its own nodes. This is **pre-existing** to the text selector and is not introduced here, but the AX path widens the number of tools that trust the marker. Recorded as inherited and accepted; the mitigation that already exists is the per-resolution random token (`text_selector.go::nextTextSelectorToken`), which a page cannot predict.
- **Iframes (grill unasked-question 5, and the most likely silent failure on a real site).** `GetFullAXTree`/`QueryAXTree` can return nodes from child frames, but the marker stamp and the downstream `chromedp` `ByQuery` are **document-scoped**. A role+name match whose owning frame is not the top document therefore resolves an attribute the query will not find. **The AX branch MUST detect this** (the node's frame id differs from the top frame's) **and return a named error** — `element matched in a child frame; frame targeting is out of scope (ADR-072 D2.6) — use a CSS locator inside that frame` — never an empty result. Dataset row.

**Actionability mechanics — the fast path, written out (grill M1):**

Revision 1 asserted "one batched CDP round trip on the fast path" while defining `CondStable` as two consecutive identical bounding boxes. Two observations separated in time cannot be one round trip. The grill was right; here is the actual sequence.

- **RT1 — one `Runtime.evaluate` against the resolved marker selector**, returning a single JSON object: `{box: {x,y,w,h}, enabled: bool, hit: "self"|"descendant"|"occluded"|"indeterminate", occluder: "tag#id"}`. All of `visible`, `enabled` and `hit-testable` are computed **in-page** in that one call: `getBoundingClientRect()` for the box, `matches('[disabled]') || getAttribute('aria-disabled')==='true'` for enabled, and `document.elementFromPoint(cx, cy)` for the hit test.
- **RT2 — the same script again, scheduled after one `requestAnimationFrame` inside the page**, so the frame wait costs no extra round trip. `CondStable` is satisfied when RT2's box equals RT1's.
- **Total *inside `waitActionable`* on the fast path: exactly TWO `Runtime.evaluate` round trips.** Not one. FR-007 asserts that number **over the gate, not over the tool** — see the scoping note immediately below.
- **On failure**, the gate retries the RT1/RT2 pair at `text_selector.go::textResolvePollInterval` (150 ms) until the tool's timeout, then returns `ErrNotActionable` naming the first condition that never became true.

**The counting seam is `waitActionable`, NOT `browser_click` (round-2 C2 — revision 2's assertion was false).** Revision 2 asserted the count over the whole click and added a dataset row asserting **zero** `DOM.getBoxModel`. Both are false, and re-derived from the pinned dependency rather than assumed:

| What chromedp issues after the gate returns | Source, `chromedp@v0.15.1` |
|---|---|
| `DOM.getBoxModel` | `Click()` (`query.go:1054-1061`) appends `NodeVisible`; `NodeVisible` (`query.go:464-487`) calls `dom.GetBoxModel().WithNodeID(...)` |
| `DOM.resolveNode` + `Runtime.callFunctionOn` | `NodeVisible`'s `callFunctionOnNode(ctx, n, visibleJS, &res)` (`query.go:439-449`) |
| `DOM.scrollIntoViewIfNeeded`, `DOM.getContentQuads`, 2 × `Input.dispatchMouseEvent` | `MouseClickNode` (`input.go:57-92`) |

§3 states that no downstream `chromedp` action changes, so none of that traffic goes away. **A counter wrapping `browser_click` therefore reads ≥1 `getBoxModel` on a green build.** FR-007 is restated as *"`waitActionable` issues exactly two `Runtime.evaluate` round trips, and zero `DOM.getBoxModel` / `DOM.getNodeForLocation`, **in addition to** whatever `chromedp.Click`/`SendKeys` issue after it returns"*, and the counting seam is scoped to the gate. That keeps the assertion falsifiable — it still fails if the gate grows a third probe or reaches for `getNodeForLocation` — while making it true.

**The duplication this exposes is real, and the gate keeps `CondVisible` anyway (round-2 unasked-Q1; §12 A-17).** Because `chromedp.Click` re-runs its own `NodeVisible` after the gate, visibility is checked twice and the gate's cost is purely additive — so US-4's "the gate is not a tax" is a claim about *magnitude* (two in-page evaluates), never about *redundancy*. Dropping the gate's `CondVisible` would take the fast path to one round trip, and it is still the wrong trade: when chromedp's own visibility wait fails, `Selector.Do` → `retryWithSleep` (`chromedp.go:773-784`) swallows the `ErrNotVisible` (`s.wait` returns `(false, nil)`) and polls to the deadline, so `sleepContext` returns a bare `context deadline exceeded`. The gate's `CondVisible` is the **only** thing in the system that turns "not visible" into a named condition an agent can act on. Keep it; pay the duplicate.

**Why `document.elementFromPoint` rather than `DOM.getNodeForLocation` (recorded so it is not "simplified" back):** `getNodeForLocation` is a separate round trip, needs a second `DOM.describeNode` to name the occluder, and cannot see into shadow roots. `elementFromPoint` gives the hit node, its tag and id, and the shadow-root descent, in the same evaluate that reads the box.

- **Shadow DOM.** `elementFromPoint` on the top document returns the shadow **host**, not the inner node, so a naive `host.contains(target)` is false and a working click becomes a hard `hit-testable` failure. The probe **MUST** descend: while the hit node has an open `shadowRoot`, re-hit-test with `shadowRoot.elementFromPoint(cx, cy)`. A **closed** shadow root cannot be descended and yields `hit: "indeterminate"`.
- **Cross-origin iframes.** The top document's `elementFromPoint` resolves the `<iframe>` element itself; the target is unreachable from that document. This yields `hit: "indeterminate"` too. (Such a target will normally have already failed resolution — see the AX iframe rule above — but a CSS locator can still name one.)
- **`indeterminate` is treated as PASS, with a structured `hit_test: "indeterminate"` field on the tool result and an `omnipus_browser_gate_indeterminate_total` counter increment.** Never a silent pass (the operator can see it), and never a hard failure (a closed shadow root or a cross-origin frame is not evidence the click is wrong). This, together with FR-034's revert switch, is the answer to the grill's M13 availability concern: the two conditions most likely to false-fail on a real site are the two that now degrade to a recorded pass rather than an error.

**FR-037 — the post-gate failure must not escape as a bare timeout (round-2 M5).** SC-003 says "no path emits a bare `timeout`", and revision 2 could not deliver it: the gate runs strictly **before** `chromedp.Click`, which then runs its own `NodeVisible` wait, and when that wait fails `retryWithSleep` polls to the context deadline and returns `context deadline exceeded` — the exact string SC-003 forbids. The window is not theoretical: it is the SPA case §13 holdout 5 describes, where the page re-renders between the gate and the dispatch.

**Therefore every action tool wraps its post-gate `chromedp` action.** A `chromedp.ErrNotVisible`, or a `context.DeadlineExceeded` from an action the gate had already passed, is translated into `ErrNotActionable{Failed: CondVisible, Detail: "the element passed the actionability gate and then stopped being visible before the click was dispatched"}`. The condition reported is `visible` because that is literally what chromedp re-checked and lost. No new condition is added — the closed set stays at four (S-13 still holds), and the counter increments under `condition="visible"` like any other visible failure. Timeouts from a tool that never entered the gate (a read-only tool, a resolution failure) are **out of this requirement's scope** and keep their existing wording.

**FR-005's stability guarantee is scoped, in writing (round-2 M9).** `CondStable` is proven **across RT1 and RT2 and nowhere else.** The click that follows dispatches at a centre computed from `MouseClickNode`'s own `DOM.getContentQuads` — a **later** read than either probe. On a page that starts or resumes animating in that interval the gate passes and the click can still land elsewhere. This is stated as a §5 non-behavior and carries a dataset row rather than being discovered in a holdout: the gate reduces the window, it does not close it. Closing it would need the dispatch coordinates to come from the gate's own second read, which means replacing `chromedp.Click` — out of scope under §3's "no downstream `chromedp` action changes at all".

**FR-034 revert switch.** `tools.browser.actionability_gate`, an enum with two values: `full` (default — the four conditions) and `visible_only` (the pre-change `chromedp.WaitVisible` behaviour, verbatim). Implemented exactly like `manifest.go::previewAllLazy`: an `atomic.Value` written from live config on every turn and read **inside** `waitActionable` — one chokepoint, no second branch, no restart. **Time-boxed (mirroring ADR-071 FR-043):** it exists to survive the operator's observation window on the FR-032 counters, and **must be deleted in the same change that acts on that data**. Landing this feature MUST also file the removal issue, referenced by number in the config key's own doc comment; leaving a permanent flag on the hot path is its own cost.

### Stream B — The four interaction verbs
**Owns:** `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file` in a new `tools_interact.go`; their `RegisterReplacing` lines and `BrowserBuiltinMetadata` entries.
**Depends on:** Stream A's `resolveTarget` + `waitActionable` interfaces (not their internals). **`browser_upload_file` additionally depends on issue #659 — see FR-029.**

- **`browser_select_option`** — accepts `value` **or** `label` (an agent reads a label, not a value attribute), sets it, and **dispatches a real `change` event**; a `<select>` mutated by `SetValue` alone fires nothing and React-style listeners never see it. This is the same lesson `browser_type`'s `clear` path already records. Multi-select accepts an array.
  - **Zero options** (`<select>` with no `<option>`): a named error, not a silent no-op. Dataset row.
  - **Partial multi-select match** (round-1 unasked-question 4): two of three labels resolve. **The call errors, names the unresolved labels, and applies NOTHING.** A partial application leaves the form in a state neither the agent nor the operator asked for, and the agent cannot tell from a success result which labels landed. Dataset row.
  - **`value` is a real parameter, not prose (round-2 unasked-question 4).** Revision 2 wrote "accepts `value` **or** `label`" and then exercised `label` in every scenario and every dataset row, which left it genuinely unclear whether `value` shipped. It does: `{value: "b"}` matches `<option value="b">`, `{label: "Beta"}` matches the option's rendered text. **Exactly one of the two may be supplied** — both populated is an `ErrLocatorConflict`-shaped error naming both fields, the same contract the `Locator` matrix applies to locators. A `value` that matches no option is the same named error as a `label` that matches none. S-61 and a dataset row now exercise it, so the claim can fail.
- **`browser_press_key`** — `chromedp.KeyEvent` globally, or `chromedp.KeyEventNode` when a locator is supplied (locator ⇒ the actionability gate applies). Accepts a **named** key set (`Enter`, `Tab`, `Escape`, `ArrowUp/Down/Left/Right`, `Backspace`, `Delete`, `Home`, `End`, `PageUp`, `PageDown`) plus modifiers. An unrecognised name is an error listing the accepted set — **never** silently typed as literal text. Rejects a `Text` locator by name (grill O3).
  - **Nothing focused, no locator** (round-1 unasked-question 6): the key is dispatched to the document — CDP's `Input.dispatchKeyEvent` targets the focused node, and with none focused the document body receives it. **This is stated, not left to discovery**, and the result carries `focused_element: null` so an agent that expected a form submit can tell why nothing happened.
  - **The no-locator call SKIPS `waitActionable`, and that skip now has an oracle (round-2 M11).** It is the **only** sanctioned bypass of the actionability gate in the whole design (§12 A-10 decided it; there is nothing to gate on when no element is named). Revision 2 stated it in two places and asserted it in none, so a later refactor that started gating it would turn a legal keystroke-to-document into a hard `visible` failure on any page with nothing focused — a silent capability loss with a green suite. **FR-010 now carries the skip in its own text**, and `TestPressKey_NoLocatorSkipsActionabilityGate` (§10 order 18) asserts it **at the seam** — `waitActionable` is not entered — never by timing, which would pass on a fast machine regardless. Dataset row. Note the skip is scoped to the gate only: the call still calls `controlledResult` and still takes the D1 lease (A-10).
- **`browser_hover`** — scroll into view (`dom.ScrollIntoViewIfNeeded`), then `Input.dispatchMouseEvent{type:"mouseMoved"}` at the box centre. Gate applies. **Must not** click.
- **`browser_upload_file`** — `chromedp.SetUploadFiles`. Confinement decided below; audit event required by FR-031; registration gated by FR-029.

> **`browser_upload_file` confinement — DECIDED, reversing revision 1 (grill M9).**
>
> `SetUploadFiles` hands Chrome an **absolute host path** and *Chrome* opens the file. Two consequences the implementation cannot dodge, unchanged from revision 1 and confirmed by the ADR:
> 1. `tools.PathHandle`'s `os.Root`-mediated I/O — the whole TOCTOU-hardness argument — **cannot apply**, because the Go process never performs the read. The only usable output is `handle.RealPath()`, which is the documented exception and reintroduces exactly the race `PathHandle` exists to close. **This residual window is accepted and stated, not hidden.**
> 2. The read happens in the **Chrome** process, outside whatever confinement covers the gateway thread.
>
> **Revision 1 chose `FSOpSend` plus a hand-rolled `policy.AllowedRoots` check. That is withdrawn.** Verified from `resolvepath.go::FSOp`'s own doc: `FSOpWrite`'s rule **is** "work dir or a mount (`policy.AllowedRoots`) only". So the revision-1 design re-implemented `FSOpWrite`'s rule **outside** the chokepoint — a second string-comparison gate, therefore a **second TOCTOU window** stacked on the accepted `RealPath()` one. Worse, `FSOpSend`'s own doc records that a path-based gate for that op was *"explicitly rejected"* by the operator because it is bypassable in one extra step, "so the real gate is tool policy". Revision 1 quoted the first half of that reasoning and then built the gate the second half rejects.
>
> **Decision (FR-012): resolve through `tools.ResolvePath` with `FSOpWrite`.** One rule, one implementation, enforced at the chokepoint, no second window. **Why an upload is classed as a write rather than a read or a send:** the operation hands a host path to a *different process* that is outside the gateway's confinement, and the set of paths it is safe to hand out that way is exactly the set `FSOpWrite`/`FSOpServe` already bound — the work directory and explicit mounts. `FSOpRead`'s "anywhere outside the secret carve-out" is far too wide for a path that leaves the process; `FSOpSend`'s no-path-restriction posture is correct for a chat disclosure the operator can see and wrong for a silent handoff to Chrome.
> **Consequence, recorded because it narrows a test fixture:** US-8/AC1's happy-path file must live in the turn's working directory or an explicit mount. A `/tmp` fixture outside those roots now correctly fails.
> **Still ratifiable:** §12 A-9 records this as a spec decision the operator may overrule; the alternative on the table is `FSOpSend` alone with tool policy as the sole gate, which is what `FSOpSend`'s doc instructs. Shipping **both** is the one option this spec rules out.

### Stream C — Dialog handling [HIGHEST WEDGE RISK]
**Owns:** `browser_handle_dialog` + the per-tab dialog listener and pending-dialog state on `sessionEntry`.
**Depends on:** nothing in A/B. Can start immediately. **See §3's Parallelization note (grill O1) for the one merge-order constraint.**

**The structural constraint the ADR names and this spec makes implementable.** `installTargetListenerLocked` attaches exactly **one** listener, on `se.tabs[0]`, and its doc explains why that is correct for `Target` discovery — *"discovery itself is browser-global"*. `Page.javascriptDialogOpening` is **not** browser-global; it is per-target. A dialog on tab 2 with a tab-0-only listener is invisible, and the tab is wedged with no record that a dialog exists. **Every tab needs its own dialog listener.**

**Three gaps revision 1 left, now closed (grill M5):**

1. **Idempotence key.** `chromedp.ListenTarget` is an **append**: calling it twice on one ctx stacks two handlers, and every dialog is then recorded twice. The design mirrors `sessionEntry.listenerTarget` exactly: a new `sessionEntry.dialogListeners map[target.ID]struct{}`, checked-and-set under `m.mu` immediately before the append, so a second install on the same target is a cheap map lookup and a return.
2. **Re-arm on ctx recreation.** ADR-041 fix F3 exists because a tab ctx dying "silently ends the listener forever unless something re-installs it", and `handleTargetEvent`'s own doc records that `mgr.Session()` runs a blocking `chromedp.Run` that **recreates a dead tab ctx**. The dialog listener must therefore be installed at **every site where a tab ctx is created**, named exhaustively so this is implementable rather than a hunt: `manager.go::createFirstTab` (which is also where `Session`'s crash-recovery path lands after tearing down a browsing context whose active tab died), `manager.go::OpenTab`, and `manager.go::adoptTarget` / `::adoptTargetWithRetry`. **The key from (1) is what makes calling it at all four sites safe.** A tab whose ctx is recreated MUST have its stale `target.ID` entry evicted from `dialogListeners` at teardown, or the re-arm is skipped and the original wedge returns with no record — the exact failure ADR-041 F3 describes. BDD scenario and TDD entry both required.
3. **A dialog opened before its listener existed is UNDETECTABLE, and the spec says so.** `Page.javascriptDialogOpening` is an **event, not queryable state** — there is no `Page.getPendingDialog`. A tab adopted or re-attached after a dialog opened never observes it, so FR-013's "check pending-dialog state on timeout" finds an **empty map** and would emit exactly the bare timeout it exists to replace.

   **The fallback predicate, WIDENED in revision 3 (round-2 M8).** Revision 2 fired the suspected-dialog message only when *"that tab's last completed command was an activation"*. That condition is **false in the motivating case**: gap 3's own premise is a tab **adopted or re-attached after** a dialog opened, and such a tab has no completed command of its own in this session — so the narrow predicate declines to fire and the bare timeout comes back. The predicate is therefore:

   > **Any CDP timeout on a tab with no recorded pending dialog gets the suspected wording.** No last-command condition gates it.

   A false positive costs one hedged sentence in an error the agent was already going to receive; a false negative costs the entire requirement. The message is already hedged and always names the recovery verb: `browser_click: the tab stopped answering and may have an open dialog that predates this session's listener — try browser_handle_dialog{accept:false}`. Distinctly worded from the confirmed case, never claiming knowledge it does not have.

   **`sessionEntry.lastActivation` survives anyway, but only to sharpen the wording, never to gate it.** When the field *does* record an activation (a click, key event, navigate or select), the message names it — *"stopped answering after a click"* — which is strictly more useful to the agent. Its full contract, because revision 2 named a field that did not exist and gave it no writer (round-2 M8): the **tool** writes it under `m.mu` after its own CDP call returns, never `handleTargetEvent` (whose doc forbids blocking); a second concurrent tool on the same tab overwrites it, which is acceptable because it is advisory wording and not a decision; and it is **cleared on ctx recreation and at tab teardown**, alongside the `dialogListeners` eviction, because a recreated tab genuinely has no completed command. Edit site 15 and §2.4 both carry it. **Nothing branches on it** — that is the point of the widening.

**Behaviour:**
- `browser_handle_dialog{accept?: bool, prompt_text?: string}` → `page.HandleJavaScriptDialog(accept)`, `.WithPromptText` when the dialog type is `prompt`.
- **`accept` defaults to `false` when omitted** — dismiss, not accept (see M8's resolution in §11). Dismissing unwedges the tab in every dialog type; accepting is the consequential one.
- Called with **no** dialog pending: a **non-error** result `{"dialog": null}`. Not an error — "check whether a dialog is blocking" is a legitimate, expected question.
- **Idempotence / retry (round-1 grill test-coverage item 5).** Agents retry. A second `browser_handle_dialog` after the dialog is already gone returns `{"dialog": null}`, never an error, and never a second `HandleJavaScriptDialog` call against a closed dialog (which CDP errors on). The pending-dialog map entry is cleared **before** the CDP call is issued, under `m.mu`, so a racing second call sees an empty map.
  - **This invariant is about the CONCURRENT case and is now tested concurrently (round-2 m9).** S-36 called the tool twice **sequentially**, which passes just as happily with the clear placed *after* the CDP call — the placement the invariant exists to forbid. **S-59** issues two calls from two goroutines against one open dialog and asserts **at the CDP seam** that `HandleJavaScriptDialog` was invoked exactly once, the loser returned `{"dialog": null}`, and neither errored. Asserting on the result shape alone would not distinguish the two placements.
- **Every other browser tool**, on a CDP timeout, must check pending-dialog state and, if one is present, return an error naming it and pointing at `browser_handle_dialog`; when none is present, apply the (3) suspected-dialog rule above.
- **`browser_navigate`'s own `onbeforeunload` (grill unasked-question 8).** A confirm raised *during* navigate's load wedges before navigate returns a tab handle. **`browser_handle_dialog` targets the browsing context's ACTIVE tab when no tab is named**, and navigate's target tab is by construction the active one — so the recovery verb reaches it. Stated because the alternative (targeting "the tab navigate returned") is unreachable in exactly this case.

**Deliberately NOT auto-dismissed.** An auto-dismiss policy is a decision about the *page's* semantics (an `onbeforeunload` confirm is not an `alert`), and silently accepting one is indistinguishable from a click the agent did not make. The tool is explicit; the *recovery pointer* is automatic.

**FR-035 — `browser_handle_dialog` is exempt from BOTH `controlledResult` and the D1 write lease (grill C5).**

*This requirement survives the lease's relocation to D1 unchanged, because the exemption is a property of the **dialog tool**, not of the lease mechanism. D2 states it; D1 §14.2 rule 3 must carry it — see §15 item 5.*

Revision 1's shared contract said "every new *action* tool must call `controlledResult`", and §5 exempted only `browser_snapshot` from the lease. Composed with the wedge, that made the only recovery verb unreachable exactly when it is needed:

- **Lease deadlock.** The `browser_click` that triggered the dialog is *still running*, blocked on CDP until the page timeout — that blockage **is** the wedge — and it holds the lease. `browser_handle_dialog` would return `{"deferred": true}` for the entire wedge window.
- **Human-viewer lockout.** `controlledResult` defers whenever a human holds the live view, and §6 records that a human on a wedged tab has no button. Composed: agent deferred, human has no affordance, tab wedged for both.

ADR D2.3 requires the opposite in terms: *"Whatever is built must guarantee the session cannot be left wedged."*

**Therefore `browser_handle_dialog` calls neither `controlledResult` nor `leaseWrite`.** It sits alongside the read-only tools that `controlledResult` already leaves ungated — `browser_screenshot`, `browser_get_text`, `browser_wait` — for a different but equally specific reason: **it is a recovery verb, not a write. Its entire effect is to return the tab to the state every other tool assumes.** Gating a recovery verb behind the mechanisms the fault disables is a deadlock, not a safety property.

**Why this is safe rather than a hole in the lease.** The lease exists to stop two writers interleaving *page mutations*. `HandleJavaScriptDialog` mutates no page state; it releases a blocked execution context. Two concurrent calls are idempotent by the map-clear-before-CDP rule above. And the elevation concern that *is* real — `accept:true` on a destructive `confirm()` — is answered by tool policy and the `accept:false` default (§11 M8 row), not by a lease.

### Stream D — `browser_snapshot`
**Owns:** `browser_snapshot` in `tools_snapshot.go`; the shared AX-tree fetch with Stream A (D2.4: *"Build them together or the second one is built twice"*).
**Depends on:** Stream A's AX fetch + filter helpers.

- `accessibility.GetFullAXTree()`, filtered on `Ignored == false`, rendered as an indented `role "name"` outline carrying whatever handle the action tools accept (`index` within the snapshot's own ordering — the **same** document ordering Stream A's multi-match uses, so a handle read from a snapshot resolves identically in the next call).
- **Values are emitted by default (FR-018).** `accessibility.Node.Value` is rendered for every node that has one. **No `include_values` parameter exists.** No role-based omission exists. Per the operator ruling; see §2.3 for the accepted risk.
- **FR-027: the rendered output passes through `cfg.SensitiveDataReplacer()` unconditionally**, before the cap is applied and before the result is returned. Defence in depth only — it substitutes registered credential plaintexts (>3 chars) with `[FILTERED]` and does nothing for arbitrary form values.
- **FR-028: the capture is visible in two places, each answering a different question** (§2.3). The **chat thread** shows *what* was captured — rendered by default, pinned by a **behavioural** regression assertion that `shouldRenderToolCall` returns `true` for all six new names (the substring check on `toolVisibility.ts` is kept only as a secondary hint; round-2 M6). A metadata-only **`browser_snapshot`** audit event records *that* a capture happened, with its shape and origin, and is read at **Settings → Security → Audit Log**. The event name is underscore-form because the `AuditEntry` contract pins `^[a-z_]+$` and a dotted name blanks that viewer entirely.
- **Cap: `config.DefaultBuiltinSuccessCap` (64,000) BYTES, node-boundary truncation. NOT `capGetText` (FR-017, grill M3).** Revision 1 said "the exact `capGetText` mechanism" and, one sentence later, "not an arbitrary byte cut mid-node". Both cannot hold: verified, `capGetText` is `text[:maxGetTextChars] + suffix` compared against `len(text)` — an arbitrary byte cut that can split a UTF-8 rune mid-sequence and always splits mid-node. The snapshot therefore builds its outline **node by node**, stops before the first node that would carry the running byte total past the cap, and appends a marker naming the cap and the number of omitted nodes: `\n[truncated at 64,000 bytes; 412 further nodes omitted]`. Consequences, all asserted:
  - The cap is expressed in **bytes** in FR-017, the BDD scenario and the dataset row. Revision 1's "exactly 64,000 characters" is false for any AX tree carrying a non-ASCII accessible name.
  - Output is **≤ 64,000 bytes**, not exactly 64,000 — a node boundary rarely lands on the byte.
  - Output is always valid UTF-8; no rune is ever split.
  - The **top** of the tree survives, which is what makes the retained portion useful.
  - `TestChokePoint_PerSurfaceCap_Snapshot` therefore mirrors `per_tool_cap_alignment_test.go`'s **cap constant**, not its mechanism — it asserts `≤ config.DefaultBuiltinSuccessCap` bytes and that the same constant is the source, and explicitly does **not** assert `capGetText`'s prefix-cut shape.
- **Fetch is not capped, only the render (grill STRIDE row 10, honest statement).** `GetFullAXTree` on a 5,000-node page runs in full before any truncation. This is bounded only by the tool's own page timeout. Measured post-hoc by §13 holdout 6; **not** bounded by a requirement in v1, and recorded as a known limit rather than implied away.
- **Context budget (grill unasked-question 2).** The cap is per-tool, and ADR-066's turn budget is separate: a 64,000-byte snapshot plus a 64,000-byte `browser_get_text` in one turn is 128,000 bytes of context from two tools that each individually obey their cap. **This spec does not change the turn budget** — that is ADR-066's chokepoint (`windowTrim`) and out of scope — but the interaction is stated so it is not discovered as a surprise.
- **FR-038 — read-only, and now with its own requirement number.** `browser_snapshot` does **not** call `controlledResult` and does **not** take the D1 write lease, matching `browser_screenshot`/`browser_get_text`/`browser_wait`. **Why this got promoted from prose to an FR in revision 3 (round-2 unasked-question 9):** D1 §14.2 rule 3 cites this exemption as *"D2 **FR-018**"* — but FR-018 is the *values-by-default* requirement and says nothing about the lease. A cross-spec citation pointing at a requirement about something else does not survive the next revision of either document. **D1 must be corrected to cite "D2 FR-038"** — with the document prefix, because D1 has its own unrelated FR-038; §15 item 2 carries it alongside D1's own exemption-set defect. FR-035 stays what it always was: the *dialog* exemption, which needs a different and stronger argument because `browser_handle_dialog` genuinely does change state.

### Stream E — Policy seeding, tier assignment, catalog sync [MANDATORY, BLOCKS BOOT]

**Split into two independently landable halves, because only one of them is blocked (round-2 m6).** Revision 2 said A-3 "blocks Stream E's fixture-literal edit only; every other stream can proceed" while also declaring Stream E the stream that must land first and that every registration depends on — which made the one open ruling block every registration, a materially different scheduling picture. Naming the halves separately removes the ambiguity:

- **Stream E1 — policy + catalog [UNBLOCKED, lands first].** `pkg/config/defaults.go` (global `sandbox.tool_policies`); `pkg/coreagent/core.go` (`allStaticToolNames`, Explorer, Researcher, Ray, Jim); `pkg/tools/browser/metadata.go`. **Nothing here waits on A-3** — tier membership does not affect policy or the coverage catalog. This is the half that unblocks every registration.
- **Stream E2 — tier fixtures [BLOCKED ON §12 A-3].** `pkg/tools/manifest_test.go:667-681` (the Tier 3 fixture) + the arithmetic literals at `:694-744`; `manifest.go::previewedLazyToolNames` and `TestVisibility_PreviewedSetIsExactlySeven` **only under Option B**; the new `pkg/gateway` partition test (FR-036). A-3 decides whether the snapshot is Tier 3 or Tier 2, and the literals differ.

**So the accurate sequencing is: E1 → registrations → E2**, and E2 can trail the rest as long as it lands in the same PR series (the tier test is build-breaking by design, so the tree is not green until it does).

**Depends on:** only the six tool **names**, which are fixed by the ADR. **E1 can and should land first** — before any tool registers.
**Ordering that is not optional:** `allStaticToolNames` must be edited **before or with** Jim's, Ray's, Explorer's and Researcher's override maps, because `validateOverrideKeys` **panics** — not errors — on an override key it does not recognise.
**Coverage is closed by one edit.** `ValidateToolPolicyCoverage` is OR-based: a global entry in `defaults.go` covers every agent. The per-agent edits set *posture*, not coverage. This is why Mia and Ava need **no** edit at all — `denyAllThenOverride` starts every `allStaticToolNames` member at `deny` and they list no browser override.
**FR-029's one subtlety, stated because it decides an ordering:** `browser_upload_file`'s **name** still enters `allStaticToolNames`, `defaults.go` and `BrowserBuiltinMetadata` in this stream, or coverage gaps appear and `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` fails. **"Held" means unregistered, not unseeded** — Stream B omits its `RegisterReplacing` line until #659 lands.

### Stream F — Error routing
**Owns:** the `file://` pointer in `ValidateURL`; the FR-030 `serve_web` grants (coordinated with Stream E, same files).
**Does NOT own the write lease.** It did in revision 2's first draft; the lease was relocated to the D1 spec by operator ruling on 2026-08-31 — see the interface-contract note in §3 and §15 item 5.

- **`file://` (FR-019).** The shared message serves five schemes and only `file` has an answer, so branch: `file` gets `"browser: file:// URLs are blocked (they would bypass filesystem confinement). To view a local file in the browser, serve it with the serve_web tool — it returns a /preview/<agent>/<token>/ http URL that browser_navigate accepts."` The other four keep the existing string. **The tool name is `serve_web`**.
- **FR-030 — the pointer must be reachable (grill M4).** Verified: `serve_web` is `allow` in the global default **and** is a member of `allStaticToolNames`, so every agent built through `denyAllThenOverride` resolves `deny` unless it lists an override — and **only Jim does**. Of the five agents that can call `browser_navigate` today (Jim, Ray, Explorer, Researcher, and Worker via `tightenGlobalCeiling`'s sparse-map inheritance of the global `allow`), **three resolve `serve_web: deny`: Ray, Explorer and Researcher.** For them the new message trades one dead end for a longer dead end that costs a failed tool call to discover — **#242's dead end relocated, which is precisely what D2.5 exists to remove.** *(The round-2 grill headline said "four of six" and the task framing said "five of six"; the verified figure is three of the five browser-capable agents. The fix is unchanged either way.)*
  **Decision: seed `serve_web: allow` for every agent that holds the browser surface.** Ray, Explorer and Researcher gain it; Jim and Worker already have it; Mia and Ava hold zero browser tools and are unaffected. Argued in §11 as a real posture change, not a bookkeeping edit.
  **And the pointer's other failure mode (grill unasked-question 9):** `gateway.preview_enabled` is live and read per-request (ADR-044); with it off, `/preview/` 404s and the route is dead for Jim too. The message therefore ends with a conditional clause rather than a promise: `…http URL that browser_navigate accepts (requires the serve_web tool and gateway.preview_enabled).` One clause, no extra round trip, honest when the flag is off.
- **Write lease — RELOCATED, not dropped.** Defined normatively in `docs/internal/specs/browser-workspace-ownership-spec.md` §14. **D2's four remaining new action tools** (`select_option`, `press_key`, `hover`, `upload_file`) are in scope automatically through that annex's registry-enforced membership rule (§14.2 rule 3) — **four**, matching D1's own wording; revision 2 said five here and in the interface contract, which was six-minus-two miscounted (round-2 M2). D2 adds no lease code, no lease test and no lease requirement of its own. The two things D2 still owns are its **exemptions** — FR-035 (dialog) and FR-038 (snapshot).

**Parallelization.** E1 lands first (names + policy, no behaviour). A is then the critical path. Once A's interface commit exists, B, C, D and F fan out — different files. Stream C is the only one that touches `manager.go`'s listener plumbing and should not be parallelised against another `manager.go` edit. E2 trails, gated on A-3.

**Three merge-order constraints a parallel agent would otherwise discover at merge.** Revision 2 named only the first; the round-2 grill found the second (M7) and the E-split makes the third explicit (m6).

1. *(round-1 O1)* Stream C can build the listener plumbing immediately, but it **cannot land `browser_handle_dialog`'s `RegisterReplacing` line before Stream E1**, because §5 forbids registering any of the six in a commit that does not seed its policy. Split Stream C's work into "plumbing + tool implementation" (any time) and "registration" (after E1).
2. **(round-2 M7) `D1 §14`'s `leaseWrite` must land before Stream C's `_RecoversWhileLeaseHeld` can be written.** That test is called *THE* acceptance test in three places, its **Given** is "a `browser_click` … **holding the write lease**", and D2 writes no lease code at all (§10 order 24) — so it is unwritable until D1's annex exists. Revision 2 stated no such ordering anywhere. **And the resolution does not have to wait**, because the test proves two independent things:
   - **The wedge-recovery half** — a tool blocked on CDP does not prevent `browser_handle_dialog` from reaching `page.HandleJavaScriptDialog`, and the blocked call then returns. A stand-in that holds the tab's CDP with a blocked call and **no lease at all** proves this today, against D2 code only. Land it now as `TestDialog_RecoversWhileCDPBlocked`.
   - **The lease-exemption half** — the blocked holder's *lease* does not defer the dialog tool. **Only this one tests FR-035**, and only this one waits for D1.

   Split the test, name each half for what it covers, and do not let the first half's green be read as evidence for FR-035.
3. **(round-2 m6) Stream E2 waits on §12 A-3; nothing else does.** E1, A's seam, C's plumbing, and B's and D's tool bodies all proceed while A-3 is open.

---

## 4. Behavioral contract (observable)

- When an agent names an element as role + accessible name on a page whose CSS classes are generated, the element resolves — through the same seam, in the same call shape, as a CSS or visible-text locator.
- When two elements share a role and name, the call **errors** with the count and the first candidates, and succeeds when an `index` is supplied. It never silently picks the first.
- When an agent supplies two locator kinds at once, the call **errors naming both fields**. There is no precedence rule to learn.
- When a role+name matches only nodes Chrome marks ignored, the error **says how many** — so the agent can tell "hidden" from "absent".
- When an agent clicks or types on an element that is disabled, still animating, or covered by an overlay, the call fails naming **which** of `visible` / `stable` / `enabled` / `hit-testable` was not met — and for `hit-testable`, what is on top.
- When the hit test cannot be performed at all (closed shadow root, cross-origin frame), the call **succeeds** and the result says `hit_test: "indeterminate"`. It neither pretends the check passed nor fails a click that would have worked.
- When an agent clicks an element that was already actionable, **the gate** issues **exactly two** `Runtime.evaluate` round trips — over and above the CDP traffic `chromedp.Click` issues after the gate returns, which this change does not touch.
- When an element passes the gate and then stops being visible before the click is dispatched, the failure still names `visible` — it is **never** a bare `context deadline exceeded` (FR-037). The gate's stability guarantee holds **across its two probes, not at the moment of dispatch**, and that limit is stated rather than implied.
- When an operator hits a false gate failure on a real site, `tools.browser.actionability_gate: visible_only` restores the previous behaviour live, with no restart and no downgrade.
- When a page presents a `<select>`, the agent can set it by visible label and a `change` event fires. A partial multi-select match errors and applies nothing.
- When a form needs Enter, Tab or Escape, the agent can send it as a discrete key event; with nothing focused the result says so.
- When a menu opens on hover, the agent can open it without clicking.
- When a form needs an attachment, the agent can attach one from the work directory or a mount — subject to an `ask` prompt on every agent, and to an audit event recording which file went to which origin. **Until issue #659 lands, the tool is not registered at all** and the agent is told the tool does not exist rather than hanging on an unanswerable approval.
- **When a page calls `alert()`/`confirm()`/`prompt()`, the tab continues to answer CDP.** Either the agent dismisses the dialog explicitly, or any other browser tool that times out on that tab reports the pending dialog and names `browser_handle_dialog`. **This holds while another tool holds the write lease, and while a human holds the live view** — the recovery verb is exempt from both. There is no state in which the tab is silently unreachable to the agent.
- When a dialog was opened before its listener existed, the timeout error says a dialog is **suspected** and names the recovery verb — it never claims certainty it does not have.
- When an agent needs to know what is on a page, `browser_snapshot` returns roles, accessible names, **field values**, and usable handles, without vision and without a pre-known CSS selector, capped at 64,000 bytes on node boundaries.
- When a snapshot is taken, the operator sees **what** was captured in the chat thread, and **that** it happened — with its shape and origin, never its values — as a `browser_snapshot` audit record readable at Settings → Security → Audit Log.
- When an agent navigates to `file://`, the error names `serve_web` as the supported route — **and every agent that can reach that error can call `serve_web`.**
- When a fresh install boots with the new tools registered, it boots. No policy-coverage abort.
- When Chromium is absent (linux/arm64 today), every browser tool fails with an error that names the missing browser — not a message that reads like a tool bug.

> **One promise revision 2 made here and could not keep, now removed (round-2 M4b).** The list used to end with *"When two writers contend for one browsing context, the loser gets a non-error `{"deferred": true, …}` — never an error, never a torn interleave, never a nil `release`."* That is a **D1 observable**, asserted by `browser-workspace-ownership-spec.md` §14 and its FR-019…FR-024. D2 has no live FR for it, no scenario and no test, so stating it here was a promise this document cannot be held to. **Inherited from D1 §14; not asserted here.** What D2 *does* promise about contention is narrower and it is above: `browser_handle_dialog` and `browser_snapshot` are never deferred by it (FR-035, FR-038).

---

## 5. Explicit non-behaviors

- The system must **not** replace chromedp, add playwright-go, or add any runtime dependency (Hard Constraint #1). Every primitive §2.2 names already ships in the pinned `chromedp`/`cdproto`.
- The system must **not** apply the actionability gate to read-only tools. `browser_get_text` keeps `WaitReady` with its 8 s budget — `WaitVisible` there reintroduces the documented ~30 s hang on `<title>`.
- The system must **not** take the write lease or call `controlledResult` from `browser_snapshot` — it is read-only, like the three tools already exempted. *(This is **FR-038** as of revision 3; see the dedicated bullet below for why it was promoted from prose to a numbered requirement.)*
- **The system must not defer a recovery verb.** `browser_handle_dialog` must **not** call `controlledResult` and must **not** take the write lease (FR-035). Gating it behind the mechanisms a wedged tab disables is a deadlock, and it directly contradicts ADR D2.3's "the session cannot be left wedged".
- The system must **not** auto-dismiss dialogs. Explicit tool, automatic *pointer*. And `accept` defaults to `false`, never `true`.
- The system must **not** install the dialog listener on tab 0 only, and must **not** install it without an idempotence key. `chromedp.ListenTarget` is an append; a second install stacks a duplicate handler.
- The system must **not** report an actionability timeout as "timeout". The unmet condition is the payload. **This extends past the gate's own return (FR-037):** a `chromedp.ErrNotVisible` or a post-gate `context deadline exceeded` from an action the gate had already passed must be translated to `ErrNotActionable{CondVisible}`, not surfaced raw. Timeouts from a path that never entered the gate are out of scope and keep their wording.
- **The system must not claim `CondStable` holds at the moment of dispatch.** It is proven across RT1 and RT2 and nowhere else; `chromedp.MouseClickNode` computes the click centre from a **later** `DOM.getContentQuads` read. Do not document, comment or test this as "the element was stable when clicked" — the honest claim is "stable across two probes one animation frame apart". Dataset row covers the animation-resumes-in-the-interval case (round-2 M9).
- The system must **not** treat an un-performable hit test as a failure. `indeterminate` passes, visibly.
- **The system must not gate `browser_press_key` with no locator on `waitActionable`** — it is the one sanctioned bypass (§12 A-10) and is asserted at the seam by `TestPressKey_NoLocatorSkipsActionabilityGate`. Equally, it must **not** be exempted from `controlledResult` or the D1 lease: the skip is the gate only (round-2 M11).
- **The system must not defer `browser_snapshot`** — no `controlledResult`, no write lease (FR-038). Its exemption has its own requirement number so D1 §14.2 can cite it correctly as **D2 FR-038**; D1 currently cites FR-018, which is about something else.
- The system must **not** hold `m.mu` across `resolveTarget`, `waitActionable`, or any dialog-listener install that issues a CDP round trip. (Per §2.2a the install issues none, so the append stays under the lock exactly like `installTargetListenerLocked`.)
- The system must **not** silently ignore a locator kind a tool does not accept — reject it by name. This covers `text` on `browser_type` **and** on `browser_press_key`.
- The system must **not** apply a partial multi-select.
- The system must **not** register any of the six tools in a commit that does not also seed their policy. A registered tool with no policy entry aborts boot.
- **The system must not register `browser_upload_file` while issue #659 is open** (FR-029). Its *name* is still seeded — held means unregistered, not unseeded.
- The system must **not** re-implement `FSOpWrite`'s path rule outside `tools.ResolvePath`. One chokepoint, one rule, one TOCTOU window (the accepted `RealPath()` one).
- **The system must not add any browser tool to `src/lib/toolVisibility.ts`'s hidden set.** FR-028's chat-render half depends on all six rendering in the thread; hiding one would silently remove a control the operator ruling requires. Pinned by a regression assertion that checks **behaviour, not a substring** — see S-43 and round-2 M6/m3.
- **The system must not give either new audit event a dotted name.** `browser_snapshot` and `browser_upload_file`, underscore form only. The `AuditEntry` contract pins `event` to `^[a-z_]+$` and `src/lib/api.ts::performRequest` **throws** on a `safeParse` failure over `z.array(AuditEntry)` — so one dotted entry blanks the entire Audit Log viewer for the operator. (Two existing events already violate this; that pre-existing defect is recorded at §12 B-6 and is not D2's to fix.)
- **The system must not replicate `browser_evaluate`'s `executeEnabled` pattern on any of the six** (grill m1). `browser_evaluate` is registered unconditionally and gated at `Execute` by `sandbox.browser_evaluate_enabled`, independent of tool policy — the one place a tool is registered-but-inert in a way the policy surface cannot see. It exists for a specific operator-approved reason and is not a template. FR-029's `browser_upload_file` hold is deliberately the **opposite** shape: not registered at all, so `ToolSearch` and the manifest tell the truth.
- The system must **not** claim, in code comments or docs, that `browser_get_text` has a redaction posture. It has a length cap (§2.3).
- The system must **not** name `web_serve` in any agent-facing string. The tool is `serve_web`.
- The system must **not** leave FR-034's revert switch in place indefinitely. It is deleted in the same change that acts on the FR-032 counters; landing the feature files the removal issue.

---

## 6. Integration boundaries

- **chromedp / CDP.** In-process. `Accessibility`, `DOM`, `Input`, `Page`, `Runtime` domains. Failure → the tool errors with the domain error text scrubbed of the internal marker (`text_selector.go::scrubMarkerFromError`). **The `Page`-domain question is resolved, not open** — see §2.2a, and the pinning test at §10 order 0.
- **Tool policy (Hard Constraint #6).** Boot, hot-reload and REST-write all consume `buildKnownBuiltinToolNames`, which reads `BrowserBuiltinMetadata`. All six names must appear in **both** that catalog and `allStaticToolNames`, or `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` fails — including `browser_upload_file` while it is registration-held.
- **Approval flow / issue #659 (`pkg/agent`).** `browser_upload_file` is seeded `ask` for every agent. `pkg/agent/loop.go`'s `AutoDenyAsk` gives a headless scheduled run a defined answer (auto-deny). **Verified: it is set only in `ProcessScheduled`'s options literal and read at one site in the turn loop; #659 (OPEN, `priority:P1-high`, `area:agent-loop`) records that it is not inherited by delegated subagents.** So a delegated worker hitting an `ask` today blocks on an approval nobody can answer. This is a **hard prerequisite**, not adjacent work — see FR-029.
- **Audit (`pkg/audit`).** New dependency for this package. `pkg/tools/browser` does not import `pkg/audit` today (`capture_session.go`'s header records that deliberately, for the gateway-boundary reason). FR-028 and FR-031 add the import and an injected `*audit.Logger`, mirroring `pkg/tools/library_tool.go::LibraryReadTool.SetAuditLogger`. A nil logger is a no-op (`pkg/audit/events.go::Emit` returns early), so the tools remain constructible in metadata-only form.
- **Manifest tiering (ADR-071).** Tier 3 is the residual — no production edit for Tier 3. The **test fixture**, the arithmetic literals, and the new FR-036 partition test are the edit sites, plus `previewedLazyToolNames` *only if* `browser_snapshot` takes Tier 2 (§12 A-3, still open).
- **`contracts/` (Hard Constraint #8) — checked, and NOT triggered.** The seventeen `Browser*.yaml` schemas in `contracts/components/schemas/` are all **live-panel WS frames**. None carries a tool result. Tool results reach the SPA through the generic transcript/tool-call channel as opaque text. The six new tools introduce **no new cross-boundary shape**, so no `contracts/` change and no 5-step process. `make verify-contracts` must stay green as a regression assertion, not as a deliverable.
  **And the snapshot handle is deliberately not a wire type (grill unasked-question 10).** It is a 0-based index inside an opaque text result. If a future UI ever renders it structurally, that is a **new wire type** requiring the full 5-step process — it must not arrive by accident because a component started parsing the text.
- **SPA.** No change, and one **assertion**: `src/lib/toolVisibility.ts` contains no `browser` reference, so all seventeen browser tools render in the chat thread. FR-028 depends on that; §5 forbids changing it.
- **Filesystem (ADR-046 / ADR-063).** `browser_upload_file` only. Chokepoint `tools.ResolvePath` with **`FSOpWrite`**; the `PathHandle`-cannot-mediate caveat and its accepted `RealPath()` TOCTOU window are in Stream B.
- **D1 boundary — four items this spec does not decide.** (a) **The write lease in its entirety** — relocated to `browser-workspace-ownership-spec.md` §14 by operator ruling, 2026-08-31. (b) D2.11's browsing-context-creation audit event. (c) D2.11's team-editing-UI disclosure. (d) D1.0a's `CaptureSharedContext` default. D2 is independent of every one of them.
- **D1.1a — the isolation mechanism changed under D2, and D2 is unaffected in surface but not in environment.** D1 now specs **one Chrome process per workspace, each with its own `--user-data-dir`**, replacing per-agent CDP browser contexts. Nothing in D2's tool surface changes: every tool already resolves its tab through `mgr.Session(...)`, which is the seam that will resolve to the right Chrome. **Two consequences D2 implementers must not assume away:** (i) there may now be **several Chrome processes on one host**, so any test that assumes a single browser process, counts processes, or reuses a hard-coded devtools endpoint must be written per-workspace — this affects the §10 integration tests, which should each stand up their own workspace rather than sharing one; and (ii) the FR-007 round-trip count and SC-004's measurement harness must run **one workspace at a time**, or they measure contention between Chromes rather than the gate.
  **(iii) The resulting process count, because this repo's testing policy exists because the CI box OOMs (round-2 unasked-question 6).** §10's integration orders that need real Chrome are 0c, 10-21, 25 — **fourteen orders**. Written per-workspace and run with `-p 1` (which §10's own run line already mandates), that is **one Chrome at a time, torn down between orders**, not fourteen concurrently: the constraint is sequential execution, and it is now stated as a requirement rather than left to a reader's assumption. **A test that stands up two workspaces to exercise cross-workspace isolation is D1's, not D2's** — no D2 scenario needs two Chromes. If an implementer finds one that seems to, that is a signal the test belongs in D1. Budget on the CI worker: one headless Chrome plus the Go test binary, which is the profile the existing `pkg/tools/browser` E2E job already runs at `-lt 180`.
- **D1.2 is superseded, and this spec contains nothing that depended on it.** Unattended delegated work no longer gets a separate browsing session — every agent on a workspace shares that workspace's browser and its logins. **Checked explicitly:** the only places this document discusses unattended agents are §11(a) and §12 A-4, and both are about *who can answer an `ask` approval prompt* (the #659 / `AutoDenyAsk` question), not about session isolation. No claim in this spec assumes a delegated agent has its own browser or its own logins. The supersession does, however, **sharpen §2.3's accepted risk**: a snapshot taken by any agent on the workspace sees the same signed-in state as every other, so "a snapshot of a signed-in page can carry a card number into the transcript" now applies to delegated work too, with no attended-operator discriminator to soften it.
- **Human live view.** `browser_handle_dialog` is agent-facing. A human driving the wheel on a tab with an open dialog has no button. Today that tab is wedged for the human too; after this work it stays wedged for the human and becomes recoverable for the agent — **and, per FR-035, the agent's recovery is no longer blocked by the human's presence**, which is the compounding case revision 1 created. **Recorded as an accepted gap, not solved** — a live-panel dialog affordance would need a new WS frame and therefore a `contracts/` change, out of scope here. §13 holdout 8 measures how bad it is.

### 6.1 Operability — what an on-call operator sees (grill M12)

Revision 1 had no logging, audit or metrics requirement anywhere; §6's "Failure → the tool errors" was the whole story. Three additions:

| Signal | Emitted by | What it answers |
|---|---|---|
| `browser_upload_file` audit event — `{agent_id, resolved_path, page_origin, fs_op, fs_op_reason, policy_decision, outcome}` (FR-031) | `browser_upload_file`, on **every** invocation including denials | *Which local file went to which remote origin, on whose authority.* The one tool that carries data outward, and the entire reason the operator ruled `ask`. `FSOpSend`'s doc records that `FSOp` exists "purely for audit and any future ask-flow" — this is that audit, finally emitted. **`fs_op` and `fs_op_reason` are new in revision 3 (round-2 O1):** classifying an upload as `FSOpWrite` is the right *path rule* (§12 A-9) but has a side effect worth one field — the audit trail records a **write** to a path the gateway process never writes to. `fs_op: "write"` plus `fs_op_reason: "path handed to Chrome, which performs the read outside gateway confinement"` keeps whoever reads this log in six months from concluding the gateway wrote the file. |
| `browser_snapshot` audit event — `{agent_id, page_origin, node_count, output_bytes, value_nodes_emitted, truncated}` (FR-028) | `browser_snapshot`, every invocation | *What was captured, and did it include field values.* **Metadata only — never the values.** An audit log that copies the card number has moved the disclosure, not recorded it. |
| **Where both are read (round-2 M6):** Settings → Security → **Audit Log** (`src/components/settings/AuditLogViewer.tsx` ← `GET /api/v1/audit-log` ← `$OMNIPUS_HOME/system/audit.jsonl`) | — | The named operator surface revision 2 owed and did not give. Both events render there with **no SPA change** — the viewer defaults to "All events" and styles an unknown name through `BADGE_FALLBACK`. **Two limits, stated:** its event-type dropdown is a hardcoded 10-name list, so the new events are visible but **not filterable**; and the event names must be **underscore-form** (`^[a-z_]+$`, per the `AuditEntry` contract) or the whole viewer throws. Adding the two names to `EVENT_TYPE_OPTIONS` would be a genuine improvement and is **deliberately out of scope** — it is an SPA change this spec otherwise does not make (§6 "SPA: no change"), and the events are readable without it. |
| `omnipus_browser_gate_failure_total{condition="visible|stable|enabled|hit-testable"}` and `omnipus_browser_gate_indeterminate_total` (FR-032) | `waitActionable`, on every failure and every indeterminate hit test | *Are my target pages systematically overlay-blocked / animation-blocked?* Without this, the "which condition failed" data exists only inside individual transcripts. It is also the data FR-034's revert switch is time-boxed against: the switch is deleted in the change that acts on these counters. |

**Runbook, in one paragraph.** A rising `gate_failure_total{condition="stable"}` or `{condition="hit-testable"}` means the gate is rejecting clicks that used to land — set `tools.browser.actionability_gate: visible_only` (live, no restart) and file against the counter. A rising `gate_indeterminate_total` means targets are inside closed shadow roots or cross-origin frames; those clicks still fire, so this is a coverage signal, not an outage. `browser_upload_file` events with `outcome: denied` and a path outside the work dir mean an agent is trying to attach files it cannot reach — a prompt problem, not a policy problem. **FR-007's round-trip budget has no runtime instrumentation and this is stated rather than implied**: a production latency regression in the gate is detectable only through the gate-failure counters and end-to-end turn latency, not directly. That is a deliberate v1 limit, recorded. **And a second, wider blind spot is recorded rather than left unmentioned (round-2 M10):** `resolveTarget` — which §2.4 rates HIGH with a larger dependent set than the gate — has no budget, no counter and no assertion of its own. The AX branch's `QueryAXTree` + `setAttributeValue` + cleanup, and the snapshot's `GetFullAXTree`, are all unmeasured in v1. §12 B-5 states why and what would close it.

---

## 7. User stories & acceptance criteria

**US-1 (P0) Target by role and accessible name.** As an agent driving a page whose CSS classes are generated, I want to name "the Submit button" the way I already reason about it, so a class-name change does not break me.
- *Why P0:* the ADR's headline D2.1 benefit; the one that survives a redesign.
- *Independent test:* a fixture page with hashed class names and a `<button>Submit</button>`; `browser_click{role:"button", name:"Submit"}` clicks it.
- AC1: **Given** a page whose only stable property is `role=button, name="Submit"`, **When** `browser_click{role, name}` runs, **Then** the button is clicked.
- AC2: **Given** the same page, **When** `browser_get_text{role, name}` and `browser_wait{role, name}` run, **Then** both resolve the same element — the seam is inherited, not per-tool.
- AC3: **Given** `browser_type` **or** `browser_press_key`, **When** `{text: "Submit"}` is passed as a locator, **Then** it is rejected by name.
- AC4: **Given** any tool, **When** both `selector` and `role` are populated, **Then** the call errors naming both fields — no precedence rule applies.

**US-2 (P0) Deterministic multi-match.** As an agent, when two things share a role and name I want to be told, not guessed for.
- AC1: **Given** three `role=button name="Delete"`, **When** `browser_click{role, name}` runs with no index, **Then** it errors naming the count `3` and the candidates.
- AC2: **Given** the same page, **When** `index: 1` is supplied, **Then** the second in document order is clicked, and the ordering is asserted directly (not inferred from the click landing).
- AC3: **Given** a hidden node also matching, **When** the query runs, **Then** the `Ignored == true` node is excluded.
- AC4: **Given** a role+name that matches **only** ignored nodes, **When** the query runs, **Then** the error names the count of ignored candidates, so "hidden" is distinguishable from "absent".

**US-3 (P0) Actionability names the cause.** As an agent, a failed click must tell me what to do next.
- *Why P0:* ADR criterion 7 — one of the two failure modes the ADR calls silent rather than wrong.
- AC1: **Given** a `<button disabled>`, **When** `browser_click` runs, **Then** it fails with `enabled` in the message, not "timeout".
- AC2: **Given** a button under a full-screen overlay, **When** `browser_click` runs, **Then** it fails with `hit-testable` **and** names the occluding element.
- AC3: **Given** a button mid-CSS-transition that settles, **When** `browser_click` runs, **Then** it waits for two identical boxes one animation frame apart and clicks — no error.
- AC4: **Given** a button that never stops moving, **When** `browser_click` runs, **Then** it fails with `stable`.
- AC5: **Given** any gate failure, **When** the error is produced, **Then** the named condition is one of exactly four literals, asserted by a test that enumerates the `ActionCondition` constants and fails if a fifth is added without updating it.
- AC6: **Given** a target inside a closed shadow root or a cross-origin iframe, **When** `browser_click` runs, **Then** it **succeeds** with `hit_test: "indeterminate"` on the result and the indeterminate counter increments — it does not hard-fail a click that would have worked.
- AC7 (**FR-037**): **Given** an element that passes the gate and is then hidden before `chromedp.Click` dispatches, **When** the click fails, **Then** the error names `visible` — it is **not** a bare `context deadline exceeded`, which is what the unmodified `chromedp` path produces.

**US-4 (P0) The gate is not a tax.** As an operator on a loaded box, I want the new safety to be cheap and to have a way back.
- AC1: **Given** an already-actionable button, **When** `browser_click` runs, **Then** **`waitActionable`** issues **exactly two** `Runtime.evaluate` round trips and **zero** `DOM.getBoxModel` / `DOM.getNodeForLocation` — asserted by a counting seam **scoped to the gate**, not to the tool, and not by a clock. *(The tool as a whole issues more, from `chromedp.Click`'s own unchanged `NodeVisible` + `MouseClickNode` path; revision 2 asserted the count over the tool, which was falsifiable and false — round-2 C2.)*
- AC2: **Given** the same button, **When** the wall-clock cost is measured per SC-004's harness, **Then** the paired per-call delta is **recorded in the PR** with its machine and method. This is a recorded measurement, **not** a pass/fail gate — see SC-004. *(Deliberately has no BDD scenario: it is a hand-run measurement, §10 order 29.)*

**US-17 (P0) The gate has a revert.** As an operator hitting a false gate failure on a production site, I want the old behaviour back without a downgrade.
- *Why P0:* the change is unconditional on every click and keystroke on every page (§2.4 rates it CRITICAL), and ADR-071 set the in-repo precedent with `previewAllLazy`.
- AC1: **Given** `tools.browser.actionability_gate: "visible_only"` set at runtime, **When** a click runs against a target that fails `stable`, **Then** it succeeds — the pre-change `WaitVisible` behaviour, applied live with no restart.
- AC2: **Given** the default config, **When** the key is unset, **Then** the four-condition gate applies (`full`).
- AC3: **Given** the merged feature, **When** the config key's doc comment is read, **Then** it names the removal issue by number. *(Deliberately has no BDD scenario: it is a source-comment assertion carried by SC-009, and the removal issue's own acceptance criteria are where the removal is decided — see §12 A-13 for who owns pulling the trigger.)*

**US-5 (P0) Forms with dropdowns.** As an agent, I want to complete a form containing a `<select>` — impossible today.
- AC1: **Given** a `<select>` with options Alpha/Beta, **When** `browser_select_option{label:"Beta"}` runs, **Then** the value is Beta **and** a `change` event fired (asserted via a listener on the fixture page, not by reading `.value`).
- AC2: **Given** a multi-select and three labels of which two resolve, **When** the tool runs, **Then** it errors naming the unresolved label and **no** option is selected.
- AC3 (**`value`, round-2 unasked-question 4**): **Given** `<option value="b">Beta</option>`, **When** `browser_select_option{value:"b"}` runs, **Then** Beta is selected and `change` fires — the same outcome as `{label:"Beta"}`; and **When** both `value` and `label` are supplied, **Then** the call errors naming both fields.

**US-6 (P1) Discrete keys.** As an agent, I want Enter to submit, Tab to advance and Escape to dismiss.
- AC1: **Given** a focused input in a form, **When** `browser_press_key{key:"Enter"}` runs, **Then** the form submits.
- AC2: **Given** `key: "Ctrl+Banana"`, **When** the tool runs, **Then** it errors listing the accepted key names — it never types "Ctrl+Banana" as text.
- AC3: **Given** no focused element and no locator, **When** the tool runs, **Then** the key dispatches to the document and the result carries `focused_element: null`.
- AC4 (**the sanctioned bypass, round-2 M11**): **Given** no locator, **When** the tool runs, **Then** `waitActionable` is **not entered** — asserted at the seam, not by timing — while `controlledResult` and the D1 lease **are** still taken. This is the only path past the gate in the design and nothing asserted it before.

**US-7 (P1) Hover menus.** As an agent, I want to reach a menu that only opens on hover.
- AC1: **Given** a nav item revealing a submenu on `mouseover`, **When** `browser_hover` runs, **Then** the submenu is visible and no click occurred (asserted by a click listener that must not fire).

**US-8 (P1) Attachments.** As an agent, I want to attach a file to a file input — safely, accountably, and never by hanging.
- AC1: **Given** an `<input type=file>` and a file **in the turn's working directory or an explicit mount**, **When** `browser_upload_file` runs, **Then** the input reports one file with that name.
- AC2: **Given** a path outside `policy.AllowedRoots`, **When** the tool runs, **Then** `tools.ResolvePath(..., FSOpWrite)` denies it and the tool returns a permission-denied result naming the path — it does not hand the path to Chrome, and it performs **no** path check of its own.
- AC3 (**corrected in revision 3 — round-2 C1**): **Given** a default install, **When** `browser_upload_file`'s policy is resolved for **every agent that holds the browser surface** — Jim, Ray, Explorer, Researcher and Worker — **Then** it is `ask` for all five. None resolves `allow`; none resolves `deny`. **And Given** Mia and Ava, who hold **no** browser tool at all, **Then** it resolves `deny`, by the same `denyAllThenOverride` route that gives them the other five, with **no per-agent edit** — *not* `ask`. See §12 A-16 for why, the ADR erratum this raises, and the two-edit-site alternative if the operator wants literal `ask` there.
- AC4 (**the #659 gate — repackaged in revision 3, round-2 C3**): **Given** the current tree, **When** the tool registry is built, **Then** `browser_upload_file` is **not registered** — `registry.Get("browser_upload_file")` returns not-found — while `buildKnownBuiltinToolNames()` **does** contain the name (seeded for coverage). *(No part of this asserts the state of a GitHub issue; a Go unit test cannot observe one, and revision 2's "the test's own guard asserts #659 is still open" named no mechanism because there is none.)*
- AC4b (**the #659 oracle**): **Given** a delegated sub-turn with no attached approver, **When** it invokes **any** tool whose resolved policy is `ask`, **Then the turn terminates with a denial** under a bounded timeout — asserted on **turn completion**, not on policy resolution. *(Revision 1's oracle asserted "policy resolves to `ask`", which is true in the hung state — a green criterion over a hanging product. Revision 2 kept the right layer but bound it to `browser_upload_file`, which FR-029 forbids from existing in a registered build; a generic `ask` tool needs no forbidden build. This criterion is **carried by #659**, not by this feature — see FR-029.)*
- AC5: **Given** any invocation, allowed or denied, **When** it completes, **Then** a `browser_upload_file` audit event (underscore form — see FR-028) records agent, resolved path, page origin, `fs_op`, policy decision and outcome.

**US-9 (P0) A dialog does not wedge the tab.** As an operator, a page calling `alert()` must not end the session.
- *Why P0:* ADR criterion 12; the ADR's own "new worst case".
- AC1: **Given** a page whose button calls `alert('hi')`, **When** the agent clicks it and then calls `browser_handle_dialog{accept:true}`, **Then** the dialog is gone **and** a subsequent `browser_get_text{selector:"body"}` **returns within the normal timeout**.
- AC2: **Given** an open dialog and **no** `browser_handle_dialog` call, **When** any other browser tool is invoked and times out, **Then** the error names the pending dialog and points at `browser_handle_dialog`.
- AC3: **Given** a dialog opened on **tab 2** (not tab 0), **When** AC1 is repeated, **Then** it holds identically. *This is the case a tab-0-only listener fails.*
- AC4: **Given** no dialog pending, **When** `browser_handle_dialog` runs, **Then** it returns a **non-error** `{"dialog": null}` — and a second call after a successful handle returns the same, never an error.
- AC5: **Given** a `prompt()`, **When** `{accept:true, prompt_text:"x"}` runs, **Then** the page receives `"x"`.
- AC6 (**FR-035, human**): **Given** a wedged tab **and a human holding the live view**, **When** the agent calls `browser_handle_dialog`, **Then** it runs — it does **not** return `{"deferred": true}` — and the tab answers CDP afterwards.
- AC7 (**FR-035, lease**): **Given** a `browser_click` still blocked on the wedged tab and holding the write lease, **When** `browser_handle_dialog` is called concurrently, **Then** it runs, the tab unwedges, and the blocked click then returns. **This is the acceptance test for "recovery from the wedged state"** — revision 1's test proved only that a tab answers CDP after a *successfully handled* dialog, i.e. the path where the mechanism already worked.
- AC8 (**FR-014, re-arm**): **Given** a tab whose chromedp ctx is destroyed and recreated by `mgr.Session()`, **When** a dialog then opens on it, **Then** it is recorded — the listener was re-armed, and exactly once (no duplicate handler).
- AC9 (**FR-013, suspected**): **Given** a tab wedged by a dialog that opened before any listener existed, **When** a browser tool times out on it, **Then** the error says a dialog is **suspected**, names `browser_handle_dialog`, and is textually distinct from AC2's confirmed message.

**US-10 (P1) Read a page as structure.** As an agent, I want to know what is on a page and what I can do next, without vision and without a pre-known selector.
- AC1: **Given** a form with three labelled fields and a submit button, **When** `browser_snapshot` runs, **Then** all four appear with role and accessible name, and a handle from the output resolves in the very next action call.
- AC2: **Given** a page whose AX tree exceeds the cap, **When** the snapshot runs, **Then** output is **≤ 64,000 bytes**, truncation lands on a **node boundary**, the output is valid UTF-8, the marker names the byte cap and the omitted-node count, and the retained portion is the top of the tree.
- AC3 (**the ruling**): **Given** a form whose password field contains `hunter2secret` and whose text field contains `4111111111111111`, **When** `browser_snapshot` runs, **Then** **both values are present in the output**. No `include_values` parameter exists on the tool schema.
- AC4 (**FR-027**): **Given** a value registered via `RegisterSensitiveValues` typed into a form field, **When** `browser_snapshot` runs, **Then** the output contains `[FILTERED]` and not the plaintext.
- AC5 (**FR-038**): **Given** a human holding the live view, **and** separately another tool holding the D1 write lease, **When** `browser_snapshot` runs, **Then** it returns the snapshot in both cases — it is never `{"deferred": true}`, because it is read-only and mutates nothing.

**US-18 (P1) An operator can see what the browser tools did.** As an operator, I need to know what was captured and what was sent.
- AC1: **Given** any `browser_snapshot`, **When** it completes, **Then** a `browser_snapshot` audit event records page origin, node count, output bytes, whether any value nodes were emitted, and whether the output was truncated — **and contains none of the captured values**.
- AC2: **Given** the shipped SPA, **When** `src/lib/toolVisibility.ts` is inspected, **Then** it contains no `browser` reference, so every browser tool call renders in the chat thread by default. Asserted as a regression test, not read as an observation.
- AC3: **Given** a gate failure, **When** it is returned, **Then** `omnipus_browser_gate_failure_total` increments with the failing condition as a label; an indeterminate hit test increments its own counter.

**US-11 (P1) The `file://` dead end names a route the agent can actually take.** As an agent told "no", I want to be told "instead" — by a tool I can call.
- AC1: **Given** `browser_navigate{url:"file:///tmp/x.html"}`, **When** it is rejected, **Then** the error contains the literals `serve_web` and `/preview/`.
- AC2: **Given** `browser_navigate{url:"javascript:alert(1)"}`, **When** it is rejected, **Then** the message is unchanged and does **not** mention `serve_web` — there is no supported route for it.
- AC3 (**FR-030, the one that matters**): **Given** the seeded config, **When** the resolved `serve_web` policy is read for **every agent that resolves `allow` for at least one `browser_*` tool**, **Then** it is `allow` for all of them. *Revision 1's AC1 asserted only the literal string, so it passed while Ray, Explorer and Researcher were pointed at a tool they resolve `deny` for.* **The predicate is `allow`, not "grants" (round-2 m2):** revision 2 said "grants **any** `browser_*` tool", and under a global `ask` on `browser_upload_file` it is genuinely unclear whether `ask` counts as granting — the same ambiguity that produced C1. Pinned to `allow`, the set is **Jim, Ray, Explorer, Researcher, Worker** regardless of how the upload posture is decided, and `_BrowsingAgentsCanCallServeWeb` computes it rather than hard-coding it.

**US-12 (P0) Boot survives the new tools.** As an operator, a fresh install must start.
- *Why P0:* Hard Constraint #6.
- AC1: **Given** a fresh install, **When** the gateway boots, **Then** `ValidateToolPolicyCoverage` returns zero gaps and boot completes — **including for `browser_upload_file`, whose name is seeded even while its registration is held.**
- AC2: **Given** the seeded config, **When** Jim's, Ray's, Explorer's and Researcher's policies are read, **Then** the five action/read tools are `allow` and `browser_upload_file` is `ask`.
- AC3: **Given** the seeded config, **When** Mia's and Ava's policies are read, **Then** **all six are `deny`** — with **no** per-agent edit, via `denyAllThenOverride`, and `browser_upload_file` included. **This is now consistent with US-8/AC3 rather than contradicting it (round-2 C1).** Revision 2 asserted `deny`×6 here and `ask`-for-Mia-and-Ava in US-8/AC3, and neither state was producible from §11's edit sites. The mechanism, re-derived: `denyAllThenOverride` (`pkg/coreagent/core.go`) writes an **explicit agent-level `deny`** for every `allStaticToolNames` member the override map does not name, and `pkg/tools/compositor.go::resolveEffectivePolicyWith` merges global × agent as **deny > ask > allow**. Agent `deny` + global `ask` = **`deny`**. `ValidateToolPolicyCoverage`'s OR-semantics is a *coverage* rule — "is there an entry on either side" — and never a resolution rule; conflating the two is what produced the contradiction.
- AC4: **Given** an override map naming a tool absent from `allStaticToolNames`, **When** it is seeded, **Then** `validateOverrideKeys` panics — the guard that makes the ordering in Stream E non-optional.

**US-13 (P1) Tier membership is a decision, not drift.** As a maintainer, a new tool must not slip into a tier silently.
- AC1: **Given** the six names, **When** `TestVisibility_TierArithmetic` runs, **Then** its literals reflect the new partition and the union count is asserted, not derived.
- AC2: **Given** each of the six, **When** `ToolManifestVisibility` is called, **Then** it returns the tier §12 A-3 records.
- AC3 (**FR-036, the one that can actually fail**): **Given** a name added to `tier3SearchOnlyToolNames` that is **not** a registered builtin (e.g. the typo `browser_selct_option`), **When** the new partition test runs, **Then** it **fails**. *Verified: the existing test cannot detect this — `ToolManifestTier(n) == ManifestLazy` is trivially true for any string outside the two enumerated maps, because Tier 3 is the residual. The fixture is hand-maintained and nothing consults the registry, so a genuinely new Tier-3 tool nobody adds produces no failure at all.*

**US-14 — WITHDRAWN (relocated to the D1 spec).** "Two writers, one context" and its four acceptance criteria moved to `docs/internal/specs/browser-workspace-ownership-spec.md` §14 and its FR-019…FR-024, by operator ruling 2026-08-31. Retained as a numbered tombstone rather than renumbered, so every existing cross-reference in this document and in the round-2 grill still resolves. **D2 asserts nothing about the lease.** Its only remaining lease-adjacent obligation is FR-035's exemption, which lives under US-9.

**US-16 (P1) A host with no Chromium says so.** As an operator on linux/arm64, I need the failure to name the cause.
- *Why:* ADR D2.7 records this as a **shipping** configuration where every `browser_*` tool is registered and guaranteed to fail (#665). Six new tools multiply the surface on which that message appears.
- AC1: **Given** a host with no resolvable Chromium, **When** any of the new tools is invoked, **Then** the error names the missing browser and points at the install path — it does not read as a defect in the tool.

**US-15 (P1) Existing browser behaviour is preserved.** As a maintainer, the added waits must not break what works.
- AC1: **Given** the named regression suite (§10), **When** it runs with `OMNIPUS_BROWSER_E2E=1`, **Then** every test passes and the CI pass floor is raised to a **stated number with stated headroom** (SC-006), never to the exact measured count.

---

## 8. BDD scenarios

**S-01: role + name resolves on a hashed-class page (Happy) — US-1/AC1, FR-001**
- **Given** a fixture page whose submit control is `<button class="_a7f3x">Submit</button>`
- **When** the agent calls `browser_click{role:"button", name:"Submit"}`
- **Then** the click lands on that button and the result echoes the role/name locator, not the internal `data-omnipus-tsel` marker

**S-02: every action tool inherits the seam (Happy) — US-1/AC2, FR-002**
- **Given** the same page
- **When** `browser_get_text`, `browser_wait`, `browser_hover` and `browser_select_option` are each called with `{role, name}`
- **Then** all four resolve the same element, and none carries its own resolution branch

**S-03: two locator kinds is an error, not a precedence rule (Error) — US-1/AC4, FR-004**
- **Given** `browser_click{selector:"#save", role:"button", name:"Save"}`
- **When** the tool runs
- **Then** it errors naming **both** `selector` and `role`, and no click occurred

**S-04: `text` is rejected as a locator by both value-carrying tools (Error) — US-1/AC3, FR-004**
- **Given** `browser_type{text:"Submit"}` and `browser_press_key{key:"Enter", text:"Submit"}`
- **When** each runs
- **Then** each errors naming `text` as an invalid locator for that tool, and neither types nor dispatches anything

**S-05: ambiguous role+name errors rather than guessing (Edge) — US-2/AC1, FR-003**
- **Given** three `<button>Delete</button>` elements
- **When** `browser_click{role:"button", name:"Delete"}` runs with no `index`
- **Then** it errors naming `3` candidates, and no click occurred (asserted by a click counter on the page)

**S-06: index selects deterministically in document order (Edge) — US-2/AC2, FR-003**
- **Given** the same three buttons, each with a distinct `data-testid`
- **When** `index: 1` is supplied
- **Then** the button with the **second** `data-testid` in source order is clicked

**S-07: AX-ignored nodes are excluded (Edge) — US-2/AC3, FR-003**
- **Given** a `<button aria-hidden="true">Delete</button>` plus one visible `<button>Delete</button>`
- **When** `browser_click{role:"button", name:"Delete"}` runs with no index
- **Then** it succeeds on the visible one — the hidden node neither wins nor makes the match ambiguous

**S-08: an all-ignored match says how many were ignored (Error) — US-2/AC4, FR-003**
- **Given** a page where the only three `role=button name="Next"` nodes are all marked ignored by Chrome
- **When** `browser_click{role:"button", name:"Next"}` runs
- **Then** the error names the count `3` of ignored candidates, so the agent can tell "hidden" from "absent"

**S-09: disabled element names `enabled` (Error) — US-3/AC1, FR-006**
- **Given** `<button disabled>Save</button>`
- **When** `browser_click` runs
- **Then** the error contains the literal `enabled` and does not consist solely of "timeout" or "context deadline exceeded"

**S-10: covered element names `hit-testable` and the occluder (Error) — US-3/AC2, FR-006**
- **Given** `<button id="save">Save</button>` beneath `<div id="overlay">` at `z-index: 9999` covering the viewport
- **When** `browser_click` runs
- **Then** the error contains `hit-testable` **and** the string `overlay`

**S-11: a settling element is waited for, not failed (Happy) — US-3/AC3, FR-005**
- **Given** a button with a 300 ms CSS transform that settles
- **When** `browser_click` runs
- **Then** it succeeds, and the stability check observed two box reads one animation frame apart that were identical

**S-12: a perpetually moving element names `stable` (Error) — US-3/AC4, FR-006**
- **Given** a button under an infinite `@keyframes` translate
- **When** `browser_click` runs
- **Then** the error contains `stable`

**S-13: the failure set is closed at exactly four (Error) — US-3/AC5, FR-006**
- **Given** the `ActionCondition` constants declared in `actionable.go`
- **When** the enumeration test runs
- **Then** exactly four constants exist with the literal values `visible`, `stable`, `enabled`, `hit-testable`, and the test fails if a fifth is declared without updating it

**S-14: an un-hit-testable target passes visibly, it does not fail (Edge) — US-3/AC6, FR-005**
- **Given** a button inside a **closed** shadow root, and a second inside a cross-origin iframe, each addressed by a CSS locator
- **When** `browser_click` runs on each
- **Then** each **succeeds**, the result carries `hit_test: "indeterminate"`, and `omnipus_browser_gate_indeterminate_total` incremented twice

**S-15: the GATE costs exactly two round trips (Performance) — US-4/AC1, FR-007**
- **Given** a static page with an immediately-actionable button, and a CDP call-counting seam **scoped to `waitActionable`** — armed on entry, disarmed on return, so it observes the gate's traffic and nothing else
- **When** `browser_click` runs once
- **Then** within that scope the counter reads exactly `2` `Runtime.evaluate` calls — RT1 (box + enabled + hit) and RT2 (the post-`requestAnimationFrame` box read) — and **zero** `DOM.getBoxModel` and **zero** `DOM.getNodeForLocation`
- **And** the test asserts nothing about the CDP traffic issued **after** the gate returns, which `chromedp.Click` owns and this change does not touch. *(Revision 2 scoped this to the whole `browser_click` and asserted zero `DOM.getBoxModel`; verified false — `Click` appends `NodeVisible`, which issues exactly that. Round-2 C2.)*

**S-16: the revert switch restores the old behaviour live (Operability) — US-17/AC1, FR-034**
- **Given** a button under an infinite `@keyframes` translate, which fails `stable` under the default config
- **When** `tools.browser.actionability_gate` is set to `visible_only` and the same click is repeated with no restart
- **Then** the click succeeds, and setting it back to `full` makes it fail with `stable` again

**S-17: `<select>` set by label fires `change` (Happy) — US-5/AC1, FR-009**
- **Given** `<select>` with Alpha/Beta and a `change` listener that sets `window.__changed = true`
- **When** `browser_select_option{label:"Beta"}` runs
- **Then** the value is Beta **and** `window.__changed === true`

**S-18: a partial multi-select applies nothing (Error) — US-5/AC2, FR-009**
- **Given** a `<select multiple>` with options Alpha/Beta and a call naming `["Alpha","Beta","Gamma"]`
- **When** the tool runs
- **Then** it errors naming `Gamma` as unresolved, and `select.selectedOptions.length === 0`

**S-19: Enter submits the form (Happy) — US-6/AC1, FR-010**
- **Given** a form with a focused text input and a `submit` listener setting `window.__submitted = true`
- **When** `browser_press_key{key:"Enter"}` runs
- **Then** `window.__submitted === true`

**S-20: unknown key name is refused, not typed (Error) — US-6/AC2, FR-010**
- **Given** a focused text input
- **When** `browser_press_key{key:"Ctrl+Banana"}` runs
- **Then** the tool errors listing the accepted key names, and the input's value is unchanged

**S-21: a key with nothing focused says so (Edge) — US-6/AC3, FR-010**
- **Given** a page with no focused element and no locator supplied
- **When** `browser_press_key{key:"Enter"}` runs
- **Then** it succeeds and the result carries `focused_element: null`

**S-22: hover opens a menu without clicking (Happy) — US-7/AC1, FR-011**
- **Given** a nav item revealing a submenu on `mouseover`, with a click listener setting `window.__clicked = true`
- **When** `browser_hover` runs
- **Then** the submenu is visible and `window.__clicked` is `undefined`

**S-23: an upload from the work directory attaches (Happy) — US-8/AC1, FR-012**
- **Given** `<input type=file>` and a file inside the turn's working directory
- **When** `browser_upload_file` runs
- **Then** `input.files.length === 1` and `input.files[0].name` is the fixture's name

**S-24: upload outside the allowed roots is denied at the chokepoint (Security) — US-8/AC2, FR-012**
- **Given** `<input type=file>` and a path outside `policy.AllowedRoots`
- **When** `browser_upload_file` runs
- **Then** `tools.ResolvePath(..., FSOpWrite)` returns the denial, the tool returns a permission-denied result naming the path, **and** `chromedp.SetUploadFiles` was never invoked (asserted at the seam, not by the absence of a file) — **and** the tool performed no path comparison of its own (asserted by the absence of any `AllowedRoots` reference in `tools_interact.go`)

**S-25: every browser-capable agent resolves `ask` on upload; the other two resolve `deny` (Boot) — US-8/AC3, FR-021**
- **Given** the seeded config
- **When** `browser_upload_file`'s resolved policy is read for **Jim, Ray, Explorer, Researcher and Worker** — the agents that hold the browser surface
- **Then** every one is `ask` — none `deny`, none `allow`
- **And When** it is read for **Mia and Ava**, who hold no browser tool
- **Then** both are `deny`, produced by `denyAllThenOverride`'s agent-level deny merging with the global `ask` under `resolveEffectivePolicyWith`'s deny-wins rule — **with no per-agent literal for either agent**, which the test asserts by checking the seed map was not hand-edited for them
- *(Revision 2's version asserted `ask` for all seven and directly contradicted S-48. Neither state was producible from §11's edit sites — round-2 C1.)*

**S-26: the tool is not registered (Gate) — US-8/AC4, FR-029**
- **Given** a build of the current tree
- **When** the tool registry is constructed
- **Then** `browser_upload_file` is absent from the registry **and** present in `buildKnownBuiltinToolNames()`
- **And** the test asserts nothing about issue #659's state — a Go unit test cannot observe a GitHub issue, and revision 2's "the test's own guard asserts #659 is still open" named no mechanism because none exists. `#659` appears in the test's name and its doc comment, which is where the human context belongs. The test is **deleted** in the PR that registers the tool; that PR cites #659 as closed. *(Round-2 C3.)*

**S-27: a delegated sub-turn with no approver is denied, not hung (Gate — the #659 oracle) — US-8/AC4b, FR-029**
- **Given** a delegated sub-turn with no attached approver, and **any** tool whose resolved policy for that agent is `ask` — deliberately **not** `browser_upload_file`, whose registration FR-029 holds, so this scenario needs no build FR-029 forbids
- **When** the sub-turn invokes that tool
- **Then** the **turn completes** with a denial recorded — asserted on turn completion under a bounded test timeout, never on policy resolution alone
- **And** while #659 is open this scenario does not hold, so its test lands `t.Skip`-ped with `#659` in the skip string; the PR that closes #659 removes the skip. It is an **acceptance criterion on #659**, not a permanently-red gate in this repo — a gate that can never go green blocks forever, and Hard Constraint #7 forbids shipping a red branch. *(Round-2 C3.)*

**S-28: an upload is audited (Operability) — US-8/AC5, FR-031**
- **Given** an upload that is allowed and one that is denied
- **When** each completes
- **Then** two `browser_upload_file` audit records exist, each carrying agent id, resolved path, page origin, policy decision and outcome

**S-29: the tab still answers CDP after a dialog (Error → recovery) — US-9/AC1, FR-013**
- **Given** a page whose button calls `alert('hi')`
- **When** the agent clicks it, then calls `browser_handle_dialog{accept:true}`, then calls `browser_get_text{selector:"body"}`
- **Then** `browser_get_text` **returns page text within the normal timeout**

**S-30: recovery works while a human holds the live view (Error → recovery) — US-9/AC6, FR-035 — THE acceptance test, part 1**
- **Given** a tab wedged by an open `alert()` **and** `mgr.Live().IsControlled(defaultSessionID)` returning true — **the key that exists today**, not a per-browsing-context key. Verified: `tools.go::controlledResult` calls `IsControlled(defaultSessionID)`, a hardcoded constant, and D1's own **FR-002c** records that this silently stops working once the live registry is re-keyed. Revision 2 phrased this **Given** as "true for that browsing context", implying a per-context capability that does not exist yet and making the test unwritable as specified (round-2 m4). When D1 lands FR-002c, this **Given** follows D1's key — it is not D2's to choose
- **When** the agent calls `browser_handle_dialog{accept:false}`
- **Then** the result is **not** `{"deferred": true}`, the call reaches `page.HandleJavaScriptDialog`, and a subsequent `browser_get_text` returns within the normal timeout

**S-31: recovery works while another tool holds the write lease (Error → recovery) — US-9/AC7, FR-035 — THE acceptance test, part 2**
- **Given** a `browser_click` that triggered an `alert()` and is still blocked on CDP, holding the write lease for that browsing context
- **When** `browser_handle_dialog{accept:false}` is called concurrently from another goroutine
- **Then** it is **not** deferred, the tab unwedges, and the blocked `browser_click` subsequently returns — **the assertion is recovery from the wedged state, not responsiveness after a dialog that was already handled successfully**
- **Sequencing, stated because revision 2 stated none (round-2 M7):** this scenario's **Given** requires D1 §14's `leaseWrite`, which D2 does not write (§10 order 24). **D1's `leaseWrite` must land before this test can be written.** It is split so the wait costs nothing:
  - **S-31a (writable today, D2 code only)** — the same scenario with a stand-in that holds the tab's CDP with a blocked call and **no lease at all**. Proves the **wedge-recovery** half: a blocked holder does not prevent the dialog tool reaching `page.HandleJavaScriptDialog`, and the blocked call then returns.
  - **S-31b (waits for D1)** — the lease-holding form above. Proves the **lease-exemption** half. **Only S-31b tests FR-035**; S-31a's green must not be read as evidence for it.

**S-32: a dialog on a non-zero tab is still seen (Edge) — US-9/AC3, FR-014**
- **Given** three open tabs, with the `alert()` triggered on tab index 2
- **When** S-29 is repeated against tab 2
- **Then** it holds identically — proving the listener is per-tab, not tab-0-only

**S-33: the listener is re-armed exactly once after a ctx recreation (Edge) — US-9/AC8, FR-014**
- **Given** a tab whose chromedp ctx is destroyed and then recreated through `mgr.Session()`'s crash-recovery path
- **When** an `alert()` is then raised on that tab
- **Then** the pending-dialog map records **exactly one** entry (not zero — the listener was re-armed; not two — the idempotence key prevented a duplicate handler)

**S-34: an unhandled dialog is reported, not silently timed out (Error) — US-9/AC2, FR-013**
- **Given** an open, unhandled `confirm()` on the active tab, recorded by the listener
- **When** `browser_click` is called on any element
- **Then** the error names the pending dialog and contains the literal `browser_handle_dialog`

**S-35: a pre-listener dialog produces a SUSPECTED message, not a bare timeout (Error) — US-9/AC9, FR-013**
- **Given** a tab **adopted after** a dialog was already open, so the pending-dialog map is empty **and the tab has no completed command of its own** — the actual motivating case from Stream C gap 3, which revision 2's "whose last completed command was a click" **Given** specified away (round-2 M8)
- **When** `browser_get_text` times out on it
- **Then** the error contains the word `may` (suspected, not asserted), names `browser_handle_dialog`, and is textually distinct from S-34's message
- **And Given** a second tab whose `lastActivation` *does* record a click, **Then** the message additionally names the activation (*"after a click"*) — the field sharpens the wording and **never gates** whether the message fires. Both rows in the dataset

**S-36: no dialog pending returns null, twice (Edge) — US-9/AC4, FR-013**
- **Given** a tab with no dialog, and separately a tab whose dialog was just handled
- **When** `browser_handle_dialog` runs on each, and then a second time on each
- **Then** all four calls return non-error `{"dialog": null}` and no `HandleJavaScriptDialog` CDP call was issued against a closed dialog

**S-37: prompt text is delivered (Happy) — US-9/AC5, FR-013**
- **Given** a page calling `prompt('name?')` and writing the answer to `window.__answer`
- **When** `browser_handle_dialog{accept:true, prompt_text:"x"}` runs
- **Then** `window.__answer === "x"`

**S-38: snapshot handles round-trip into an action (Happy) — US-10/AC1, FR-015/FR-016**
- **Given** a form with three labelled inputs and a submit button
- **When** `browser_snapshot` runs and its handle for "Email" is passed to `browser_type`
- **Then** the text lands in the email field

**S-39: snapshot obeys the byte cap on a node boundary (Edge) — US-10/AC2, FR-017**
- **Given** a page whose AX tree renders beyond 64,000 bytes, including nodes with non-ASCII accessible names
- **When** `browser_snapshot` runs
- **Then** the output is ≤ 64,000 bytes, is valid UTF-8 (no split rune), ends at a complete node, carries a marker naming both the byte cap and the omitted-node count, and retains the top of the tree

**S-40: the snapshot returns field values by default (Disclosure — the ruling) — US-10/AC3, FR-018**
- **Given** a signed-in fixture page with `<input type=password value="hunter2secret">` and `<input value="4111111111111111">`
- **When** `browser_snapshot` runs with no extra parameters
- **Then** the output contains **both** `hunter2secret` and `4111111111111111`, and the tool's JSON schema contains no `include_values` property

**S-41: a registered credential is filtered from the snapshot (Security) — US-10/AC4, FR-027**
- **Given** a plaintext registered via `RegisterSensitiveValues` and typed into a form field on the fixture page
- **When** `browser_snapshot` runs
- **Then** the output contains `[FILTERED]` and does **not** contain the plaintext — while S-40's unregistered values are still present, proving the replacer is defence in depth and not the control that protects form values

**S-42: the snapshot is audited without copying what it captured, and the record is reachable (Operability) — US-18/AC1, FR-028**
- **Given** the S-40 fixture
- **When** `browser_snapshot` runs
- **Then** a **`browser_snapshot`** audit record exists carrying page origin, node count, output bytes, `value_nodes_emitted: true` and `truncated: false` — **and containing neither `hunter2secret` nor `4111111111111111`**
- **And** its `event` name matches `^[a-z_]+$`, so `GET /api/v1/audit-log` still validates against `AuditLogResponseSchema` and the Settings → Security → Audit Log viewer still renders. Asserted directly against the contract's regex, because a dotted name would throw `ApiSchemaError` over the **whole** entries array and blank the operator's only durable view — the surface FR-028 rests on (round-2 M6)

**S-43: browser tool calls stay visible in the chat thread (Operability) — US-18/AC2, FR-028**
- **Given** the shipped `src/lib/toolVisibility.ts`
- **When** the regression assertion runs
- **Then** `shouldRenderToolCall(name, …, verboseChatEnabled=false)` returns `true` for **all six** new names — `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, `browser_handle_dialog`, `browser_snapshot`
- **And** the substring check (`the file contains no "browser"`) is retained only as a **secondary** assertion. *Asserting the absence of a substring goes green if hiding is later introduced through a different mechanism — a new predicate, a name list imported from elsewhere, a category rule. The behavioural assertion is the oracle; the substring is a hint (round-2 M6). This is also the assertion §11(b) reason (iii) leans on, so that argument now rests on a guard rather than on an unpinned property (round-2 m3).*

**S-44: gate failures are counted per condition (Operability) — US-18/AC3, FR-032**
- **Given** the disabled-button and overlay fixtures
- **When** each click fails
- **Then** `omnipus_browser_gate_failure_total{condition="enabled"}` and `{condition="hit-testable"}` each incremented by one

**S-45: `file://` names the route; `javascript:` does not (Error) — US-11/AC1-AC2, FR-019**
- **Given** `browser_navigate{url:"file:///tmp/x.html"}` and `browser_navigate{url:"javascript:alert(1)"}`
- **When** both are rejected
- **Then** the first contains `serve_web`, `/preview/` and the `gateway.preview_enabled` caveat; the second contains none of them and matches the pre-change message

**S-46: every browsing agent can call the tool the error names (Policy) — US-11/AC3, FR-030**
- **Given** the seeded config
- **When** the set of agents that resolve **`allow`** for at least one `browser_*` tool is computed — `allow`, not "grants", so `ask` never changes the set (round-2 m2) — and each one's resolved `serve_web` policy is read
- **Then** every one is `allow`, and the computed set is `{Jim, Ray, Explorer, Researcher, Worker}`. *This scenario fails on the current tree for Ray, Explorer and Researcher, and is the reason FR-030 exists.*

**S-47: fresh install boots with the new tools (Boot) — US-12/AC1, FR-021**
- **Given** a fresh `$OMNIPUS_HOME` and a build with the tools registered
- **When** the gateway boots
- **Then** `ValidateToolPolicyCoverage(cfg, buildKnownBuiltinToolNames())` returns zero gaps and no abort is logged — including for the registration-held `browser_upload_file`

**S-48: seeded posture matches §11's table exactly (Boot) — US-12/AC2-AC3, FR-021**
- **Given** the seeded config
- **When** each agent's resolved policy for the six is read
- **Then** Jim = Ray = Explorer = Researcher = `allow`×5 + `ask` for `browser_upload_file`; Mia = Ava = `deny`×6 — **`browser_upload_file` included** — with their values coming from `denyAllThenOverride`'s default merged against the global `ask` under deny-wins, and **no per-agent literal**; Worker inherits the global (`allow`×5 + `ask`) through `tightenGlobalCeiling`'s sparse map
- *(S-25 and this scenario are now consistent. In revision 2 they asserted flatly contradictory Mia/Ava states and `TestCoreAgentSeed_UploadIsAskForEveryAgent` and `TestCoreAgentSeed_BrowserD2Posture` could not both pass — round-2 C1.)*

**S-49: an unknown override key panics (Boot guard) — US-12/AC4, FR-022**
- **Given** an override map naming `browser_selct_option` (typo)
- **When** `coreAgentSeed` runs
- **Then** `validateOverrideKeys` panics naming the unknown tool

**S-50: tier arithmetic is updated deliberately (Build) — US-13/AC1-AC2, FR-020**
- **Given** the six new names
- **When** `TestVisibility_TierArithmetic` runs
- **Then** the four set sizes and the union count match §12 A-3's chosen option, asserted as literals

**S-51: the tier partition is checked against the real registry (Build) — US-13/AC3, FR-036**
- **Given** `tier3SearchOnlyToolNames` with `browser_select_option` replaced by the typo `browser_selct_option`
- **When** the new partition test runs
- **Then** it **fails**, naming the typo as a Tier-3 fixture entry that is not a registered builtin — and, run against the correct fixture, the four-set union equals `buildKnownBuiltinToolNames()` exactly, in both directions

**S-52, S-53, S-54 — WITHDRAWN (relocated to the D1 spec).** The three lease scenarios (second writer defers; abandoned lease force-released; reaper skips a leased context) are the D1 spec's to specify and test, per its §14 annex. Numbers retained as tombstones so S-55…S-57 keep their identifiers and every cross-reference resolves.

**S-55: a host with no Chromium names the missing browser (Error) — US-16/AC1, FR-033**
- **Given** a resolver configured to find no Chromium (the linux/arm64 shipping state, #665)
- **When** each of the new tools is invoked
- **Then** every error names the missing browser and the install path, and none reads as a defect in the tool

**S-56: chromedp still enables the Page domain per target (Dependency pin) — FR-013/FR-014**
- **Given** a freshly created tab with no explicit `page.Enable` anywhere in our code
- **When** a `ListenTarget` callback is installed and the page calls `alert()`
- **Then** the callback observes `page.EventJavascriptDialogOpening` — pinning `chromedp.Context.attachTarget`'s bring-up list against a dependency bump

**S-57: existing browser behaviour is preserved (Regression) — US-15, FR-008**
- **Given** the named regression suite in §10
- **When** it runs with `OMNIPUS_BROWSER_E2E=1`
- **Then** all listed tests pass and the CI floor has been raised to SC-006's stated number

**S-58: a post-gate visibility loss is named, not timed out (Error) — US-3/AC7, FR-037** *(new in revision 3, round-2 M5)*
- **Given** a button that is actionable when `waitActionable` runs, and a fixture that hides it (`display:none`) on the very next animation frame — after the gate returns and before `chromedp.Click` finishes its own `NodeVisible` wait
- **When** `browser_click` runs
- **Then** the error is `ErrNotActionable` with `Failed: CondVisible`, contains the literal `visible`, and **does not** consist solely of `timeout` or `context deadline exceeded`
- **And** `omnipus_browser_gate_failure_total{condition="visible"}` incremented, and the `ActionCondition` set is still exactly four (S-13 unaffected — no fifth condition is introduced)
- *Verified this is the untranslated behaviour today: `chromedp.Selector.Do`'s `s.wait` returns `(false, nil)` on `ErrNotVisible`, so `retryWithSleep` (`chromedp.go:773-784`) polls to the deadline and `sleepContext` returns `ctx.Err()` — a bare `context deadline exceeded`, exactly what SC-003 forbids.*

**S-59: two concurrent dialog handles issue exactly one CDP call (Concurrency) — US-9/AC4, FR-013** *(new in revision 3, round-2 m9)*
- **Given** one open `alert()` on a tab, and a CDP seam counting `Page.handleJavaScriptDialog` invocations
- **When** two `browser_handle_dialog{accept:false}` calls are issued **concurrently from two goroutines**
- **Then** the seam counts **exactly one** invocation, the losing call returns non-error `{"dialog": null}`, neither call errors, and the tab answers CDP afterwards
- *This is the scenario the "clear the map entry **before** the CDP call, under `m.mu`" invariant exists for. S-36's two **sequential** calls pass just as happily with the clear placed after the CDP call, so they do not test it. Assert at the CDP seam, never on the result shape.*

**S-60: the snapshot is never deferred (Read-only) — US-10/AC5, FR-038** *(new in revision 3, round-2 unasked-question 9)*
- **Given** a human holding the live view (`IsControlled` true), and separately another action tool holding the D1 write lease for the same browsing context
- **When** `browser_snapshot` runs in each case
- **Then** both return the snapshot, neither returns `{"deferred": true}`, and the tool's source contains **no** call to `controlledResult` and **no** call to `leaseWrite` (asserted structurally, like S-24's `AllowedRoots` absence check)
- *This exemption existed only as prose in revision 2, which is why D1 §14.2 rule 3 cites it as "D2 FR-018" — a requirement about values-by-default. It now has its own number for D1 to cite.*

**S-61: `<select>` set by option value (Happy) — US-5/AC3, FR-009** *(new in revision 3, round-2 unasked-question 4)*
- **Given** `<select>` with `<option value="a">Alpha</option><option value="b">Beta</option>` and a `change` listener setting `window.__changed = true`
- **When** `browser_select_option{value:"b"}` runs
- **Then** the selected option is Beta and `window.__changed === true` — the same outcome `{label:"Beta"}` produces
- **And When** `{value:"b", label:"Beta"}` is supplied together, **Then** the call errors naming both fields and selects nothing
- **And When** `{value:"zzz"}` matches no option, **Then** it is the same named error a non-matching `label` produces
- *FR-009 has said "accepts `value` **or** `label`" since revision 1 and not one scenario or dataset row exercised `value`, so nothing distinguished "a shipped parameter" from "a sentence".*

**S-62: `browser_press_key` with no locator bypasses the gate (Edge) — US-6/AC4, FR-010** *(new in revision 3, round-2 M11)*
- **Given** a page with nothing focused, a `browser_press_key{key:"Enter"}` call with **no** locator, and a seam that records whether `waitActionable` was entered
- **When** the tool runs
- **Then** the seam records that `waitActionable` was **never entered**, the call succeeds, and the result carries `focused_element: null`
- **And** `controlledResult` **was** consulted and the D1 lease **was** taken — the bypass is the gate only (§12 A-10)
- **And** the assertion is at the seam, never by measuring elapsed time, which would pass on a fast machine whether or not the gate ran

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/grill)

**37 live FRs** (FR-001…FR-038, FR-023 withdrawn), **59 live BDD scenarios** (S-01…S-62, S-52…S-54 withdrawn), **17 live user stories** (US-1…US-18, US-14 withdrawn). The three withdrawn identifiers are tombstoned rather than renumbered — deliberately, so that this document, both grills of it and the D1 spec's §14 cross-reference all keep resolving. A tombstone is not a dangling row: it carries no test **because D2 asserts nothing there**, and it names where the requirement actually lives.

**The coverage claim, stated so it is true (round-2 M3).** Revision 2 claimed *"every live FR has at least one US, one BDD scenario and one TDD entry"* and was contradicted three rows below by FR-025 and FR-026, both carrying `—` in the US and BDD columns. Corrected:

> **Every live FR has at least one TDD entry.** Every live FR **except the two build-gate FRs** (FR-025 `no contracts/ change`, FR-026 `no new runtime dependency`) additionally has at least one US and one BDD scenario. Those two are **build gates, not behaviour**: they have no actor, no observable outcome and nothing a Given-When-Then would add. They trace directly to **SC-007** and **SC-008**, and are marked `— (build gate)` rather than `—`, so the empty cell reads as a decision instead of an omission.

**Two acceptance criteria are deliberately non-automated**, and are likewise marked rather than left looking like gaps: **US-4/AC2** (SC-004's hand-run wall-clock measurement, §10 order 29) and **US-17/AC3** (the config key's doc comment naming the removal issue, carried by SC-009).

| FR | US | BDD | Test (TDD, §10 order) | ADR / grill |
|---|---|---|---|---|
| FR-001 role+name resolves via the shared seam | US-1 | S-01 | `TestResolveTarget_RoleName_ResolvesOnHashedClasses` (11) | D2.1 |
| FR-002 every action tool inherits the seam | US-1 | S-02 | `TestResolveTarget_AllActionToolsShareSeam` (12) | D2.1 |
| FR-003 deterministic order + index + ignored-filter + ignored-count | US-2 | S-05, S-06, S-07, S-08 | `TestResolveTarget_MultiMatch_ErrorsWithCount`, `_IndexSelectsDocumentOrder`, `_ExcludesIgnoredNodes`, `_AllIgnoredNamesTheCount` (10) | D2.1 / grill m9 |
| FR-004 exactly one locator kind; per-tool matrix enforced | US-1/AC3-AC4 | S-03, S-04 | `TestLocator_ConflictNamesBothFields`, `TestLocator_PerToolMatrix_Table` (8) | D2.1 / grill m3, m4, O3 |
| FR-005 four-condition gate on every action tool; indeterminate passes | US-3 | S-11, S-14 | `TestWaitActionable_AllFourConditions`, `_StabilityOneFrameApart`, `_IndeterminateHitTestPasses` (13) | D2.2 / grill M13 |
| FR-006 failure names the unmet condition (closed set of 4) | US-3 | S-09, S-10, S-12, S-13 | `TestWaitActionable_NamesFailedCondition_Table`, `TestActionCondition_SetIsExactlyFour` (7) | D2.2 / criterion 7 |
| FR-007 **`waitActionable`** issues exactly two `Runtime.evaluate` round trips and zero `getBoxModel`/`getNodeForLocation`, **in addition to** `chromedp`'s own post-gate traffic | US-4/AC1 | S-15 | `TestWaitActionable_FastPathRoundTripCount` (14) — counting seam **scoped to the gate** | D2.2 / r1 M1, M2 / **r2 C2** |
| FR-008 existing click/type behaviour preserved | US-15 | S-57 | the §10 regression list (26) | §2.4 blast radius |
| FR-009 `browser_select_option` by **`value` or `label`** (exactly one) + `change` event; partial multi-select errors | US-5 | S-17, S-18, **S-61** | `TestSelectOption_ByLabel_FiresChange`, **`_ByValue_FiresChange`**, **`_ValueAndLabelConflict`**, `_PartialMultiSelectAppliesNothing`, `_ZeroOptionsErrors` (17) | D2.3 / criterion 8 / **r2 unasked-Q4** |
| FR-010 `browser_press_key` named keys; unknown refused; unfocused reported; **no-locator call SKIPS `waitActionable`** (the one sanctioned gate bypass) | US-6 | S-19, S-20, S-21, **S-62** | `TestPressKey_Enter_SubmitsForm`, `_UnknownKeyErrors`, `_NoFocusReportsNull`, **`_NoLocatorSkipsActionabilityGate`** (18) | D2.3 / criterion 9 / **r2 M11** |
| FR-011 `browser_hover` opens without clicking | US-7 | S-22 | `TestHover_OpensMenu_NoClick` (19) | D2.3 / criterion 10 |
| FR-012 `browser_upload_file` confined via `ResolvePath(FSOpWrite)` | US-8/AC1-AC2 | S-23, S-24 | `TestUploadFile_AttachesFileFromWorkDir`, `_DeniedAtChokepointOutsideRoots` (20) | D2.3 / criterion 11 / grill M9 |
| FR-013 dialog handled; **tab still answers CDP**; suspected-dialog fallback (**no last-command precondition**); clear-before-CDP is idempotent under concurrency | US-9/AC1-AC2, AC4-AC5, AC9 | S-29, S-34, S-35, S-36, **S-59** | `TestDialog_TabStillRespondsAfterHandle`, `_UnhandledDialogNamedInTimeout`, `_PreListenerDialogIsSuspected`, `_NoDialogReturnsNullTwice`, **`_ConcurrentHandlesIssueOneCDPCall`**, `_PromptTextDelivered` (15) | D2.3 / criterion 12 / r1 M5 / **r2 M8, m9** |
| FR-014 dialog listener on **every** tab, keyed and re-armed | US-9/AC3, AC8 | S-32, S-33 | `TestDialog_OnNonZeroTab_StillDetected`, `_ListenerReArmedExactlyOnceAfterCtxRecreation` (16) | *(this spec)* / grill M5 |
| FR-015 `browser_snapshot` returns roles+names+values+handles | US-10/AC1 | S-38 | `TestSnapshot_ReturnsRolesNamesHandles` (21) | D2.4 / criterion 13 |
| FR-016 snapshot handles resolve in the next action | US-10/AC1 | S-38 | `TestSnapshot_HandleResolvesInNextCall` (21) | D2.4 |
| FR-017 snapshot cap: 64,000 **bytes**, node-boundary, UTF-8-safe | US-10/AC2 | S-39 | `TestChokePoint_PerSurfaceCap_Snapshot` (22) | ADR-066 B-15 / grill M3 |
| FR-018 **snapshot returns field values by default; no `include_values`** | US-10/AC3 | S-40 | `TestSnapshot_ReturnsFieldValuesByDefault`, `TestSnapshot_SchemaHasNoIncludeValues` (23) | **D2.11 operator ruling** / grill C1 |
| FR-019 `file://` error names `serve_web` + `/preview/` + the flag caveat | US-11/AC1-AC2 | S-45 | `TestValidateURL_FileScheme_NamesServeWeb`, `_JavascriptSchemeUnchanged` (6) | D2.5 / criterion 14 |
| FR-020 tier assignment + drift-test edits (**§12 A-3 open**) | US-13/AC1-AC2 | S-50 | `TestVisibility_TierArithmetic`, `TestVisibility_PreviewedSetIsExactlySeven` (5) | D2.8 |
| FR-021 policy seeded for every agent; boot survives; upload is `ask` for **every browser-capable** agent and `deny` for the two that hold no browser tool | US-8/AC3, US-12 | S-25, S-47, S-48 | `TestToolPolicyCoverage_SixNewBrowserTools_NoGaps` (2), `TestCoreAgentSeed_BrowserD2Posture` (3), **`TestCoreAgentSeed_UploadIsAskForEveryBrowsingAgent`** (4) | D2.9 + **operator ruling** / r1 C3 / **r2 C1** |
| FR-022 catalog sync (`allStaticToolNames` ↔ metadata) | US-12/AC4 | S-49 | `TestValidateOverrideKeys_PanicsOnUnknown` (1), `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` (existing) | Hard Constraint #6 |
| ~~FR-023 write lease~~ **WITHDRAWN — relocated to the D1 spec** | ~~US-14~~ | ~~S-52…S-54~~ | ~~`TestLeaseWrite_*`~~ | Normative home: `browser-workspace-ownership-spec.md` §14 + its FR-019…FR-024, FR-019a. Operator ruling 2026-08-31. **This row is a tombstone, not a dangling requirement**: it has no test because D2 asserts nothing here, and the number is retained so cross-references resolve. |
| FR-024 Explorer/Researcher parity: 5 × `allow` + `ask` on upload | US-12/AC2 | S-48 | `TestCoreAgentSeed_ExplorerResearcherBrowserParity` (4) | D2.9 corrected / grill C3, M11 |
| FR-025 no `contracts/` change | — *(build gate)* | — *(build gate)* | `make verify-contracts` (27) → **SC-007** | Hard Constraint #8 / r2 M3 |
| FR-026 no new runtime dependency | — *(build gate)* | — *(build gate)* | `go mod tidy` produces no diff; `go.mod` unchanged (28) → **SC-008** | Hard Constraint #1 / r2 M3 |
| **FR-027** snapshot output passes through `SensitiveDataReplacer` | US-10/AC4 | S-41 | `TestSnapshot_RoutedThroughSensitiveReplacer` (23) | **D2.11 mandated mitigation (i)** / r1 C2 |
| **FR-028** the capture is visible in two places: the **chat thread** shows *what* was captured; a metadata-only **`browser_snapshot`** audit event records *that* it happened, read at Settings → Security → Audit Log | US-18/AC1-AC2 | S-42, S-43 | `TestSnapshot_EmitsMetadataOnlyAuditEvent`, `TestSnapshot_AuditEventNameMatchesContractPattern`, `TestToolVisibility_AllSixBrowserToolsRender` (23) | **D2.11 mandated mitigation (ii)** / r1 C2 / **r2 M6, m3** |
| **FR-029** `browser_upload_file` unregistered until #659 lands; seeded regardless. The **gate** is order 0a (green, real); the **oracle** is order 0b, skipped and owned by #659 | US-8/AC4, AC4b | S-26, S-27 | `TestUploadFile_NotRegistered` (0a), `TestDelegatedSubTurn_AskWithNoApprover_Terminates` (0b, `t.Skip` citing #659) | **ADR D2.9 hard prerequisite** / r1 C4 / **r2 C3** |
| **FR-030** every browser-capable agent resolves `serve_web: allow` | US-11/AC3 | S-46 | `TestCoreAgentSeed_BrowsingAgentsCanCallServeWeb` (4) | D2.5 / grill M4 |
| **FR-031** audit event on every `browser_upload_file` invocation | US-8/AC5 | S-28 | `TestUploadFile_EmitsAuditEvent` (20) | grill M12 / `resolvepath.go::FSOpSend` doc |
| **FR-032** per-condition gate-failure + indeterminate telemetry | US-18/AC3 | S-44 | `TestWaitActionable_IncrementsFailureCounters` (13) | grill M12 |
| **FR-033** no-Chromium error names the missing browser | US-16 | S-55 | `TestBrowserTools_NoChromium_ErrorNamesMissingBrowser` (25) | ADR D2.7 / #665 / grill m7 |
| **FR-034** actionability-gate revert switch, live, time-boxed | US-17 | S-16 | `TestActionabilityGate_RevertSwitchIsLive` (13) | ADR-071 `previewAllLazy` precedent / grill M13 |
| **FR-035** `browser_handle_dialog` exempt from `controlledResult` **and** the D1 write lease | US-9/AC6-AC7 | S-30, S-31 (a+b) | `TestDialog_RecoversWhileHumanControls`, `_RecoversWhileCDPBlocked` (S-31a, writable now), `_RecoversWhileLeaseHeld` (S-31b, **after D1's `leaseWrite` lands** — only this one tests FR-035) (15) | ADR D2.3 "cannot be left wedged" / r1 C5 / **r2 M7**. **D1 §14.2 rule 3 must carry it — see §15 item 2.** |
| **FR-036** tier partition equals the registered builtin catalog | US-13/AC3 | S-51 | `TestManifestTierPartition_CoversRegisteredBuiltinCatalog` (5) | ADR-071 FR-034 / r1 M7. *Round-2 O3 **ran** the oracle: the four-set union is exactly 87 names and equals `allStaticToolNames` in both directions with zero symmetric difference — satisfiable today, not merely asserted.* |
| **FR-037** a post-gate `chromedp` visibility/deadline failure is translated to `ErrNotActionable{CondVisible}` — never a bare timeout; the closed condition set stays at four | US-3/AC7 | S-58 | `TestWaitActionable_PostGateVisibilityLossNamesCondition` (13a) | **r2 M5** — makes SC-003 achievable |
| **FR-038** `browser_snapshot` is read-only: no `controlledResult`, no write lease | US-10/AC5 | S-60 | `TestSnapshot_NotDeferredByViewerOrLease` (21) | **r2 unasked-Q9** — D1 §14.2 rule 3 currently cites the wrong FR (FR-018) for this exemption; it must cite **D2 FR-038** — written with the prefix, because D1 has its OWN unrelated FR-038 (a `max_browsers` cap). See §15 item 2 |

**Scenario → FR coverage check** (every scenario traced, no orphans):
S-01→FR-001 · S-02→FR-002 · S-03→FR-004 · S-04→FR-004 · S-05→FR-003 · S-06→FR-003 · S-07→FR-003 · S-08→FR-003 · S-09→FR-006 · S-10→FR-006 · S-11→FR-005 · S-12→FR-006 · S-13→FR-006 · S-14→FR-005 · S-15→FR-007 · S-16→FR-034 · S-17→FR-009 · S-18→FR-009 · S-19→FR-010 · S-20→FR-010 · S-21→FR-010 · S-22→FR-011 · S-23→FR-012 · S-24→FR-012 · S-25→FR-021 · S-26→FR-029 · S-27→FR-029 · S-28→FR-031 · S-29→FR-013 · S-30→FR-035 · S-31→FR-035 · S-32→FR-014 · S-33→FR-014 · S-34→FR-013 · S-35→FR-013 · S-36→FR-013 · S-37→FR-013 · S-38→FR-015 + FR-016 · S-39→FR-017 · S-40→FR-018 · S-41→FR-027 · S-42→FR-028 · S-43→FR-028 · S-44→FR-032 · S-45→FR-019 · S-46→FR-030 · S-47→FR-021 · S-48→FR-021 · S-49→FR-022 · S-50→FR-020 · S-51→FR-036 · S-52/S-53/S-54 → withdrawn (D1 §14) · S-55→FR-033 · S-56→FR-013 + FR-014 (dependency pin) · S-57→FR-008 · **S-58→FR-037 · S-59→FR-013 · S-60→FR-038 · S-61→FR-009 · S-62→FR-010.**

**Counts, so a reader can check rather than trust:** 38 FR identifiers issued, 1 tombstoned (FR-023) → **37 live**. 62 scenario identifiers issued, 3 tombstoned (S-52…S-54) → **59 live**. 18 US identifiers issued, 1 tombstoned (US-14) → **17 live**. Every live scenario appears in the list above exactly once; every live FR appears in the table above exactly once.

---

## 10. TDD plan (ordered; Gate → Unit → Integration → E2E)

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| **0a** | `TestUploadFile_NotRegistered` | **Gate** | FR-029 | **This is the gate that holds Stream B's upload registration, and it is GREEN, not red.** Asserts `browser_upload_file` is absent from the registry AND present in `buildKnownBuiltinToolNames()`. **No issue-state guard** — a Go unit test cannot observe a GitHub issue's state without a network call, nothing in `.github/workflows/` or the test tree does anything like it, and revision 2 named no mechanism because there is none (round-2 C3). `#659` lives in the test name's doc comment, where human context belongs. **Deleted in the same PR that registers the tool**, which must cite #659 as closed. |
| **0b** | `TestDelegatedSubTurn_AskWithNoApprover_Terminates` | **Oracle, carried by #659 — not a gate in this repo** | FR-029 | The oracle at the right layer: a delegated sub-turn invoking **any** `ask`-policy tool with no approver must **complete with a denial** under a bounded test timeout. **Written against a generic `ask` tool, not `browser_upload_file`** — so it needs no build FR-029 forbids to exist (revision 2's version required exactly such a build, which made it unrunnable as well as red). **Lands `t.Skip`-ped**, skip string `"#659: AutoDenyAsk is not inherited by delegated sub-turns"`; **the PR that closes #659 removes the skip.** A permanently-red committed test is not a gate — it contradicts Hard Constraint #7 and reds `pr.yml` forever, and a gate that can never go green blocks forever. **What actually unblocks Stream B's registration: 0a green + #659 closed + this skip removed and passing.** |
| **0c** | `TestChromedpEnablesPageDomainPerTarget` | Integration | FR-013/FR-014 | **Replaces revision 1's spike** — the question is answered (§2.2a); this pins the answer against a chromedp bump. |
| 1 | `TestValidateOverrideKeys_PanicsOnUnknown` | Unit | FR-022 | Cheapest; proves the Stream E ordering constraint before any seeding |
| 2 | `TestToolPolicyCoverage_SixNewBrowserTools_NoGaps` | Unit | FR-021 | **The boot blocker.** Must be red before Stream E, green after |
| 3 | `TestCoreAgentSeed_BrowserD2Posture` | Unit | FR-021 | Jim/Ray/Explorer/Researcher allow×5; Mia/Ava deny via the default, not a literal; Worker via the global |
| 4 | `TestCoreAgentSeed_UploadIsAskForEveryBrowsingAgent` / `_UploadIsDenyForNonBrowsingAgents` / `_ExplorerResearcherBrowserParity` / `_BrowsingAgentsCanCallServeWeb` | Unit | FR-021, FR-024, FR-030 | **Renamed and split in revision 3 (round-2 C1).** The first asserts `ask` for Jim/Ray/Explorer/Researcher/Worker; the second asserts `deny` for Mia/Ava **through the real compositor** (`resolveEffectivePolicyWith`), not by reading the seed map — the seed map alone would show only the agent-side `deny` and miss the merge that produces it. Together they replace `_UploadIsAskForEveryAgent`, which asserted a state the edit sites cannot produce and which contradicted order 3. `_BrowsingAgentsCanCallServeWeb` **computes** the agent set from resolved policy — the predicate is **`allow` for at least one `browser_*` tool** (round-2 m2), so `ask` never moves the set |
| 5 | `TestVisibility_TierArithmetic` (edit) + `TestVisibility_PreviewedSetIsExactlySeven` (edit) + **`TestManifestTierPartition_CoversRegisteredBuiltinCatalog`** (new) | Unit | FR-020, FR-036 | Build-breaking by design. **The new test lives in `pkg/gateway`** (the only package that imports both `pkg/tools` and the browser/sysagent metadata, so `buildKnownBuiltinToolNames` is reachable) and asserts set equality in **both** directions against the four-tier union |
| 6 | `TestValidateURL_FileScheme_NamesServeWeb` / `_JavascriptSchemeUnchanged` | Unit | FR-019 | Pure string; no browser. Asserts the literals `serve_web`, `/preview/` and `gateway.preview_enabled` |
| 7 | `TestWaitActionable_NamesFailedCondition_Table` / `TestActionCondition_SetIsExactlyFour` | Unit | FR-006 | Table across the four conditions against a stubbed CDP seam; the second enumerates the constants so a fifth cannot be added silently |
| 8 | `TestLocator_ConflictNamesBothFields` / `TestLocator_PerToolMatrix_Table` | Unit | FR-004 | The matrix in §3, asserted row by row |
| 9 | *(reserved — merged into 8)* | | | |
| 10 | `TestResolveTarget_MultiMatch_ErrorsWithCount` / `_IndexSelectsDocumentOrder` / `_ExcludesIgnoredNodes` / `_AllIgnoredNamesTheCount` / `_EmptyAXTreeErrors` / `_ChildFrameMatchErrors` | Integration | FR-003 | Real Chrome; ordering asserted directly, never inferred |
| 11 | `TestResolveTarget_RoleName_ResolvesOnHashedClasses` | Integration | FR-001 | The D2.1 headline |
| 12 | `TestResolveTarget_AllActionToolsShareSeam` | Integration | FR-002 | Guards against a per-tool resolution branch reappearing |
| 13 | `TestWaitActionable_AllFourConditions` / `_StabilityOneFrameApart` / `_IndeterminateHitTestPasses` / `_IncrementsFailureCounters` / `TestActionabilityGate_RevertSwitchIsLive` | Integration | FR-005, FR-032, FR-034 | Real Chrome; overlay + disabled + animated + closed-shadow-root + cross-origin-iframe fixtures |
| **13a** | `TestWaitActionable_PostGateVisibilityLossNamesCondition` | Integration | **FR-037** | **New (round-2 M5).** A fixture that hides the target on the frame after the gate returns. Asserts `ErrNotActionable{CondVisible}`, not `context deadline exceeded`. Without this, SC-003's "no bare timeout" is unachievable while the tools keep `chromedp.Click` |
| 14 | `TestWaitActionable_FastPathRoundTripCount` | Integration | FR-007 | **Counts `Runtime.evaluate` calls at a seam SCOPED TO `waitActionable`** — armed on entry, disarmed on return. Host-independent, CI-runnable, falsifiable. **Must not wrap `browser_click`**: `chromedp.Click`'s own `NodeVisible` issues `DOM.getBoxModel` and `MouseClickNode` issues `ScrollIntoViewIfNeeded`+`GetContentQuads`, so a tool-scoped counter fails on day one (round-2 C2) |
| 15 | `TestDialog_TabStillRespondsAfterHandle` / `_RecoversWhileHumanControls` / **`_RecoversWhileCDPBlocked`** / `_RecoversWhileLeaseHeld` / `_UnhandledDialogNamedInTimeout` / `_PreListenerDialogIsSuspected` / `_NoDialogReturnsNullTwice` / **`_ConcurrentHandlesIssueOneCDPCall`** / `_PromptTextDelivered` | Integration | FR-013, FR-035 | **`_RecoversWhileLeaseHeld` is THE acceptance test** and it **cannot be written until D1 §14's `leaseWrite` lands** (round-2 M7) — its Given holds the lease and D2 writes no lease code (order 24). `_RecoversWhileCDPBlocked` is its writable-today half: same wedge, a blocked CDP call and no lease, proving recovery-from-wedged without proving the exemption. **Only `_RecoversWhileLeaseHeld` tests FR-035.** `_ConcurrentHandlesIssueOneCDPCall` is new (round-2 m9): two goroutines, one open dialog, assert **one** `HandleJavaScriptDialog` at the CDP seam — the sequential `_NoDialogReturnsNullTwice` passes with the map clear on the wrong side of the CDP call |
| 16 | `TestDialog_OnNonZeroTab_StillDetected` / `_ListenerReArmedExactlyOnceAfterCtxRecreation` | Integration | FR-014 | The tab-0-only bug and the ADR-041-F3-class re-arm bug |
| 17 | `TestSelectOption_ByLabel_FiresChange` / **`_ByValue_FiresChange`** / **`_ValueAndLabelConflict`** / `_PartialMultiSelectAppliesNothing` / `_ZeroOptionsErrors` | Integration | FR-009 | `change` asserted by a page listener, not `.value`. The two `value` tests are new (round-2 unasked-Q4): FR-009 has claimed "`value` **or** `label`" since revision 1 with zero coverage of `value` |
| 18 | `TestPressKey_Enter_SubmitsForm` / `_UnknownKeyErrors` / `_NoFocusReportsNull` / **`_NoLocatorSkipsActionabilityGate`** | Integration | FR-010 | The last is new (round-2 M11) and asserts **at the seam** that `waitActionable` is not entered — never by timing — while `controlledResult` and the lease still are. It is the design's only sanctioned gate bypass and nothing guarded it |
| 19 | `TestHover_OpensMenu_NoClick` | Integration | FR-011 | Click-counter must stay zero |
| 20 | `TestUploadFile_AttachesFileFromWorkDir` / `_DeniedAtChokepointOutsideRoots` / `_EmitsAuditEvent` | Integration | FR-012, FR-031 | Denial asserted **at the `SetUploadFiles` seam**; and asserted that the tool itself contains no `AllowedRoots` comparison |
| 21 | `TestSnapshot_ReturnsRolesNamesHandles` / `_HandleResolvesInNextCall` / **`_NotDeferredByViewerOrLease`** | Integration | FR-015/016, **FR-038** | The last is new (round-2 unasked-Q9): asserts the snapshot returns under both an `IsControlled` viewer and a held lease, and structurally that the tool's source calls neither `controlledResult` nor `leaseWrite`. FR-038 exists so D1 §14.2 can cite a requirement that is actually about the exemption |
| 22 | `TestChokePoint_PerSurfaceCap_Snapshot` | Unit | FR-017 | Mirrors `per_tool_cap_alignment_test.go`'s **constant**, not `capGetText`'s mechanism. Asserts ≤ cap in bytes, valid UTF-8, node-boundary end, marker content |
| 23 | `TestSnapshot_ReturnsFieldValuesByDefault` / `_SchemaHasNoIncludeValues` / `_RoutedThroughSensitiveReplacer` / `_EmitsMetadataOnlyAuditEvent` / **`_AuditEventNameMatchesContractPattern`** / **`TestToolVisibility_AllSixBrowserToolsRender`** | Integration + Unit | FR-018, FR-027, FR-028 | **Fixed oracles, no conditional shape.** The disclosure test asserts a filled password field's value **is** present; the replacer test asserts a registered plaintext **is not**; the audit test asserts the record contains the metadata and **not** the values. **Two changes in revision 3 (round-2 M6, m3):** the name test asserts the event matches `AuditEntry.yaml`'s `^[a-z_]+$` — a dotted name throws `ApiSchemaError` over the whole entries array and blanks the operator's Audit Log view, which is the surface FR-028 rests on; and the `vitest` visibility test is renamed and strengthened from "the file contains no `browser` substring" to **`shouldRenderToolCall` returns `true` for all six names** with `verboseChatEnabled=false`. A substring-absence assertion goes green if hiding is later introduced by a different mechanism |
| ~~24~~ | ~~`TestLeaseWrite_*`~~ — **WITHDRAWN, owned by the D1 spec** (`browser-workspace-ownership-spec.md` §10 / §14) | — | — | D2 writes no lease test. Order number retained so 25–29 keep their identifiers. **D2 does still test its two exemptions** — `TestDialog_RecoversWhileHumanControls` and `_RecoversWhileLeaseHeld` at order 15 (FR-035), which are dialog tests, not lease tests. |
| 25 | `TestBrowserTools_NoChromium_ErrorNamesMissingBrowser` | Integration | FR-033 | Table over all six with a resolver stubbed to find nothing |
| 26 | Full `pkg/tools/browser` suite with `OMNIPUS_BROWSER_E2E=1` | E2E | FR-008 | Then raise the floor per SC-006 |
| 27 | `make verify-contracts` | Build | FR-025 | Must stay green with no `contracts/` diff |
| 28 | `go mod tidy` diff check | Build | FR-026 | |
| 29 | **SC-004 wall-clock measurement** | Measurement, **not a gate** | SC-004 | Run once, by hand, per SC-004's harness. Number + machine + method recorded in the PR body. Deliberately **not** in the CI table — see SC-004 |

**Note on order 29's placement.** Revision 1 listed the performance test in the same table as executable tests, which overstated its status (grill, test-coverage item 6). It is now explicitly a recorded measurement with no pass/fail, and the assertable part of FR-007 lives at order 14 where CI can run it.

### Regression requirements

**These tests exercise the code paths this work changes and MUST keep passing, by name:**

| Test | File:line | Why it is at risk |
|---|---|---|
| `TestExecute_HappyChain_NavigateWaitClickGetText` | `execute_e2e_test.go:163` | The click path gains the four-condition gate |
| `TestExecute_Type_PersistsInDOM` | `execute_e2e_test.go:892` | The type path's `WaitVisible` is replaced |
| `TestTextSel_Click_HasTextPseudo_ClicksLink` | `text_selector_e2e_test.go:109` | Text branch rerouted through `resolveTarget` |
| `TestTextSel_Click_TextParam_ClicksButton` | `text_selector_e2e_test.go:151` | Same |
| `TestTextSel_Specificity_ClicksButtonNotWrappingDiv` | `text_selector_e2e_test.go:191` | Specificity must survive the new seam |
| `TestTextSel_TypeTool_PseudoSelector_TypesIntoInput` | `text_selector_e2e_test.go:379` | `browser_type` moves off `resolvePseudoOnlySelector` |
| `TestTextSel_Specificity_NoExtraProse_ClicksButtonNotDiv` | `text_selector_e2e_test.go:511` | Same |
| `TestTextSel_TypeTool_PseudoSelector_TypesIntoContentEditable` | `text_selector_e2e_test.go:878` | Contenteditable + the gate |
| `TestExecute_TargetBlankClick_AdoptsNewTab` | `tab_adoption_e2e_test.go:77` | The per-tab dialog listener installs at `adoptTarget` |
| `TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded` | `tabs_test.go:908` | Same tab plumbing |
| `TestListenerReArm_*` (the `se.listenerTarget` bookkeeping tests) | `tabs_test.go:347-413` | The dialog listener adds a **second**, independent per-tab key; these must not start asserting it |
| `TestChokePoint_PerSurfaceCap_B15_GetText` | `per_tool_cap_alignment_test.go:11` | The snapshot must not change `browser_get_text`'s cap or its `capGetText` mechanism |
| `TestRegisterTools_RewireMustApplyNewSecurityState` | `register.go`'s cited regression | Six more `RegisterReplacing` calls on the hot-reload path |
| `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | `pkg/gateway` | Six names must land in metadata **and** `allStaticToolNames` |
| all of `tools_control_test.go`, `shared_control_test.go` | — | `controlledResult` gains the lease as a sibling; the deferral shape must not change, **and these must be extended to assert `browser_handle_dialog` does NOT consult it** (FR-035) |
| all of `blocked_schemes_test.go` | — | Asserts the current `file://` message text; **this test WILL need updating**, and it must be updated to assert the new literal, never deleted |
| all of `text_selector_test.go` | — | Unit-level seam behaviour |

**How to run the browser suite (do not run the full Go suite — OOM):**
`CGO_ENABLED=0 OMNIPUS_BROWSER_E2E=1 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/tools/browser` — matching the CI job. `skipIfNoBrowser` (`browser_e2e_test.go`) skips in CI without that env var, which is exactly how coverage silently went to zero in #615.

### Test datasets

| Input | Expected | Traces to |
|---|---|---|
| `role=button, name="Submit"`, one match | resolves, clicks | FR-001 |
| same, three matches, no index | error naming `3` + candidates; **zero** clicks | FR-003 |
| same, three matches, `index:1` | second in document order | FR-003 |
| same, **one** match, `index:0` | resolves that match (an explicit index on a unique match is legal) | FR-003 |
| same, one match, `index:1` | error naming the count `1` | FR-003 |
| same, three matches, `index:9` | error naming the count | FR-003 |
| same, `index:-1` | validation error naming the field; **no** CDP call issued | FR-003 |
| same, one visible + one `aria-hidden` | resolves the visible one, not ambiguous | FR-003 |
| same, **all three** matches ignored by Chrome | error naming `3` ignored candidates | FR-003 |
| `role=button, name=""` (empty accessible name) | validation error naming `name` as required when `role` is supplied; never a wildcard match | FR-003 |
| **empty AX tree** (about:blank / uncommitted document) | named error, not a nil-deref and not a bare "not found" | FR-003 |
| role+name matching only in a **child frame** | named error citing D2.6's frame-targeting exclusion, suggesting a CSS locator | FR-003 |
| `{selector:"#a", role:"button", name:"B"}` | error naming both `selector` and `role` | FR-004 |
| `browser_type{text:"Submit"}` as a locator | rejected by name | FR-004 |
| `browser_press_key{key:"Enter", text:"Submit"}` | rejected by name | FR-004 |
| `<button disabled>` | error contains `enabled`; `gate_failure_total{condition="enabled"}` +1 | FR-006, FR-032 |
| button under a `z-index:9999` overlay | error contains `hit-testable` **and** `overlay`; counter +1 | FR-006, FR-032 |
| button in a 300 ms transition that settles | succeeds | FR-005 |
| button under an infinite keyframe translate | error contains `stable` | FR-006 |
| same, with `actionability_gate: visible_only` | succeeds | FR-034 |
| button inside a **closed** shadow root | succeeds; `hit_test: "indeterminate"`; indeterminate counter +1 | FR-005, FR-032 |
| button inside an **open** shadow root | succeeds; `hit_test: "descendant"` (the probe descended) | FR-005 |
| button inside a **cross-origin iframe**, CSS locator | succeeds; `hit_test: "indeterminate"` | FR-005 |
| already-actionable button, one click, **counter scoped to `waitActionable`** | exactly **2** `Runtime.evaluate`; **0** `DOM.getBoxModel`, **0** `DOM.getNodeForLocation` **within the gate**. Nothing asserted about the post-gate `chromedp.Click` traffic, which issues ≥1 `getBoxModel` and this change does not touch | FR-007 |
| element actionable at the gate, hidden on the next frame, before dispatch | error names `visible`; **not** a bare `context deadline exceeded`; `gate_failure_total{condition="visible"}` +1 | FR-037 |
| element still animating when `chromedp.MouseClickNode` reads `getContentQuads` (animation resumes after the gate's two probes) | the click may land off-target — **accepted and stated**, not caught. `CondStable` is proven across RT1/RT2 only. No assertion; this row exists so the limit is recorded rather than discovered | FR-005 |
| `<select>` set by label, `change` listener armed | value set **and** listener fired | FR-009 |
| `<select>` set by **`value`** (`{value:"b"}` against `<option value="b">Beta</option>`) | Beta selected **and** listener fired — same outcome as by label | FR-009 |
| `<select>` with **both** `value` and `label` supplied | error naming both fields; nothing selected | FR-009 |
| `<select>` with `{value:"zzz"}` matching no option | same named error a non-matching `label` produces | FR-009 |
| `<select>` with **zero** `<option>` elements | named error | FR-009 |
| `<select multiple>`, labels `["Alpha","Beta","Gamma"]`, only Alpha/Beta exist | error naming `Gamma`; `selectedOptions.length === 0` | FR-009 |
| `key:"Ctrl+Banana"` | error listing accepted keys; input unchanged | FR-010 |
| `key:"Enter"`, nothing focused, no locator | succeeds; `focused_element: null`; **`waitActionable` not entered** (seam assertion); `controlledResult` and the lease **were** taken | FR-010 |
| hover target with a click counter | menu visible, counter `0` | FR-011 |
| upload path **inside** the work dir | attached; audit event `outcome: ok` | FR-012, FR-031 |
| upload path outside `AllowedRoots` | denied by `ResolvePath(FSOpWrite)` **before** `SetUploadFiles`; audit event `outcome: denied` | FR-012, FR-031 |
| upload of a **0-byte** file inside the work dir | attached; `input.files[0].size === 0` — an empty file is a legitimate upload, not an error | FR-012 |
| upload of a file **larger than the page's own limit** (e.g. 100 MB against a 10 MB `accept` limit) | `SetUploadFiles` succeeds — the page's validation is the page's business; the tool reports attachment, not acceptance | FR-012 |
| upload path that is a **symlink** into the work dir | resolved by `ResolvePath`'s realpath check; `RealPath()` TOCTOU window accepted and stated | FR-012 |
| upload path that **does not exist** (inside the work dir) | named error **before** `SetUploadFiles` is reached. **State which layer rejects it:** `ResolvePath(FSOpWrite)` has *write* semantics and may legitimately admit a not-yet-existing path, so the **tool** must `Stat` the resolved path and refuse — otherwise Chrome fails internally and the agent sees an opaque CDP error instead of a named one (round-2 m7) | FR-012 |
| upload path that is a **directory** | named error at the same tool-level `Stat`, before `SetUploadFiles`; `ResolvePath` does not distinguish file from directory (round-2 m7) | FR-012 |
| **browser-capable** agent (Jim/Ray/Explorer/Researcher/Worker), `browser_upload_file` policy | `ask` — never `deny`, never `allow` | FR-021 |
| **non-browsing** agent (Mia/Ava), `browser_upload_file` policy | `deny` — agent-level `deny` from `denyAllThenOverride` merged with global `ask` under deny-wins; asserted through the compositor, not off the seed map; **no per-agent literal** (round-2 C1) | FR-021 |
| delegated sub-turn, no approver, **any `ask`-policy tool** | **turn terminates** with a denial. Deliberately not `browser_upload_file`, whose registration FR-029 holds — the oracle needs no forbidden build (round-2 C3) | FR-029 |
| `alert()` on tab 0, then handle, then `get_text` | `get_text` returns within the normal timeout | FR-013 |
| `alert()` on tab 2, then handle, then `get_text` | identical | FR-014 |
| `alert()` while a human holds the live view | `browser_handle_dialog` runs; **not** `{"deferred": true}` | FR-035 |
| `alert()` while a blocked click holds the lease | `browser_handle_dialog` runs; the blocked click then returns | FR-035 |
| `alert()` on a tab whose ctx was recreated | recorded; **exactly one** map entry | FR-014 |
| tab **adopted after** a dialog opened, so no pending entry **and no completed command of its own** | error says a dialog **may** be open; names `browser_handle_dialog`. **This is the motivating case, and revision 2's last-command precondition excluded it** (round-2 M8) | FR-013 |
| tab with no pending entry whose `lastActivation` records a click | same suspected message, additionally naming the activation (*"after a click"*) — the field sharpens wording, never gates the message | FR-013 |
| two concurrent `browser_handle_dialog` calls, one open dialog | **exactly one** `Page.handleJavaScriptDialog` at the CDP seam; loser returns `{"dialog": null}`; neither errors | FR-013 |
| `browser_snapshot` under an `IsControlled` viewer, and under a held write lease | returns the snapshot in both cases; never `{"deferred": true}` | FR-038 |
| `prompt()` with `{accept:true, prompt_text:"x"}` | page receives `"x"` | FR-013 |
| `browser_handle_dialog` with no dialog pending, called twice | non-error `{"dialog": null}` both times; no CDP call against a closed dialog | FR-013 |
| `browser_handle_dialog` with `accept` omitted | dismisses (`accept=false`) | FR-013 |
| any tool timing out with a dialog pending | error names the dialog + `browser_handle_dialog` | FR-013 |
| AX tree rendering >64,000 bytes, ASCII only | ≤64,000 bytes; node boundary; marker names the byte cap + omitted count; top retained | FR-017 |
| AX tree with **non-ASCII** accessible names crossing the cap | ≤64,000 **bytes**; output is valid UTF-8; no split rune | FR-017 |
| password field `value="hunter2secret"` | value **present** in the snapshot | FR-018 |
| the same value **registered** via `RegisterSensitiveValues` | `[FILTERED]` present; plaintext absent | FR-027 |
| any snapshot | audit record carries origin/node count/bytes/`value_nodes_emitted`/`truncated`, and **none** of the captured values | FR-028 |
| the emitted audit event names | `browser_snapshot`, `browser_upload_file` — both match `AuditEntry.yaml`'s `^[a-z_]+$`. A dotted name fails `AuditLogResponseSchema.safeParse`, `performRequest` throws, and the operator's Audit Log view goes blank entirely | FR-028, FR-031 |
| any upload audit record | carries `fs_op: "write"` and `fs_op_reason` explaining why an upload is classed a write, so the log does not read as the gateway writing a file it never wrote (round-2 O1) | FR-031 |
| all six new tool names, `verboseChatEnabled=false` | `shouldRenderToolCall` returns `true` for each — asserted behaviourally, with the substring check kept only as a secondary hint | FR-028 |
| `file:///tmp/x.html` | error contains `serve_web`, `/preview/` and `gateway.preview_enabled` | FR-019 |
| `javascript:alert(1)` | pre-change message; no `serve_web` | FR-019 |
| every agent resolving **`allow`** for at least one `browser_*` tool (= Jim, Ray, Explorer, Researcher, Worker) | resolved `serve_web` is `allow` | FR-030 |
| fresh install, tools registered | zero coverage gaps; boot completes | FR-021 |
| override key `browser_selct_option` (typo) in an agent seed | `validateOverrideKeys` panics | FR-022 |
| the same typo in `tier3SearchOnlyToolNames` | the partition test **fails** | FR-036 |
| any browser tool on a host with no Chromium | error names the missing browser and the install path | FR-033 |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-038** as tabulated in §9 (FR-023 tombstoned). All MUST.

**SC-001 (headline demonstration — NOT an automated criterion).** An agent completes a form containing a text input, a `<select>`, a file attachment and an Enter-key submit, on a page with generated class names, using **only** role + accessible-name locators — end to end, no CSS selector anywhere in the call sequence. This is the single scenario that is impossible on every axis today. **It is a demonstration performed by hand and recorded in the PR, with a named owner; it has no test row and no automation** (grill: SC-001 was previously presented alongside seven automated criteria without saying so). It also **depends on FR-029**: the file-attachment leg cannot be demonstrated until #659 lands, so SC-001 ships in two parts and the PR records which part is done.

**SC-002.** A fresh install boots with all seventeen browser tools **seeded** and **zero** `ValidateToolPolicyCoverage` gaps. Of the seventeen, **sixteen are registered, one of which (`browser_evaluate`) is registered-and-runtime-inert unless `sandbox.browser_evaluate_enabled`; and one (`browser_upload_file`) is seeded but unregistered while FR-029 holds it.** *Spelled out because "sixteen" carries exactly the same trap "seventeen" did (round-2 m5): both numbers are true and misleading at once, and stating only the caveat that applied to the old number left the new one bare. Neither exception is a template — see §5.*

**SC-003.** Every actionability failure **on a path through `waitActionable`** is reported with the failing condition named; the failure-message test table covers all four literals, the constant-set test proves there is no fifth, and **no such path emits a bare "timeout"**.
  **Two scope statements, both load-bearing (round-2 M5).** *(i)* The scope is the gate, **and FR-037 extends it forward** through the post-gate `chromedp` dispatch — otherwise the criterion is unachievable while the tools keep `chromedp.Click`: verified, `Click` re-runs its own `NodeVisible` after the gate, `Selector.Do`'s `s.wait` swallows the resulting `ErrNotVisible` as `(false, nil)`, and `retryWithSleep` polls to the deadline and returns a bare `context deadline exceeded`. Revision 2 asserted "no path emits a bare timeout" over everything and would have shipped a criterion the first SPA re-render falsifies. *(ii)* Timeouts on paths that **never enter the gate** — read-only tools, resolution failures, the dialog wedge — are outside this criterion and are governed by FR-013's own wording rules instead.

**SC-004 (recorded measurement, NOT a pass/fail gate).** Revision 1's "≤150 ms p95 delta on `performance-2x`" is **withdrawn** — four independent defects, all conceded (grill M2): the statistic was undefined (p95-of-after minus p95-of-before is not p95-of-paired-deltas, and only the second is a per-call budget); no harness existed and a Go benchmark cannot compare two builds; it was unrunnable in CI, and project policy makes CI the authority for Go results, so the criterion had no green anyone would ever see; and at n=100 on a host ADR-072 §7 itself measures at **85–99% utilisation**, the 5th-worst sample's spread swamps 150 ms in both directions. Separately, 150 ms is roughly two orders of magnitude larger than a local CDP round trip, so a gate costing 20× the click it guards would still have passed.

  **Replaced by two things.** The **assertable** part is FR-007: the fast path issues exactly two `Runtime.evaluate` round trips, counted at a seam, host-independent, and runnable in CI (§10 order 14). The **wall-clock** part is this recorded measurement, defined precisely enough to reproduce:
  - **Statistic:** the **paired per-call delta** — for each iteration, `t_after − t_before` for the same click on the same page against the same Chrome instance; report the **median and the p95 of those paired deltas**, plus n.
  - **Harness:** two binaries built from the same tree (one with `actionability_gate: full`, one with `visible_only` — the FR-034 switch makes this a config flip rather than two builds, which is why it exists), **one** Chrome instance, a static local fixture page with an immediately-actionable button, **≥1000** iterations, the first 100 discarded as warm-up, alternating A/B per iteration so drift affects both arms equally.
  - **Comparison:** `benchstat` over the two sample sets.
  - **Host:** whatever machine ran it — **recorded by name in the PR body along with `nproc`, total RAM and the load average at the time**. Not pinned, because nothing in this repo pins one; recorded, because an unattributed number is the failure `docs/internal/false-green-patterns.md` exists for.
  - **Threshold:** none. This is a number the reviewer reads, not a gate a build passes. **If the measured median delta exceeds the round-trip cost of two `Runtime.evaluate` calls by more than 5×, that is a finding to raise** — but a finding, argued on the evidence, not an automatic red.

**SC-005.** After a handled `alert()`/`confirm()`/`prompt()` on **any** tab index, a subsequent `browser_get_text` returns within the normal page timeout — **including when a human holds the live view and when another action tool holds the write lease** (FR-035). Asserted as the tab's continued responsiveness from the wedged state, not from the post-recovery state.

**SC-006.** The `pkg/tools/browser` suite passes with `OMNIPUS_BROWSER_E2E=1`, every test in §10's regression list included, and the CI pass floor at `.github/workflows/pr.yml:481` is raised **to a stated number with stated headroom, not to the measured count**. Verified: the current literal is `-lt 180` and its own comment reads *"Raise this number (never lower it without re-verifying the true count) as more tests land"* — its intent is a **floor with headroom**, and ratcheting to the exact measured count makes the next legitimately-skipped test a red build (grill m6). **The rule: new floor = floor(measured × 0.95), rounded down to the nearest 10.** The PR body must record the measured count, the machine, the date and the resulting floor. *(Illustrative only: if the suite measures 244 after this work, the floor becomes 230 — the actual number comes from the actual run.)*

**SC-007.** `make verify-contracts` green with a zero-line diff under `contracts/`, `pkg/api/generated/` and `src/lib/api/generated/` — the positive evidence for FR-025.

**SC-008.** `gofmt -l . | wc -l` is `0`; `golangci-lint run --build-tags=goolm,stdjson` exits 0; `go.mod` unchanged (FR-026).

**SC-009.** Every one of the three operability signals in §6.1 is emitted and asserted: a `browser_upload_file` audit event on every invocation, a metadata-only `browser_snapshot` audit event — both in underscore form, which SC-009 asserts against the `AuditEntry` contract pattern because a dotted name blanks the operator.s Audit Log view entirely (§12 B-6), and per-condition gate counters. **And the FR-034 removal issue exists and is referenced by number in the config key's doc comment.**

### Seeded policy table (FR-021, FR-024, FR-030) — the exact target state

| Tool | Global | Jim | Ray | Explorer | Researcher | Mia | Ava | Worker |
|---|---|---|---|---|---|---|---|---|
| `browser_select_option` | allow | allow | allow | allow | allow | deny¹ | deny¹ | inherits global² |
| `browser_press_key` | allow | allow | allow | allow | allow | deny¹ | deny¹ | inherits global² |
| `browser_hover` | allow | allow | allow | allow | allow | deny¹ | deny¹ | inherits global² |
| `browser_handle_dialog` | allow | allow | allow | allow | allow | deny¹ | deny¹ | inherits global² |
| `browser_snapshot` | allow | allow | allow | allow | allow | deny¹ | deny¹ | inherits global² |
| `browser_upload_file` | **ask** | **ask** | **ask** | **ask** | **ask** | deny¹˒³ | deny¹˒³ | inherits global² (**ask**) |
| `serve_web` (**FR-030, new grants in bold**) | allow | allow | **allow** | **allow** | **allow** | deny¹ | deny¹ | inherits global² (allow) |

¹ **Automatic, no per-agent edit** — `denyAllThenOverride` starts every `allStaticToolNames` member at `deny`; Mia and Ava list no browser override. The ADR's D2.9 table reads as four hand-edits; two of them cost nothing.
² `IDWorker` uses `tightenGlobalCeiling` — a **sparse** map. Absent names inherit the **global** ceiling. Today Worker inherits `allow` for all eleven browser tools and for `serve_web` by exactly this route; the six follow, so `browser_upload_file` lands at **`ask`** for Worker. Recorded as intended, not discovered.

³ **CORRECTED IN REVISION 3. Mia and Ava resolve `deny`, not `ask` — and revision 2's footnote here confused two different mechanisms (round-2 C1).**

  Revision 2's footnote claimed the global `ask` reaches Mia and Ava because *"the coverage validator's OR-semantics mean the global entry covers them"*. **That is a coverage rule being used as a resolution rule.** They are different functions with different jobs, and the difference decides this cell:

  | Mechanism | Function | What it answers |
  |---|---|---|
  | **Coverage** — OR-based | `config.ValidateToolPolicyCoverage` | *Is there an entry for this (agent, tool) on **either** side?* A global entry satisfies it for every agent. This is a boot-abort guard and it never chooses a value. |
  | **Resolution** — most-restrictive-wins | `pkg/tools/compositor.go::resolveEffectivePolicyWith`, fed by `pkg/agent/instance.go::agentToolsCfgToPolicy` | *Which value applies?* `deny > ask > allow` when both sides match. `agentToolsCfgToPolicy`'s own comment: *"global × agent most-restrictive-wins … a global deny always blocks."* |

  **Re-derived from source rather than asserted:** `pkg/coreagent/core.go::denyAllThenOverride` iterates `allStaticToolNames` and writes `config.ToolPolicyDeny` for **every** name, then applies the overrides — so once `browser_upload_file` is in `allStaticToolNames` (edit site 1), Mia and Ava carry an **explicit agent-level `deny`** for it. Mia's seed (`case IDMia:`) and Ava's (`case IDAva:`) both call `denyAllThenOverride` and neither names a browser tool. Agent `deny` + global `ask` → **`deny`**.

  **So the state revision 2 asserted was not producible from its own edit-site list** (which contains no Mia/Ava edit), and `TestCoreAgentSeed_UploadIsAskForEveryAgent` (order 4) and `TestCoreAgentSeed_BrowserD2Posture` (order 3) could not both pass. **"`ask` for every agent" is therefore read as "`ask` for every agent that holds the browser surface"** — Jim, Ray, Explorer, Researcher, Worker — which is exactly the population the ruling's own reasoning is about ("not `deny` for delegation-tier workers"). The global `ask` entry is seeded exactly as the ruling directs; it is the two zero-browser agents' own least-privilege posture that overrides it downward.

  **This is DECIDED, not ruled — and it is overrulable in two edit sites.** Full argument, the ADR erratum it raises, and the alternative are at **§12 A-16**; it is listed in §15 among the overrulable items.

**Three posture decisions this table encodes, each argued rather than inherited from a group label:**

**(a) `browser_upload_file` = `ask` for every agent that holds the browser surface, delegation-tier workers included (round-1 grill C3; scope corrected by round-2 C1).** Revision 1 proposed `deny` for Explorer and Researcher, reasoning that unattended workers have no operator to answer an `ask`. **That is verbatim the argument the operator overrode** (ADR D2.9, 2026-08-31). The ruling stands and the concern it overrode is answered by FR-029, not by a per-agent `deny`: #659 lands, `AutoDenyAsk` is inherited by delegated sub-turns, and an unattended `ask` becomes a clean refusal instead of a hang. **Until #659 lands the tool is not registered at all**, so the failure mode the `deny` proposal existed to prevent cannot occur.

  **The population is the five browser-capable agents, and the two that hold no browser tool resolve `deny` by their own least-privilege default** — see footnote ³ and §12 A-16. The ruling's concern was delegation-tier workers, and every one of them (Explorer, Researcher, Worker) is `ask`; nothing the ruling argued for is lost.

**(b) `browser_handle_dialog` gets its own row, not a group grant (grill M8).** The concern is real and specific: `browser_handle_dialog{accept:true}` on `confirm("Delete this account?")` or an `onbeforeunload` is an affirmative, consequential action — Stream C's own argument is that accepting one "is indistinguishable from a click the agent did not make". Ray's seed says he "researches and reports, he doesn't build or run", and already denies `browser_evaluate` for exactly this class of reason, with the reason inline. Granting `allow` by folding the verb into "the five action/read tools" would be inheriting a decision from a label.

  **Decision: `allow`, with `accept` defaulting to `false`.** Three reasons, in order of weight. (i) **A denied recovery verb is a wedged session.** The only agent that can unwedge a tab is the agent that wedged it; `deny` reproduces exactly the failure ADR D2.3 forbids, and Ray, Explorer and Researcher are among the agents most likely to hit a dialog, because they browse arbitrary public sites. (ii) **The dangerous half is `accept:true`, and the default is now `accept:false`** — dismissal unwedges every dialog type, and dismissal of a destructive `confirm()` is the *safe* answer, so an agent that reflexively calls the tool with no argument does the conservative thing. (iii) **`accept:true` is recorded**: it appears in the tool call the operator sees in the chat thread. **This leg now rests on a guard rather than on an unpinned property (round-2 m3).** Revision 2 leant it on the same zero-`browser`-in-`toolVisibility.ts` fact that FR-028 needed a regression test for, and gave the dialog tool no equivalent assertion — an argument leaning on a property the spec elsewhere treats as needing a guard. **S-43 now asserts `shouldRenderToolCall` returns `true` for all six names, `browser_handle_dialog` among them**, so (iii) cites a test. *(Reasons (i) and (ii) carry the decision on their own regardless.)* **Overrulable:** an operator who disagrees can seed `browser_handle_dialog: ask` for the research agents; the tool works identically, it just prompts. Recorded here so the decision is visible rather than implied.

**(c) `serve_web: allow` for Ray, Explorer and Researcher is a real posture change, argued (grill M4).** `serve_web` scaffolds and serves a web app from the sandbox. Granting it to three research-tier agents widens what they can do beyond reading. **Why it is nonetheless right:** (i) FR-019's whole purpose is to end #242's dead end, and a pointer to a tool the recipient resolves `deny` for is the same dead end one failed tool call further away; (ii) all three already hold `write_file` or its equivalent within their confinement, so the marginal capability is *serving* an already-writable file over the existing preview listener, not new write reach; (iii) the preview route is already token-authenticated and confined by `gateway.preview_enabled`. **The alternatives considered and rejected:** making the error message conditional on the caller's resolved policy (the `ValidateURL` seam has no access to the calling agent's policy map, so this needs plumbing that D2 does not otherwise justify) and saying "this may not be granted to you" in the message (honest, but leaves the capability gap the fix exists to close). **Note the existing ten-allow/one-deny shape of these agents' grants is the precedent this reasons from** — their browser grant is *not* uniform, it embeds a least-privilege judgement excluding `browser_evaluate`, and this decision makes the same kind of judgement explicitly rather than by group.

### Required edit sites, exhaustive (`file::symbol` form — grill m2)

| # | Site | Change | Order |
|---|---|---|---|
| 1 | `pkg/coreagent/core.go::allStaticToolNames`, browser block | six names | **First**, or `validateOverrideKeys` panics |
| 2 | `pkg/config/defaults.go`, global `sandbox.tool_policies` browser block | six entries: five `allow`, `browser_upload_file: ask` | **This one edit closes the boot-abort risk for every agent** (OR-semantics) |
| 3 | `pkg/coreagent/core.go` `case IDJim:` | 5 × `allow` + `browser_upload_file: ask` | after 1 |
| 4 | `pkg/coreagent/core.go` `case IDRay:` | same, **plus `serve_web: allow`** (FR-030) | after 1 |
| 5 | `pkg/coreagent/core.go` `case IDExplorer:` | same, **plus `serve_web: allow`** (FR-030) | after 1 |
| 6 | `pkg/coreagent/core.go` `case IDResearcher:` | same, **plus `serve_web: allow`** (FR-030) | after 1 |
| **6a** | `pkg/coreagent/core.go` `case IDMia:` / `case IDAva:` | **NO EDIT — and that is the decision, not an omission (round-2 C1).** Both call `denyAllThenOverride` and name no browser tool, so all six land at agent-level `deny` and `browser_upload_file` resolves `deny` against the global `ask` under deny-wins. **If §12 A-16 is overruled**, this row becomes two real edits: add `"browser_upload_file": ask` to each override map. That is the entire cost of reversing it | — |
| 7 | `pkg/tools/browser/metadata.go::BrowserBuiltinMetadata` | six metadata instances (**including `browser_upload_file`**, which is seeded even while unregistered) | with 1 |
| 8 | `pkg/tools/browser/register.go::RegisterTools` | **five** `RegisterReplacing` calls; the sixth lands with #659 (FR-029) | after 2 |
| 9 | `pkg/tools/manifest_test.go:667-681` | Tier 3 fixture (**62 → 68**, or 67 if `browser_snapshot` takes Tier 2). *Range re-verified at revision 3 and **unchanged**: `{` at 667, `}` at 681. Round-2 m1 claimed it runs to ~688 and is **rejected** — 683-693 are the next test's doc comment (§14.2)* | with 8; **Stream E2, gated on A-3** |
| 10 | `pkg/tools/manifest_test.go:694-744` | literals `62`, `87` (→ `68`/`93`, or `67`/`93` with previewed `7`→`8`). *End bound corrected 745 → 744, the function's closing brace* | with 9; **Stream E2** |
| 11 | `pkg/tools/manifest_test.go` `TestVisibility_PreviewedSetIsExactlySeven` | **only** under a Tier-2 snapshot | conditional on §12 A-3 |
| 12 | `pkg/tools/manifest.go::previewedLazyToolNames` | **only** under a Tier-2 snapshot | conditional on §12 A-3 |
| 13 | `pkg/gateway` (new test file) | `TestManifestTierPartition_CoversRegisteredBuiltinCatalog` (FR-036) | with 9 |
| 14 | `pkg/tools/browser/manager.go::ValidateURL` | the scheme-specific `file://` branch | any time |
| 15 | `pkg/tools/browser/manager.go` — `sessionEntry` | `dialogListeners map[target.ID]struct{}` + pending-dialog state **+ `lastActivation` (round-2 M8: revision 2's suspected-dialog rule read a field that did not exist and named no writer)**. **Install** the per-tab listener at `createFirstTab`, `OpenTab`, `adoptTarget`, `adoptTargetWithRetry`. **Evict all three pieces of state at every teardown site, named exhaustively (round-2 unasked-Q7): explicit `browser_close_tab`, the `Session()` ctx-recreation path, AND `ReapIdleSessions`** — the reaper was previously marked "not modified by D2", which was wrong for D2's own state; a stale `target.ID` surviving a reap makes the re-arm a silent no-op and the ADR-041-F3 wedge returns. **`lastActivation` writer:** the tool, under `m.mu`, after its CDP call returns — never `handleTargetEvent`, whose doc forbids blocking. Nothing branches on it | Stream C |
| ~~16~~ | ~~`ReapIdleSessions` lease interaction~~ — **withdrawn, owned by the D1 spec** | — | — |
| 17 | `pkg/config` — `tools.browser.actionability_gate` | new enum key, default `full` (FR-034) | Stream A |
| 18 | `pkg/tools/browser` — audit logger injection | `SetAuditLogger` on the snapshot + upload tools (FR-028, FR-031) | Streams B, D |
| 19 | `.github/workflows/pr.yml:481` | raise the floor per SC-006's formula | last |
| 20 | `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` §4.1 | prose Tier 3 list — **NOT part of this change; see §12 m5** | separate PR |
| 21 | `docs/internal/architecture/ADR-072-...md` D2.11 | the ActivityPanel erratum and the `types.go:206` line-number erratum — **ADR owner's call, not this spec's**; see §2.3 | separate |

---

## 12. Ambiguity self-audit

**Status legend:** **RULED** = decided by the operator, recorded, not re-litigable here. **DECIDED** = decided by this spec on the evidence; overrulable, and the alternative is named. **OPEN** = genuinely needs a ruling before the affected stream opens.

| # | Ambiguity | Disposition |
|---|---|---|
| **A-1** | **`serve_web` vs `web_serve`.** The registered name is **`serve_web`** (`pkg/tools/web_serve.go::ToolNameWebServe`), corroborated by `previewedLazyToolNames`. | **RESOLVED BY CODE.** FR-019 specifies the literal `serve_web`; its test asserts the literal so the wrong name cannot leak back in. **The ADR has since corrected itself** (D2.5's inline note, 2026-08-31). Root `CLAUDE.md`'s ADR-044 paragraph still says `web_serve` and is a separate doc fix. *(Grill O2: revision 1 presented this as a live contradiction with the ADR; it is now a stale-elsewhere issue, and this row is downgraded accordingly.)* |
| **A-2** | **The snapshot's disclosure posture.** | **RULED, 2026-08-31 (Daniel Piatkowski): the snapshot returns field values by default.** Revision 1 proposed omit-by-default for `textbox`/`searchbox`/`combobox`/`spinbutton`/`slider` roles plus an `include_values: true` opt-in. **That was offered and declined** — an agent cannot verify a form is correctly filled before submitting it without seeing what is in the fields. The proposal is **deleted**, not deferred: FR-018 emits values unconditionally, and the tool schema has **no** `include_values` property (asserted by `TestSnapshot_SchemaHasNoIncludeValues`). The accepted risk — a card number or a partially typed password reaching the conversation and the 90-day transcript — is stated in §2.3 and in §4. The two non-optional mitigations are **FR-027** (sensitive-value replacer, defence in depth) and **FR-028** (operator-inspectable capture), each with a scenario, a test and a dataset row. **One correction the ruling's own text needs:** the ADR names the ActivityPanel as the visibility surface; verified against `src/hooks/useRunningActivity.ts`, that panel renders only subagent spans, background bash sessions and judge verdicts, never an arbitrary tool call. FR-028 therefore specs chat-thread rendering plus an audit event, and §11 edit site 21 files the ADR erratum. |
| **A-3** | **`browser_snapshot`'s manifest tier.** D2.4 calls it "the default way an agent reads a page"; Tier 3 is search-only. Tier 3 in code is the **residual**, not an enumerated list. | **OPEN — operator ruling required before Stream E's fixture edit.** **Option A — Tier 3 (all six).** Cost: zero production edits; fixture 62→68; arithmetic 87→93. Consequence: the agent must `ToolSearch` for `browser_snapshot` once per session before it can read a page structurally, which contradicts D2.4's own "default way" wording — and every alternative (`browser_screenshot`, `browser_get_text`) is *also* Tier 3, so the agent may never discover any of them and simply not read the page. **Option B — Tier 2 (`browser_snapshot` only).** Cost: one production edit (`previewedLazyToolNames`), previewed 7→8, fixture 62→67, arithmetic 87→93, and `TestVisibility_PreviewedSetIsExactlySeven` must be renamed. Consequence: one preview line's tokens on **every turn's** manifest, and the previewed set — sized at 7 by a deliberate ADR-071 decision — grows by one. **Recommendation: Option B.** **And a governance point this spec cannot settle: growing ADR-071's previewed set is an amendment to another accepted ADR.** Option B therefore needs **ADR-071's ratifier**, not merely this spec's approval — and this document does not name that person, because naming them is itself ADR-071's call. The token cost is also unquantified here — one preview line, on every turn, for the life of the setting. **STILL OPEN AT REVISION 3, deliberately.** It is the one genuinely open ruling in the document. **Nothing downstream assumes an answer:** edit sites 9-12 carry both literal sets side by side; §15 item 1 states both; Stream E is split so that only **E2** waits on it (round-2 m6) while E1, A, B, C, D and F proceed; and no FR, scenario, dataset row or success criterion names a tier for `browser_snapshot`. If a later reader finds a place that presumes Tier 2 or Tier 3, that is a defect, not a decision. |
| **A-4** | **Explorer's and Researcher's posture on the six.** | **RULED for `browser_upload_file`; DECIDED for the other five.** The ADR's D2.9 table originally omitted both agents; its 2026-08-31 correction adds them. **`browser_upload_file` = `ask`** for both, per the operator ruling — revision 1's `deny` proposal is **deleted**, and its reasoning ("unattended delegation-tier workers with no operator to answer an `ask`") is verbatim the argument the ruling overrode; FR-029 is the correct answer to that concern. **The other five = `allow`**, decided on the corrected premise: **their existing grant is TEN browser tools, not eleven.** `browser_evaluate` is deliberately excluded from both, and Explorer's block says so inline (*"(NOT browser_evaluate)"*), as does Ray's (*"NOT browser_evaluate — arbitrary JS"*). So "parity with their existing grant" does not mean "the whole surface" — it means *ten allow plus one deliberate deny*, an embedded least-privilege judgement about arbitrary-code-adjacent verbs. Re-derived from that shape: none of the five is arbitrary-code-adjacent (`select_option`, `press_key`, `hover`, `snapshot` are all bounded verbs; `handle_dialog` is argued separately in §11(b)), so `allow` is consistent with the judgement already made rather than an extension of it. |
| **A-5** | **`Page` domain enablement per tab.** | **CLOSED BY EVIDENCE, not a spike.** `chromedp@v0.15.1/chromedp.go::Context.attachTarget` executes `page.Enable()` for every non-worker target during its own bring-up, and every tab in this package reaches that path. No per-tab enable is needed, no extra round trip lands on FR-007's neighbours, and M6's "no" branch — which would have required a CDP call inside a function documented as "must be called with `m.mu` held" — does not arise. Additionally, `chromedp@v0.15.1/target.go` lists `EventJavascriptDialogOpening` among its **explicitly ignored** page events, confirming chromedp installs no browser dialog handler of its own. Pinned by §10 order 0c against a dependency bump. |
| **A-6** | **Measuring the gate's cost.** | **RESTRUCTURED (was: an unpinned-host assumption).** Split in two. The **assertable** part is FR-007's CDP round-trip count, host-independent and CI-runnable. The **wall-clock** part is SC-004, a recorded measurement with a defined statistic (paired per-call delta), a defined harness (≥1000 iterations, alternating arms, one Chrome, `benchstat`), no threshold, and the host recorded by name. Revision 1's ≤150 ms p95 is withdrawn in full — see SC-004 for the four reasons. |
| **A-7** | **`ErrNotActionable.Failed` reports the FIRST unmet condition, not all of them.** A disabled element under an overlay is both. | **DECIDED, recorded.** First-in-evaluation-order (`visible → stable → enabled → hit-testable`), because a later condition is meaningless while an earlier one is false. Deterministic and testable; a set-valued report would make the table test order-dependent for no agent benefit. |
| **A-8** | **`browser_snapshot`'s handle format.** D2.4 says "the handles needed to act on them" without naming a form. | **DECIDED, recorded.** A 0-based `index` into the snapshot's own ordering, which is the **same** document ordering Stream A's multi-match uses — so a handle read from a snapshot resolves identically in the next call. An opaque node id would be a second identity scheme that goes stale on the next DOM mutation with no way for the agent to tell. **And it is deliberately not a wire type** — §6's `contracts/` note. |
| **A-9** | **`browser_upload_file`'s `FSOp` and confinement.** | **DECIDED — `FSOpWrite`, reversing revision 1.** Revision 1 chose `FSOpSend` plus a hand-rolled `policy.AllowedRoots` check. Verified: **`FSOpSend` + `AllowedRoots` IS `FSOpWrite`'s path rule**, so that design re-implemented a chokepoint rule outside the chokepoint — a second string check, therefore a second TOCTOU window on top of the accepted `RealPath()` one. And `FSOpSend`'s own doc records that a path-based gate for that op was *"explicitly rejected"* by the operator, "so the real gate is tool policy"; revision 1 cited the first half of that reasoning and built the gate the second half rejects. **`FSOpWrite` gives the identical practical rule, enforced once, at the chokepoint.** Why an upload is a write: the operation hands a host path to a process outside the gateway's confinement, and the set of paths safe to hand out that way is exactly the set `FSOpWrite`/`FSOpServe` already bound. **Named alternative if overruled:** `FSOpSend` **alone**, tool policy as the sole gate, exactly as its doc instructs. Shipping **both** is the one option ruled out. **Consequence:** US-8/AC1's fixture narrows to a work-dir-or-mount path. **The `RealPath()` TOCTOU window remains, is unavoidable (`SetUploadFiles` has Chrome perform the read), and is stated rather than hidden.** |
| **A-10** | **Whether `browser_press_key` without a locator is an "action" for lease/deferral purposes.** | **DECIDED, recorded.** Yes — it calls `controlledResult` and takes the write lease, because it fights for the cursor exactly as `browser_type` does. It skips `waitActionable` when no locator is supplied (there is nothing to gate on), and the result reports `focused_element: null` when nothing had focus. **Symmetrically (grill O3): it rejects a `Text` locator by name**, the same collision `browser_type` has, because `key` is a value and not a locator. |
| **A-11** | **The tool count in ADR §4 ("11 → 17").** | **No action.** Verified correct: 11 registered + 6 = 17. Recorded so it is not re-litigated. Note SC-002's caveat about what "registered" means for `browser_evaluate` and `browser_upload_file`. |
| **A-12** | **`browser_handle_dialog`'s policy posture** (new; grill M8). | **DECIDED — `allow` with `accept` defaulting to `false`.** Full argument in §11(b): a denied recovery verb is a wedged session; the dangerous half is `accept:true` and the default is now the conservative one; and the call is visible to the operator in the chat thread. **Overrulable to `ask` for the research agents** without changing any code. |
| **A-13** | **Whether the actionability gate needs a revert switch** (new; grill M13). | **DECIDED — yes, FR-034, and time-boxed.** The change is unconditional on every click and keystroke on every page, `CondHitTestable` fails on shadow-DOM and iframe-hosted targets and `CondStable` on any permanently animating page — both common. The overcomplexity objection (a permanent flag on a hot path has its own cost) is real, which is why the switch copies ADR-071 `previewAllLazy`'s **time-boxed** shape: one atomic read inside one chokepoint, deleted in the same change that acts on the FR-032 counters, with the removal issue referenced by number in the key's doc comment (SC-009). **Who owns pulling the trigger, and on what (round-2 unasked-question 3):** "deleted in the change that acts on the data" needs someone to decide the data says act, and revision 2 named nobody and no threshold. **The removal issue carries both**, and this spec states what it must contain: an **owner** (the operator, or a named maintainer the operator designates — this spec cannot assign a person), a **review date** no later than **60 days after the feature merges**, and a **decision rule over the FR-032 counters**: if `gate_failure_total{condition="stable"|"hit-testable"}` over the window is a **negligible fraction of total gated actions** (the operator sets the number when the first real data exists — this spec deliberately does not invent a threshold with no baseline behind it), the switch is deleted; if it is not, the *gate* is the thing to fix and the switch stays only until that fix lands. **A switch with no review date is a permanent flag on a hot path**, which is the overcomplexity objection this design conceded was real. |
| **A-14** | **The ADR's `accessibility.Node.Value` citation** (new). | **Erratum, no design impact.** ADR D2.11 cites `cdproto/accessibility/types.go:206`; verified, that line is `AXPropertySource.Value`. The field the snapshot reads is `Node.Value` at `types.go:461`. Recorded so it is not re-derived; filed with the D2.11 ActivityPanel erratum (§11 edit site 21). |
| **A-15** | **Whether the `Locator` abstraction earns its keep** (new; grill m8). | **DECIDED — yes, on the strength of the matrix, not the struct.** The objection was fair: `resolveTarget` "supersedes" two functions that both survive as internal branches, to add one locator kind whose output is the same marker selector the text path already produces. What justifies it is the **per-tool locator matrix** in §3, which revision 1 had one entry of and now has five — two tools reject `Text` for the same value-vs-locator reason, two tools accept no locator at all, and one tool has a legal no-locator mode. That table has to live somewhere and be enforced somewhere; a struct with a validation function is the cheapest place. **If the matrix is ever reduced back to one row, this decision should be revisited.** |
| **B-1** | **Snapshot fetch cost is unbounded** (assumption, recorded). | `GetFullAXTree` runs in full on a 5,000-node page before any truncation, bounded only by the tool's page timeout. **Not** bounded by a requirement in v1. Measured post-hoc by §13 holdout 6. Recorded rather than implied away. |
| **B-2** | **Snapshot vs the ADR-066 turn budget** (assumption, recorded). | The 64,000-byte cap is per-tool; the turn budget is ADR-066's `windowTrim` and is out of scope. A snapshot plus a `get_text` in one turn is 128,000 bytes from two tools that each obey their own cap. Stated, not solved. |
| **B-3** | **What the `ask` prompt for `browser_upload_file` shows the operator** (grill unasked-question 3). | **Out of this spec's control, and flagged.** The approval prompt is rendered by the agent-loop/SPA approval flow, not by the tool. An approval that names only the tool is not a decision — the operator needs **the resolved path and the target origin**. Both are in FR-031's audit fields, so the data exists; **whether the prompt renders them is an approval-flow change this spec does not own.** **Promoted from a note to a stated exit condition on #659 (round-2 unasked-question 5).** Revision 2 filed it as "a note against #659's work", which the round-2 grill correctly called the weakest of the three dispositions in §15 — it is the entire value of the `ask` the operator ruled for, and a note is not a commitment. **FR-029 already makes #659 a hard prerequisite for registering `browser_upload_file`; this spec therefore states the prerequisite in full: #659 must deliver both (a) `AutoDenyAsk` inheritance by delegated sub-turns, and (b) an approval prompt that renders the resolved path and the target origin.** Both are the same flow and the same change. **This spec cannot bind another issue's scope** — that is the operator's or #659's owner's call — so if (b) is declined there, it must be declined **explicitly** and this row updated to record that the `ask` ships as a yes/no with no facts. What it must not do is stay a note nobody acts on. Listed in §15 item 5. |
| **B-4** | **`gateway.preview_enabled = false` makes the `file://` route dead for everyone** (round-1 unasked-question 9). | **Handled in the message, not in code.** FR-019's string ends `(requires the serve_web tool and gateway.preview_enabled)`. One clause, no extra round trip, honest when the flag is off. A conditional message would need `ValidateURL` to read live gateway config, which it does not do today. |
| **A-16** | **Whether "`ask` for every agent" reaches the two agents that hold no browser tool** (new; round-2 C1). | **DECIDED — no, they resolve `deny`. Overrulable in two edit sites.** The mechanism is settled and re-derived from source (footnote ³ under §11's table): `denyAllThenOverride` writes an explicit agent-level `deny` for every `allStaticToolNames` member, and `resolveEffectivePolicyWith` merges deny-wins, so Mia and Ava resolve **`deny`** unless someone adds a literal override. Revision 2 asserted `ask` for them via a footnote that mistook the **coverage** validator's OR-semantics for a **resolution** rule; the two scenarios that followed (S-25, S-48) contradicted each other and neither state was producible. **Why `deny` is the right reading of the ruling, not merely the convenient one:** the ruling's words are *"`ask` in the GLOBAL tool policy, for every agent — not per-agent, and not `deny` for delegation-tier workers"*, and its stated reasoning is entirely about **unattended delegated work**. Every delegation-tier agent (Explorer, Researcher, Worker) is `ask` under this reading. Mia and Ava are least-privilege chat agents that hold **zero** browser tools and whose seeds route browser work away by design; giving them `ask` would be a real, if small, capability grant — policy is per-tool, so an `ask` there means Mia *can* invoke the tool subject to an approval, which is not something the ruling contemplated. **The ADR erratum this raises, and it needs the ADR's owner:** ADR D2.9's table lists Mia = Ava = `ask` for `browser_upload_file` **while its own next paragraph says "Mia and Ava need no edit at all — `denyAllThenOverride` gives them deny for free"**. Those cannot both hold. Recommended amendment: the table's Mia/Ava cell reads `deny`, with a footnote that the global `ask` is overridden downward by their agent-level default. **The alternative if overruled:** add `"browser_upload_file": ask` to Mia's and Ava's override maps (§11 edit site 6a) and flip S-25, S-48, US-8/AC3, US-12/AC3 and order 4 back. Two lines of Go; no code change beyond seed data. |
| **A-17** | **Whether the gate still needs `CondVisible`, given `chromedp.Click` re-checks visibility right after it** (new; round-2 unasked-question 1). | **DECIDED — keep it, and the duplication is the accepted cost.** Verified: `Click()` appends `NodeVisible`, which issues `dom.GetBoxModel` plus a `Runtime.callFunctionOn` visibility probe **after** the gate returns. Dropping the gate's `CondVisible` would take the fast path from two `Runtime.evaluate` round trips to one and remove a genuinely duplicated check on the hottest path in the change — a real saving. **It is still wrong, because the two checks do different things.** When chromedp's own wait fails, `Selector.Do`'s `s.wait` returns `(false, nil)`, `retryWithSleep` polls to the context deadline, and `sleepContext` returns `ctx.Err()` — a bare `context deadline exceeded` with no condition named. The gate's `CondVisible` is the **only** producer of a named `visible` failure in the system, and naming the unmet condition is ADR criterion 7, the entire point of the gate. **Consequence stated rather than buried:** the gate's cost is **purely additive**, so US-4's "not a tax" is a claim about magnitude (two in-page evaluates), never about the gate replacing work chromedp would otherwise do. Revision 2's framing implied the latter. |
| **A-18** | **What errors an operator sees in `visible_only` mode** (new; round-2 unasked-question 2). | **DECIDED, recorded.** With FR-034's switch at `visible_only` the gate is the pre-change `chromedp.WaitVisible` verbatim, so **`ErrNotActionable` never fires** and SC-003, S-09's oracle and the FR-032 per-condition counters describe the `full` mode only. In `visible_only` the operator sees exactly what they saw before this change: a bare `context deadline exceeded` on a failed wait. **That is not a regression — it is the mode's whole purpose** (restore prior behaviour byte-for-byte when the gate false-fails on a real site), and it is why the switch is **time-boxed**: it trades the named-condition diagnostic away, so leaving it on indefinitely silently gives back ADR criterion 7. **FR-037 is likewise `full`-mode only.** The FR-032 counters keep working in both modes for `indeterminate`, but `gate_failure_total` is necessarily zero in `visible_only`, which the runbook must not read as "no failures". Stated here because an operator flipping the switch during an incident should know the diagnostic goes with it. |
| **B-5** | **`resolveTarget`'s CDP cost is unbudgeted** (new assumption, recorded; round-2 M10). | **Recorded, not measured, in v1 — deliberately, with the reason.** FR-007 budgets `waitActionable` at two round trips and order 14 counts them. The **resolution** seam issues its own traffic and none of it is budgeted: the AX branch runs `QueryAXTree` + `DOM.setAttributeValue` + a cleanup `removeAttribute` per call, and the snapshot path runs `GetFullAXTree`, which B-1 already concedes is unbounded. §2.4 rates `resolveActionSelector`/`resolvePseudoOnlySelector` **HIGH** with a **wider** dependent set than the gate ("click, type, get_text, wait + all six new tools"), so v1 measures the cheaper half of the change and not the more widely used half. **Why not fix it now:** the useful number is not a round-trip count — the AX branch's cost is dominated by tree size, so a fixed count would be satisfied by a call that takes 400 ms on a real page, and a *useful* budget needs the same paired-measurement machinery SC-004 defines and deliberately leaves non-gating. **What would close it:** extend order 14's counting seam over `resolveTarget` as a second, non-gating recorded count (the seam already exists, so the marginal cost is small) and record CSS-branch versus AX-branch numbers side by side in the PR, exactly as SC-004 records the gate's. Filed as v1-deferred; it must not be discovered as a surprise in holdout 10. |
| **B-6** | **The audit-log viewer already breaks on dotted event names** (new, pre-existing; found while resolving round-2 M6). | **Out of scope, recorded so D2 does not compound it.** `contracts/components/schemas/AuditEntry.yaml` pins `event` to `pattern: '^[a-z_]+$'`; the generated `src/lib/api/generated/schemas.ts::AuditEntry` carries `z.string().regex(/^[a-z_]+$/)`; `AuditLogResponse.entries` is `z.array(AuditEntry)`; and `src/lib/api.ts::performRequest` **throws `ApiSchemaError`** on a `safeParse` failure rather than dropping the offending row. **So one dotted entry blanks the entire Audit Log viewer.** Two shipped emitters already write dotted names into the same `audit.jsonl`: `audit.EventChannelPairing = "channel.pairing"` (`pkg/agent/loop.go:4357`) and `audit.EventCliValidate = "cli.validate"` (`pkg/gateway/rest_clivalidate.go:370`) — so on any install where a channel has been paired or a CLI validated, Settings → Security → Audit Log is already broken. **D2's obligation is narrow and absolute: use underscore-form names** (`browser_snapshot`, `browser_upload_file`), asserted by `TestSnapshot_AuditEventNameMatchesContractPattern` (§10 order 23) and forbidden in §5. **Fixing the pre-existing pair is a separate change** — it is either a contract widening (allow `.` in the pattern, regenerate, re-verify) or two event renames with a log-format consideration, and both need an owner this spec does not have. File it; do not fold it in. |

---

## 13. Holdout evaluation scenarios (post-implementation, NOT in the TDD plan or the traceability matrix)

1. **(happy)** On a real public site with generated class names, an agent fills a multi-field form end to end using **only** `browser_snapshot` + role/name locators + `browser_select_option` + `browser_press_key{Enter}` — no CSS selector in the whole trace. *(SC-001's first part.)*
2. **(happy)** An agent attaches a real file to a real upload form; the operator sees exactly one `ask` prompt, approves, and the upload completes. **Blocked on #659 (FR-029)** — record it as blocked, not as passing. *(SC-001's second part.)*
3. **(error)** A site fires `confirm()` on navigate-away. The agent hits it, does **not** call `browser_handle_dialog`, and the next browser tool's error tells it what happened and what to call. The agent recovers unaided.
4. **(error)** A cookie banner overlays the page. `browser_click` on the underlying CTA fails naming `hit-testable` **and** the banner element; the agent dismisses the banner and retries successfully — without the operator intervening.
5. **(edge)** A single-page app re-renders between the snapshot and the action. The handle is stale. Assert the failure is a *named* resolution error, not a click on the wrong element.
6. **(edge)** A page with 5,000 AX nodes. Snapshot returns within the page timeout, capped at ≤64,000 bytes on a node boundary, top retained, marker naming the byte cap and omitted-node count. **Record the fetch time separately** — B-1 states the fetch is unbounded, and this is where that gets a number.
7. **~~(edge)~~ WITHDRAWN — this is a D1 holdout (round-2 M4c).** "Two agents on one browsing context both drive a form; the loser's `{"deferred": true}` is visible in the transcript and it waits and retries" exercises the **write lease**, which revision 2 relocated to `browser-workspace-ownership-spec.md` §14. D2 defines no lease and cannot evaluate one. Number retained so 8-11 keep their identifiers. **What D2 *does* hold out on contention is new item 12 below** — the two exemptions, which are D2's.
8. **(edge)** A dialog opens on tab 2 while the operator is watching tab 0 in the live panel. The agent recovers tab 2. **Record what the human sees** — this is the accepted gap in §6, and the holdout exists to measure how bad it is, not to pass.
9. **(edge)** `linux/arm64`, where no Chromium is present (ADR D2.7 / #665). All new tools are registered and fail. Confirm the failure names the missing browser and does not read as a tool bug. **Promoted to FR-033/US-16 with a real test (§10 order 25)** — this holdout is now the *field* confirmation of an automated assertion, not the only coverage (grill m7).
10. **(regression)** With the actionability gate live, run a 10-minute mixed browsing session on real sites and compare per-click latency against the same build with `actionability_gate: visible_only`. **Also record the FR-032 counter values** — this is the run that tells the operator whether the gate is rejecting real clicks, and the data FR-034's time-box is measured against.
11. **(security)** Snapshot a real signed-in page with a partially filled payment form. Confirm the values **are** present (the ruling), the audit event carries only metadata, and the operator can see the tool call in the chat thread. **Then open Settings → Security → Audit Log and confirm the `browser_snapshot` record is actually there and the viewer still loads** — this is FR-028's second surface and the one thing a code-level test cannot fully cover, since the failure mode (a schema-invalid entry blanking the whole view) is end-to-end. **This is the holdout that shows the accepted risk to a human before it ships.**
12. **(edge, new — replaces the withdrawn holdout 7)** With another action tool genuinely blocked on the same browsing context, confirm that **`browser_snapshot` and `browser_handle_dialog` still return** (FR-038, FR-035) while the four leased verbs defer. Record what the transcript shows the agent, because "deferred, wait and retry" is only useful behaviour if the agent can tell it apart from a failure. *The deferral half belongs to D1; the two exemptions are D2's, and this is where they are seen working together on a real page rather than on a fixture.*

---

## 14.1 Disposition of the round-1 grill of this spec (30 findings)

**28 FIXED · 1 WITHDRAWN · 1 RELOCATED.** *(Revision 2's header read "all 30 addressed, none deferred", which overstated the sheet by two — round-2 **m8**. Two dispositions were correct on the merits and mislabelled: **M2** is a **withdrawal**, not a fix — the ≤150 ms p95 requirement is gone and FR-007's round-trip count plus a non-gating measurement is a different and weaker claim; **M10** is a **relocation** — the finding is now another document's, which §14.1's own cell already said. Relabelled below. 28/1/1 is a stronger claim than 30/0/0 precisely because it is checkable, and a disposition table that overstates is worse than one with open rows.)*

| ID | Disposition | Where |
|---|---|---|
| **C1** snapshot specs the declined design | **FIXED** | FR-018 emits values unconditionally; no `include_values`; §12 A-2 is RULED not open; §10 order 23 has a fixed oracle (S-40) |
| **C2** the two mandated mitigations are unspecced | **FIXED** | FR-027 (replacer) + FR-028 (visibility) as first-class FRs with S-41/S-42/S-43, §10 order 23, dataset rows, SC-009. **ADR erratum filed** (§2.3, §11 site 21) because "reachable in the ActivityPanel" is false on current code |
| **C3** `deny` for delegation-tier workers contradicts the ruling | **FIXED** | §11 table: `ask` for every agent; §12 A-4 RULED; S-25/S-48; §10 order 4 |
| **C4** #659 absent; oracle green over a hang | **FIXED** | FR-029; §10 orders 0a/0b as gates; US-8/AC4; S-26/S-27. `_AskWithNoApprover_Terminates` **fails while #659 is open**, by design |
| **C5** recovery verb gated by what the wedge disables | **FIXED** | FR-035; §5 non-behavior; S-30/S-31 (S-31 is the wedged-state acceptance test); §11(b) |
| **M1** fast-path assertion unsatisfiable | **FIXED** | §3 writes out RT1/RT2; FR-007 asserts **two** round trips; S-15 |
| **M2** ≤150 ms p95 unmeasurable | **WITHDRAWN** *(requirement replaced, rationale in SC-004)* — **not** "fixed" | The ≤150 ms p95 budget is **gone**, and what replaced it is a different and weaker claim: FR-007's round-trip count (assertable, CI-runnable, and rescoped again in revision 3 — round-2 C2) plus SC-004's non-gating recorded measurement, §10 order 29. The four reasons the original was unmeasurable are conceded in full at SC-004. Defensible, argued, and **not a fix of the original requirement** — labelling it one alongside 28 real fixes overstated the sheet (round-2 m8) |
| **M3** cap self-contradictory, chars vs bytes | **FIXED** | FR-017: byte budget, node-boundary, UTF-8-safe, **not** `capGetText`; S-39; dataset rows including non-ASCII |
| **M4** `serve_web` pointer unreachable | **FIXED** | FR-030; §11(c) argues the posture change; US-11/AC3 asserts callability, not the literal; S-46 |
| **M5** listener: no re-arm, no key, no pre-existing-dialog answer | **FIXED** | Stream C's three numbered gaps; FR-014; S-33; the "suspected dialog" fallback (S-35) |
| **M6** `Page`-domain spike has no design either way | **FIXED by evidence** | §2.2a resolves it from chromedp's source; the "no" branch does not arise; pinned by §10 order 0c |
| **M7** tier test cannot detect Tier-3 drift | **FIXED** | FR-036; `TestManifestTierPartition_CoversRegisteredBuiltinCatalog` in `pkg/gateway`; S-51 asserts the typo **fails** |
| **M8** `browser_handle_dialog` posture unanalysed | **FIXED** | §11(b) gives it its own row and argument; `accept` defaults to `false`; §12 A-12 |
| **M9** `FSOpSend` + hand-rolled roots re-implements `FSOpWrite` | **FIXED** | FR-012 selects `FSOpWrite`; §12 A-9; S-24 asserts the tool contains **no** `AllowedRoots` comparison |
| **M10** `leaseWrite` contract under-specified | **RELOCATED (D1 §14)** — **not** "fixed" | The finding was correct and D2 did not fix it; it now belongs to another document: the lease moved to the D1 spec by operator ruling (2026-08-31), and `browser-workspace-ownership-spec.md` §14.1/§14.2 carries a fuller contract than revision 2 drafted — a bounded, cancellable wait (`leaseWaitTimeout`, default 2 s, asserted strictly less than the shortest action-tool timeout), an always-idempotent `release()` via `defer`, a named lock order (`writeLease → pool.mu → m.mu`), a registry-enforced membership rule, and a stated no-fairness bound. D2 asserts nothing about the lease and therefore cannot under-specify it. |
| **M11** "eleven-tool surface" is wrong; it is ten | **FIXED** | §2.1 corrected for Explorer/Researcher/Ray; §12 A-4 re-derived from ten-allow/one-deliberate-deny |
| **M12** no observability anywhere | **FIXED** | §6.1 (three signals + runbook); FR-028, FR-031, FR-032; SC-009 |
| **M13** unconditional hot-path change, no revert | **FIXED** | FR-034 (time-boxed, `previewAllLazy` shape); US-17; S-16; shadow-DOM and iframe dataset rows; `indeterminate` degrades to a recorded pass |
| **m1** `executeEnabled` uniqueness unrecorded | **FIXED** | §2.1 row + §5 non-behavior; SC-002's caveat |
| **m2** `file:line` will go stale | **FIXED** | §2.1 and §11 converted to `file::symbol`; line numbers kept only for the manifest fixture and the CI literal |
| **m3** "exactly one … resolved in that order" is two contracts | **FIXED** | `Locator` errors naming both fields; `ErrLocatorConflict`; S-03; dataset row |
| **m4** `Index int` + `HasIndex bool` | **FIXED** | `Index *int`; the illegal state is unrepresentable; dataset rows for `index:-1` and `index:0`-on-unique |
| **m5** ADR-071 §4.1 edit has no ratifier | **FIXED** | Moved out of this change: §11 edit site 20 marks it a **separate PR**, and §12 A-3 records that growing the previewed set likewise needs ADR-071's ratifier |
| **m6** SC-006 conflates a floor with an equality | **FIXED** | SC-006 states the formula (`floor(measured × 0.95)`, rounded down to 10) and requires the count, machine and date in the PR |
| **m7** arm64 messaging is holdout-only | **FIXED** | FR-033 + US-16 + S-55 + §10 order 25; holdout 9 becomes field confirmation |
| **m8** `Locator` abstraction may not pay for itself | **FIXED** | The per-tool matrix is enumerated (five rows); §12 A-15 records the revisit condition |
| **m9** `Ignored == false` hides reachable elements | **FIXED** | The no-match error names the ignored-candidate count; S-08; dataset row |
| **O1** merge order C-before-E unstated | **FIXED** | §3 Parallelization's closing paragraph |
| **O2** §2.3's "the ADR contradicts the code" framing is stale | **FIXED** | §2.3 rewritten as "what was verified / what was ruled"; §12 A-1 downgraded to a stale-elsewhere note; §2.2a and A-5 closed by evidence |
| **O3** `browser_press_key` and a `Text` locator | **FIXED** | Per-tool matrix row; FR-004; S-04; §12 A-10 |

**Structural-integrity items from the round-1 grill's §3, also addressed:**
- *Every acceptance scenario has ≥1 BDD scenario* — the six gaps (US-6/AC1, US-8/AC1, US-8/AC3, US-9/AC4, US-9/AC5, US-3/AC5) are now S-19, S-23, S-25, S-36, S-37, S-13.
- *Test datasets cover boundary / edge / error* — the nine named gaps (empty AX tree, zero-option `<select>`, partial multi-select, 0-byte and oversized upload, negative index, index-on-unique, empty accessible name, iframe/shadow-root targets, non-ASCII against the cap) all have rows.
- *Success criteria measurable, no subjective language* — SC-004 restructured, SC-006 given a formula, SC-001 explicitly labelled a manual demonstration with an owner.
- *Test-coverage weaknesses 1–6* — the dialog acceptance test now exercises the wedged state (S-31) plus the viewer, ctx-recreation and pre-listener cases; FR-036 fixes the oracle-free tier test; US-8/AC3's oracle moved to turn completion; idempotency/retry covered for `browser_handle_dialog` (S-36, and concurrently by S-59) and for repeated uploads (dataset); the performance test is out of the executable table. **Corrected in revision 3 (round-2 M4d):** this bullet previously claimed "the lease has starvation and abandoned-holder coverage" as though D2 carried it. **It does not and never will — that coverage is `browser-workspace-ownership-spec.md` §14's, in its FR-019…FR-024 and its test 18.** D2 writes no lease test (§10 order 24).

---

## 14.2 Disposition of the round-2 grill of this spec (26 findings)

**25 ACCEPTED · 1 REJECTED.** The rejection is evidence-backed and stated in full below; everything else is taken.

| ID | Disposition | Where |
|---|---|---|
| **C1** policy section conflates coverage with resolution; S-25 and S-48 contradict; neither state producible | **FIXED** | Footnote ³ under §11's table rewritten around `denyAllThenOverride` + `resolveEffectivePolicyWith` (deny-wins), with the coverage-vs-resolution distinction tabulated. Mia/Ava = `deny` in the table. S-25 and S-48 now agree. US-8/AC3, US-12/AC3, §10 order 4 (split and renamed), edit site 6a, two dataset rows. **§12 A-16** records the decision as DECIDED-overrulable with the two-edit-site alternative and files the **ADR D2.9 erratum** (its table says `ask`, its own next paragraph implies `deny`) |
| **C2** FR-007 falsifiable and false; `chromedp.Click` appends `NodeVisible` → `DOM.getBoxModel` | **FIXED** | Counting seam rescoped to `waitActionable`. FR-007 restated as "two `Runtime.evaluate` **in addition to** chromedp's post-gate traffic"; the zero-`getBoxModel` claims moved inside the gate's scope. §3 carries the verified post-gate CDP table. S-15, US-4/AC1, dataset row, §10 order 14. The redundancy question is answered at **§12 A-17** — keep `CondVisible`, because chromedp's own failure is a bare deadline and the gate is the only producer of a named `visible` |
| **C3** 0a asserts an issue's state from Go; 0b permanently red *and* the unblocking gate; S-27 needs a forbidden build | **FIXED** | 0a → `TestUploadFile_NotRegistered`, guard deleted, `#659` in the doc comment, **green**. 0b → written against a **generic `ask` tool**, lands `t.Skip`-ped citing #659, owned by #659's PR. S-26/S-27 rewritten; US-8/AC4 split into AC4 (registration) and AC4b (the oracle). **What unblocks Stream B is stated explicitly:** 0a green + #659 closed + 0b's skip removed and passing |
| **M1** §15's named blocker already closed in HEAD | **FIXED — downgraded, NOT closed** | §15 item 2 rewritten. D1 §14.2 rule 3 *was* widened to five in `335d56fe` — but D1's own round-2 review (**CRIT-104**) found the set omits `browser_list_tabs`, and D1's **MAJ-008** disposition row still reads "a closed set of three" and "D2's five new action tools". Recorded as "**D1 §14 must resolve its own exemption set**", with the FR-038 citation correction added |
| **M2** "five new ACTION tools" is arithmetically impossible | **FIXED** | **Four**, in both places — §3's interface-contract comment (now with the four/two split enumerated) and Stream F |
| **M3** §9's own coverage claim false for FR-025/FR-026 | **FIXED** | §9's preamble restated to except the two build-gate FRs explicitly; both rows marked `— (build gate)` and pointed at SC-007/SC-008. US-4/AC2 and US-17/AC3 marked non-automated by design in-place |
| **M4** lease tombstoned in the matrix, alive in §1/§4/§13/§14 | **FIXED** | §1's in-scope bullet replaced by an explicit out-of-scope note; §4's `{"deferred": true}` promise removed and marked *inherited from D1 §14, not asserted here*; §13 holdout 7 withdrawn (new holdout 12 covers what D2 actually owns); §14.1's structural bullet corrected to name D1 as the owner |
| **M5** SC-003 unachievable while the tools keep `chromedp.Click` | **FIXED** | SC-003 scoped to `waitActionable` **and** the residual closed by new **FR-037** (translate a post-gate `ErrNotVisible`/deadline into `ErrNotActionable{CondVisible}`), with S-58, §10 order 13a, a dataset row and a §5 non-behavior. Verified the untranslated path: `s.wait` returns `(false, nil)`, `retryWithSleep` polls to the deadline, `sleepContext` returns `ctx.Err()` |
| **M6** "operator-inspectable" names no operator surface | **FIXED, and the surface is named** | Settings → Security → **Audit Log** (`AuditLogViewer.tsx` ← `GET /api/v1/audit-log` ← `system/audit.jsonl`) — verified to render unknown event names by default. §2.3 rewritten to say which half answers *what* and which answers *that*; §2.1 gains two rows; §6.1 gains the surface row. **Two limits found and stated:** the event dropdown is a hardcoded 10-name list, and `AuditEntry`'s `^[a-z_]+$` forces **underscore-form event names** — a dotted name throws and blanks the whole viewer (**§12 B-6** records the pre-existing `channel.pairing`/`cli.validate` violations as out of scope). S-43 strengthened to a behavioural assertion over all six names |
| **M7** `_RecoversWhileLeaseHeld` has an unstated D1 dependency | **FIXED** | §3's Parallelization gains it as **merge-order constraint 2**, with the split: **S-31a** (`_RecoversWhileCDPBlocked`, writable today, proves wedge-recovery) and **S-31b** (`_RecoversWhileLeaseHeld`, after D1, **the only one that tests FR-035**). §9's FR-035 row and §10 order 15 both carry it |
| **M8** suspected-dialog fallback reads a field that does not exist and is false in its own motivating case | **FIXED** | Predicate **widened**: any CDP timeout with no recorded pending dialog gets the suspected wording; nothing gates on last-command. `sessionEntry.lastActivation` survives only to *sharpen* the message, with its writer, lock, lifetime and concurrency stated; added to edit site 15 and §2.4. S-35's **Given** rewritten to the adopted-tab case it previously excluded, plus a second row for the sharpening |
| **M9** `CondStable` is discarded; the click dispatches from a later box | **FIXED (accepted in writing)** | Stated as a §5 non-behavior and in §4: stable **across the two probes, not at dispatch**, because `MouseClickNode` computes the centre from its own later `getContentQuads`. Dataset row for "animation resumes between gate and dispatch". Closing it would mean replacing `chromedp.Click`, which §3 forbids |
| **M10** `resolveTarget`'s cost unbudgeted and unmentioned | **ACCEPTED — recorded as a stated v1 limit (§12 B-5)** | The reviewer offered either option; this spec takes the "record it explicitly and say why" branch. §2.4's row, §6.1's runbook and **B-5** all now carry it, with what would close it (extend order 14's existing seam as a second non-gating count, CSS vs AX side by side). Deliberately **not** given a round-trip budget: the AX branch's cost is tree-size-dominated, so a fixed count would be satisfied by a 400 ms call |
| **M11** the gate's one sanctioned bypass has no oracle | **FIXED** | Folded into FR-010's text; **S-62** + `TestPressKey_NoLocatorSkipsActionabilityGate` (§10 order 18) assert at the seam, not by timing; US-6/AC4; dataset row; §5 non-behavior. The skip is the gate only — `controlledResult` and the lease still apply |
| **m1** `manifest_test.go:667-681` is the wrong range | **REJECTED — the range is correct** | Re-derived at revision 3: `var tier3SearchOnlyToolNames = []string{` is at **667** and its closing `}` at **681**; **682 is blank and 683-693 are the doc comment of `TestVisibility_TierArithmetic`**, which opens at 694. The literal does not run to 688. **The one real correction taken:** the arithmetic test's end bound is **744** (its closing brace), not the 745 the spec said — edit site 10 and §2.1 updated. Recorded rather than silently kept, per the ADR §8 discipline: a reviewer's claim is evidence to check, not a finding to apply |
| **m2** "grants any `browser_*`" is ambiguous under a global `ask` | **FIXED** | Predicate pinned to **"resolves `allow` for at least one `browser_*` tool"** in US-11/AC3, S-46, §10 order 4 and the dataset row. The set is `{Jim, Ray, Explorer, Researcher, Worker}` regardless of how C1 is decided |
| **m3** §11(b)(iii) leans on an unpinned property | **FIXED** | S-43 extended to all six names behaviourally; §11(b)(iii) rewritten to cite that guard. Reasons (i) and (ii) still carry the decision alone |
| **m4** S-30's Given implies a per-context `IsControlled` that does not exist | **FIXED** | Given phrased against `IsControlled(defaultSessionID)` — verified hardcoded at `tools.go::controlledResult` — with the **D1 FR-002c** dependency named, and a note that the key follows D1 when it lands |
| **m5** SC-002's "sixteen" carries the same unstated caveat "seventeen" did | **FIXED** | SC-002 restated: seventeen seeded; sixteen registered, one of those (`browser_evaluate`) inert unless `sandbox.browser_evaluate_enabled`; one (`browser_upload_file`) seeded but unregistered |
| **m6** A-3 blocks the first stream, not "only the fixture edit" | **FIXED** | Stream E split into **E1** (policy + catalog, unblocked, lands first) and **E2** (tier fixtures, gated on A-3). Sequencing restated as **E1 → registrations → E2**. §15 item 1 and §12 A-3 both corrected |
| **m7** upload dataset misses non-existent and directory paths | **FIXED** | Two rows, each naming **which layer rejects**: `ResolvePath(FSOpWrite)` has write semantics and may admit a non-existent path, so the **tool** must `Stat` the resolved path and refuse before `SetUploadFiles` — otherwise the agent gets an opaque CDP error |
| **m8** "all 30 addressed" is 28 true and 2 mislabelled | **FIXED** | §14.1's header now reads **28 FIXED / 1 WITHDRAWN / 1 RELOCATED**; M2's and M10's rows relabelled with the reason in each cell |
| **m9** a stated concurrency invariant has no concurrent test | **FIXED** | **S-59** + `_ConcurrentHandlesIssueOneCDPCall` (§10 order 15): two goroutines, one dialog, assert **one** `HandleJavaScriptDialog` **at the CDP seam**. Dataset row. The sequential S-36 passes with the map clear on the wrong side and therefore never tested it |
| **O1** `FSOpWrite` makes the audit trail record a write the gateway never performs | **ACCEPTED** | FR-031's event gains `fs_op` and `fs_op_reason`; §6.1's row and a dataset row carry them |
| **O2** §2.2a holds up under independent check; `ListenTarget` runs before the ignored-event switch | **ACKNOWLEDGED, no change needed** | The observation confirms §2.2a and additionally verifies the thing §2.2a depends on but does not claim (`runListeners` fires before `target.go`'s ignored-event switch, so the dialog event reaches a per-tab listener). Recorded here so the corroboration is not lost; order 0c remains the pin |
| **O3** citation reliability is high; the FR-036 oracle was **run** | **ACKNOWLEDGED, and one consequence taken** | The reviewer ran FR-036's set comparison — four-set union = 87 = `allStaticToolNames`, zero symmetric difference in both directions — so `TestManifestTierPartition_CoversRegisteredBuiltinCatalog` is **satisfiable today**, not merely asserted. Noted in §9's FR-036 row. **The consequence, and it cuts against complacency:** high citation reliability across two rounds is exactly the condition under which the ADR's §8 failures happened, so revision 3 re-derived rather than trusted — and that is how m1 was caught |

---

## 15. What still needs an operator ruling before implementation

Still exactly **one** genuinely open ruling. Numbered 1–6 across all categories so nothing is lost between them.

### Open — needs a decision before the affected work starts

1. **§12 A-3 — `browser_snapshot`'s manifest tier. THE one open item.** Option A (Tier 3, zero production edits, but the "default way to read a page" is search-only) versus Option B (Tier 2, one production edit, one preview line's tokens on **every** turn, and an amendment to ADR-071's deliberately-sized previewed set). **Recommendation: Option B.**
   - **Whose ruling it is.** Option B amends **ADR-071's** previewed set, so it needs **ADR-071's ratifier**, not this spec's approver. This document does not name that person — naming them is ADR-071's call, and asking the operator to "rule" without saying whether his ruling is sufficient was the round-2 grill's unasked-question 8. **If the operator is ADR-071's ratifier, one decision settles it; if not, this needs routing to whoever is.**
   - **What it actually blocks (corrected — round-2 m6).** Revision 2 said it "blocks Stream E's fixture-literal edit only; every other stream can proceed", while also making Stream E the stream everything else waits on. Both cannot be true. **With Stream E split: it blocks E2 (tier fixtures) only.** E1 (policy + catalog), Stream A's seam, C's plumbing, and B's and D's tool bodies all proceed. Registrations depend on **E1**, which is unblocked.
   - **Nothing downstream assumes an answer.** Edit sites 9-12 carry both literal sets; no FR, scenario, dataset row or success criterion names a tier for the snapshot. A later reader who finds one has found a defect.

### Cross-spec — D1 must act, and D2 cannot

2. **D1 §14.2 rule 3's exemption set is D1's to settle, and it is not settled.** *(Rewritten in revision 3 — round-2 **M1** was right that revision 2's version was stale, and the correct disposition is "downgraded", not "closed".)*
   **What changed:** commit `335d56fe` — whose own subject is *"widen D1's lease exemption to five"* — amended `browser-workspace-ownership-spec.md` §14.2 rule 3 to *"a closed, named set of **five**"*, adding `browser_snapshot` and `browser_handle_dialog` with rows citing D2. Verified at HEAD. Revision 2 quoted the superseded "three" text and declared D2's Streams C and D blocked on it, which would have cost a round trip and invited a duplicate amendment.
   **Why this is still a live dependency rather than a closed one — three defects, all D1's:**
   - **(a) The set is five and should be six.** D1's own round-2 review (**CRIT-104**) found `browser_list_tabs` (`tabs.go:28`, registered at `register.go:76`) is registered, read-only, and **in neither category** — so D1's registry-driven `TestWriteLease_EveryActionToolIsLeased` requires it to take the **write** lease, and Jim's `browser_list_tabs` — the literal call in D1's own headline scenario — would defer behind an unrelated agent's `browser_navigate`. **That is D1's defect to fix**, and D2 must not write a spec that assumes a set D1 is about to change.
     **Status at the time of writing: being fixed concurrently, uncommitted.** The D1 spec in this worktree carries **unstaged** edits taking the set to **six** (adding `browser_list_tabs`, with a new AC4a explaining the omission) and restating D2's contribution as *"four leased, two exempt"* — which agrees with this document's M2 correction. **Recorded as in-flight, not as done:** the change is not committed, D2 cannot verify a moving file, and this item closes when D1's revision lands.
   - **(b) D1's own disposition table still says "three".** Its **MAJ-008** row reads *"the exemption is a closed set of three"* and *"D2's five new action tools"*. A reader who greps D1 for the number finds the wrong answer in one place and the right one in another. *(D1's in-flight revision appears to address this too — its own §16 now lists "five/three/'five D2 tools'/'four D2 tools' — four different figures" as a correction.)*
   - **(c) D1 cites the wrong D2 requirement for the snapshot, and this one is NOT yet fixed.** D1 cites the snapshot's exemption as *"**D2 FR-018**"* — which is the *values-by-default* requirement and says nothing about the lease. Its in-flight AC4 still reads `D2 FR-018`. **Revision 3 creates FR-038 precisely so there is a correct citation.**
     > **⚠️ Disambiguation, because these two documents have now collided on a number.** **D1 has its own FR-038** — a `max_browsers` cap on the browser pool, unrelated to anything here. The citation D1 needs is therefore **"D2 FR-038"**, written with the document prefix, exactly as it already writes "D2 FR-035". A bare `FR-038` inside D1 means D1's own cap requirement and would be actively misleading. **This spec does not renumber to dodge the collision** — FR numbers here are cited by both grills and by D1, and the no-renumbering rule is what keeps every cross-reference resolving; the prefix is the fix.
   **Required of D1, then:** land its exemption-set revision (six), reconcile its MAJ-008 row, and re-point the snapshot citation at **D2 FR-038**. **D2's obligation is complete** — it states both exemptions (FR-035, FR-038), each with its own argument, scenario and test. *(Round-2 M7's ordering constraint is separate and lives in §3's Parallelization: D1's `leaseWrite` must exist before S-31b can be written, and S-31a covers the wedge-recovery half meanwhile.)*

### Decided here, and cheap to reverse — flagged so the operator can overrule

3. **§12 A-16 — `browser_upload_file` resolves `deny` for Mia and Ava** (new in revision 3). The ruling's global `ask` is seeded exactly as directed; the two agents that hold **no** browser tool override it downward through their own least-privilege default, which is the same mechanism giving them `deny` on the other five. **The ADR's D2.9 table says `ask` for them and its own next paragraph implies `deny`** — that contradiction predates this spec and needs an ADR erratum either way. **Reversing:** add `"browser_upload_file": ask` to Mia's and Ava's override maps (§11 edit site 6a) and flip S-25, S-48, US-8/AC3, US-12/AC3 and §10 order 4. Two lines of seed data, no code.
4. **§12 A-9 — `FSOpWrite` for `browser_upload_file`.** The named alternative is `FSOpSend` alone with tool policy as the sole gate. Reversing is a one-line change plus a test-fixture path.
5. **§12 A-12 — `browser_handle_dialog: allow` with `accept` defaulting to `false`.** Reversing to `ask` for the research agents is a seed-map edit with no code change.

### Outside this spec's control, and must not be forgotten

6. **Issue #659** (`AutoDenyAsk` not inherited by delegated subagents; OPEN, `priority:P1-high`, `area:agent-loop`). FR-029 holds `browser_upload_file`'s registration until it lands. **Two things ride on it, and revision 3 promotes the second from a note to a stated prerequisite (round-2 unasked-question 5):**
   - **(a) the inheritance itself** — without it a delegated worker hitting an `ask` blocks on an approval nobody can answer. §10 order 0b is the oracle, skipped until #659 closes.
   - **(b) the approval prompt's *content*** — the resolved path and the target origin. Both are already in FR-031's audit fields, so the data exists; whether the prompt renders them is an approval-flow change this spec does not own. **An `ask` that names only the tool is a yes/no with no facts, which is not the control the operator ruled for.** This spec cannot bind #659's scope: if (b) is declined there, it must be declined **explicitly**, and §12 B-3 updated to record that the `ask` ships without facts.

---

**Next:** re-grill this revision, rule on **A-3** (routing it to ADR-071's ratifier if that is not the same person), then implement — **Stream E1 first** (names + policy, no behaviour, unblocked), then A as critical path, then B/C/D/F in parallel, **E2 once A-3 is ruled**, then the 7-reviewer gate, then SC-001 as the headline demonstration (part 2 blocked on #659).

**Three things a reviewer of revision 4 should check first**, because they are where this revision spent its judgement rather than merely its edits:

1. **§11 footnote ³ and §12 A-16** — the coverage-vs-resolution distinction is the whole of C1, and the decision it produces (Mia/Ava = `deny`) contradicts the ADR's D2.9 table. If that reading is wrong, S-25, S-48, US-8/AC3, US-12/AC3, order 4 and edit site 6a all move together.
2. **§14.2's m1 row** — one round-2 finding is **rejected**. The evidence is a line-range re-derivation anyone can repeat in ten seconds (`sed -n '667,681p' pkg/tools/manifest_test.go`). If the rejection is wrong, say so with the same specificity.
3. **§12 B-6** — resolving M6 surfaced a pre-existing defect nobody filed: the Audit Log viewer already throws on two shipped dotted event names. D2 avoids compounding it and does not fix it. Confirm that is the right boundary, or route the fix.

---

## Relationship to issue #509 / ADR-048 Option B — MUST be closed when this lands

**This work implements what #509 describes.** That issue — *"Per-agent browser
isolation compatible with WebRTC capture (ADR-048 Option B)"* — specifies
"one Chrome instance (own user-data-dir) per browser-capable agent, so each
agent's tabs sit in that instance's own default context (capturable) while
remaining isolated". That is this design, with **workspace** where #509 says
**agent**.

**#509 was closed `NOT_PLANNED` on 2026-08-19.** This work revives a
deliberately declined option, which is a reversal that must be visible rather
than implicit.

**Required actions, both of them:**

1. **Reopen #509 now** (or file a successor referencing it) so the trail is
   honest while this is built. A design that silently revives a not-planned
   issue leaves the next reader unable to tell whether the decline still holds.
2. **Close #509 when this lands**, citing the implementing PR. It is the
   issue this work completes, and leaving it closed-as-not-planned after
   shipping the thing it asked for makes the tracker lie in the other
   direction.

**One piece of evidence from #509 that strengthens this design and was not
previously recorded here:** CDP-created browser contexts fail capture for
**two** independent reasons, not one — `chrome.tabCapture` returns "Invalid tab
specified", **and** `chrome-extension://` pages will not load in them at all
(`ERR_BLOCKED_BY_CLIENT`). Both verified on real Chrome 150 (commit
`687c7c6e`). The second reason is the more final of the two.

**And one existing constraint #509 records that this design must reconcile
with:** today's v1 is "fenced to effectively single-browser-agent use — capture
start is denied when another agent has live tabs". A pool of N browsers changes
that fence's meaning. **Not yet verified against code in this document** — the
adjacent comment is `pkg/tools/browser/capture_session.go:839`; the precise
enforcement path must be found and reconciled before implementation.
