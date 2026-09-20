import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { ChipListInput } from '@/components/workspaces/ChipListInput'
import { validateWriteSetPath } from '@/lib/writeSetValidation'

// The two PLAN-MEMBER fields (`write_set` and `is_join`, ADR-053 §Contract
// Surface / US-11 G-16), each as a WHOLE field — label, control and helper
// line together — so the Create Task form and the Task detail panel cannot
// drift apart on a setting whose whole job is to make a refusal actionable.
//
// Both call sites render `WriteSetField` and `JoinMemberCheckbox`; neither
// re-assembles a label and a help line by hand. The bare `WRITE_SET_*`
// constants stay exported because the components below are their only
// consumers and a test may want to assert on the exact copy, NOT as a
// licence to hand-build the field a third time — an earlier version of this
// file exported only the constants, and the two call sites had already
// drifted apart on markup and spacing by the time anyone looked.
//
// Both fields are meaningful ONLY on a task that belongs to a plan
// (Task.yaml: "Meaningful only when `plan_id` is set; ignored on a standalone
// task"), so both call sites render them behind that condition.
//
// The copy deliberately describes the CONSEQUENCE rather than the field:
// `pkg/plan/lint.go` refuses to approve a plan whose parallel members declare
// overlapping write sets, and refuses one whose convergence point is not an
// authored join member. Those two refusals name a fix; until now the
// interface offered no way to apply it.

/** Label for the `write_set` field — what the paths ARE, in the reader's terms. */
export const WRITE_SET_LABEL = 'Files this task writes'

/** Helper line under the `write_set` field — states the rule it feeds. */
export const WRITE_SET_HELP =
  'Paths this task creates or edits. Two tasks that can run at the same time must not write the same path — the plan refuses to start if they do. Leave empty when the footprint is not known up front.'

/** The join checkbox's own visible text — named for what ticking it DOES. */
export const JOIN_LABEL = 'This task merges parallel work into one result'

/** Helper line under the join checkbox — states the rule it feeds. */
export const JOIN_HELP =
  'Tick this when two or more tasks that run in parallel feed into this one. A plan refuses to start when parallel work converges on a task that is not marked as the merge point.'

/**
 * The `write_set` field: its label, the path editor, and the helper line that
 * states the rule the paths feed — one component, so the two call sites get
 * the same field rather than the same two strings.
 *
 * `labelStyle` is the ONE thing that legitimately differs between them, and
 * it is presentation only: the Create Task form labels every field with a
 * sentence-case `<Label>`, while the detail panel's whole column uses an
 * uppercase micro-label (its local `Field` wrapper). The label TEXT, the
 * help TEXT, the control and the `htmlFor` pairing are identical either way.
 */
export function WriteSetField({
  id,
  paths,
  onChange,
  labelStyle = 'form',
}: {
  id: string
  paths: string[]
  onChange: (paths: string[]) => void
  labelStyle?: 'form' | 'section'
}) {
  return (
    <div className="flex flex-col gap-[var(--space-1)]">
      {labelStyle === 'section' ? (
        <label
          htmlFor={id}
          className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]"
        >
          {WRITE_SET_LABEL}
        </label>
      ) : (
        <Label htmlFor={id} className="text-[var(--color-secondary)]">
          {WRITE_SET_LABEL}
        </Label>
      )}
      {/* The path editor is `ChipListInput` configured for PATHS. This is the
          only place that configuration exists — the former `WriteSetInput`
          wrapper was deleted, because with a single call site it forwarded
          props and nothing else. Two settings below are deliberate departures
          from the tag configuration of the same editor, because these are
          paths and not tags:

            - No case folding (that lives in `validateWriteSetPath`, which does
              not lowercase). A path's case is load-bearing: `pkg/Plan` and
              `pkg/plan` are different files on a case-sensitive checkout, and
              `pkg/plan/lint.go` compares them literally.
            - Monospace, neutral-surface chips rather than accent-coloured
              pills, so a write set reads as code the lint will compare rather
              than as free-form labels.

          `keepDraftOnError` is the third: a rejected path stays in the box so
          the author can correct it in place, rather than retyping a long path
          from scratch. */}
      <ChipListInput
        id={id}
        values={paths}
        onChange={onChange}
        validate={validateWriteSetPath}
        noun="path"
        maxLength={500}
        keepDraftOnError
        ariaLabel="Add a path this task writes"
        placeholder="e.g. pkg/plan/lint.go"
        inputClassName="font-mono"
        chipClassName="rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] font-mono text-[var(--color-secondary)] max-w-[200px]"
        chipRemoveClassName="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
        testId="write-set-input"
        chipTestId="write-set-chip"
      />
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">{WRITE_SET_HELP}</p>
    </div>
  )
}

/**
 * The `is_join` control: a checkbox whose visible label is the sentence
 * describing its effect, plus the standing helper line.
 *
 * `htmlFor`/`id` pairing makes the sentence itself the click target, so the
 * hit area matches what a reader would expect to press.
 */
export function JoinMemberCheckbox({
  id,
  checked,
  onCheckedChange,
}: {
  id: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex flex-col gap-[var(--space-1)]">
      <div className="flex items-start gap-[var(--space-2)]">
        <Checkbox
          id={id}
          data-testid="plan-member-join-checkbox"
          checked={checked}
          onCheckedChange={(v) => onCheckedChange(v === true)}
          className="mt-[var(--border-width-hairline)]"
        />
        <label htmlFor={id} className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] cursor-pointer">
          {JOIN_LABEL}
        </label>
      </div>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">{JOIN_HELP}</p>
    </div>
  )
}
