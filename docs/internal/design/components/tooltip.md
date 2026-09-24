# Tooltip

Tooltip is a hand-built (no Radix dependency) primitive following the WAI-ARIA tooltip pattern: a trigger carrying `aria-describedby` while open, referencing a `role="tooltip"` bubble. It reveals on hover, keyboard focus, or tap (touch has no hover), and dismisses on Escape without moving focus off the trigger — the trigger stays exactly where the operator left it, only the supplementary text goes away.

Deliberately not built on `@radix-ui/react-tooltip` (not a dependency of this project): the interaction surface — hover/focus reveal, Escape dismiss, no focus trap, no portal/positioning engine — is small enough that a hand-built primitive is lower-risk than adding a new overlay/positioning dependency. It generalizes the hand-rolled tooltip pattern already used ad hoc in several screens (`ConnectionStatus.tsx`, `FullCalendarView.tsx`, `voice-provider-sub.tsx`, `AgentListScreen.tsx`) into one catalogued component; those existing call sites are unmigrated, tracked separately.

The trigger carries `data-ds-action` for the shared 44px coarse-pointer hit-region expansion (`src/styles/library.css`), and relies on the global `:focus-visible` rule for its focus ring — it does not set its own `focus-visible:outline-*`/`ring-*` utility.

In forced-colors mode the bubble's border (`--color-border` → `CanvasText`) contrasts against its background (`--color-surface-3` → `Canvas`), and the trigger's focus outline resolves to `Highlight`, matching the rest of the kit.
