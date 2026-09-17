# Lane D — Consistency of Consumption

Grep/Glob-driven, 2026-09-17 worktree. Scope: `src/**/*.tsx` excluding tests (317 files). Primary lens: Nielsen H4 Consistency & Standards, plus the Web Interface Guidelines (semantic controls, one pattern per job). Visual rendering of screens was **not** observed — counts and citations are from source. Anything that would need a screenshot to confirm is marked UNVERIFIED.

## Maturity verdict: 3 (defined)

The design system exists and is the intended path: 34 primitives under `src/components/ui/`, brand tokens in `src/styles/globals.css` `@theme`, Phosphor-only icons, and a real overlay stack (Sheet / Dialog / AlertDialog). Settings (24 of 29 files), workspaces (26 of 44), and library *dialogs* actually import those primitives. Tokens (`var(--color-*)`) are consumed even when the component is not — that is why this is not ad-hoc.

What keeps it at 3, not 4: the *component* layer is optional. Card has **one** consumer (the marketing landing page). 98 files ship a raw `<button>` and never import `Button`. Empty states are reinvented per module. Confirmations split across four mechanisms, one of them the browser’s native `window.confirm`. There is no lint or CI gate that would fail a new hand-rolled button, card, or `text-[10px]`. Defined library, unmanaged adoption.

---

## Library consumption findings

**Inventory (non-test TSX, excluding the primitive files themselves)**

| Surface | Files that import `@/components/ui/*` | Files in module | Share |
|---|---:|---:|---:|
| settings | 24 | 29 | 83% |
| calendar | 3 | 4 | 75% |
| screens | 5 | 7 | 71% |
| agents | 13 | 21 | 62% |
| workspaces | 26 | 44 | 59% |
| providers | 4 | 8 | 50% |
| layout | 3 | 5 | 60% |
| skills | 3 | 5 | 60% |
| library | 17 | 59 | 29% |
| chat | 6 | 51 | 12% |
| sessions | 0 | 1 | 0% |
| **All src (excl. `ui/`)** | **112** | **283** | **40%** |

Routes under `src/routes/_app/` are thin wrappers and mostly do not import primitives — that is not bypass. Chat, library preview, and session chrome are.

**Primitive vs hand-rolled (the controls that already exist)**

| Primitive | Files that import it | Hand-rolled stand-in |
|---|---:|---|
| `Button` | 72 | 98 files with `<button>` and **no** Button import; 38 more import Button *and* still emit raw `<button>` |
| `Card` | **1** (`src/routes/landing.tsx:11`) | 89 card-shaped `rounded-(lg\|xl) border … bg-[var(--color-surface-*)]` blocks across 42 files |
| `Input` | 45 | 14 files with raw `<input>` and no Input import |
| `Textarea` | 13 | 2 raw `<textarea>` |
| `Switch` | 10 | 5 files with `type="checkbox"` (one of them `role="switch"`) |
| `Checkbox` | 3 | same 5 checkbox files |
| `Select` | 13 | 3 raw `<select>` |
| `Tabs` | 6 | 4 files with `role="tab"` (WorkspaceTabBar, CalendarToolbar, AskUserQuestionCard, BasePreview) |
| `Badge` | 19 | custom badge spans (AuditLogViewer, PolicyBadge) plus Tailwind `emerald-*` / `red-500` palettes |
| `Dialog` | 24 | 1 custom overlay (`image-lightbox.tsx`) plus `window.confirm` |
| `Sheet` | 15 | none — slide-overs *do* consume Sheet (width/API still drifts; see case study) |
| `AlertDialog` | 14 | ConfirmActionModal wraps it in workspaces; other modules inline their own; RestartConfirmDialog uses Dialog instead |

### Findings

