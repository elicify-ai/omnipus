import { Label } from '@/components/ui/label'
import { AcceptanceCriteriaEditor } from './AcceptanceCriteriaEditor'
import type { AcceptanceCriterion } from '@/lib/api'

interface DefinitionOfDoneEditorProps {
  /** The task's (or goal's) `dod[]` list — DISTINCT from `criteria[]` (ADR-086 FR-003/FR-048). */
  dod: AcceptanceCriterion[]
  onChange: (dod: AcceptanceCriterion[]) => void
  /** Author identity stamped on newly-added DoD items (ADR-049 D2 rule 3 — mandatory). */
  currentAuthor: { kind: 'agent' | 'user'; id: string }
}

/**
 * Definition-of-Done editor primitive (GOAL-FR-048; joint delivery plan
 * C-48/C-82). A THIN WRAPPER around `AcceptanceCriteriaEditor` — it owns no
 * criterion state and no editing logic of its own. It supplies only the DoD
 * chrome the reference design
 * (`docs/internal/design/task-form-criteria-dod-demo.html`) calls for: the
 * "Definition of Done" label, the same required asterisk the Title field
 * uses, and the standing helper line below the editor.
 *
 * Consumed by `CreateTaskSlideOver.tsx` (create half, GOAL-FR-047/FR-048) and
 * `TaskDetailPanel.tsx` / `CalendarEventSlideOver.tsx` (edit half, D-C's
 * uniform creation-and-edit gate). Neither caller edits this file — see the
 * joint delivery plan's U1 row.
 */
export function DefinitionOfDoneEditor({ dod, onChange, currentAuthor }: DefinitionOfDoneEditorProps) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label className="text-[var(--color-secondary)]">
        Definition of Done <span className="text-[var(--color-error)]">*</span>
      </Label>
      <AcceptanceCriteriaEditor
        criteria={dod}
        onChange={onChange}
        currentAuthor={currentAuthor}
        inputAriaLabel="Definition of Done item"
      />
      <p className="text-xs text-[var(--color-muted)]">
        Standing gates, judged on every attempt. Add at least one.
      </p>
    </div>
  )
}
