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

type GroupId = 'context' | 'overrides' | 'agents'

export interface LimitsSectionProps { // not-wire-format: React props
  prefillOverride?: { provider: string; model: string }
}

export function LimitsSection({ prefillOverride }: LimitsSectionProps) {
  const [open, setOpen] = useState<Record<GroupId, boolean>>({
    context: false,
    overrides: !!prefillOverride,
    agents: false,
  })
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

  const toggle = (g: GroupId) => (expanded: boolean) => setOpen((o) => ({ ...o, [g]: expanded }))

  return (
    <div className="space-y-[var(--space-3)]">
      <ListGroup label="Context and tool results" expanded={open.context} onExpandedChange={toggle('context')} data-testid="limits-group-context">
        <div className="pt-[var(--space-2)]">{context.fieldsView}</div>
      </ListGroup>
      <ListGroup
        label="Model overrides"
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
