# Progress

`Progress` is the semantic progress-bar primitive. It renders determinate output only for a finite value between zero and a finite positive `max`; `null`, missing, non-finite, negative, and out-of-range values are indeterminate. Unknown active progress remains visibly present through a reduced-motion-safe pulse rather than displaying a fabricated value.

`label` is an accessible-name convenience and does not render visible text. Callers should supply `label` or `aria-label` now. The optional-name compatibility remains temporarily because existing application callers are migrated in C4; unnamed callers are application debt, not the destination contract. Callers own visible labels and all operation state.

In forced-colors mode, the track uses the system `Canvas` colour with a `CanvasText` boundary, while the filled or indeterminate segment uses `Highlight`. The distinct system colours preserve a truthful filled-track cue without fabricating progress.

Numeric accessibility attributes are owned by the component: the minimum is zero, the maximum is the normalized `max`, and the current value is omitted for indeterminate progress. The public type rejects numeric ARIA overrides, and runtime forwarding cannot replace those computed values. Caller naming, `getValueLabel`, and custom `aria-valuetext` remain supported.
