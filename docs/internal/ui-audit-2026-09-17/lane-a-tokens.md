# Lane A — Design Tokens & Theming

Audit date: 2026-09-17.
Scope: Omnipus SPA under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/src`.
Criteria: elicify-ui-ux-design (visual-system, checklist §§5–6 and §12), frontend-design, brand spec `docs/internal/brand/brand-guidelines.md`.
Method: read `src/styles/globals.css` and satellite theme files, then count literals with `rg` over `src/**/*.{tsx,ts,css}`, excluding tests and generated OpenAPI types unless noted.

## Maturity verdict: 3 (defined)

There is a real token file, it matches the five brand colours and the three brand fonts, dark is the structural default, and the shadcn primitives (button, card, input) consume those tokens. That is a defined system, not ad-hoc.

It is not managed, and it is not best-in-class. Colour tokens are a flat list of hex values with semantic names — there is no primitive ramp underneath, and no component layer above. Type, radius, shadow, and motion are almost entirely untokenized. A second, competing status palette in the calendar paints “in progress” blue instead of Forge Gold. Six hundred and sixteen `text-[Npx]` classes sit below the 12px floor. Tailwind’s default red/amber/blue scale is still used next to the brand tokens. Hex is copied into TypeScript maps because consumers concatenate an alpha suffix onto a string — a real constraint, but it means CSS is no longer the single source of truth.

Qualitative answer: **gappy**. The foundation exists and the brand seeds are correct; coverage, layering, and enforcement are not.

## What exists (inventory with file:line)

### Single CSS source

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/src/styles/globals.css` is the only `@theme` block. Imported from `src/main.tsx:9`. No `tailwind.config.*`. Tailwind v4 `@theme` (lines 16–89) plus a handful of `@utility` helpers (lines 99–128).

Self-hosted fonts (no CDN): Outfit 400/700, Inter 400/500/600, JetBrains Mono 400/500 (`globals.css:3–9`).

Lock tests: `src/test/theme.test.ts` asserts the five brand colour tokens and the three font stacks against the CSS file (string contains, not computed style).

### Colour tokens (`@theme`, `globals.css:17–53`)

| Token | Value | Brand role |
|---|---|---|
| `--color-primary` | `#0a0a0b` | Deep Space Black — match |
| `--color-secondary` | `#e2e8f0` | Liquid Silver — match |
| `--color-accent` | `#d4af37` | Forge Gold — match |
| `--color-success` | `#10b981` | Emerald — match |
| `--color-error` | `#ef4444` | Ruby — match |
| `--color-warning` | `#EAB308` | extension (not in brand sheet) |
| `--color-info` | `#3B82F6` | extension |
| `--color-cancelled` | `#F97316` | extension |
| `--color-accent-hover` | `#c49e2f` | accent hover |
| `--color-error-hover` | `#dc2626` | error hover |
| `--color-mount` | `#8ea3bd` | library mount icon |
| `--color-surface-0` | `#0a0a0b` | same hex as primary |
| `--color-surface-1` | `#111113` | raised |
| `--color-surface-2` | `#141416` | raised |
| `--color-surface-3` | `#222228` | raised |
| `--color-muted` | `#9ca3af` | muted text (WCAG note in-file) |
| `--color-border` | `#2d3748` | borders |

Four surface steps. No 5–10 shade ramp per hue (visual-system / Refactoring UI). `--color-primary` and `--color-surface-0` are the same hex under two names — alias, not a layer.

Semantic names are bound directly to hex. There is no primitive (`gold-500`) → semantic (`color-accent` / `color-text-on-accent`) → component (`button-primary-bg`) stack.

### Typography tokens (`globals.css:55–58`)

| Token | Stack | Brand role |
|---|---|---|
| `--font-headline` | Outfit, system-ui, -apple-system, sans-serif | headlines — match |
| `--font-body` | Inter, system-ui, -apple-system, sans-serif | body — match |
| `--font-mono` | JetBrains Mono, ui-monospace, monospace | code — match |

Utilities `.font-headline` / `.font-body` / `.font-mono` at `globals.css:214–222`. Body uses `--font-body` and `line-height: 1.6` (`globals.css:186–191`) — that line-height matches the brand body spec.

