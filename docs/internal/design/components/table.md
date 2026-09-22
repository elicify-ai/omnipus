# Table

Table is a public presentation primitive in the Omnipus design system. It preserves the existing Sovereign Deep appearance and consumes shared foundation tokens.

## Contract

- Variants: default, selected.
- Sizes: default, dense.
- States: default, hover, selected, overflow-focus.
- Public exports: Table, TableHeader, TableBody, TableRow, TableHead, TableCell.
- Theme: dark.

The colocated stories cover every declared variant, size and state, plus dense and narrow-viewport presentations. The Overflow Region story proves that the named wrapper has real horizontal overflow and accepts focus. Its manifest keyboard check sends ArrowRight through Playwright and verifies that the region advances horizontally.

`containerProps` target the actual horizontal overflow container. A scrollable data region should provide `role="region"`, an accessible label, and `tabIndex={0}`. When that container itself has focus, ArrowLeft and ArrowRight move it by 40px where movement is available. This explicit step matches the typical native movement while avoiding WebKit's intermittent failure to perform the default scroll. The caller's `onKeyDown` runs first; preventing default cancels the built-in movement. Arrow events from controls inside table cells are left untouched.

The selected story applies `data-state="selected"` to the actual row. The reflow exemption is limited to `[data-table-scroll]`, the element that owns the intentional horizontal overflow. The manifest maps applicable unit, interaction, accessibility, keyboard, motion, forced-colour, root-size, zoom and reflow checks to executable evidence.