**Important → Card is a published primitive that the product does not use.**
The Card component is exported from `src/index.ts` and `@omnipus/ui`, and is documented as the elevated dark surface. The only import in the SPA is the marketing landing page (`src/routes/landing.tsx:11`). Every in-app “card” is a one-off `div`/`button` with the same border + surface-1 recipe: `AgentCard` (`src/components/agents/AgentCard.tsx:47`), `AgentProfile`’s `StatCard` (`src/components/agents/AgentProfile.tsx:2975`), Skills MCP rows (`src/components/screens/SkillsScreen.tsx:309`), AgentList workspace rows (`src/components/screens/AgentListScreen.tsx:85`).
**Fix:** Either adopt Card (with a compact / list-row variant so AgentCard and Skills rows fit) or stop publishing it. A primitive with one consumer is documentation, not a system.

**Important → Button is optional. Icon buttons and list rows reinvent it.**
72 files import `Button`. 98 files that contain `<button>` never do. Worst clusters: chat tools (`ActivityBar`, `ToolCallBadge`, `GenericToolCall`, `FileTreeView`, …), library explorer/preview, `SearchModal` hover-reveal icon buttons (`src/components/search/SearchModal.tsx:382`), `ListStates.ErrorState` retry (`src/components/shared/ListStates.tsx:30` — the *shared* empty/error kit still hand-rolls a button), `PolicyBadge` (`src/components/shared/PolicyBadge.tsx:24`). 38 files import Button *and* still emit raw `<button>` (AgentProfile, TaskDetailPanel, CreateTaskSlideOver, login, onboarding, …).
The primitive only has four sizes (`default` / `sm` / `lg` / `icon` at 36px — `src/components/ui/button.tsx:26`). Dense chrome wants 28–32px ghost-icon buttons, so authors skip the primitive.
**Fix:** Add `size="icon-sm"` (and keep `ghost`) that matches the SearchModal / ActivityBar pattern. Then lint raw `<button>` outside `src/components/ui/` except for a short allow-list (FullCalendar, React Flow handles). `Button asChild` already covers Link-styled CTAs (`UsageScreen.tsx:422` and `AgentListScreen.tsx:62` currently copy the gold/outline recipes by hand).

**Important → Switch exists; the agent wizard still ships a native checkbox as a switch.**
`Switch` is the Radix primitive (`src/components/ui/switch.tsx`). Settings and Skills consume it. The create-agent wizard does not: `InheritToggle` is `<input type="checkbox" role="switch">` (`src/components/agents/wizard/InheritToggle.tsx:32`). AgentProfile skill grants (`src/components/agents/AgentProfile.tsx:1939`), wizard Advanced and Step3Tools, and RecordFieldEditor also use raw checkboxes instead of `Checkbox` / `Switch`.
**Fix:** `InheritToggle` → `Switch`. Skill grants → `Checkbox`. Same control, same focus ring, same accent.

**Minor → Badge is used in 19 files, then bypassed for the same job.**
`PolicyBadge` (`src/components/shared/PolicyBadge.tsx:6`) paints allow/ask/deny with Tailwind `emerald-500` / `amber-500` / `red-500` instead of `--color-success` / `--color-warning` / `--color-error`, and is a raw `<button>`, not `Badge`. `AuditLogViewer`’s `ChainStatusBadge` (`src/components/settings/AuditLogViewer.tsx:72`) does the same with `emerald-400` / `red-400` plus chrome dingbats.
**Fix:** Extend `badgeVariants` with `success`/`error` (already present) and a compact `button` slot, or restyle PolicyBadge on those variants and the brand tokens.

**Minor → `@omnipus/ui` is a re-export of `src/index.ts`, not a second library.**
`packages/ui/src/index.ts:3` re-exports the SPA tree. No dual design system. Not a finding — recorded so it is not mistaken for one.

---

## Icon discipline findings

Phosphor is the actual icon system. No competing sets.

