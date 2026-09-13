import { Checkbox } from '@/components/ui/checkbox'

// Shared copy + the join control for the two PLAN-MEMBER fields (`write_set`
// and `is_join`, ADR-053 §Contract Surface / US-11 G-16), so the Create Task
// form and the Task detail panel cannot drift apart on the wording of a
// setting whose whole job is to make a refusal actionable.
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
    <div className="flex flex-col gap-1.5">
      <div className="flex items-start gap-2">
        <Checkbox
          id={id}
          data-testid="plan-member-join-checkbox"
          checked={checked}
          onCheckedChange={(v) => onCheckedChange(v === true)}
          className="mt-[1px]"
        />
        <label htmlFor={id} className="text-xs text-[var(--color-secondary)] cursor-pointer">
          {JOIN_LABEL}
        </label>
      </div>
      <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">{JOIN_HELP}</p>
    </div>
  )
}
