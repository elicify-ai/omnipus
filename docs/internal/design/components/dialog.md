# dialog

Source: `src/components/ui/dialog.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `Dialog, DialogPortal, DialogOverlay, DialogTrigger, DialogClose, DialogContent, DialogHeader, DialogFooter, DialogTitle, DialogDescription`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

Dialog delegates open state and focus management to Radix Dialog. The default modal root traps focus and blocks background interaction; Escape, outside interaction, and the built-in “Close” button request dismissal unless a caller prevents the relevant event. Closing normally restores focus to the trigger. Callers may select Radix’s nonmodal mode or customize its focus and dismissal handlers.

`DialogContent` renders its panel and overlay through a portal. The panel has a 90dvh maximum height, vertical scrolling, and contained overscroll. `overlayClassName` changes the overlay presentation without removing the overlay or changing the root’s interaction mode. Use `DialogTitle` and `DialogDescription` to supply the dialog’s name and description.

`DialogFooter` preserves its desktop row geometry. Below `sm`, actions stack in DOM order with the standard compact gap; coarse pointers receive the larger separation needed for adjacent 44px hit regions.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.
