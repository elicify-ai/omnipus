// model-effort-picker.tsx — RED SCAFFOLDING for the effort-levels feature
// (train T7, branch landing/effort). Spec: effort-levels-spec-r10.md,
// "Design-System Components" / "Reachability": new components
// `ModelEffortPicker` (domain) and `EffortControl` (composite: chip +
// popover + radio group).
//
// The RED component tests (src/components/ui/model-effort-picker.test.tsx and
// the T-F2/T-F3 suites) render these components and assert the S-07/S-08/S-10
// to S-14/S-54/S-57/S-60/S-73/S-74 behaviour. GREEN replaces both stubs with
// the real publication (catalog entry, barrel export, @source line, manifest)
// composed from the catalogued RadioGroup/Popover/Button primitives; the prop
// shapes below are the contract the tests pin. Both stubs render nothing, so
// every rendered-output assertion fails until the real components land.

export interface EffortValue {
  provider: string
  model: string
  /** Stored effort: '' (Not set), 'provider_default', a level, 'on', 'off'. */
  effort: string
}

export interface EffortChangeMeta {
  /** Note shown with the change (mapping note), if any. */
  note?: string
}

export interface ModelEffortPickerProps {
  value: EffortValue
  onChange?: (effort: string, meta?: EffortChangeMeta) => void
  /** The inherited value a Not set slot resolves to (chat: agent's setting). */
  inherit?: string
  scope:
    | 'chat'
    | 'agent-primary'
    | 'agent-fallback'
    | 'default-model'
    | 'recap-model'
    | 'recap-fallback'
    | 'onboarding'
    | 'replacement'
  /** Contextual accessible name, e.g. "Effort for fallback 2, MiniMax M3". */
  contextName?: string
  /** Recap slots default to Off and have no Not set row (FD-19). */
  defaultEffort?: 'off'
}

/** Shared model-and-effort picker body (radio group inside the popover). RED stub. */
export function ModelEffortPicker(_props: ModelEffortPickerProps): null {
  return null
}

export interface EffortControlProps extends ModelEffortPickerProps {
  /** Blocked-step keep-open view state is internal to the control. */
  keepOpenOnBlocked?: boolean
}

/** The chip + popover control wrapping ModelEffortPicker. RED stub. */
export function EffortControl(_props: EffortControlProps): null {
  return null
}