**Missing vs brand type scale** (guidelines §4): H1 48px / 700 / 1.1, body 16px / 400 / 1.6, caption 12px / 500 / 1.4. None of those sizes, weights, or roles exist as tokens. Root size is `clamp(12px, var(--user-font-size, 14px), 20px)` (`globals.css:141`) — product default **14px**, not the brand 16px. Settings default is the same 14 (`ProfileSection.tsx:67`).

`--font-sans` is not overridden, so Tailwind’s `font-sans` utility is not Inter.

### Spacing, radius, shadow, motion

Present:

- `--spacing-sidebar` (`globals.css:62`)
- `--spacing-tap-target-min: 44px` and `--spacing-tap-target-comfortable: 40px` (`globals.css:68–69`)
- `--spacing-chrome-header: 44px` (`globals.css:76`)
- `--spacing-browser-tabs: 34px` (`globals.css:85`)
- `--ease-spring: cubic-bezier(0.34, 1.56, 0.64, 1)` (`globals.css:88`)

Absent from `@theme`: `--radius-*`, `--shadow-*`, `--text-*` / type-scale, duration tokens, gold-with-alpha tokens.

Radius in primitives is Tailwind’s default scale (`rounded-md` on Button `button.tsx:7`, `rounded-xl` on Card `card.tsx:10`). Shadows are `shadow-lg` / `shadow-md` plus one-off `box-shadow` literals in satellite CSS.

### Dark-first machinery

Dark is the **only** theme, applied at the document:

```
body { background-color: var(--color-primary); color: var(--color-secondary); }
```

`globals.css:186–191`. No `.dark` class, no `data-theme`, no `prefers-color-scheme`, no ThemeProvider (`rg` over `src/**/*.{tsx,ts,css}` returned 0). That is dark-as-structure, not dark-as-a-mode. 2026 table-stakes (checklist §12) asks for semantic tokens whose *values* swap per theme, AA in both modes, and a manual override. None of that machinery exists.

Focus ring is tokenised: 1px Forge Gold, 2px offset (`globals.css:168–178`).

### Satellite theme files (token consumers, with some literals)

- `src/styles/fullcalendar-theme.css` — maps FullCalendar `--fc-*` onto `var(--color-*)`. Still hardcodes `--fc-event-text-color: #0a0a0b` (line 45), today-pill `color: #0a0a0b` (line 172), and `rgba(212, 175, 55, …)` gold tints (lines 34, 40, 204, 493).
- `src/components/workspaces/reactflow-theme.css` — mostly tokens. `font-family: var(--font-body, 'Inter', sans-serif)` (line 89). `font-size: 9px` on attribution (line 67). Hardcoded `box-shadow: 0 4px 16px rgba(0, 0, 0, 0.5)` (line 23).

### Parallel TypeScript palettes (second sources of truth)

These exist because some consumers build `#RRGGBBAA` by concatenating `1a` onto a hex string (`statusColors.ts:102–110`) — `var(--color-warning)1a` is invalid CSS. The constraint is real; the result is duplicated hex.

| File | Role |
|---|---|
| `src/lib/statusColors.ts:38–45` | Task status hex (inbox `#9ca3af`, next `#3B82F6`, in_progress `#D4AF37`, …) |
| `src/lib/statusColors.ts:112` | `TASK_CANCELLED_COLOR = '#EAB308'` (warning yellow, **not** `--color-cancelled` `#F97316`) |
| `src/lib/planStateColors.ts:30–36` | Plan state hex, same values, deliberately not shared |
| `src/lib/planStateColors.ts:87` | `PLAN_CANCELLED_COLOR = '#EAB308'` |
| `src/lib/constants.ts:14` | `AVATAR_COLORS` — eight hex including off-brand `#22C55E`, `#A855F7` |
| `src/components/calendar/types.ts:220–263` | **Competing** chip palette (see Findings) |
| `src/components/chat/AttachmentCard.tsx:54–68` | Per-file-type hex, documented as intentional |
| `src/components/chat/mermaid-renderer.tsx:196–278` | ~60 mermaid theme hexes, some “keep in sync” comments |

### How components actually consume tokens

