// LimitsSection — Settings → Limits (Settings redesign #1519, US-6 AS9,
// FR-004, FR-019, FR-072). Three grouped lists, each starting collapsed:
// Context and tool results, Model overrides, and Agents and goals. The first
// two write PUT /settings/context, the third PUT /performance; all three sit
// on one Save bar, because drafts belong to the section (FR-019) and Save
// sends each changed value to its own endpoint (FR-015).
//
// `?provider=&model=` (the "No context length" link) opens Limits with Model
// overrides expanded and one new unsaved row filled in (FR-004).

import { useEffect, useRef, useState } from 'react'
import { ListGroup } from '@/components/ui/list-group'
import { useContextForm } from './ContextSection'
import { usePerformanceForm } from './PerformanceSection'
import { SaveBarPanel, SaveOutcomeLine } from './SaveBar'
import { useFormSaveBar, type FormSavePart } from './useFormSaveBar'
import { settingsRow, settingsRowId } from './settingsSections'
import { useSettingsShell } from './SettingsShellContext'

type GroupId = 'context' | 'overrides' | 'agents'

// FR-075: a row jump into a closed group opens that group first, so the row
// can be scrolled to and highlighted.
const ROWS_IN_GROUP: Record<GroupId, string[]> = {
  context: ['MCP tool result cap', 'Built-in tool result cap', 'Failed tool result cap', 'Tool-result share limit', 'Default context window'].map(settingsRowId),
  overrides: [],
  agents: [
    'Max parallel agents',
    'Tries per goal',
    'Idle reminder quiet window',
    'Stalled-work check-ins',
    'Check-in interval',
    'Max tool calls per turn',
    'Tool loading',
  ].map(settingsRowId),
}

export interface LimitsSectionProps { // not-wire-format: React props
  prefillOverride?: { provider: string; model: string }
}

export function LimitsSection({ prefillOverride }: LimitsSectionProps) {
  const [open, setOpen] = useState<Record<GroupId, boolean>>({
    context: false,
    overrides: !!prefillOverride,
    agents: false,
  })
  const { targetRow } = useSettingsShell()
  useEffect(() => {
    if (!targetRow) return
    const group = (Object.keys(ROWS_IN_GROUP) as GroupId[]).find((g) => ROWS_IN_GROUP[g].includes(targetRow))
    if (group) setOpen((o) => (o[group] ? o : { ...o, [group]: true }))
  }, [targetRow])
  const pendingFocus = useRef<(() => void) | null>(null)
  const saveRef = useRef<() => Promise<void>>(async () => {})

  const context = useContextForm({ prefillOverride })
  const performance = usePerformanceForm({ onReconfirmed: () => void saveRef.current() })

  // Pressing Save with an invalid value opens the group that holds it, then
  // moves focus there (FR-013).
  const [focusTick, setFocusTick] = useState(0)
  const openThenFocus = (group: GroupId, focus?: () => void): (() => void) => () => {
    pendingFocus.current = focus ?? null
    setOpen((o) => ({ ...o, [group]: true }))
    setFocusTick((t) => t + 1)
  }
  useEffect(() => {
    if (!pendingFocus.current) return
    const f = pendingFocus.current
    pendingFocus.current = null
    f()
  }, [focusTick, open])

  const contextPart: FormSavePart = {
    ...context.part,
    focusInvalid: openThenFocus(context.invalidGroup ?? 'context', context.part.focusInvalid),
  }
  const performancePart: FormSavePart = {
    ...performance.part,
    focusInvalid: openThenFocus('agents', performance.part.focusInvalid),
  }

  const { bar, message, save } = useFormSaveBar([contextPart, performancePart])
  useEffect(() => {
    saveRef.current = save
  }, [save])

  // FR-066: a group whose read failed opens, so its error and Retry show
  // without a click; the other groups keep working.
  const contextFailed = context.loadFailed
  const performanceFailed = performance.loadFailed
  useEffect(() => {
    if (contextFailed) setOpen((o) => ({ ...o, context: true, overrides: true }))
  }, [contextFailed])
  useEffect(() => {
    if (performanceFailed) setOpen((o) => ({ ...o, agents: true }))
  }, [performanceFailed])

  const toggle = (g: GroupId) => (expanded: boolean) => setOpen((o) => ({ ...o, [g]: expanded }))

  return (
    <div className="space-y-[var(--space-3)]">
      <ListGroup label="Context and tool results" expanded={open.context} onExpandedChange={toggle('context')} data-testid="limits-group-context">
        <div className="pt-[var(--space-2)]">{context.fieldsView}</div>
      </ListGroup>
      <ListGroup
        label="Model overrides"
        {...settingsRow('Model overrides')}
        count={context.overrideCount}
        expanded={open.overrides}
        onExpandedChange={toggle('overrides')}
        data-testid="limits-group-overrides"
      >
        <div className="pt-[var(--space-2)]">{context.ready ? context.overridesView : context.fieldsView}</div>
      </ListGroup>
      <SaveOutcomeLine message={message('context')} />
      <ListGroup label="Agents and goals" expanded={open.agents} onExpandedChange={toggle('agents')} data-testid="limits-group-agents">
        <div className="pt-[var(--space-2)]">{performance.view}</div>
      </ListGroup>
      <SaveOutcomeLine message={message('performance')} />
      {performance.dialogs}
      <SaveBarPanel {...bar} />
    </div>
  )
}
