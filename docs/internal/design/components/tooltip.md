# Tooltip

Tooltip is shadcn/ui's Tooltip on `@radix-ui/react-tooltip` 1.2.14 (W2-1, R45): `TooltipProvider`, `Tooltip`, `TooltipTrigger`, `TooltipContent`. Callers compose `<Tooltip><TooltipTrigger asChild>{control}</TooltipTrigger><TooltipContent>{text}</TooltipContent></Tooltip>`; the control keeps its own accessible name. Each `Tooltip` carries its own provider (open delay 0ms, skip delay 300ms), so no app-root provider is needed. The earlier hand-built wrapper and its `content`/`label`/`side`/`interactive` props are removed.

Behaviour is Radix's: it opens on mouse hover and keyboard focus and closes on pointer leave (after the hoverable grace area), blur, Escape and trigger pointer-down. While open the trigger carries `aria-describedby` pointing at the `role="tooltip"` bubble; focus never moves and Tab is not trapped. Escape is handled by the topmost dismissable layer, so a tooltip inside a dialog closes alone. Touch does not open a tooltip (founder answer Q2 = A, 2026-11-06).

The bubble is portalled, placed `top` by default with a 4px offset and an 8px collision padding against the viewport, and sized to its text up to the caption measure. Non-control triggers (a truncated name, a label) are a focusable `span tabIndex={0}`. In forced-colors mode the bubble repaints border, fill and text to `CanvasText`/`Canvas`.