Wave 0 spec asked for `bg-primary` / `text-secondary` utilities. The SPA almost never uses them (**15** hits in 3 files). It uses the verbose form `bg-[var(--color-…)]` (**970** hits / 215 files) and `text-[var(--color-…)]` (**2298** hits / 256 files). Functionally equivalent; the short utilities are unused.

Button default: Forge Gold fill, Deep Space text (`button.tsx:12–13`). Card: `surface-1`, Liquid Silver, `rounded-xl` (`card.tsx:10`). Wordmark: Outfit + gold `.ai` (`Wordmark.tsx:10–11`).

## Findings

### Important — Calendar chips are a second status palette; “in progress” is not Forge Gold

**What.** `src/lib/statusColors.ts:8–10` states the marquee live-work colour **must** be Forge Gold `#D4AF37`, and that each view previously hard-coding its own hex was the bug this module exists to stop. The calendar then defines its own map:

```
done:        #34D399
in_progress: #60A5FA   ← blue, not gold
blocked:     #FBBF24
failed:      #F87171
inbox/next:  #94A3B8
```

`src/components/calendar/types.ts:220–227`. Same task is gold on the board/graph and sky-blue on the calendar.

**Violates.** Brand 60-30-10 (Forge Gold is the 10% accent for live/CTA). Jakob’s Law / consistency (checklist §4). The repo’s own `statusColors.ts` contract.

**Fix.** Point `STATUS_STYLE.bg` at `statusColor()` (or a tokenised equivalent). If calendar chips need brighter fills for AAA text on `#0A0A0B`, derive those fills from the same hue tokens (a gold-400/gold-500 ramp), do not invent a second hue.

### Important — No primitive → semantic → component layer; hex lives in CSS and in TypeScript

**What.** `@theme` is a flat dictionary. Material 3 / Polaris / Carbon all separate (1) primitive ramps, (2) semantic roles that swap per theme, (3) component tokens. Here `--color-accent` *is* `#d4af37`. Task/plan maps then re-state the same hex (`statusColors.ts:38–45`, `planStateColors.ts:30–36`) because of the alpha-concat trick (`statusColors.ts:102–110`). `--color-cancelled` is `#F97316` while cancelled pills use `#EAB308` (`TASK_CANCELLED_COLOR`, line 112) — the cancelled *token* is not the cancelled *colour*.

**Violates.** Checklist §12 (“color system on semantic tokens”). Carbon/Polaris layering. Single source of truth.

**Fix.** Add primitive ramps (`gold-100…900`, `red-…`, `gray-…`). Point semantic roles at them. Emit a small JS module from the CSS (or Style Dictionary) so TS maps import values instead of duplicating them. Prefer `color-mix(in srgb, var(--color-accent) 10%, transparent)` over string-concatenated `#RRGGBBAA`. Align `--color-cancelled` with the colour actually painted for cancelled, or rename the token.

### Important — Type scale is untokenized; 616 classes are below 12px

**What.** No `--text-*` / `--font-size-*` tokens. Brand body is 16px; product root is 14px (`globals.css:141`, `ProfileSection.tsx:67`). Production `text-[Npx]` (excluding tests):

| Size | Count |
|---|---|
| 10px | 364 |
| 11px | 211 |
| 9px | 39 |
| 13px | 19 |
| 12px | 19 |
| 14px | 12 |
| 8px | 1 |
| 7px | 1 |
| **Total** | **666 in 153 files** |
| **Below 12px** | **616** |

Citations for the floor violations:

- 7px: `src/components/search/SearchModal.tsx:251`
- 8px: `src/components/browser/BrowserLiveView.tsx:2742`
- 9px (representative): `src/components/shared/ToolPolicyEditor.tsx:318`, `src/components/workspaces/team/WorkspaceTeamGraph.tsx:166`, `src/components/library/LibraryEntryRow.tsx:217`
- 10px cluster: `src/components/chat/ChatScreen.tsx` (19), `src/components/chat/tools/GenericToolCall.tsx` (18), `src/components/library/search/LibrarySearchBar.tsx` (14), plus 117 files with at least one `text-[10px]`
- CSS: `reactflow-theme.css:67` `font-size: 9px`; FullCalendar `0.65rem` / `0.6rem` at `fullcalendar-theme.css:546–560`

