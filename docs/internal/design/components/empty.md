# Empty

`Empty` is the "nothing here yet" surface with shadcn/ui's Empty interface (W2-1, R41): `Empty`, `EmptyHeader`, `EmptyMedia` (`variant="icon"` draws a 40px surface box and is decorative), `EmptyTitle` (a `div`, never a heading, so the caller's heading order holds), `EmptyDescription` and `EmptyContent` for actions. Each slot carries `data-slot` with its kebab name. It is static content with no role; a caller that must announce a "no results" state puts `role="status"` on its own wrapper.

It replaced the earlier `EmptyState` component in W2-1; its callers (the Skills screen and the notification panel) now compose `Empty` directly.
