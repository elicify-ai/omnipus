# SegmentedControl

`SegmentedControl` is a `role="group"` cluster of independently-tabbable toggle
buttons (`aria-pressed`) for a single-select choice presented as a flat button
row: view-mode switches, preset pickers, period selectors. It is built on the
`Button` primitive, never a raw `<button>`.

This is the WAI-ARIA "group of toggle buttons" pattern, and it is deliberately
**not** a roving-tabindex widget. Every item keeps its own normal Tab stop —
there is no roving `tabindex` and no arrow-key navigation. The exclusive-
choice, roving-tabindex pattern lives in `RadioGroup` (`./radio-group.tsx`);
`SegmentedControl` preserves the distinction rather than collapsing the two
shapes into one component. Every one of its audited call sites (view/edit
mode toggles, auth-method pickers, risky-setting selectors, calendar view
switchers, usage period selectors, and others) already renders exactly this
shape by hand — `role="group"` plus per-button `aria-pressed` plus ordinary
tab order — so the component formalizes an existing pattern rather than
introducing a new one.

## Contract

- Variants: default.
- Sizes: default.
- States: default (unselected), selected, disabled.
- Public exports: SegmentedControl, SegmentedControlItem.
- Theme: dark.

`SegmentedControl` is controlled — there is no uncontrolled mode. It requires
an accessible name via `aria-label` or `aria-labelledby` (a discriminated
union statically forbids supplying both or neither). The selected item is
marked `aria-pressed="true"` and carries `data-state="on"`; every other item
is `aria-pressed="false"` with `data-state="off"`. A group-level `disabled`
prop disables every item unless an individual `SegmentedControlItem` sets its
own `disabled`, which always wins.

## Keyboard and pointer behavior

Each item is a normal, explicit Tab stop (`tabIndex={0}`), so keyboard users
move between segments with the browser's ordinary Tab order, not arrow keys.
Activation is a plain click or Space/Enter on the focused button, routed
through `Button`'s native `<button>` semantics. There is no `role="tablist"`,
no `aria-controls`, and no roving `tabindex` — adding any of those would
misrepresent the widget, since nothing here manages focus or exposes a
controlled panel.

## Forced-colors mode

Because every segment renders through `Button`, it inherits Button's forced-
colors contract: a `ButtonText` border, a `ButtonFace` background, and
`ButtonText` text, with `forced-color-adjust: none` so those explicit system
colors apply consistently regardless of the segment's selected/unselected
Tailwind classes. The shared `:focus-visible` rule additionally switches the
1px, 2px-offset focus outline from Forge Gold to the system `Highlight`
color under `forced-colors: active`, so focus stays visible and
distinguishable from the `ButtonFace` background rather than washing out
against it. The selected item's `aria-pressed="true"` state remains present
in the accessibility tree in forced-colors mode exactly as it does in
ordinary rendering — forced colors changes paint, not semantics.

The colocated stories cover the default selection, a disabled item, a real
pointer-driven selection change, and a narrow-viewport presentation. The
manifest maps applicable unit, interaction, accessibility, keyboard, pointer,
motion, forced-colour, root-size, zoom and reflow checks to executable
evidence.