**Violates.** Visual-system: body ≥ 16px web (14px only for dense UI); **never below 12px**. Brand §4 body 16px / caption 12px. Checklist §5 (≤ 7 distinct sizes, modular scale ≥ 1.2). 666 arbitrary sizes is many more than 7.

**Fix.** Tokenise a closed scale (caption 12 / body 16 / title / display 48 as brand specifies). Replace `text-[10px]`/`text-[11px]` with `text-xs` or a `text-caption` token at 12px. Lint-ban `text-[Npx]` below 12. Revisit the 14px root vs 16px brand.

### Important — Tailwind’s default palette is still in the product

**What.** `text-red-400`, `text-amber-400`, `bg-red-600`, `bg-amber-500/10`, `text-blue-400`, `text-violet-300`, `bg-emerald-500/20` — **78** `text-(red|amber|blue|green|yellow|orange)-*` hits across **32** production files, plus more `bg-` / `border-` siblings. Representative:

- `src/components/screens/ConnectorsScreen.tsx:768` `bg-red-600 hover:bg-red-700 text-white` (destructive uses Tailwind red, not `--color-error`)
- `src/components/connectors/EmailMailboxPanel.tsx:815` same
- `src/components/settings/AuditLogViewer.tsx:34–49` blue/red/amber badge scale
- `src/components/shared/ToolPolicyEditor.tsx:387–388` amber/red policy chips
- `src/components/workspaces/TaskCard.tsx:80,83` P1 `bg-red-500/20 text-red-400`, P4 `bg-blue-500/20 text-blue-400`
- `src/routes/onboarding.tsx:567` `border-red-500/40 bg-red-500/10 text-red-400`

Those colours are Tailwind’s default ramps, not Sovereign Deep. They sit next to `var(--color-error)` / `var(--color-warning)` on neighbouring screens.

**Violates.** Brand 2–3 hues + grey + semantic (checklist §6). Wave 0 US-1: “without manual color values.”

**Fix.** Map warning/error/info chips onto `--color-warning` / `--color-error` / `--color-info` (with `/10` `/20` alpha, or `color-mix`). ESLint `no-restricted-syntax` on `red-`, `amber-`, `blue-`, `violet-`, `emerald-` utilities except in tests.

### Important — Hardcoded hex and rgba outside the token file

**Quoted hex, production, tests and generated types excluded: 125 occurrences in 12 files.**

Largest cluster — Mermaid theme, ~60 literals, including invented node fills and a 12-stop pie ramp:

- `src/components/chat/mermaid-renderer.tsx:204–278` (`primaryColor: '#23232a'`, `pie1: '#d4af37'`, …). Lines 210 and 220 comment “keep in sync with globals.css”.

Other production hex (not tests):

- Calendar chips: `types.ts:208, 221–226, 230, 240, 249, 263`
- File-type accents: `AttachmentCard.tsx:54–68` (`#E5484D`, `#2563EB`, `#16A34A`, `#7C3AED`, …)
- Avatars: `constants.ts:14`
- Status/plan maps: `statusColors.ts:38–45,112`, `planStateColors.ts:30–36,87`
- Onboarding stroke: `onboarding.tsx:614,617` `'#d4af37'` / `'#2d3748'`
- Signature/PDF canvas: `LibrarySignaturePad.tsx:102`, `LibraryPdfPreview.tsx:1470` `strokeStyle = '#111111'`
- Brand-icon fallbacks: `brand-icon.tsx:80–82` `#1A1A1C`, `#2A2A2E`, `#FFFFFF` (stale vs `--color-surface-2` `#141416` / `--color-border` `#2d3748`)
- Sandbox buttons: `SandboxSection.tsx:1300,1330` `color: '#fff'`

**Tailwind arbitrary hex (production):**

- `AskUserQuestionCard.tsx:376` `text-[#1a1503]` on a gold button (should be `--color-primary`)
- `BashOutput.tsx:164` and `FileReadPreview.tsx:104` `bg-[#0d1117]` (GitHub dark, not a surface token)

