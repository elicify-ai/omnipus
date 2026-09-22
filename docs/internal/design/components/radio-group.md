# RadioGroup

RadioGroup is the shared WAI-ARIA radio-group primitive (`role="radiogroup"` wrapping `role="radio"` children), built on the `Button` primitive rather than a native `<input type="radio">` or a third-party package. It is controlled only — there is no uncontrolled mode — and requires an accessible name via `aria-label` or `aria-labelledby`.

It preserves roving tabindex: exactly one option, the checked one, is a Tab stop (`tabindex="0"`); every other option sits at `tabindex="-1"`. Left/Right and Up/Down arrow keys both move focus and immediately select the adjacent option, matching how a native radio group behaves, and wrap past the first and last option rather than stopping. Home and End jump straight to the first and last enabled option regardless of current position. A disabled item — set on the item itself, or inherited from the group's `disabled` prop unless a per-item `disabled` overrides it — is skipped entirely during arrow navigation and rejects pointer and keyboard activation, exactly like a disabled native radio button.

The `orientation` prop (`horizontal` by default, or `vertical`) controls layout and sets `aria-orientation` for assistive technology; arrow-key navigation honors both axes regardless of orientation, so ArrowRight/ArrowDown always move forward and ArrowLeft/ArrowUp always move back.

In forced-colors mode, every option renders with the same `ButtonText`-on-`ButtonFace` boundary and text color regardless of checked state — RadioGroup has no dedicated dot or checkmark indicator, so the checked/unchecked distinction is carried by the `aria-checked` state rather than an additional color cue, the same contract SegmentedControl uses for `aria-pressed`.