| Source | Count |
|---|---:|
| Files importing `@phosphor-icons/react` (non-test) | 196 |
| lucide-react / heroicons / react-icons / tabler / radix-icons / fontawesome / react-feather / MUI icons | **0** |
| Inline `<svg>` in non-test TSX | 2 (both justified; see below) |
| Emoji→Phosphor translator (chat *output* only) | sanctioned (`src/lib/rehype-phosphor-emoji.ts`, `src/lib/phosphor-emoji-icons.tsx`) |

Unicode-range grep for emoji/dingbats in non-test TSX hit **7 files**. Six are comments (`★-eligible`, `✕-cancel`, `⚠️ This listener`, mermaid example `📦`). One is rendered chrome.

### Findings

**Important → Audit log chrome uses dingbats and a Tailwind palette, not Phosphor and not brand tokens.**
`src/components/settings/AuditLogViewer.tsx:72` renders `Chain verified ✓` and `:76` renders `Chain broken ✗`, with `emerald-400` / `red-400` classes. Repo rule: no emoji in UI chrome; Phosphor only. Badge already has `success` / `error` variants.
**Fix:** Phosphor `CheckCircle` / `XCircle` + `Badge variant="success"|"error"`. Drop the dingbats and the emerald/red utilities.

**Minor → Two inline SVGs; both are legitimate exceptions, not a second icon set.**
- `src/components/library/icons/MountFolderIcon.tsx:36` — composed from Phosphor path data (FolderSimple + ArrowUpRight) so every mount surface shares one mark. Documented as the icon-consistency pivot of 2026-09-07.
- `src/components/library/preview/viewparts/ChartPart.tsx:126` — a data chart (`role="img"`), not an icon.

**Minor → Search result rows use a keyboard-return glyph `↵` as chrome.**
`src/components/search/SearchModal.tsx:366` (`aria-hidden`). Not an emoji, but it is a unicode dingbat in chrome. Prefer Phosphor `CornerDownLeft`.

Icon discipline is the strongest part of consumption. Do not treat it as a crisis; the AuditLogViewer line is the one chrome break.

---

## Tailwind sprawl findings (counts + citations)

Tokens *are* used (`var(--color-surface-*)`, `var(--color-accent)` appear throughout, including in hand-rolled markup). The hole is the **type scale** and a handful of layout numbers that never made it into `@theme`.

`src/styles/globals.css` `@theme` defines colour, font family, sidebar width, tap-target (44px), chrome-header (44px), browser-tabs (34px). It does **not** define 10px / 11px / 9px type. Authors filled the gap with arbitrary values.

**Arbitrary `[Npx]` totals (non-test TSX)**

| Pattern | Occurrences | Files |
|---|---:|---:|
| any `[Npx]` | **811** | 175 |
| `text-[Npx]` | **666** | — |
| `w-[Npx]` | 87 | — |
| `h-[Npx]` | 66 | — |
| `max-w-[…]` | 51 | — |
| `h-[44px]` / `min-h-[44px]` / `w-[44px]` (the tap-target token already exists) | 23 | 14 |
| `h-chrome-header` / `min-h-tap-target-*` (the actual utilities) | 15 | 8 |
| `bg-[#…]` / `text-[#…]` / `border-[#…]` | 5 | 3 |

**`text-[Npx]` is not random — it is an unofficial type scale**

| Class | Count | What it is standing in for |
|---|---:|---|
| `text-[10px]` | 364 | missing `text-micro` |
| `text-[11px]` | 211 | missing `text-caption` |
| `text-[9px]` | 39 | overline / “HB” chips |
| `text-[13px]` | 19 | between `text-xs` (12) and `text-sm` (14) |
| `text-[12px]` | 19 | `text-xs` already |
| `text-[14px]` | 12 | `text-sm` already |
| `text-[8px]` / `text-[7px]` | 1 each | should not exist |

Top files by any `[Npx]`: `AgentProfile.tsx` (49), `ChatScreen.tsx` (23), `LibrarySearchBar.tsx` (20), `AuditLogViewer.tsx` (19), `GenericToolCall.tsx` (19), `CommandPreview.tsx` (18).