**CSS colon-hex outside `@theme`:** `fullcalendar-theme.css:45,172` (`#0a0a0b`). The other colon-hex hits in that file are comments documenting tokens.

**`rgba(212, 175, 55, …)` gold tints** (token exists; alpha does not): `onboarding.tsx:550,559,616,773,896,1184,1464`; `login.tsx:126,139,169–170`; `fullcalendar-theme.css:34,40,204,493`; `command.tsx:84`; `LibraryCodeEditor.tsx:62,70,72,76`; `LibraryPdfPreview.tsx:1697,1705`; `FullCalendarView.tsx:264`; graph shadows in `TaskNode.tsx:90,93`.

**Violates.** Wave 0 US-1 / US-2. Checklist §4 “radii, shadows, type styles reused from tokens.”

**Fix.** Add `--color-accent-muted` / `--color-accent-ghost` (or a documented `color-mix` helper). Replace `#0d1117` with `surface-0`/`surface-1`. Replace mermaid literals with `getComputedStyle` reads of the CSS variables at init (mermaid accepts any string, including `var()` only if it supports it — if not, resolve once). Keep AttachmentCard’s per-type hues only if they are promoted to named `--color-file-*` tokens.

### Important — Dead and dangling token names

**What.**

- `font-inter` / `font-outfit` are used (`calendar.tsx:19,42,51,54`) but `@theme` only defines `--font-headline`, `--font-body`, `--font-mono`. Those classes do nothing; the calendar falls through to whatever the parent set.
- `brand-icon.tsx:83` `var(--font-outfit, Outfit, sans-serif)` — `--font-outfit` does not exist; it works only via the Outfit fallback.
- `BrowserLiveView.tsx:3113` `text-[var(--color-text-secondary)]` — **`--color-text-secondary` is not defined anywhere in `src/`**. The status line inherits whatever the parent colour is.

**Violates.** Wave 0 US-1 AC2 (font-headline / font-body / font-mono). Documented-but-unwired (a named custom property that nothing sets).

**Fix.** Alias `--font-outfit: var(--font-headline)` and `--font-inter: var(--font-body)`, or rename the classes. Define `--color-text-secondary` as an alias of `--color-muted`, or change the one call site.

### Important — No radius, shadow, or motion token set

**What.** One easing token (`--ease-spring`, `globals.css:88`). No duration scale. No elevation shadows. No radius scale. Primitives pick `rounded-sm` / `md` / `lg` / `xl` / `full` independently. Satellite CSS invents `10px` (`reactflow-theme.css:21,49`), `6px` (`fullcalendar-theme.css:427`), `4px` (line 239), `3px` (`globals.css:234`). Shadows: `0 8px 24px rgba(0,0,0,0.5)` (`fullcalendar-theme.css:428`), `0 4px 16px rgba(0,0,0,0.5)` (`reactflow-theme.css:23`).

**Violates.** Visual-system motion table (100–500 ms, named easings). Carbon elevation. Checklist §4 consistency of radii/shadows.

**Fix.** `--radius-sm/md/lg/xl/full`, `--shadow-1/2/3`, `--duration-fast/normal/slow` (e.g. 120 / 200 / 300 ms) and `--ease-out` / `--ease-spring`. Point Card/Dialog/Button at them.

### Minor — `font-sans` is not Inter; Fira Code is an extra family

`font-sans` appears 22 times in 5 files (`GenericToolCall.tsx` 13 of them). `--font-sans` was never set, so those nodes are Tailwind’s default stack, not Inter. `markdown-shared.tsx:236` and `LibraryCodeEditor.tsx:53` declare `fontFamily: '"JetBrains Mono", "Fira Code", monospace'` instead of `var(--font-mono)`. Fira Code is not a brand font and is not loaded.

**Violates.** Brand §4 (three families). Checklist §5 (≤ 2 typefaces — brand already allows 3; a fourth is drift).

**Fix.** `--font-sans: var(--font-body)` in `@theme`. Replace the inline stacks with `var(--font-mono)`.

### Minor — Shiki theme is Vitesse Dark, not Sovereign Deep

