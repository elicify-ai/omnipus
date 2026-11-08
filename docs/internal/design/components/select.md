# Select

Select is the Radix-backed single-value choice primitive. Its compound API preserves controlled and uncontrolled values, form name, required and disabled semantics, trigger naming, keyboard navigation, focus return and portal behavior.

`SelectTrigger` has two variants (W2-1, R39/R41), exposed as `selectTriggerVariants` and carried as `data-variant`: `field` (default) is the form control and keeps its classes unchanged; `ghost` is the compact, borderless toolbar filter trigger — 32px, sized to its content, muted text, accent text when `active` (a filter value is applied), an 11px caret, and a 44px box on a coarse pointer. The look lives in the variant; callers pass layout classes only.
