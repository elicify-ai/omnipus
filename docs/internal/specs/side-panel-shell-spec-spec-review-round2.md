# Adversarial Review: Workspace side-panel shell (shared, resizable) — round 2

**Spec reviewed**: `docs/internal/specs/side-panel-shell-spec.md` (commit `851e10429`, fix round 1 applied), plus the interview output `docs/internal/specs/spec-side-panel-shell.md` (Decisions Log SP-1..SP-24)
**Review date**: 2026-09-26
**Reviewer**: grill-spec, round 2 (read-only). Code claims checked by direct reads on `feat/resizable-side-panels` (merge base with `release/v0.1.1` = `d81bcb1ec`). GitNexus was not used; every "Verified" claim names the file it was read from.
**Round detection**: `side-panel-shell-spec-spec-review.md` (round 1) exists → this is round 2.
**Mode**: plan-spec (BDD scenarios, FR-xxx, SC-xxx, traceability matrix all present).
**Verdict**: **REVISE**

## Executive Summary

Fix round 1 closed the round-1 data-loss path and most of its majors, but it introduced new defects and left
several old ones half-fixed. The largest are: (1) the Browser panel's identity is a session, not a workspace,
so the new handle registry, presence channel and window names collide across browser sessions — and a
fixed window name still re-navigates (blanks) an existing live-browser tab once the handle is lost, which
is round-1 MAJ-002 coming back by a side door; (2) the geometry numbers still contradict each other, and the
45% default is not capped by the new chat floor, so on 1024–1119px screens with the sidebar pinned the default
squeezes the chat below 360px; (3) "history REPLACE" does not give "Back behaves exactly as today", because
older history entries keep stale `panel` values; (4) the Storybook-only demo cannot execute half the §9
click-test rows (no router, no full-page routes, no API mocking in this repo's Storybook). No finding is a
data-loss or security incident, so the verdict is REVISE, not BLOCK.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 13 |
| MINOR | 11 |
| OBSERVATION | 3 |
| **Total** | **27** |

Certainty legend: **Verified** = read in code or computed from the spec's own numbers (evidence named);
**Inferred** = logical from code or platform behaviour, not executed; **Unknown** = could not establish.

---

## Findings

### MAJOR Findings

#### [MAJ-201] The Browser panel's identity is a session, but the registry, presence and window name key it by workspace

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: §8.1 (`context` for Browser = `sessionId, agentId`), §8.3 (registry and presence keyed `panelId × workspaceId|app`; window name `omnipus-panel-<panelId>-<workspaceId|app>`), US-3 ("Browser from a global screen uses the `app` bucket"), FR-005, FR-009
- **Description**: The Browser panel's context has no `workspaceId` (§8.1; today `src/store/ui.ts::UiStore.browserPanel` is `{sessionId, agentId}` — **Verified**). Every Browser key therefore falls into the `app` bucket, whatever screen it was opened from:
  - Registry and presence: Expand for agent X's session, then "Watch live" / Expand for agent Y's session → the registry hit on `browser × app` focuses **agent X's** tab and posts Y's context to it, or presence says "already open" and nothing opens for Y.
  - Window name: every Browser pop-out is named `omnipus-panel-browser-app`, so a second pop-out for a different session reuses (navigates) the first one (see MAJ-202).
  - Width memory: US-3 implies a Browser opened inside a workspace uses that workspace's bucket; the key rule in §8.1 (`context.workspaceId`) makes it always `app`. The two statements disagree.
- **Impact**: Operator with two agents' live browsers loses the first viewer or is sent to the wrong one; width memory behaves differently from what US-3 promises.
- **Recommendation**: State per panel what its identity key is: Browser = `browser × sessionId × agentId` for registry, presence and naming; the width bucket = the session's workspace (known server-side per ADR-075 "workspace-scoped browser sessions", or `app` when the chat has none) — pick one and write it into §8.1 and US-3. Add a dataset row: two sessions, two agents, same workspace.

---

#### [MAJ-202] A fixed window name still navigates the existing tab when the handle is lost — MAJ-002 returns by a side door

- **Lens**: Incorrectness
- **Affected section**: §8.3 fact 1 ("a stable per-workspace+panel window name is still used for NEW opens … never as a reuse mechanism"), §8.3 "A handle is lost when the source tab reloads … falls back to BroadcastChannel presence", 150ms bound
- **Description**: The browser does not care what the spec intends the name for: `window.open(url, name)` with a name that an existing tab already carries **navigates that tab** (the very fact §8.3 cites from MDN). The fallback path reaches exactly that call: source tab reloads (handle gone) → toggle/Expand → presence ping → the other tab does not answer within 150ms (backgrounded, frozen or discarded by the browser's memory saver) → "opens normally" → `window.open(url, 'omnipus-panel-browser-app')` → the live-browser tab is navigated to `about:blank` (today's Browser opens `about:blank` first — `BrowserLivePanel.tsx::BrowserLivePanel.handlePopOut`, **Verified**) and its viewer is torn down. For a Library tab with unsaved edits the navigation fires that tab's `beforeunload` prompt in the background.
- **Impact**: The round-1 teardown defect is still reachable after any reload of the source tab. **Inferred** (high confidence: the platform rule is the one the spec itself quotes).
- **Recommendation**: Drop the stable name entirely. New opens use `'_blank'` (Browser already does, today); identity travels in the presence message, not in `window.name`. Delete "identity label and collision guard" from §8.3 and the SP-18/§18 wording that still describes name-based re-focus (see MIN-210).

---

#### [MAJ-203] The geometry numbers still contradict each other, and the default width breaks the chat floor

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: §7 Geometry ("Default width MUST be 45% of the row clamped to [320px, 720px]"), US-2, US-3 AS-6, §11 scenarios "Drag resize clamps at both bounds", "Double-click resets to default", "Window shrink re-clamps the open panel", "Keyboard resize full walkthrough", width-memory outline, §12 width-bounds dataset rows 4 and 9
- **Description** (all **Verified** by arithmetic on the spec's own formula `min(0.70·row, row − sidebar − 360)`):
  1. *Default vs ceiling.* The default is clamped to [320, 720] only, never to the SP-17 ceiling. With the sidebar pinned (256px), `0.45·row > row − 616` for every row below 1120px. At 1024px: default = 461px, ceiling = 408px → the chat gets 1024 − 256 − 461 = 307px, below the 360px floor that §7 also calls a MUST. Two machine-verifiable constraints contradict on the most common laptop widths.
  2. *Stale 70% numbers.* "Window shrink re-clamps" says 1000px → 700px; by SP-17 it is 640px (dataset row 4 says 640). US-3 AS-6 and dataset row 9 also say 700px at 1000px. "Keyboard walkthrough" says End → "the 70% bound". "Double-click resets" says "45% of the **content area**" (round-1 MIN-001's undefined term, marked FIXED in §19).
  3. *Unstated sidebar state.* "Drag resize clamps" expects 980px at 1400px — true only with the sidebar unpinned; pinned it is 784px (dataset row 1). The width-memory outline row "tasks, 950px → 950px (≤70%)" names no window width at all.
- **Impact**: qa-lead writes tests from contradictory oracles; whichever number the implementer picks, a spec-derived test fails.
- **Recommendation**: Define `default = clamp(0.45·row, 320, min(720, ceiling))`. Rewrite every BDD scenario and dataset row with explicit row width and sidebar state, recomputed from SP-17: 1000/unpinned → 640; row 9 → "640 at 1000"; End → "the SP-17 ceiling"; "content area" → "row". Add dataset rows 1024/pinned (default 408) and 1119 vs 1120/pinned.

---

#### [MAJ-204] The overlay mode serves a 40-pixel window band and hides most of the chat it claims keeps working

- **Lens**: Overcomplexity / Incorrectness
- **Affected section**: SP-17, US-2, §6 "Window too narrow for both floors", §7 Geometry overlay rule, dataset row 7, FR-003
- **Description** (**Verified** from `src/store/sidebar.ts::SIDEBAR_PIN_BREAKPOINT` = 1024, "Below it the sidebar is always overlay regardless of the persisted preference", and `--spacing-sidebar: min(256px, 80vw)` in `src/styles/globals.css`):
  - Overlay starts when `row − sidebar < 680`. Below 1024px the sidebar is never in the row (sidebar term 0), so overlay happens only at 640–679px. At ≥1024px with the sidebar pinned, `row − 256 ≥ 768 > 680`, so overlay never happens. The whole overlay mode — layout state, tests, click-test row 3, the "not the retired overlay" carve-out in §7 and §17 — exists for a 40px band of window widths.
  - Inside that band the promise "the chat keeps the full row and stays interactive" does not hold in practice: at 640px the overlay is up to 448px wide (dataset row 7), leaving 192px of visible chat; the panel sits on the right, where the composer's send control and the transcript's scrollbar are.
- **Impact**: Significant build and test cost for a case almost nobody hits, and when it is hit the chat is mostly covered anyway.
- **Recommendation**: Founder question (SP-17 was a team-lead recommendation "not objected", not a founder-originated rule): **Q1** — (A, recommended) move the phone-takeover breakpoint to 680px and delete overlay mode; (B) keep overlay but cap it so the chat's composer stays visible (e.g. overlay ≤ row − 360); (C) keep as is. Whichever is chosen, state what "interactive" means for the covered part of the chat.

---

#### [MAJ-205] Transitions that start in the URL cannot be gated "before touching the URL"

- **Lens**: Infeasibility / Ambiguity
- **Affected section**: §7 State machine ("a false cancels it (no state change, URL unchanged, panel stays)"), §8.1 ("The shell awaits it BEFORE touching the store, URL or content"), §8.2 Source of truth ("One model, no dual authority"), US-4 AS-3/AS-4, BDD "Deep-link replace with an unsaved Library edit prompts", "Workspace switch with an unsaved Library edit prompts"
- **Description**: Deep-link replace, a pasted URL in the same tab, Back/Forward, and workspace switch (a sidebar click navigates to `/workspaces/B/chat`) all change the URL **first**; the store learns about it afterwards. "Await before touching the URL" is impossible for these, and "no dual authority" is false: the URL writes the store on these paths, the store writes the URL on the others. The spec does not say whether these navigations are **blocked** (the repo already has the mechanism: `src/routes/_app/library.tsx::LibraryRoute` uses TanStack `useBlocker` with `confirmDiscardLibraryEdits`, **Verified**) or **allowed then reverted**. The workspace-switch BDD ("a cancel keeps the Library on workspace A") does not say whether the route also stays on A.
- **Impact**: Two reasonable builds diverge: one blocks navigation (route stays on A), one lets it through and leaves a workspace-A Library beside a workspace-B chat, with the URL and store disagreeing.
- **Recommendation**: Add to §8.2: "URL-initiated transitions are gated by a router blocker (`useBlocker`, precedent `library.tsx::LibraryRoute`) that runs the outgoing panel's `beforeLeave`; on cancel the navigation does not happen (route, URL and panel unchanged)." Replace "one model, no dual authority" with the actual two-direction rule. State the workspace-switch cancel outcome explicitly.

---

#### [MAJ-206] Replace-only history does not make Back behave "exactly as today" — older entries keep stale panel values

- **Lens**: Incorrectness
- **Affected section**: SP-22, US-7 AS-7, §7 URL, §8.2 Cross-route navigation, FR-010, BDD "Panel toggles leave browser history untouched"
- **Description**: History REPLACE rewrites only the **current** entry. Sequence: on workspace chat, open Library (entry E2 becomes `…chat?panel=library`) → go to Settings (push E3) → come back to chat and close the panel → Back twice lands on E2 → the URL says `panel=library` → the deep-link restore rule opens the Library the operator closed. Today Back never opens or switches a panel (panels are not in the URL at all). Same for Forward, and for switching workspaces in between. **Inferred** (high confidence: follows from the spec's own restore rule).
- **Impact**: Back reopens or switches panels unexpectedly, and with a dirty Library it raises the discard prompt on a Back press — the opposite of SP-22's promise.
- **Recommendation**: Specify the popstate rule: either (A, recommended) on Back/Forward the store wins — the `panel` param of the restored entry is ignored and immediately re-projected from the store (replace) — or (B) the URL wins and SP-22's wording changes to "Back restores the panel state that entry had". Add a BDD scenario for the sequence above.

---

#### [MAJ-207] Reload and deep links cannot restore the Browser panel, because its session is not in the URL

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: US-7 AS-1 ("Given **any** panel open … reload … the same panel restores"), §6 "Reload with `?panel=browser` and a valid session: the Browser panel restores", §8.2 ("The workspace Chat route is the only route whose search schema declares `panel`"; "`panel=browser` WITH session/agent context restores normally"; "none in session state"), SP-23 `&agent=…` for Mail
- **Description**: The projection writes only `panel`. The chat route today declares no search schema at all (`src/routes/_app/workspaces.$workspaceId.chat.tsx`, **Verified**), and the spec adds only `panel`. So after a reload the URL is `…chat?panel=browser` with no session, the store is empty, and "session state" is undefined (sessionStorage? the chat session store?). By SP-21 the param is then dropped. US-7 AS-1 and §6 promise the opposite. Mail's `&agent=…` is likewise undeclared.
- **Impact**: Either reload silently loses the Browser panel (contradicting AS-1) or an implementer adds `session`/`agent` to the URL on their own, putting session ids into every shared chat link.
- **Recommendation**: Decide and write it down: (A) the chat route also declares `session` and `agent` (Browser) and `agent` (Mail), written by the projection — and then MIN-009's shared-link test is mandatory; or (B) Browser is excluded from reload restore and US-7 AS-1 says "any panel except Browser". Define "session state" or delete it.

---

#### [MAJ-208] The pop-out re-dock rule regresses a UAT fix and fires in every open tab

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: §6 "Browser pop-out close re-docks only into an EMPTY panel slot", §7 State machine ("Pop-out re-dock fires only when no panel is open"), test 8c, regression dataset row 6, §19 MAJ-006
- **Description** (**Verified**):
  - `LibraryPanel.tsx` module doc, "UAT fix (Dana, re-verified v8)": re-dock was made **unconditional** because the "only when nothing is docked" guard *was* the bug — the operator re-opens the docked Library, navigates the pop-out to workspace B, closes it, and expects the docked Library to follow to B. The spec's rule ("only when NO panel is open") restores the Dana bug when the open panel is the Library itself. If instead the same-panel case updates the open Library, that is a workspace re-target that remounts the explorer (`key={libraryPanel.workspaceId ?? 'root'}`) and needs the guard — the spec says "no unmount, no guard needed on this path".
  - `popout-closed` is broadcast origin-wide on `pagehide` by **any** `/library` tab, including one the user opened by hand, and including a reload or navigate-away of that tab (`src/routes/_app/library.tsx`, `src/lib/libraryHandoff.ts`). Every other app tab with an empty slot re-docks the Library. Under SP-18, where manual full-page tabs are a first-class case, closing one pops the Library open in all other tabs.
  - No FR states the re-dock rule; test 8c maps to no FR in the matrix.
- **Impact**: A regression of a verified UAT fix, plus surprise panels appearing across tabs.
- **Recommendation**: Only the tab that opened the pop-out (holds the handle — the Browser's existing `watchPopoutClosed` pattern) re-docks. Same-panel case: follow the pop-out's last workspace (Dana), through `beforeLeave`. Different panel open: no-op. Add an FR (e.g. FR-018) and map 8c to it.

---

#### [MAJ-209] Expand's failure path and its user-gesture timing are unspecified for every panel except the Browser

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: FR-008, FR-009 ("re-invoking its toggle **or Expand** … via BroadcastChannel presence (150ms bound)"), §5 Error flows (only the Browser's toast is preserved), §8.3 fact 2
- **Description**:
  - Today the Library opens with `'noopener,noreferrer'`, so `window.open` always returns null and a blocked pop-up is invisible; C4 then closes the docked panel regardless (`LibraryPanel.tsx::LibraryPanel.handlePopOut`, **Verified**). The spec removes `noopener`, so a block becomes detectable — but it never says what happens: toast and keep the panel (Browser's behaviour), or close anyway. As written (FR-008 "MUST … close the docked panel"), a blocked pop-up leaves the operator with no Library at all.
  - The Browser opens its tab synchronously inside the click, with the comment "A trusted blank tab preserves the synchronous user gesture" (**Verified**). If Expand waits up to 150ms for a presence reply before `window.open`, browsers with a short user-activation window may block the pop-up. **Inferred** (medium confidence; the length of the activation window differs by browser).
- **Impact**: Panels silently disappear on pop-up-blocking browsers; the presence wait can itself cause the block.
- **Recommendation**: Add to §5 Error flows and FR-008: "If the new tab cannot open (`window.open` returns null or throws), show the existing error toast and keep the docked panel open — for every panel." Specify ordering for Expand: registry check (synchronous) → `window.open` immediately; the presence wait applies to toggle clicks only (or keep a continuously maintained presence list so no wait is needed — OBS-202).

---

#### [MAJ-210] The Storybook-only demo cannot execute half of the §9 click-test list, and the wave-1 E2E plan names panels that do not exist yet

- **Lens**: Infeasibility
- **Affected section**: SP-16, §9 (stories inventory, click-test rows 1, 5, 8–13, 15), SC-001, §12 test 16, FR-016
- **Description** (**Verified**):
  - This repo's Storybook (`.storybook/main.ts`, `preview.tsx`) has no router decorator and no query client; `package.json` has no request-mocking library (no `msw`); 33 of 36 stories are `src/components/ui/` primitives. The spec requires "real explorer on fixture data (no gateway)" — the Library explorer is query-driven — with no mocking seam named, and §17 says "no new deps".
  - Rows 1 ("URL has `?panel=library`"), 5 (workspace switch), 8–11 (Expand opens the full-page Library route in a new tab; re-focus; manual tab), 12–13 (paste a URL into a fresh tab) need the app's hash router and its full-page routes; a static Storybook build serves `iframe.html?id=…`, not `/#/library`.
  - Row 15 needs the Browser in "driving" state to show that Escape releases driving; the Browser story is a "static placeholder view (no live WebRTC required)", which has no driving state.
  - Test 16 automates rows 6, 7, 12, 14 and 16 in wave 1, but those rows use Tasks, Calendar and Mail, which are unregistered in wave 1 — by the spec's own rule `?panel=calendar` is dropped, so row 12 must fail against the wave-1 app.
- **Impact**: SC-001, the gate before any build, is either unpassable or will be "passed" with rows quietly skipped — the false-green pattern the repo rules warn about.
- **Recommendation**: Split the demo checklist: rows runnable on Storybook (resize, clamps, reset, one-at-a-time with stand-ins, phone, keyboard, landmark) vs rows that need the real app (URL, new tab, deep links, Browser driving) — the latter verified in wave 1 against the running app. Name the Storybook harness the stories need (memory router decorator, fixture query client) and whether a mocking dependency is allowed. Re-scope test 16 per wave: wave-1 rows use Library/Browser only.

---

#### [MAJ-211] SC-004 cannot pass: logging in drops the shared link

- **Lens**: Incorrectness
- **Affected section**: SC-004 ("A shared link `…chat?panel=<id>` restores the named panel on a machine that has never visited the app"), US-7 AS-2
- **Description** (**Verified**): `src/routes/_app.tsx` redirects an unauthenticated visit to `{ to: '/login' }` with no return address, and `src/routes/login.tsx` navigates to `'/'` after sign-in. A machine that has never visited the app has no session, so the colleague lands on the default workspace's chat with no panel — and not even in the linked workspace.
- **Impact**: A success criterion that fails against today's code, or pressure to "prove" it on an already-logged-in machine.
- **Recommendation**: Either (A) add a wave-1 requirement that the login redirect preserves the original hash URL (path + search) — a real, small change that also fixes workspace links generally — or (B) reword SC-004 to "on a machine already signed in". **Q2** for the founder (A / B), recommendation A.

---

#### [MAJ-212] "Workspace switch re-targets the open panel" is undefined for the Browser and an undeclared behaviour change for the Library

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: US-3 AS-5, §6 "Panel open + workspace switch", BDD "Workspace switch re-targets and re-widths the open panel", §12 "three intentional behaviour changes"
- **Description**:
  - Browser: a browser session is bound to a workspace server-side (ADR-075 "workspace-scoped browser sessions"; `ChatControls.tsx` comment "the panel … resolves which workspace's browser … by reading the workspace off this very session's meta", **Verified**). "Re-target to the new workspace's content" has no meaning for it: close it, keep showing workspace A's session, or start a session in B (a paid session, which SP-21 forbids from links)?
  - Library: today the docked Library does not follow a workspace switch — its store slice keeps the open-time workspace, and the explorer can browse other workspaces or the virtual root internally (`LibraryPanel.tsx`, `onWorkspaceChange`, **Verified**). Re-targeting it on switch is a new behaviour that yanks an operator out of the folder they were browsing, and it is missing from the §12 list of intentional behaviour changes that get regression tests.
  - The width key uses `context.workspaceId` (open-time); the Library may be showing another workspace by then.
- **Impact**: Three plausible builds for the Browser; an unlisted behaviour change for the Library.
- **Recommendation**: Per panel, state the workspace-switch rule (suggested: Browser stays on its session and keeps its bucket; Library follows only if it was opened scoped to the outgoing workspace, not from the virtual root; workspace panels follow). Add the Library rule to the intentional-changes list with a regression test. State which workspace the width key uses when the Library's content workspace differs from its open-time context.

---

#### [MAJ-213] Which entry points "switch to the open tab" is not defined, and the registry goes stale when the full-page tab navigates

- **Lens**: Ambiguity
- **Affected section**: SP-18, US-6, §5 ("When a panel's **toggle** is clicked …"), FR-009 ("toggle or Expand"), §8.3
- **Description**:
  - SP-18 is stated for the toggle (and Expand). The sidebar "Library", the top-bar "Open library", "Watch live" and a deep link are not toggles. Do they also switch to the already-open tab, or open the panel docked? Today the founder-approved C4 note says "nothing stops the operator re-opening the docked panel (sidebar …) while the fullscreen tab stays open" (`LibraryPanel.tsx`, **Verified**); the spec may remove that silently.
  - The registry key is the open-time workspace. The full-page Library tab can navigate to another workspace (it announces `workspace-changed` continuously). After Expand for A and navigating the tab to B, clicking the Library toggle in A focuses a tab showing B, and "posts the current context" — does that yank the tab back to A?
- **Impact**: Inconsistent behaviour across entry points; the per-workspace promise breaks after the first in-tab navigation.
- **Recommendation**: Add a table: entry point × "full-page tab for same scope exists" → behaviour. Key presence on what the full-page tab currently shows (it already announces it), and state what the context post does to a tab showing a different workspace.

---

### MINOR Findings

#### [MIN-201] Round-1 MAJ-007's visibility half is still open although §19 marks it FIXED
- **Lens**: Inconsistency
- **Affected section**: §9 rows 1, 6, 7; US-5; §19 MAJ-007
- **Description**: The tab strip collapses to the dropdown when the chat column's top bar is below 1152px (`WorkspaceTabContainer.tsx` `@container`/`@6xl`, **Verified**). With a pinned sidebar and a 320px panel, the full strip needs a ≥1728px window, so the "entry pressed" state is inside a closed dropdown on most screens. Round 1 asked for the click-test rows to name the viewport and add dropdown variants; they still name no viewport.
- **Recommendation**: Give every §9 row a viewport width; add dropdown-variant rows; change §19's disposition to what was actually done.

#### [MIN-202] Traceability holes
- **Lens**: Incompleteness
- **Affected section**: §11, §13 matrix, §8.2 MIN-009 bullet, FR-001
- **Description**: No BDD scenario for US-1 AS-4 (all six panels identical), the cross-user half of US-3 AS-2, US-5 AS-2, AS-3 (pressed state moves) or AS-6 (test ids persist). The MIN-009 cross-account test ("a second account … sees the visible denial") has no US, no BDD and no test row — it points to "test-16 scope", but test 16 is the §9 list, which does not contain it. Test 8c maps to no FR (see MAJ-208). FR-001 is tagged `[wave 1]` but requires all six panels.
- **Recommendation**: Add the missing scenarios and a numbered test for the cross-account check; re-tag FR-001 "[wave 1 shell; panels per wave]".

#### [MIN-203] Boundary rows missing, and two geometry statements are wrong
- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §12 width-bounds dataset, US-8 AS-3, US-2 ("0 when it is not pinned, i.e. below its 1024px breakpoint")
- **Description**: No rows at 679/680 (overlay switch), 1023/1024 (pin honoured), or pinned-vs-unpinned at ≥1024 — the sidebar can be unpinned at any width (`sidebar.ts` `isPinned` is a user preference, **Verified**), so "i.e. below 1024" is wrong. US-8 AS-3 says crossing 640px "docks at its remembered width"; at 640–679px it overlays.
- **Recommendation**: Add the rows; reword to "0 when not pinned (always the case below 1024px)"; fix US-8 AS-3.

#### [MIN-204] Width-storage details are inconsistent and partly impossible
- **Lens**: Inconsistency / Infeasibility
- **Affected section**: §7 Persistence ("one namespaced JSON entry"), §8.1 (`panel-width.<user>.<id>.<ws|app>`), FR-005, §19 MIN-004
- **Description**: One JSON entry vs one localStorage key per combination — pick one. The SPA holds no user id, only a display `username` (`src/store/auth.ts`, **Verified**). "Deleted workspaces' entries are pruned on the next write" needs the current user's workspace list at write time and cannot work for another user's entries, yet §19 says "stale other-user keys pruned". No behaviour is stated when storage is full or unavailable (private mode, quota exceeded).
- **Recommendation**: One entry, keyed by the identifier that actually exists (state it); prune only the signed-in user's entries; storage failure = width works for the session, nothing is persisted, no error shown.

#### [MIN-205] Keyboard resizing writes and hands over on every key press
- **Lens**: Incompleteness
- **Affected section**: FR-005 ("keyboard adjustment" writes), FR-015, §7 Performance
- **Description**: Each 16px arrow press is a "user choice" → a storage write and, for the Browser, a remote-viewport handover with its "Resizing browser…" status. Ten presses = ten handovers. The rAF rule covers drag only.
- **Recommendation**: Persist and hand over when keyboard input settles (e.g. 300ms after the last key), same as drag release.

#### [MIN-206] "Open browser" needs a network call, contradicting §7, and runs before the leave guard
- **Lens**: Inconsistency
- **Affected section**: §7 ("must not gate panel open/close on any network call"), US-4, FR-013
- **Description**: With no active session, "Open browser" calls `createSession` before opening the panel (`ChatControls.tsx`, **Verified**). If the Library is dirty and the operator cancels the discard prompt, a session was already created for nothing.
- **Recommendation**: Reword §7 to "no network call in the shell's own open/close"; require the leave guard to run before `createSession` on this path.

#### [MIN-207] Presence and context messages trust any same-origin page, including agent-built apps
- **Lens**: Insecurity (Spoofing, Information disclosure)
- **Affected section**: §8.3 presence protocol and "posts the current context over the existing handoff BroadcastChannel"; no threat-model section in the spec
- **Description**: Agent-built dev apps are served at `/preview/<token>/` on the main listener, same origin as the SPA, opened as a new tab (ADR-044 "Serve /preview/ on the main gateway listener", **Verified**). Such a page can join the SPA's channels: answer every presence ping (the operator's Library toggle then always shows "already open elsewhere"), send fake `popout-closed` messages, and read posted Library selections (file paths). Same-origin pages already have wide power, so this is small — but it is new, and the spec states no validation.
- **Recommendation**: Add a short STRIDE note: validate message shape, never post file paths on the broadcast channel (post to the registry handle via `postMessage` instead), and record the same-origin preview assumption in §17.

#### [MIN-208] Escape rules: one wrong claim, one gap
- **Lens**: Incorrectness / Ambiguity
- **Affected section**: §7 Accessibility, US-9, BDD "Keyboard resize full walkthrough"
- **Description**: The spec says IME input "calls `preventDefault()`" — it does not; composition must be detected with `isComposing` (as `BrowserLiveView.tsx::handleKeyDown` does via `textComposition.nativeKey`, **Verified**). (Radix layers do call `preventDefault` when they dismiss — `@radix-ui/react-dismissable-layer` 1.1.17, **Verified** — so that part holds.) Where focus goes after an Escape close is not in US-9's per-case list; the BDD says "the toggle that opened it". "Focus inside the shell" is undefined for portalled content (DOM tree vs React tree).
- **Recommendation**: Ignore Escape while `event.isComposing`; add Escape to the focus-return list; define "inside the shell" as the DOM subtree of the shell root.

#### [MIN-209] ADRs cited by number only, one ambiguously
- **Lens**: Ambiguity
- **Affected section**: §8.2 ("the ADR-044 session cookie"), §2.3/§7 (ADR-039, ADR-061)
- **Description**: The repo rule is to cite ADRs by title. Three `ADR-044-*` files exist (`live-browser-video-streaming`, `preview-on-main-listener`, `spike-results`); the cookie decision is in the preview one.
- **Recommendation**: Cite by title throughout.

#### [MIN-210] The Decisions Log and §18 still describe the replaced window-name mechanism
- **Lens**: Inconsistency
- **Affected section**: `spec-side-panel-shell.md` SP-18 ("only tabs Omnipus opened under a stable per-workspace+panel window name can be reliably re-focused"), spec §18 ("stable window name for app-opened tabs")
- **Description**: §8.3 replaced name-based re-focus with the handle registry; the founder decision record and clarifications still describe the old mechanism.
- **Recommendation**: Amend SP-18's rationale text (mechanism only — the decision itself is unchanged) and §18.

#### [MIN-211] Reloading a virtual-root Library changes what it shows and where its width is stored
- **Lens**: Incorrectness
- **Affected section**: §8.2 Library bullet, US-7 AS-1, US-3
- **Description**: A Library opened from the sidebar sits at the virtual root (`openLibraryPanel()` with no workspace, `Sidebar.tsx`, **Verified**) and uses the `app` width bucket. The URL projection on a workspace chat is `?panel=library`; on reload it restores the Library scoped to the route's workspace, in that workspace's bucket. AS-1's "the same panel restores" is only half true.
- **Recommendation**: State it as an accepted difference, or project `&scope=root`.

---

### Observations

#### [OBS-201] Rows 8–11 do not need to be manual
- **Lens**: Overcomplexity
- **Affected section**: §12 test 16
- **Suggestion**: Playwright handles multiple tabs natively (`context.waitForEvent('page')`); the cross-tab rows can be automated in the wave-1 E2E suite against the real app, which also removes their dependency on the Storybook demo (MAJ-210).

#### [OBS-202] The 150ms presence wait slows every panel open
- **Lens**: Overcomplexity / Performance
- **Affected section**: §8.3
- **Suggestion**: In the common case (no other tab) every toggle click waits the full 150ms. Full-page tabs could announce themselves on load and on `pagehide`, and each app tab keep a live list, so the click is decided instantly — this also removes the Expand timing problem in MAJ-209.

#### [OBS-203] GitNexus impact still not run
- **Lens**: Inoperability
- **Affected section**: §3.2 (OBS-003 carry-over)
- **Suggestion**: Still pending. Run `impact` on `UiStore`, `AppShell`, `WorkspaceTabBar`, `LibraryPanel` and `BrowserLivePanel` before wave 1 starts, as §3.2 already requires.

---

## Structural Integrity Results (plan-spec mode)

| Check | Result | Notes |
|---|---|---|
| Every user story has acceptance scenarios | PASS | US-1..US-9 |
| Every acceptance scenario has a BDD scenario | FAIL | US-1 AS-4; US-3 AS-2 (cross-user half); US-5 AS-2, AS-3, AS-6 (MIN-202) |
| Every BDD scenario has `Traces to:` | PASS | |
| Every BDD scenario has a test in the TDD plan | PARTIAL | Scenarios using wave-2/3 panels (Calendar, Mail, Tasks) have no wave-1-runnable test (MAJ-210) |
| Every FR in the traceability matrix | PASS | FR-001..FR-017; but the re-dock rule has no FR (MAJ-208) |
| Every BDD scenario in the matrix | PARTIAL | "Pop-out close never clobbers" / test 8c unmapped |
| Datasets cover boundaries and errors | FAIL | Missing 679/680, 1023/1024, 1119/1120, unpinned ≥1024 (MIN-203); rows 4 vs 9 contradict (MAJ-203) |
| Regression impact addressed | PARTIAL | Library workspace-follow change (MAJ-212) and Dana re-dock regression (MAJ-208) missing |
| Success criteria measurable, no subjective language | PARTIAL | SC-004 unachievable as written (MAJ-211); SC-001 depends on infeasible rows (MAJ-210) |

## Test Coverage Assessment

- **Missing test levels**: URL/deep-link/new-tab behaviour is assigned to a Storybook demo that cannot host it (MAJ-210); these need E2E against the running app in wave 1.
- **Missing negative tests**: pop-up blocked for Library Expand (MAJ-209); storage write failure (MIN-204); Back to an entry with a stale `panel` value (MAJ-206); presence reply arriving after the 150ms timeout.
- **Missing boundary tests**: see MIN-203 and the default-vs-ceiling rows in MAJ-203.
- **Missing concurrency tests**: two Browser sessions for different agents (MAJ-201); a transition requested while a `beforeLeave` dialog is already open (the edge case "last open wins … cancels the race loser" has no test and "race loser" is undefined).
- **Idempotency**: repeated Expand with a handle present is covered (test 14); repeated Expand after a source reload is not (MAJ-202).
- **Regression blind spots**: the Dana re-dock UAT fix (MAJ-208), the C4 "re-open docked while the full tab stays open" behaviour (MAJ-213), `library.tsx::LibraryRoute`'s `useBlocker` behaviour.

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| BroadcastChannel presence + context post | Risk | — | — | Risk | Risk | — | Same-origin `/preview/` apps can spoof or read (MIN-207) |
| URL `panel` (+ possibly `session`/`agent`) | — | — | — | Risk | — | — | If session ids join the URL (MAJ-207 option A), shared links carry them; relies on per-user stream authorization (MIN-009 assumption, still untested) |
| Width storage (localStorage) | — | Low | — | Low | — | — | Key identity undefined (MIN-204); no secrets stored |
| Window handle registry | — | — | — | — | Risk | — | Name reuse tears down a live viewer (MAJ-202) |
| Shell state / guard | — | — | — | — | — | — | Guard path sound for store-initiated transitions; URL-initiated ones unspecified (MAJ-205) |

## Unasked Questions

1. **Q1** (MAJ-204): Keep overlay mode for the 640–679px band, or move the phone breakpoint to 680px and drop overlay? (A drop / B cap overlay / C keep) — recommend A.
2. **Q2** (MAJ-211): Should signing in return the user to the link they opened (path + `panel`)? (A yes, in wave 1 / B reword SC-004) — recommend A.
3. On Back/Forward, does the store or the URL decide which panel is open (MAJ-206)?
4. Does a Browser deep link carry `session`/`agent` in the chat URL, or is the Browser excluded from reload restore (MAJ-207)?
5. What does a workspace switch do to an open Browser panel, and to a Library opened at the virtual root (MAJ-212)?
6. Do the sidebar, "Open library" and "Watch live" also switch to an already-open full-page tab, or only the toggle (MAJ-213)?
7. What happens to the docked panel when its new tab is blocked (MAJ-209)?
8. Which harness do the stories use for router and data — and is a request-mocking dependency allowed (MAJ-210)?

---

## Dispatch focus checks (architect addendum)

| Focus question | Answer | Evidence |
|---|---|---|
| Did fix round 1 close CRIT-001? | Yes — no data-loss path survives in store-initiated transitions; URL-initiated ones are MAJ-205 (major, not critical) | spec §7 state machine, §8.1 `beforeLeave` |
| Did it close each round-1 MAJ? | Mostly. Half-closed: MAJ-002 (returns via fixed window name, MAJ-202), MAJ-003 (see below), MAJ-006 (re-dock rule regresses Dana, MAJ-208), MAJ-007 (visibility half, MIN-201) | findings above |
| Does SP-16 "stories are the demo" still satisfy SP-10's real-browser click test? | No, not for rows 1, 5, 8–13, 15 — a static Storybook build has no router, no full-page routes, no data mocking | MAJ-210; `.storybook/preview.tsx` (decorators = `exposeVerificationMetadata` only), `package.json` (no `msw`) |
| Does the SP-17 overlay fallback interact correctly with SP-7 one-at-a-time? | One-at-a-time itself is unaffected (overlay is still one panel). But at 640–679px the overlay covers up to 448px of a 640px row, including the right end of the chat's top bar, so the tab-strip toggles that switch/close panels may be hidden under the overlay — only the panel's own close control remains. Moot if Q1 = A | MAJ-204; spec §7 overlay rule, dataset row 7 |
| Does per-workspace switch-only scoping resolve MAJ-003? | Only for workspace-scoped panels. It leaves a fourth description: the Browser has no `workspaceId` (it is session-scoped), so every Browser collapses into the `app` bucket (MAJ-201); and the key is the open-time workspace, which goes stale once the full-page tab navigates (MAJ-213) | `src/store/ui.ts::UiStore.browserPanel` = `{sessionId, agentId}` |

## Questions for the founder

Only genuine product decisions are listed. Everything else in this report is an implementation
correction the fix round should just make, following the default named below.

**Q1 — Narrow-window overlay (MAJ-204).** Overlay mode only ever triggers for windows 640–679px wide, and there it covers up to 448px of a 640px chat. Options: **A** (recommended) move the full-screen phone takeover up to 680px and delete overlay mode; **B** keep overlay but cap it so at least 360px of chat (the message box) stays visible; **C** keep as specified.

**Q2 — Shared links through sign-in (MAJ-211).** Today signing in always lands on `/`, so a shared `…chat?panel=library` link opened by a signed-out colleague loses both the workspace and the panel, and SC-004 cannot pass. Options: **A** (recommended) wave 1 adds "sign-in returns you to the link you opened"; **B** reword SC-004 to "on a machine already signed in".

**Q3 — Browser panel after reload / in shared links (MAJ-207).** Only `panel` is in the address, so a reload cannot restore which agent's browser was open. Options: **A** (recommended) exclude the Browser from reload restore (it reopens from "Watch live"), keeping session ids out of shareable links; **B** put `session` and `agent` in the chat address so it restores, accepting that shared links carry a session id (access still checked per user).

**Q4 — What a workspace switch does to an open panel (MAJ-212).** Today the docked Library stays where the operator was browsing when they switch workspace; the spec now makes it follow the new workspace, and says nothing about the Browser. Options: **A** (recommended) Browser stays on its session; Library follows only if it was opened scoped to the workspace being left (not from the all-workspaces root); Tasks/Calendar/Mail follow; **B** every panel closes on workspace switch; **C** every panel follows (Browser closes).

**Q5 — Which buttons "switch to the already-open tab" (MAJ-213).** SP-18 covers the panel toggle and Expand. Options: **A** (recommended) every entry point for the same panel and workspace (sidebar Library, "Open library", "Watch live", toggle, Expand) switches to the open full-page tab; **B** only the toggle and Expand switch — the sidebar and "Open library" still open a docked copy, as today's approved C4 behaviour allows.

**Q6 — What the demo gate is (MAJ-210, SP-16 vs SP-10).** Storybook cannot run the address-bar, new-tab, deep-link or live-driving rows. Options: **A** (recommended) Storybook stories are the demo for layout rows (resize, limits, reset, one-at-a-time, phone, keyboard); the address/new-tab/deep-link/driving rows are click-tested in the running app in wave 1, and a dev-only request-mocking library may be added for stories; **B** Storybook only, and the rows it cannot run are dropped from the pre-build gate.

**Not founder questions — fix round applies the default:**

| Finding | Default the fix round applies |
|---|---|
| MAJ-201 | Browser identity = session + agent for registry, presence and naming |
| MAJ-202 | Drop the fixed window name; new tabs open as `_blank` |
| MAJ-203 | `default = clamp(0.45·row, 320, min(720, ceiling))`; recompute every number from SP-17 |
| MAJ-205 | Router blocker (`useBlocker`, precedent `src/routes/_app/library.tsx::LibraryRoute`); cancel leaves route, address and panel unchanged |
| MAJ-206 | Store wins on Back/Forward — honours SP-22's "Back behaves as today" |
| MAJ-208 | Only the opener tab re-docks; same-panel case follows the pop-out's last workspace (Dana fix kept) |
| MAJ-209 | Blocked new tab → error toast, docked panel stays open, every panel; `window.open` before any presence wait |
| All MIN/OBS | As recommended in each finding |