**Long ad-hoc `className` strings (≥180 characters): 58.** These are the Button/ghost-icon/card recipes being pasted instead of variants. Longest:

| Chars | Where |
|---:|---|
| 455 | `src/components/workspaces/graph/DependencyEdge.tsx:65` |
| 312 | `src/components/browser/BrowserLiveView.tsx:3016` |
| 301 | `src/components/search/SearchModal.tsx:386` |
| 300 | `src/components/screens/AgentListScreen.tsx:447` |
| 293 | `src/components/agents/CommandPreview.tsx:323` |
| 264 × 2 | `src/components/agents/AgentProfile.tsx:1713` and `:1723` (identical ghost-icon recipes, 10 lines apart) |

`h-[44px]` vs `h-chrome-header`: the token and utility exist (`globals.css:76`, `:115`) and are used in 8 files. 14 other files still write `h-[44px]` / `w-[44px]` (`CalendarToolbar`, `ChatControls`, `PlansFilterBand`, `AgentProfile`, …). Same number, two spellings.

Hard-coded hex that bypasses tokens: `AskUserQuestionCard`, `BashOutput`, `FileReadPreview` (`bg-[#…]` / `text-[#…]` / `border-[#…]`, 5 hits). Plus `onboarding.tsx:773` `backgroundColor: 'rgba(212,175,55,0.12)'` — Forge Gold as a magic number instead of `color-mix` on `--color-accent` (AgentListScreen:90 already does this the right way).

### Findings

**Important → 10px and 11px are the real small type, and they are not tokens.**
364 + 211 hits. Every dense surface (chat activity, library, workspaces, settings) invented the same two sizes. That is a missing scale step, not sloppiness.
**Fix:** Add `--text-micro: 10px` and `--text-caption: 11px` (and matching line-heights) to `@theme`. Codemod `text-[10px]` → `text-micro`, `text-[11px]` → `text-caption`. Lint new `text-[Npx]`. Map `text-[12px]`/`text-[14px]` back to `text-xs`/`text-sm`.

**Important → Tap-target and chrome-header tokens are unused by the files that need them most.**
`min-h-tap-target-min` / `h-chrome-header` exist; 14 files still write `h-[44px]`.
**Fix:** Codemod the 23 `h-[44px]`/`w-[44px]` hits onto the utilities. Same 44, one name.

**Minor → 58 class strings over 180 characters are duplicated Button/card variants.**
`AgentProfile.tsx:1713` and `:1723` are the exhibit: two identical 264-character ghost-icon classes. `SearchModal.tsx:383` and `:386` are the same pattern for rename/delete.
**Fix:** Falls out of the Button `icon-sm` + Card compact variant work. Do not extract a `cn` helper per file — that freezes the sprawl.

---

## Inline style findings

No styled-components, no Emotion, no CSS modules in the SPA. The two extra CSS files are theme overrides for FullCalendar and React Flow (third-party canvases) — legitimate.

**`style={{…}}` in non-test TSX: 352 occurrences across 77 files.**

| File | Count | What it is |
|---:|---:|---|
| `src/routes/onboarding.tsx` | 48 | Colour and background via inline `var(--color-*)` instead of classes |
| `src/components/settings/DiagnosticsSection.tsx` | 38 | Score colour, bar fill width, icon colour |
| `src/components/settings/DevicesSection.tsx` | 29 | same family |
| `src/components/ui/model-selector.tsx` | 17 | (inside the primitive — positioning) |
| `src/routes/login.tsx` | 13 | same family as onboarding |
| `src/components/providers/ProviderPicker.tsx` | 13 | |
| `src/components/chat/ChatScreen.tsx` | 10 | |
| `src/components/calendar/FullCalendarView.tsx` | 8 | event colour — legitimate dynamic |
| `src/components/workspaces/graph/TaskNode.tsx` | 5 | node colour — legitimate dynamic |
| `src/components/agents/AgentCard.tsx:57` | 1 | `backgroundColor: agent.color` — legitimate dynamic |

