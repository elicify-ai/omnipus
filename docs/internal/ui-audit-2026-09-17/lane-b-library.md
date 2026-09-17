# Lane B — Component Library Maturity

**Date:** 2026-09-17
**Lens:** H4 Consistency & Standards (Nielsen), plus the Web Interface Guidelines (loading / empty / error, focus, labels).
**Scope:** the reusable library — `src/components/ui/` primitives, `src/components/shared/` composites, and layout chrome — not a screen-by-screen UI review. Screen folders are counted only to show how much UI still lives outside the library.
**Method:** Glob / Grep / Read on `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session`. Every count below was produced by `find` / `rg`, not estimated. Absences (`components.json`, Storybook, `src/components/ui/CLAUDE.md`) were confirmed by glob returning zero.

## Maturity verdict: 2 (emerging)

The SPA has a real primitive kit — a themed shadcn/Radix subset in `src/components/ui/`, imported from 122 files — but it is not yet a component library in the Polaris / Carbon / full-shadcn sense. Conventions exist in the best primitives (`Button`, `Input`, `Dialog`) and are ignored in neighbours (`Command`, `Popover`, `Badge`). Loading / empty / error are not library states; they are per-screen inventions, with two competing “the one” error components and two competing save indicators. There is no Storybook, no `components.json`, no props table, no `CLAUDE.md` for `ui/` or `shared/`, and the publishable `@omnipus/ui` package re-exports four primitives and two layout shells. New UI is still written by copying a nearby file.

That is past ad-hoc (the kit is real and widely used) and short of defined (the intended API is not written down, not complete, and not followed).

**Scoring hypotheses (investigation protocol).** H1 “this is a managed/best-in-class shadcn library” is rejected: no Storybook, `cva` in 2 of 36 `ui/` files, missing Tooltip/Skeleton/RadioGroup, stub package. H4 “this is ad-hoc CSS copy-paste” is rejected: 26 primitive families, Radix wrappers, `cn()` on 25 of 34 `ui/` TSX files, `forwardRef` on the Radix subset, and deliberate consolidations (`ChipListInput`, `FormError`, `QueryErrorState`, `ConfirmActionModal`). H3 “defined, with drift only at the edges” is the close call — the primitive core would score 3 in isolation — but the library-as-a-product fails the defined bar on documentation, state coverage, and API consistency, so the overall score is 2.

## Inventory (counts + component list grouped by kind)

Production React files under `src/components/` (`.tsx`, tests excluded): **283**.

| Kind | Files | What this means |
|---|---:|---|
| Primitive families (`src/components/ui/`, generic) | 26 files | The shadcn/Radix kit. |
| Domain widgets parked in `ui/` | 8 TSX + 2 helper `.ts` | App-specific; should not live next to `Button`. |
| Shared composites (`src/components/shared/`) | 11 files / 13 exported components | Cross-screen patterns. |
| Layout chrome (`src/components/layout/`) | 5 TSX | App shell, not primitives. |
| Screen-specific (agents, chat, library, workspaces, settings, …) | 233 | Feature UI. Some *should* be library composites and are not. |

`packages/ui/` is not a second library. It is a stub: `packages/ui/src/index.ts` re-exports `src/index.ts`, which itself exports only `Button`, `Card`, `Input`, `Dialog` (+ subparts), `AppShell`, `Sidebar`, `useSidebarStore`, and `cn`. No `vite.config`, no README, no `dist/`.

### Primitives — 26 families in `src/components/ui/`

Radix-wrapped shadcn subset:

