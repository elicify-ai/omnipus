# DisclosureRow

`DisclosureRow` is the shared "tool call row header" primitive: a full-width disclosure
toggle that expands or collapses a detail panel the caller renders below it. It is built
on the `Button` primitive (never a raw `<button>`), rendered as a single flex item so a
caller can place an independent sibling action — e.g. a "Watch live" launcher — beside it
in their own flex row without nesting two interactive controls inside one another.

C2-PREP's `inventory.json` flagged this exact shape on 9 near-identical hand-built rows
across `src/components/chat/` and recommended building it once rather than hand-swapping
each file separately: `GenericToolCall.tsx` (the canonical shape), `ToolCallBadge.tsx`,
`ActivityPanel.tsx`, `SubagentBlock.tsx`, `FileReadPreview.tsx`, `WebSearchResult.tsx`,
`WebFetchPreview.tsx`, `BrowserNavigate.tsx`, `BrowserTool.tsx`, `BashOutput.tsx`. A tenth
file, `FileTreeView.tsx`, shares the identical header shape but is owned by a different
ledger group (tree-depth indentation) — once that lane points its header at this
primitive too, the shape converges to one definition everywhere.

## Contract

- Variants: default.
- Sizes: default.
- States: collapsed, expanded, not-expandable (disabled).
- Public exports: DisclosureRow.
- Theme: dark.

`DisclosureRow` is controlled — there is no uncontrolled mode. `expanded` and
`onExpandedChange` own the open/closed state; `expandable` tells the row whether it
currently has anything to disclose at all.

## The aria-expanded omission convention

When `expandable` is `false` (the call is still running, or there is no args/result/error
to show), the row:

- is natively `disabled`, removing it from the tab order;
- OMITS `aria-expanded` entirely rather than pinning it to `false`.

This is a deliberate convention carried over byte-for-byte from `GenericToolCall.tsx` and
`ToolCallBadge.tsx`'s own code comments: pinning `aria-expanded="false"` on a row that can
never actually expand would falsely announce "collapsible, currently collapsed" to
assistive technology — an inert-focusable trap. Every one of the ~9 audited call sites
folds its own "is there anything to show" AND "is it still running" logic into the single
boolean it passes as `expandable`; `DisclosureRow` does not take a separate `isRunning`
prop, because the callers' conditions for "nothing to disclose" already differ slightly
(some gate on `isRunning` alone, some on `hasDetail = !isRunning && (args || result ||
error)`).

## Sibling-not-nested convention

A disclosure toggle must never contain a second interactive control — a `<button>` cannot
nest inside a `<button>`. `BrowserNavigate.tsx`'s "Watch live" launcher is the audited
precedent: it renders as a sibling of the toggle inside the caller's own
`flex items-center` row, never as a child. `DisclosureRow` renders as a single `flex-1`
item for exactly this reason — it has no slot for a second action, and callers that need
one compose it beside `DisclosureRow`, not inside it.

## Keyboard and pointer behavior

`DisclosureRow` is a normal, explicit Tab stop (`tabIndex={0}`) when expandable, routed
through `Button`'s native `<button>` semantics — activation is a plain click or
Space/Enter on the focused button. There is no roving tabindex and no arrow-key
navigation; each row in a list of tool calls is its own ordinary Tab stop, matching every
audited call site.

## Forced-colors mode

Because `DisclosureRow` renders through `Button`, it inherits Button's forced-colors
contract unchanged: a `ButtonText` border, a `ButtonFace` background, and `ButtonText`
text, with `forced-color-adjust: none`.

The colocated stories cover the default two-row toggle interaction, the disabled
(non-expandable) state, an explicit open/close interaction test, and a narrow-viewport
presentation. The manifest maps applicable unit, interaction, accessibility, keyboard,
pointer, motion, forced-colour, root-size, zoom and reflow checks to executable evidence.