### Findings

**Important → Onboarding and Diagnostics treat brand tokens as inline CSS, not as Tailwind classes.**
`onboarding.tsx:542` `style={{ backgroundColor: 'var(--color-primary)', color: 'var(--color-secondary)', … }}`; `:773` hard-codes Forge Gold `rgba(212,175,55,0.12)` while `AgentListScreen.tsx:90` already uses `color-mix(in srgb, var(--color-accent) 15%, transparent)`. `DiagnosticsSection.tsx:114`–`:282` is a wall of `style={{ color: 'var(--color-muted)' }}` that is exactly `className="text-[var(--color-muted)]"`.
Dynamic values (agent colour, graph node colour, progress width, FullCalendar event colour) **should** stay inline. Token lookups should not.
**Fix:** Codemod `style={{ color: 'var(--color-X)' }}` → `text-[var(--color-X)]` (and the bg/border equivalents) in onboarding, login, Diagnostics, Devices, Sandbox, ProviderPicker. Leave width percentages and per-entity colours.

**Minor → `image-lightbox.tsx:163` is a hand-rolled modal overlay (`fixed inset-0 z-[200]`, raw close button) instead of Dialog.**
Dialog’s overlay is `z-50` (`src/components/ui/dialog.tsx:26`). The lightbox invents `z-[200]` to stack above it. That is a z-index token gap as well as a primitive bypass.
**Fix:** Either a `Dialog` `variant="lightbox"` (full-bleed, no padding, higher z) or a documented `--z-lightbox` token. Do not leave a magic 200.

---

## Pattern drift case studies (2–3 patterns, cross-module comparison)

### 1. Empty states — one shared kit, five local copies, three “just a sentence”

Files compared side by side:

| Module | File | What the empty screen actually is |
|---|---|---|
| shared (canonical) | `src/components/shared/ListStates.tsx:16` | icon + one muted sentence, `py-16`. **No title, no action.** Raw retry `<button>` on ErrorState (`:30`) |
| skills / connectors / notifications | `SkillsScreen.tsx:301`, `ConnectorsScreen` (import), `NotificationPanel.tsx:108` | the only three consumers of the canonical EmptyState |
| library | `LibraryExplorer.tsx:1559` | **byte-for-byte the same props** (`icon`, `message`) redeclared locally, different layout (`absolute inset-0` vs `py-16`) |
| workspaces board | `BoardView.tsx:368` | a single `<p className="text-sm">`. No icon |
| workspaces list | `ListView.tsx:274` | a table cell, `text-xs`. No icon |
| workspaces graph | `GraphView.tsx:450` and `:482` | rich: 64px duotone icon in a glowing well, headline, body. Closest to “best” |
| chat activity | `ActivityPanel.tsx:223` | `text-xs` sentence, `py-6`. No icon |
| usage | `UsageScreen.tsx:411` | icon + title + body + gold CTA, but the CTA is a hand-rolled `<Link>` not Button (`:422`) |
| knowledge | `KnowledgeEmptyState.tsx:201` | domain-specific state machine (justified — not a list empty) |
| library views | `ViewPartsRenderer.tsx:126` | another local `function EmptyState`, `text-[13px]` + `text-[11px]`, no icon |

Canonical EmptyState is too thin (message only), so every surface that needs a title or an action forks. Board and List are in the *same* product tab and still disagree on size (`text-sm` vs `text-xs`) and whether an icon appears.

**Important → Promote EmptyState to a primitive with slots, then delete the copies.**
Slots: `icon`, `title`, `description`, `action`. Board/List/Activity become the thin variant (description only). Graph/Usage/Skills become the full variant. LibraryExplorer’s local function (`:1559`) is a copy-paste bug — delete it and import the shared one. KnowledgeEmptyState stays; it is a different pattern (first-run / error / offer).

