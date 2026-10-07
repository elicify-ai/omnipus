# HoverCard

Radix-backed shadcn/ui Hover Card port, not Base UI. Source composition: https://ui.shadcn.com/docs/components/radix/hover-card. The installed @radix-ui/react-hover-card types confirm controlled `open`/`onOpenChange`, `openDelay`/`closeDelay`, Portal and Escape handling; Trigger opens on focus as well as non-touch pointer enter.

Public HoverCard, HoverCardTrigger, HoverCardContent. Content always uses a Radix Portal, collision positioning, tokenised panel surface/border/floating elevation, and content-sized width capped to the viewport and a readable 28rem maximum. Its own available-height scroll is intentional; it never inherits column/table/canvas clipping. Forced colours use system colours. Global focus remains the only focus-ring owner.

An asChild trigger preserves the child's existing tabindex; a standalone trigger is stamped for WebKit. Domain task composition uses hover/focus, Escape dismissal, and touch tap as preview-only, with a separate Open task action. No Tooltip internals are changed. Unit evidence is the one T14 consumer regression; interaction/keyboard/browser/accessibility and environmental checks are declared in the eleven-kind manifest and Default/HoverAndFocus stories.