| Family | File | Notes |
|---|---|---|
| Accordion | `accordion.tsx` | Item / Trigger / Content |
| AlertDialog | `alert-dialog.tsx` | Built on `@radix-ui/react-dialog`, not `@radix-ui/react-alert-dialog` (intentional, no new dep) |
| Avatar | `avatar.tsx` | Root / Image / Fallback. Size via ad-hoc map, not `cva` |
| Badge | `badge.tsx` | `cva` variants. No ref, no size |
| Button | `button.tsx` | `cva` variant + size + `asChild`. No loading |
| Calendar | `calendar.tsx` | `react-day-picker` wrapper |
| Card | `card.tsx` | Header / Footer / Title / Description / Content |
| Checkbox | `checkbox.tsx` | |
| Command | `command.tsx` | `cmdk`. Template-string `className`, inline styles, no `cn()` |
| DatePicker | `date-picker.tsx` | Composite primitive; shares `DateTriggerButton` with DateTimePicker |
| DateTimePicker | `date-time-picker.tsx` | |
| Dialog | `dialog.tsx` | Portal / Overlay / Trigger / Close / Content / Header / Footer / Title / Description |
| DropdownMenu | `dropdown-menu.tsx` | Full Radix menu including RadioGroup / Sub |
| Input | `input.tsx` | Native attributes only. No size, no invalid variant |
| Label | `label.tsx` | |
| Popover | `popover.tsx` | Same `cn()` skip as Command |
| Progress | `progress.tsx` | Value only; no indeterminate |
| Select | `select.tsx` | |
| Separator | `separator.tsx` | |
| Sheet | `sheet.tsx` | Same Dialog primitive, slide-over chrome |
| Slider | `slider.tsx` | |
| SmartSelect | `smart-select.tsx` | Select below 5 items, searchable Command above. No ref |
| Switch | `switch.tsx` | |
| Table | `table.tsx` | No `TableFooter` / `TableCaption` |
| Tabs | `tabs.tsx` | |
| Textarea | `textarea.tsx` | Same gaps as Input |

**Missing vs a full shadcn/ui kit (verified absent from `src/components/ui/`):** Tooltip, Skeleton, RadioGroup (as its own primitive — only `DropdownMenuRadioGroup` exists), Form/Field, Alert (non-dialog), ScrollArea, HoverCard, ContextMenu, Menubar, NavigationMenu, Breadcrumb, Pagination, Drawer, Collapsible, Toggle / ToggleGroup, Resizable, Sonner. `@radix-ui/react-tooltip` is listed in `packages/ui/package.json:35` and never imported from `src/`.

### Domain widgets currently in `ui/` — 8 components + 2 helpers

These are reusable, but they are product features, not primitives:

- `AutoSaveIndicator.tsx`
- `BrandDisclaimer.tsx`
- `BrandIcon.tsx`
- `ErrorBoundary.tsx`
- `FormError.tsx`
- `ModelSelector.tsx` (large catalog combobox; `variant: 'default' | 'ghost'`)
- `RestartConfirmDialog.tsx`
- `ToastContainer.tsx` (store-driven, not Radix Toast)
- Helpers: `channel-logo.ts`, `model-ordering.ts`

### Shared composites — 11 files, 13 exported components (`src/components/shared/`)

| Component | File | Job |
|---|---|---|
| `SkeletonList` | `ListStates.tsx` | Three pulsing rows. No size / count / shape props |
| `EmptyState` | `ListStates.tsx` | Icon + message. No action slot |
| `ErrorState` | `ListStates.tsx` | Message + optional Retry (raw `<button>`) |
| `QueryErrorState` | `QueryErrorState.tsx` | Claims to be “the ONE” blocking query error |
| `RouteFallback` | `RouteFallback.tsx` | Suspense fallback wrapping `SkeletonList` |
| `RouteErrorFallback` | `RouteErrorFallback.tsx` | Chunk-load recovery |
| `AdvancedDisclosure` | `AdvancedDisclosure.tsx` | Collapsible “Advanced” (does not use `Accordion`) |
| `CriteriaBreakdown` | `CriteriaBreakdown.tsx` | Workspace criteria |
| `IconRenderer` | `IconRenderer.tsx` | Phosphor-by-name |
| `PolicyBadge` | `PolicyBadge.tsx` | Allow / Ask / Deny toggle chip (raw `<button>`) |
| `RiskySettingControl` | `RiskySettingControl.tsx` | Confirm-to-change setting |
| `ToolPolicyEditor` | `ToolPolicyEditor.tsx` | Per-agent tool policy matrix |
| `Wordmark` | `Wordmark.tsx` | Brand wordmark |

### Layout chrome — 5 files (`src/components/layout/`)

`AppShell`, `Sidebar`, `ScreenHeader`, `NotificationPanel`, `CrossWorkspaceApprovalBanner`. Shared across routes; not primitives.

### Screen-specific — 233 files, by folder

| Folder | Production TSX | Typical contents |
|---|---:|---|
| `library/` | 59 | Explorer, 7 dialogs, knowledge, preview |
| `chat/` | 51 | Thread, composer, tool-call UIs, two lightboxes |
| `workspaces/` | 44 | Board / list / graph / team, 5 slide-overs |
| `settings/` | 29 | One section file per settings area |
| `agents/` | 21 | Cards, wizard, approvals |
| `providers/` | 8 | Picker + three sign-in dialogs |
| `screens/` | 7 | Route shells |
| `skills/` | 5 | Browser, MCP modal, channel config |
| `calendar/` | 4 | FullCalendar + recurrence + event slide-over |
| `browser/` | 2 | Live view |
| `search/` | 1 | Command palette modal |
| `sessions/` | 1 | Session tree |
| `connectors/` | 1 | Email mailbox |