### 2. Confirmation dialogs — four mechanisms for one job

Files compared side by side:

| Mechanism | Where | Overlay dismiss? | Primitive |
|---|---|---|---|
| `ConfirmActionModal` | `PlanActionButton.tsx:196`, `TaskActionButton.tsx:184` | **No** (explicitly blocked) | AlertDialog wrapper. The *only* shared confirm. Used only for Execute/Stop |
| Inline `AlertDialog` | `ChatScreen.tsx:3253` (harmful-upload), `LibraryExplorer.tsx:1408` / `:1441` (skills disclosure, unmount), `McpServerModal.tsx:707`, `EmailMailboxPanel.tsx:773`, `RemoveProviderDialog.tsx:32`, `ConnectorsScreen`, `SkillsScreen`, `AgentProfile` | No (AlertDialog default) | same primitive, copy-pasted footer every time |
| `RestartConfirmDialog` | `ExecProxyStatusCard.tsx:198` | **Yes** (it is a Dialog, not an AlertDialog) — `RestartConfirmDialog.tsx:13` | Dialog + Button. Different dismiss rules than every destructive confirm |
| `window.confirm` | `src/components/library/preview/unsavedGuard.ts:42` | native, unthemed, embed-unsafe | none. AlertDialog’s own header (`alert-dialog.tsx:8`) exists specifically to replace this |

Workspaces went out of its way to stop this drift (ConfirmActionModal’s header comment, `ConfirmActionModal.tsx:29`, cites ADR-052: “never drifts between surfaces”). That discipline stopped at the workspace Execute/Stop buttons. Library discard, gateway restart, chat upload, MCP stdio, mailbox delete, and provider remove each invent their own.

RestartConfirmDialog is the sharp H4 break: a confirm that *does* dismiss on overlay click, sitting next to AlertDialogs that *must not*.

**Critical → One confirm, one dismiss rule.**
1. Make `ConfirmActionModal` (or a slightly more generic `ConfirmDialog` on AlertDialog) the only confirm. Pending label, destructive variant, and overlay-does-not-dismiss already exist.
2. Rewrite `RestartConfirmDialog` onto AlertDialog so restart-later cannot be dismissed by a misclick.
3. Replace `window.confirm` in `unsavedGuard.ts:42` with the same modal (this one is async — the current boolean API will need a promise/callback, which is the real reason it is still native).
4. Stop inlining AlertDialog footers in ChatScreen / LibraryExplorer / McpServerModal / EmailMailboxPanel.

### 3. Slide-over panels — they consume Sheet, then disagree on width, overlay, and API

This is the *good* pattern: every slide-over found uses `Sheet` from `src/components/ui/sheet.tsx`. No `fixed inset-0` panel except the lightbox. Drift is inside the primitive.

Files compared side by side (all `SheetContent side="right"`):

| Surface | File:line | Width | Overlay | Width API |
|---|---|---|---|---|
| New task / plan / workspace / calendar event | `CreateTaskSlideOver.tsx:461`, `CreatePlanSlideOver.tsx:386`, `NewWorkspaceSlideOver.tsx:100`, `CalendarEventSlideOver.tsx:496` | `w-full sm:max-w-md` (~28rem) | yes | `className` override |
| Task detail (board/list) | `TaskDetailSlideOver.tsx:19` | `sm:w-[420px] md:w-[480px]` | yes | `className` |
| Task detail (nested, from TaskDetailPanel) | `TaskDetailPanel.tsx:1303` | `sm:w-[380px] md:w-[460px]` — **different numbers for the same panel** | yes | `className` |
| Notifications | `NotificationPanel.tsx:88` | `sm:w-[360px]` | yes | `className` |
| Chat activity | `ActivityPanel.tsx:215` | `w-[90vw] sm:w-[22.5rem]` | **`overlay={false}`** | `className` |
| Create agent | `CreateAgentWizard.tsx:386` | `w-full sm:max-w-3xl` | yes | `className` (comment in sheet.tsx:65 still says this uses `widthClass`; it does not) |
| Edit agent | `AgentProfile.tsx:2956` | default `w-[90vw] sm:max-w-2xl` | yes | default |
| Provider sheets | `ProvidersSection.tsx:320` / `:1233` | `w-[90vw] sm:max-w-lg` | yes | **`widthClass` — the only callers of the documented API** |