`markdown-shared.tsx:229` `theme="vitesse-dark"`. Wrapper background is tokenised (`!bg-[var(--color-surface-2)]`) but syntax colours are Vue/Vitesse greens and blues, not Liquid Silver / Forge Gold. `fontSize: '11px'` (line 234) is also below 12px.

**Fix.** A custom Shiki theme from the token set, or at least `fontSize` via a caption token.

### Minor — Wave 0 short utilities unused

`bg-primary` / `text-secondary` / `text-accent` were the promised authoring API (wave0-brand-design-spec US-1 AC4). Fifteen hits vs thousands of `*[var(--color-*)]`. Not wrong, just two dialects.

**Fix.** Prefer the short utilities in new code; they are what `@theme` is for.

### Not a defect (called so it is not re-litigated)

- Dark-only (no light theme) matches “dark-first” as a product claim. It fails the 2026 “both modes + override” table-stakes item; that is a product decision, not a broken default.
- `AVATAR_COLORS` off-brand greens/purples are identity swatches, not chrome. Acceptable if they stay off chrome.
- AttachmentCard per-type hues are labelled intentional (`AttachmentCard.tsx:42–46`). Still untokenized; severity is Important only if they leak into chrome.
- WhatsApp QR `bg-white` (`WhatsAppPairingBody.tsx:75`) and PDF/signature `bg-white` are document surfaces that must stay white. Legitimate exceptions; tokenise as `--color-paper` if they spread.
- `text-white` on `--color-error` fills is a contrast choice (Ruby on white vs Liquid Silver). Prefer a `--color-on-error` token rather than raw `white`.

## Gaps vs best-in-class (Material 3 / Polaris / Carbon)

| Capability | M3 / Polaris / Carbon | Omnipus today |
|---|---|---|
| Primitive ramps (8–10 steps / hue) | Yes | No — one hex per role |
| Semantic layer that swaps per theme | Yes | Names are the hex; no theme swap |
| Component tokens (`button.primary.bg`) | Yes (esp. Carbon, M3) | CVA classes read semantic vars directly |
| Typed JS/TS token objects generated from CSS | Common | Hand-copied hex in 6+ TS modules |
| Light + dark, AA in both, manual override | Table stakes 2026 | Dark only, no override |
| Type scale as tokens | M3 display/headline/title/body/label | None; 666 px literals |
| Radius / elevation / motion tokens | Yes | One spring easing |
| Lint / Stylelint against off-token colour | Typical at managed (4) | None — theme.test.ts only checks the CSS file contains strings |
| 60-30-10 + 2–3 hues | Enforced by semantic roles | True in chrome; broken by calendar blue, Tailwind red/amber, mermaid pie ramp |

Closest analogue: an early shadcn + CSS-variable setup that got the brand seeds right and then grew satellite palettes instead of extending the token file.

## Top 3 fixes by impact

1. **One status colour, owned by CSS.** Delete the calendar `STATUS_STYLE` hex map (`types.ts:220–227`) and drive chips from `statusColors` / tokenised ramps so “in progress” is Forge Gold on every view. Generate the TS hex maps from the CSS (or `color-mix` for tints) so alpha concatenation stops being a reason to duplicate `#D4AF37`. This is the brand-visible bug.

2. **Tokenise type and enforce a 12px floor.** Add a closed scale that includes brand H1 48 / body 16 / caption 12. Replace the 666 `text-[Npx]` classes; lint-ban anything below 12px. Decide 14px vs 16px root against the brand sheet (today they disagree).

3. **Finish the layers the `@theme` file started.** Primitive ramps; semantic aliases (`--color-text-secondary`, `--font-sans`, `--font-outfit`); gold-alpha, radius, shadow, duration tokens; ban Tailwind default `red-*` / `amber-*` / `blue-*` in production. Point mermaid, FullCalendar gold tints, and `#0d1117` code wells at those tokens. Then theme.test.ts can assert computed style, not just that a string appears in a file.

---

Evidence: counts from `rg` on this worktree, 2026-09-17. Contrast ratios of calendar chips vs board pills were not measured in a browser — the hue divergence is observed in source only. Marked UNVERIFIED: whether `font-inter`/`font-outfit` compute to anything via an undocumented Tailwind plugin (none is present in `globals.css` `@theme`; likely no-ops).