**Test coverage of the library itself:** 14 test files under `src/components/ui/` covering 14 of 36 production files. Untested primitives include `accordion`, `avatar`, `badge`, `checkbox`, `command`, `dialog`, `dropdown-menu`, `label`, `popover`, `progress`, `select`, `separator`, `sheet`, `slider`, `smart-select`, `switch`, `table`, `tabs`, `textarea`, `toast-container`, `RestartConfirmDialog`, `brand-disclaimer`.

**No `CLAUDE.md`** in `src/components/ui/`, `src/components/shared/`, or `src/components/layout/` (feature folders have one; the library does not).

## API consistency findings

The intended convention, visible in `Button` and `Input`, is: Radix primitive + `forwardRef` + `displayName` + `cn()` class merge + `disabled:` opacity + CSS-variable tokens. `cva` `variant` / `size` is the shadcn standard but is used in **2 files only** (`button.tsx`, `badge.tsx`).

**Conforming examples**

- `Button` — `cva` variants + sizes, `asChild`, `forwardRef`, `disabled:pointer-events-none disabled:opacity-50` (`src/components/ui/button.tsx:6-61`).
- `Input` — `forwardRef`, `cn()`, disabled styles, native attribute passthrough (`src/components/ui/input.tsx:5-22`).
- `Dialog` / `Sheet` / `Select` / `DropdownMenu` / `AlertDialog` — Radix composition, `forwardRef`, `displayName`, `cn()`.
- `DatePicker` + `DateTimePicker` share `DateTriggerButton` instead of copying the trigger (`src/components/ui/date-picker.tsx:43-58`).
- `SmartSelect` requires `ariaLabel` so the trigger is not named after its current value (`src/components/ui/smart-select.tsx:35-40, 53`).
- `ChipListInput` is the one chip-editor; `TagInput` is a configured wrapper (`src/components/workspaces/ChipListInput.tsx:7-19`, `src/components/workspaces/TagInput.tsx:22-35`). This is the pattern the rest of the library should follow.

### Important — `cva` is not a library convention; it is two files

**What.** Only `Button` and `Badge` use `class-variance-authority`. `Avatar` invents a local `sizeClasses` map (`src/components/ui/avatar.tsx:8-12, 20`). `ModelSelector` has a `variant: 'default' | 'ghost'` prop implemented as a ternary class string (`src/components/ui/model-selector.tsx:177, 214, 812-813`). `Card`, `Input`, `Textarea`, `Tabs` have no `variant` / `size` at all.

**Fix.** Pick one: every visual variant goes through `cva` (shadcn) or none do. Add `size` to `Input` / `Textarea` / `Badge`; add `variant` to `Alert`/status chrome; stop hand-rolling size maps.

### Important — className merging is not uniform (`cn()` vs template strings vs inline styles)

**What.** 25 of 34 `ui/` TSX files call `cn()`. These do not:

| File | Pattern | Line |
|---|---|---|
| `command.tsx` | `` className={`… ${className ?? ''}`} `` + `style={{ color: 'var(--…)' }}` | 10, 24, 84-85 |
| `popover.tsx` | same template string + inline style | 17-22 |
| `AutoSaveIndicator.tsx` | template string, default `className = ''` | 37, 48-50 |
| `error-boundary.tsx` | inline `style={{ color: 'var(--color-error)' }}`, no `cn()` | 39-46 |
| `RestartConfirmDialog.tsx` | no `className` prop at all | 11-28 |
| `model-selector.tsx` | mixed; trigger classes are ternaries | 812-813 |

`cn()` exists specifically so Tailwind classes override correctly (`src/lib/utils.ts:21-23`). Template concatenation cannot.

**Fix.** `cn()` on every public root. Move inline `style={{ color: var(--token) }}` to Tailwind token classes. Give `RestartConfirmDialog` a `className`.

### Important — `Button` and `Badge` merge `className` two different ways

**What.** `Button` feeds `className` *into* `cva` then wraps with `cn()` (`src/components/ui/button.tsx:55`). `Badge` does the current shadcn form: `cn(badgeVariants({ variant }), className)` (`src/components/ui/badge.tsx:40`). Both work; they are two APIs.

