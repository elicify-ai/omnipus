# RunningIndicator

RunningIndicator is a public, read-only status composite. It uses the shared Phosphor spinning-arrow treatment without depending on a task model, application store, or API client.

## Contract

| Input | Meaning |
|---|---|
| `tokens` omitted | Spinner-only presentation; accessible name **Running**. No zero, placeholder, or unavailable label is substituted. |
| `tokens` supplied | Known count formatted by the shared formatter: under 1,000 as-is, then one decimal place with `k` or `M`. A known zero renders **0 tok**. |
| `streaming` | Defaults to true. False keeps the arrow visible but still. This is presentation state, not a new runtime running-state detector. |
| `className` | Caller layout classes pass through to the status root. |

Public exports are `RunningIndicator` and the type `RunningIndicatorProps`. The root exposes `role="status"`, a descriptive accessible name, and the **Running** title. The decorative SVG is hidden from assistive technology; the status itself carries its meaning. There is no pointer action, keyboard operation, or focusable control.

Normal motion spins the arrow while `streaming` is true. With the reduced-motion preference, the arrow remains visible without animation. This follows the existing design-system motion contract. The earlier wireframe mockup's 3.4-second slow spin is not an approved requirement.

## Task and plan use

FR-022/SP-41, with the founder's 2026-10-05 clarifications PI1/PI2/PI3, uses **spinner-only** on running Board cards, compact Board subtask rows, List rows, Graph task nodes, and running plan tiles in the Plans band. Task indicators follow the real task's `in_progress` status; plan tiles follow the plan's `running` state. Running task nodes in a plan graph use the same arrow, replacing the graph's old running pulse dot.

No task or plan consumer supplies a count. No cached session counter, proxy total, mock value, or new usage query is introduced. Chat's existing token counter is unchanged.

## Publication and verification

The composite has a catalog entry, named value/type barrel exports, a case-correct library CSS source line, and a coverage manifest. The pure formatter is an internal foundation dependency, not a separately published API.

`SpinnerOnly`, `WithCount`, `Zero`, and `Still` stories declare observable presentation checks. The manifest maps all eleven check kinds; only keyboard and pointer are inapplicable because this display has no operation or hit target. The reduced-motion check targets the actual streaming SVG, not a static parent. Accessibility, forced colors, root-size, zoom, and reflow remain applicable.

Manifest declarations are not execution evidence. Static Storybook, component, browser, package, and screenshot verification results must be collected before the publication is reported fully verified.
