# Switch

Switch is the Radix-backed immediate setting primitive. It preserves controlled and uncontrolled checked state, form identity, required and disabled semantics, keyboard operation, reduced motion, and a 44px coarse-pointer hit region. Disabled switches reject pointer and keyboard changes.

In forced-colors mode, Switch uses `CanvasText` for its boundary. The unchecked state pairs a `Canvas` track with a `CanvasText` thumb; the checked state pairs a `Highlight` track with a `HighlightText` thumb. The distinct system colours preserve both state and track/thumb separation without changing ordinary rendering.