**Fix.** One pattern. Prefer `cn(variants({ variant, size }), className)` so callers can override.

### Important — ref forwarding is Radix-only, not library-wide

**What.** Radix wrappers forward refs. These public components do not: `Badge` (`badge.tsx:38`), `SmartSelect` (`smart-select.tsx:45`), `FormError` (`FormError.tsx:51`), `AutoSaveIndicator` (`AutoSaveIndicator.tsx:37`), `ToastContainer`, `ModelSelector` (`model-selector.tsx:214`), `BrandIcon`, `ErrorBoundary`, `RestartConfirmDialog`. `Badge` is also a `<div>` with no `forwardRef`, so it cannot be a focusable child of `PopoverTrigger asChild`.

**Fix.** `forwardRef` + `displayName` on every public component, including composites. `Badge` should be a `<span>` (or `asChild`) so it can sit inside buttons.

### Important — `disabled` is styled, not modelled

**What.** Native-disabled styling exists on `Button` / `Input` / `Select` / `Switch` / `Checkbox`. There is no shared `isLoading` / `pending` prop. Callers inline it:

- `RestartConfirmDialog` — `{saving ? 'Saving...' : 'Save & Restart Later'}` (`RestartConfirmDialog.tsx:22-24`)
- `ConfirmActionModal` — `confirmLabel` / `pendingLabel` (`ConfirmActionModal.tsx:20-24, 62-75`)

`Input` / `Textarea` have no invalid/error visual. `FormError` documents `aria-invalid` as a *caller* duty (`FormError.tsx:27-28`) and `Input` itself never styles `[aria-invalid]`. Only `ModelSelector` sets `aria-invalid` inside the library (`model-selector.tsx:592, 804`).

**Fix.** `Button` gains `loading?: boolean` (spinner, `aria-busy`, keep width). `Input` / `Textarea` / `SelectTrigger` gain `aria-invalid` border using `--color-error`. Field-level error is `FormError` wired by a `Field` wrapper so callers cannot forget `aria-describedby`.

### Minor — naming is mixed inside `ui/`

**What.** shadcn files are kebab-case (`alert-dialog.tsx`, `dropdown-menu.tsx`). Later additions are PascalCase (`AutoSaveIndicator.tsx`, `FormError.tsx`, `RestartConfirmDialog.tsx`). `model-selector.tsx` is the only file with a leftover `'use client'` directive (`model-selector.tsx:1`).

**Fix.** kebab-case for every `ui/` file. Drop `'use client'` (this is Vite, not Next). Move PascalCase domain widgets out of `ui/`.

### Minor — raw `<button>` bypasses the Button primitive

**What.** 72 files import `Button`. 178 files (including tests) contain a raw `<button`. Production library offenders: `ListStates.ErrorState` (`ListStates.tsx:29-36`), `QueryErrorState` (`QueryErrorState.tsx:67-74`), `RouteErrorFallback` (`RouteErrorFallback.tsx:11-17`), `PolicyBadge` (`PolicyBadge.tsx:24-37`), `ErrorBoundary` (`error-boundary.tsx:43-57`), `ToastContainer` action/dismiss (`toast-container.tsx:47-64`), `SmartSelect` searchable trigger (`smart-select.tsx:113-136`). Some of those are justified (asChild targets). Most are “I needed a small control and copied a class string”.

**Fix.** Library chrome uses `Button variant="ghost" | "outline" | "link" size="sm"`. Ban raw `<button>` in `ui/` and `shared/` except asChild slots.

### Minor — `outline-none` without a local focus replacement, on top of a global ring

**What.** The app has a sanctioned global `:focus-visible` hairline (`src/styles/globals.css:168-179`). Several primitives still set `outline-none` (`command.tsx:24,84`, `popover.tsx:17`, `select.tsx:113`, `dropdown-menu.tsx:22,78,94,117`). `ErrorState`’s Retry sets `focus:outline-none` with no replacement (`ListStates.tsx:33`), which fights the global ring.

**Fix.** Delete `outline-none` / `focus:outline-none` from library components unless `data-no-focus-ring` is intentional and documented.

## State coverage findings

The library does **not** have a systematic loading / empty / error / disabled / skeleton matrix. A few components handle some states well. Most states are invented at the call site.