Sheet documents `widthClass` (`sheet.tsx:37`) with defaults `right: w-[90vw] sm:max-w-2xl` (`:71`). One module uses it. Everyone else stuffs width into `className` and hopes `cn`/tailwind-merge wins. Create forms happen to agree (`sm:max-w-md`). Task detail disagrees with itself by 40px. Activity is the only panel that hides the overlay, so chat stays interactive behind it — a real product choice, currently an unstated exception.

**Important → Pick three Sheet sizes and the `widthClass` API, then use them.**
Proposed: `sm` = `max-w-md` (create forms, notifications, activity), `md` = `max-w-lg` (task detail, providers), `lg` = `max-w-2xl` (edit agent; create-agent wizard is the one justified `max-w-3xl`). Pass them as `widthClass` (or a `size` enum). Delete the `sm:w-[420px] md:w-[480px]` / `sm:w-[380px] md:w-[460px]` pair. Document `overlay={false}` as the Activity-panel exception, not a silent prop.

---

## Gaps vs best-in-class

A managed (4) or best-in-class (5) system would have:

1. **A type scale that matches what the UI already is.** 10px and 11px are not “arbitrary” here — they are the product’s caption sizes, living outside `@theme`.
2. **Component consumption as the default, enforced.** ESLint (`no-restricted-syntax` on raw `<button>` / `<input type="checkbox">` / `window.confirm` / `text-[Npx]`) or a CI grep like the existing `scripts/check-no-*.sh` family. The repo already runs that pattern for retired surfaces; it does not run it for the design system.
3. **One empty state, one confirm, three sheet sizes.** Those three patterns are 80% of the visual drift a user can see without reading code.
4. **Card and Button variants that fit dense chrome**, so AgentCard / SearchModal / PolicyBadge have no reason to bypass.
5. **Token-only colour.** `emerald-*` / `red-500` / `rgba(212,175,55,…)` should not appear next to `--color-success` / `--color-error` / `--color-accent`.
6. **No undocumented z-index.** Lightbox `z-[200]` vs Dialog `z-50` is how a future overlay renders under the thing it is supposed to cover. UNVERIFIED in the running app.

What is already at 4 in isolation: Phosphor (196 files, zero competing sets), Sheet adoption for slide-overs, Dialog adoption for named dialogs, brand colour tokens in class strings.

---

## Top 3 fixes by impact

1. **Put 10px / 11px on the type scale and ban new `text-[Npx]`.** 575 of the 666 arbitrary type classes are these two sizes. One `@theme` addition plus a codemod removes the largest visual inconsistency in the SPA, including in files that will never import a primitive.
2. **Make `Button` (with a compact ghost-icon size) the only chrome button, and `ConfirmDialog` the only confirm.** That collapses the 98 Button-bypass files and the four confirm mechanisms (ConfirmActionModal, inline AlertDialog, RestartConfirmDialog-as-Dialog, `window.confirm` in `unsavedGuard.ts:42`) into two primitives with one dismiss rule. Highest H4 payoff.
3. **Give EmptyState title / description / action slots and delete the local copies.** LibraryExplorer’s duplicate (`:1559`), Board’s sentence, List’s table cell, Activity’s sentence, Usage’s hand-rolled CTA, and Graph’s rich well become one component with two densities. Pair with three named Sheet sizes so create / detail / wizard stop inventing pixel widths.

Do these three and the SPA moves from 3 (defined) to 4 (managed). Skipping them and adding more primitives will not — Card already exists, and almost nobody uses it.
