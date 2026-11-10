# TextToggle

TextToggle is a labelled on/off button for a cramped row. It is not a sliding switch. The word stays visible in both states. Off is plain muted text with no fill. On is the same word on a quiet surface fill with accent text, the same selected treatment as SegmentedControlItem, including its small lift.

A screen reader hears pressed or not pressed from `aria-pressed`. The control does not use `aria-checked`. Disabled rejects pointer and keyboard changes and keeps the current pressed state. On a coarse pointer the control grows to the 44px touch minimum inside its own box, so the shared hit-area expansion does not cover the control beside it.

In forced-colors mode the off state pairs `CanvasText` on `Canvas`. The on state pairs `HighlightText` on `Highlight`. The button keeps the catalogued Button's `ButtonText` border, so the control still has a contrasting boundary when the page is forced into a high-contrast palette.