| State | Library support | Reality |
|---|---|---|
| Disabled | CSS on primitives | Present. No pending/loading distinct from disabled. |
| Loading | None on `Button` / `Input` / `Progress` | `ModelSelector.catalogStatus: 'loading' \| 'error' \| 'ready'` (`model-selector.tsx:163`) is the exception. `Progress` has no indeterminate (`progress.tsx:8-20`). |
| Skeleton | `SkeletonList` only | Fixed three `h-16` rows (`ListStates.tsx:3-13`). Screens hand-roll `animate-pulse` (Usage, Settings, Sidebar, Team, Graph, Agent list, Audit log, … — 20+ call sites). |
| Empty | `EmptyState` (icon + message, no action) | Re-implemented locally in Library, Board, Graph, knowledge, view-parts. |
| Error (query) | Two “the one” components | `ErrorState` and `QueryErrorState` both exist; both still used. |
| Error (field) | `FormError` | Adopted by **3** call sites. 42 files still roll `role="alert"` by hand. |
| Save status | Two indicators | `AutoSaveIndicator` (12 call sites) and `SaveStatus` (5 settings sections). |
| Toast | `ToastContainer` | Custom store toasts, not a primitive. |

### Critical — empty / error / skeleton are not one system, so screens invent them

**What.** `ListStates.tsx` exports `SkeletonList`, `EmptyState`, `ErrorState`. `QueryErrorState.tsx:1-9` then declares itself “the ONE shared blocking error state” and lists the previous hand-rolls it was meant to kill. Both are still live:

- `ErrorState` — `SkillsScreen.tsx:189,297,407`, `ConnectorsScreen.tsx:1097`
- `QueryErrorState` — Calendar, workspace tabs, Library explorer/preview, knowledge outline/backlinks

`EmptyState` (`ListStates.tsx:16-22`) takes only `icon` and `message`. No title, no action, no secondary action — so a dead-end empty is the default. Call sites that need a CTA write a new component:

- Local `EmptyState` in `LibraryExplorer.tsx:1559-1565` (absolute-fill, not the shared one)
- `BoardEmptyState` (`BoardView.tsx:368-376`) — text only, no icon, no action
- `GraphEmptyState` / `GraphPlanEmptyState` (`GraphView.tsx:450,482`)
- `KnowledgeEmptyState` — a real state machine, and the one empty that *does* offer an action (`KnowledgeEmptyState.tsx`)
- `ViewPartsRenderer` local `EmptyState` (`ViewPartsRenderer.tsx:126`)

Skeletons are copy-pasted `animate-pulse` divs. Examples: `UsageScreen.tsx:51-61`, `AgentListScreen.tsx:645`, `WorkspaceTeamTab.tsx:565-578`, `Sidebar.tsx:464`, `ProvidersSection.tsx:1123`, `MemorySection.tsx:17`. None use a `Skeleton` primitive because there isn’t one.

Web Interface Guidelines: empty states must not render broken UI; errors must include a next step; loading copy ends in `…`. H1 (visibility of status) and H4 (same component = same behaviour) both fail here.

**Fix.** Promote a single `ListState` API: `variant: 'loading' | 'empty' | 'error'`, required `message`, optional `action: { label, onClick }`. `Skeleton` primitive with `className` (shadcn’s `<Skeleton className="h-4 w-32" />`). Delete `ErrorState` or make it a thin wrapper of `QueryErrorState`. Ban new local `EmptyState` functions. Give `EmptyState` an action slot so “No tasks yet” can offer “Create task”.

### Important — two save indicators, two ellipsis conventions, two type names

**What.**

| | `AutoSaveIndicator` | `SaveStatus` |
|---|---|---|
| File | `src/components/ui/AutoSaveIndicator.tsx` | `src/components/settings/SaveStatus.tsx` |
| Type | `AutoSaveStatus` = idle / saving / saved / error / **conflict** | `SaveState` = idle / saving / saved / error |
| Idle | stays mounted, `opacity-0` (live-region trick) | returns `null` |
| Copy | `Saving...` (three dots, line 57) | `Saving…` (ellipsis, line 26) |
| Used by | 12 files (settings + library + agents + workspaces) | 5 settings sections (Memory, Sandbox, Context, PromptGuard, SkillTrust) |

`RestartConfirmDialog.tsx:24` has a third: `{saving ? 'Saving...' : 'Save & Restart Later'}`. Settings sections that have not migrated still inline `Saving...` (`MemorySection.tsx:576`, `SandboxSection.tsx:1259`, `SecuritySection.tsx:747`).

Web Interface Guidelines: loading states end with `…`, not `...`.

