# ResizeSeparator

ResizeSeparator is the side-panel shell's resize control: a keyboard- and
pointer-accessible separator (WAI-ARIA separator pattern) that publishes the
open panel's live width while dragging and commits it on release, with a
width-memory reset on double-click.

## Contract

- Role `separator`, focusable, `aria-valuenow`/`aria-valuemin`/`aria-valuemax`
  in pixels, `aria-valuetext` "N pixels wide".
- Pointer drag commits on release; keyboard (arrows, 16px steps, mirrored;
  Home/End to bounds) commits 300ms after the last keypress; double-click
  resets to the default width (deletes the stored width — the default is
  re-derived at read time).
- Public exports: `ResizeSeparator`.
- Theme: dark. Variants: docked. States: idle, dragging.

The colocated stories cover the docked presentation and the dragging state;
the manifest maps applicable unit, interaction, axe, browser, keyboard,
pointer, forced-colors, reduced-motion, zoom and reflow checks to executable
evidence (root-size is inapplicable: the separator is a 16px-wide control in
a fluid row, not a size-bearing root).
