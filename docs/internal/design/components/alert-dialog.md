# alert-dialog

Source: `src/components/ui/alert-dialog.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `AlertDialog, AlertDialogPortal, AlertDialogOverlay, AlertDialogTrigger, AlertDialogContent, AlertDialogHeader, AlertDialogFooter, AlertDialogTitle, AlertDialogDescription, AlertDialogCancel, AlertDialogAction`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

AlertDialog is composed from Radix Dialog and exposes `role="alertdialog"` on its content. The root retains Radix’s default modal focus handling. Outside pointer and focus interactions are prevented from dismissing the surface; caller outside-interaction handlers are still invoked after prevention. Escape retains Radix dismissal behavior unless a caller prevents it. The panel has a 90dvh maximum height, vertical scrolling, and contained overscroll.

An enabled `AlertDialogCancel` requests closure through Radix Close. `AlertDialogAction` requires an `onClick` callback and invokes the caller without automatically closing the dialog. The caller owns controlled open state, asynchronous work, and when an action result should close the surface. This primitive does not introduce a separate pending-operation policy. Supply `AlertDialogTitle` and `AlertDialogDescription` for the accessible name and description.

## Footer spacing (D7)

Callers place Cancel first and Action last in `AlertDialogFooter`. The footer preserves that DOM order at every breakpoint; CSS must not reverse it.

| Condition | Layout | Gap |
|---|---|---|
| Width below `sm`, fine pointer | Column, Cancel above Action | Unprefixed `gap-2` (0.5rem) |
| Width below `sm`, coarse pointer | Column, Cancel above Action | `max-sm:pointer-coarse:gap-6` (1.5rem) |
| `sm` and up, any pointer | Row, Action right-most (`sm:justify-end`) | `sm:space-x-2` with `sm:gap-0` |

The combined Tailwind variant is `max-sm:pointer-coarse:gap-6`. Both conditions are required: unprefixed `gap-6` would enlarge fine-pointer stacks, and `pointer-coarse:gap-6` without `max-sm` can fight the `sm+` row gap. Button chrome stays `h-9`; this is spacing only.

Visual delta: D7 Normalization. Stacked coarse-pointer gap `0.5rem` → `1.5rem`. Dismissal, Escape, overlay-click block, and Action-does-not-auto-close are unchanged.