**Fix.** One `SaveIndicator`. Keep the always-mounted live region and the `conflict` state from `AutoSaveIndicator`. Use `…`. Migrate the five `SaveStatus` call sites. Add `loading` to `Button` so dialogs stop inlining the word.

### Important — `FormError` is the documented field-error contract and almost nobody uses it

**What.** `FormError.tsx:1-28` is explicit: this is the single source of truth for field errors (`role="alert"`, `id` for `aria-describedby`). Imports: `CreateAgentWizard`, `RecurrenceEditor`, `CalendarEventSlideOver` — **three files**. Meanwhile 42 files emit `role="alert"` themselves, and `LibraryErrorBanner` (`LibraryErrorBanner.tsx:1-10`) is a second “ONE error-presentation pattern” for Library mutations.

**Fix.** A `Field` composite (`Label` + control + `FormError`) that wires `id` / `aria-describedby` / `aria-invalid`. Lint or a grep gate: new field errors go through `FormError`. Keep `LibraryErrorBanner` only if it is the form-*mutation* banner (not field-level) and document that split.

### Minor — disabled is the only state most primitives actually implement

**What.** `Button` tests cover disabled (`button.test.tsx:54-56`). There is no loading / empty / error story for `Button`, `Input`, `Select`, `Tabs`, `Table`, `Card`. `ModelSelector` is the outlier that *does* model catalog loading, error+retry, empty-catalog, and disabled-empty (`model-selector.tsx:127-167, 474-505`) — proof the team knows how, and did it once for a painful UAT bug.

**Fix.** Treat `ModelSelector`’s four-state catalog as the template. Every picker and every list primitive gets the same four: loading, empty, error+retry, ready.

## Duplication findings

Some duplication is already paid down (`ChipListInput`, `DateTriggerButton`, `ConfirmActionModal`). The rest is still two (or seven) components for one job.

### Important — seven Library name-dialogs that are the same dialog with different copy

**What.** `LibraryNewFolderDialog.tsx:9-11` says it “deliberately mirrors LibraryRenameDialog”. `LibraryNewNoteDialog.tsx:10-11` says it “mirrors LibraryNewFolderDialog”. The set is:

- `LibraryAddMountDialog.tsx`
- `LibraryMountsDialog.tsx`
- `LibraryNewFolderDialog.tsx`
- `LibraryNewNoteDialog.tsx`
- `LibraryNewVaultDialog.tsx`
- `LibraryRenameDialog.tsx`
- `LibraryTransferDialog.tsx`

All are `Dialog` + `Input` + `Label` + `Button` + `LibraryErrorBanner`. The differences are validation and the submit endpoint.

**Fix.** One `NameDialog` (title, label, validator, submit, error, pending). Variants for “note appends `.md`” and “collision check is a nicety”. This is the `ChipListInput` lesson applied to dialogs.

### Important — confirm / restart dialogs are three patterns

**What.**

| Pattern | File | Primitive |
|---|---|---|
| AlertDialog confirm | `ConfirmActionModal.tsx` | `AlertDialog` — overlay click does not dismiss |
| Dialog confirm | `RestartConfirmDialog.tsx` | `Dialog` — overlay click *does* dismiss via `onOpenChange` |
| Dialog + poll | `GatewayRestartModal.tsx` | `Dialog` + health poll |

`RestartConfirmDialog` is also mis-filed in `ui/` and hard-codes gateway copy (`RestartConfirmDialog.tsx:16-18`), so it is not reusable. Destructive vs default is a `className` override on `ConfirmActionModal` (`ConfirmActionModal.tsx:76-79`) instead of `Button variant="destructive"`.

**Fix.** One confirm: `ConfirmActionModal` (already the ADR-052 standard). `RestartConfirmDialog` becomes a call of it, or dies in favour of `GatewayRestartModal`. Overlay-dismiss policy lives in one place.

### Important — picker family is a forest, not a tree

**What.** Searchable pickers:

- `Select` (Radix, non-searchable)
- `SmartSelect` (Select if ≤5 items, Command otherwise)
- `ModelSelector` (virtualised catalog + free-text + ghost variant)
- `ModelPicker` — data wrapper around `ModelSelector` (`composer/ModelPicker.tsx:10-18`) — this one is *correct* layering
- `AgentPicker`, `ProviderPicker`, `ExecutorSelector`, `MCPServerPicker`, `AddAgentPicker`, `AgentDelegatePicker`

