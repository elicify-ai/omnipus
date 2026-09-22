# sheet

Source: `src/components/ui/sheet.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `Sheet, SheetPortal, SheetOverlay, SheetTrigger, SheetClose, SheetContent, SheetHeader, SheetFooter, SheetTitle, SheetDescription`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

Sheet uses Radix Dialog for open state, focus management, and dismissal. The default modal sheet traps focus and blocks background interaction. Escape, outside interaction, and the built-in “Close” button request dismissal unless the caller prevents the relevant event; closing normally restores trigger focus.

`SheetContent` supports top, bottom, left, and right placement, defaulting to right. Its content scrolls vertically with contained overscroll. `overlay={false}` omits the visual overlay; it does not switch the root to nonmodal behavior. `showClose={false}` omits the built-in close button while retaining Radix dismissal behavior and any caller-provided close control. Use `SheetTitle` and `SheetDescription` for the accessible name and description.

`SheetFooter` preserves its desktop row geometry. Below `sm`, actions stack in DOM order with the standard compact gap; coarse pointers receive the larger separation needed for adjacent 44px hit regions.

Sheet supports named `sm`, `md`, and `lg` widths. The existing `widthClass` prop remains supported and takes precedence during migration.

`Sheet` preserves the Radix `modal` root option. The default modal sheet declares `aria-modal="true"`; `modal={false}` selects Radix's nonmodal interaction branch and omits that claim. An explicit `aria-modal` supplied to `SheetContent` retains caller precedence.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.