`SmartSelect` and `ModelSelector` both reinvent “button + Popover + Command list”. Agent/provider/MCP pickers each reimplement that again with local search state.

**Fix.** `SmartSelect` is the generic searchable select. Domain pickers become data wrappers (the `ModelPicker` pattern). Do not add an eighth.

### Important — `AdvancedDisclosure` reimplements `Accordion`

**What.** `accordion.tsx` is a Radix accordion. `AdvancedDisclosure.tsx:29-35` is `useState` + caret icons + conditional mount, modelled on a copy-paste from `CreateAgentModal`. Two collapsible primitives.

**Fix.** `AdvancedDisclosure` becomes an `Accordion` with one item, or `Accordion` is documented as the only collapsible and `AdvancedDisclosure` is deleted after call sites migrate.

### Minor — cards, badges, lightboxes, sign-in dialogs

**Cards.** `AgentCard` and `WorkerCard` share layout (Badge + `IconRenderer` + bordered surface) and diverge on purpose (worker omits default-star and chat). Still two copies of the chrome. Other “cards” (`TaskCard`, `AttachmentCard`, `AskUserQuestionCard`, `GoalEchoCard`, `JudgeVerdictThreadCard`, `DefaultModelCard`, `ExecProxyStatusCard`) do not use `ui/card.tsx` consistently.

**Badges.** `Badge` (`cva`) vs `PolicyBadge` (raw button + template string, `PolicyBadge.tsx:30-31`) vs `ToolCallBadge` vs `RollupBadge`. Status colour is re-specified each time (`emerald-500` in `PolicyBadge` vs `--color-success` tokens in `Badge`).

**Lightboxes.** `image-lightbox.tsx` is the primitive; `MediaLightbox.tsx:1-9` is the store-mounted singleton that wraps it. That split is correct — not a duplicate. Do not add a third.

**Sign-in.** `SignInDialog` vs `ReSignInDialog` is a documented behavioural fork (`ReSignInDialog.tsx:5-20`), not accidental duplication. `ManageSignInDialog` and `ReAuthDialog` need a pass to confirm they are not a third and fourth copy of the same chrome. **UNVERIFIED** beyond the header comments.

### Minor — `AgentCard` / `WorkerCard` should be one card with a `kind` variant

**What.** `WorkerCard.tsx:13-19` lists the differences in a comment (show executor badge + test-run; omit chat / heartbeat / default-star). That is a variant, not a second component.

**Fix.** `AgentCard variant="colleague" | "worker"`.

## Documentation findings

### Critical — the code is the only spec; there is no component catalog

**What.** Verified absent (glob = 0):

- Storybook / `.storybook/` / `*.stories.*`
- `components.json` (the shadcn CLI manifest)
- Any README under `src/components/`
- `src/components/ui/CLAUDE.md`, `shared/CLAUDE.md`, `layout/CLAUDE.md`
- `packages/ui` README, vite config, or `dist/`
- Props tables

What exists instead:

- Header comments on some files (`FormError.tsx:1-28`, `QueryErrorState.tsx:1-23`, `ChipListInput.tsx:6-19`, `ConfirmActionModal.tsx:29-41`). These are good, and they are not a catalog.
- Per-feature `CLAUDE.md` (agents, chat, library, workspaces, settings, …) — module maps, not component APIs.
- `docs/using-omnipus-ui.md` — a user tour of the product, not a component library guide.
- `docs/internal/brand/brand-guidelines.md` — brand, not APIs.
- `src/index.ts` / `@omnipus/ui` — claims “Sovereign Deep component library” and exports four primitives.

H10 (help and documentation): a designer or a new agent looking for “the empty state” currently greps. They will find three.

**Fix.** In order of cost:

1. A markdown catalog in `src/components/ui/README.md`: one row per family, variants, states, do/don’t. Same file for `shared/`.
2. `components.json` so shadcn CLI additions land in the same place with the same conventions.
3. Storybook (or Histoire / Ladle) with a matrix of variant × size × disabled × loading × invalid for every primitive. Empty / error / skeleton as first-class stories.
4. Make `@omnipus/ui` export the actual catalog, or stop calling the stub a design system.

### Important — no contribution rule, so `ui/` absorbs domain widgets

**What.** Feature modules have a `CLAUDE.md` that says where new files go. `ui/` does not, so `ModelSelector`, `RestartConfirmDialog`, `AutoSaveIndicator`, and `BrandIcon` landed next to `Button`. The published export list was never updated, so the “library” a third-party would install is not the library the app uses.

**Fix.** A 20-line `src/components/ui/CLAUDE.md`: what may live here (generic, no product nouns), the API checklist (`cva`, `cn`, `forwardRef`, `disabled`, `className`, kebab-case), and “if it says Model / Gateway / Library, it is not a primitive”.

## Gaps vs best-in-class

Benchmark: shadcn/ui done fully, Radix patterns, Polaris / Carbon component APIs.

| Expectation | Omnipus today |
|---|---|
| Complete primitive kit (shadcn ~50) | ~26 families. No Tooltip, Skeleton, RadioGroup, Field, Alert, ScrollArea, Collapsible. Tooltip is a *dependency* of `@omnipus/ui` and unused. |
| `cva` + `cn` + `forwardRef` on every primitive | `cva` on 2 files. `cn()` skipped on Command/Popover/several composites. Refs skipped on Badge and all domain widgets. |
| Variant × size × state matrix (Polaris `Button`) | `Button` has variant+size+disabled. No loading, no full-width, no icon-only API beyond `size="icon"`. `Input` has none of it. |
| Field composite (Label + Control + Error + Description) | `Label`, `Input`, `FormError` are separate; 3 call sites wire them. |
| Empty state with action (Polaris `EmptyState`, Carbon `EmptyState`) | Shared `EmptyState` is icon + sentence. Knowledge is the only empty with a CTA. |
| Skeleton primitive | `SkeletonList` of three rows. Screens invent pulse blocks. |
| One loading / error / empty per list | Two error components, many local empties, pulse-div skeletons. |
| Tooltip as the hover-label primitive | Native `title=` (`PolicyBadge.tsx:17,28`). No Tooltip component. FullCalendar “Tooltip” hits are calendar event titles, not a UI primitive. |
| Storybook + a11y addon + props table | None. Tests cover 14 of 36 `ui/` files. |
| Published package = the kit | `@omnipus/ui` exports Button, Card, Input, Dialog, AppShell, Sidebar. |
| Tokenised colour, no raw palettes | Mostly CSS variables. `PolicyBadge` uses `emerald-500` / `amber-500` / `red-500` (`PolicyBadge.tsx:6-8`). `AutoSaveIndicator` uses `text-emerald-400` (`AutoSaveIndicator.tsx:62-64`). |
| Ellipsis convention (`…`) | Mixed `Saving...` / `Saving…` in the library itself. |
| Focus ring owned by the system | Global ring is good (`globals.css:168-179`). Library files still set `outline-none`. |
| Destructive confirm never dismissed by overlay (Radix AlertDialog) | True for `ConfirmActionModal`. False for `RestartConfirmDialog` (plain `Dialog`). |

Polaris / Carbon also version the library, changelog every component, and refuse new product UI that does not use the kit. Omnipus has none of that governance.

## Top 3 fixes by impact

1. **Make loading / empty / error a library API, then delete the copies.** One `Skeleton` primitive; one `EmptyState` with an action slot; one query `ErrorState` (merge `ListStates.ErrorState` into `QueryErrorState`); one `SaveIndicator`; `Button loading`; `Input` invalid styles. This is the H1 + H4 + H9 gap users actually feel — a failed fetch, an empty board, and a save-in-flight currently look different on every screen.

2. **Freeze the primitive contract and finish the kit.** Written checklist in `src/components/ui/CLAUDE.md`: `cva` + `cn` + `forwardRef` + `className` + `disabled` + kebab-case. Apply it to `Command`, `Popover`, `Badge`, `Avatar`, `SmartSelect`. Add the missing primitives the app already fakes (Tooltip, Skeleton, RadioGroup, Field). Move `ModelSelector` / `AutoSaveIndicator` / `RestartConfirmDialog` / `BrandIcon` out of `ui/`. Until this exists, every new file will copy the nearest neighbour, conventions included.

3. **Give the library a catalog.** A markdown inventory is the cheap version; Storybook is the real one. Export what `src/components/ui/` actually contains from `@omnipus/ui`, or stop describing that package as the design system. Without a place to look, “use the library” is an aspiration and 233 screen files will keep growing.

---

**Evidence note.** Counts from `find src/components -name '*.tsx' ! -name '*.test.tsx'` (283) and per-folder `find`. Import counts from `rg`. Absences (Storybook, `components.json`, `ui/CLAUDE.md`, Tooltip usage, `packages/ui` build files) from glob/rg returning zero. Sign-in dialog internals beyond their header comments: UNVERIFIED.
