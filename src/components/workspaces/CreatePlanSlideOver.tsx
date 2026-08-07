import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import { AcceptanceCriteriaEditor } from './AcceptanceCriteriaEditor'
import { cn } from '@/lib/utils'
import {
  createPlan,
  updatePlan,
  executePlan,
  fetchAgents,
  buildTaskAssigneeItems,
  plansQueryKeys,
  isApiError,
  parsePlanApproveTaskErrors,
} from '@/lib/api'
import type { AcceptanceCriterion, Plan, PlanApproveTaskError, PlanUpdateRequest } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'

interface CreatePlanSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
  /** Present = edit an existing plan (also enables Approve while `draft`). Absent = create. */
  plan?: Plan | null
}

interface FormState {
  title: string
  goal: string
  description: string
  ownerAgentId: string
  dod: AcceptanceCriterion[]
  boundsRounds: string
  boundsDays: string
}

/** PUT /plans/{id} body. `state` is never sent from the SPA (ADR-052 G2/FR-007). */
type PlanUpdateBody = Omit<PlanUpdateRequest, 'state'>
/** The `bounds` sub-object of that body, straight off the generated contract type. */
type PlanUpdateBounds = NonNullable<PlanUpdateRequest['bounds']>

type SaveVars = { kind: 'create' } | { kind: 'update'; body: PlanUpdateBody }

/**
 * Structural deep-equality for the DoD diff. `JSON.stringify` comparison is
 * NOT a substitute: criteria loaded from the wire carry the server's key order
 * while criteria built by `AcceptanceCriteriaEditor` carry its object-literal
 * order, so two structurally identical sets can stringify differently — which
 * would report a phantom change and (on a non-draft plan) turn an untouched
 * Save into a 409.
 */
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false
    return a.every((v, i) => deepEqual(v, b[i]))
  }
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  const aKeys = Object.keys(a as object)
  const bKeys = Object.keys(b as object)
  if (aKeys.length !== bKeys.length) return false
  return aKeys.every(
    (k) =>
      Object.prototype.hasOwnProperty.call(b, k) &&
      deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
  )
}

type BoundsRead =
  | { kind: 'blank' }
  | { kind: 'value'; value: number }
  | { kind: 'invalid' }

/**
 * Read one bounds input. Both wire fields are `type: integer, minimum: 1`, so
 * anything else is invalid and must be reported, never coerced.
 *
 * `parseInt` (the previous reader) silently TRUNCATES a fractional string —
 * "3.5" became 3 and was submitted as if the user had typed it. That is the
 * same defect `AcceptanceCriteriaEditor` already fixed for its exit-code field
 * (see its round-1 `Number` + `Number.isInteger` comment); this applies the
 * same rule to the same class of input.
 */
function readBoundsInput(raw: string): BoundsRead {
  const trimmed = raw.trim()
  if (trimmed === '') return { kind: 'blank' }
  const n = Number(trimmed)
  if (!Number.isInteger(n) || n < 1) return { kind: 'invalid' }
  return { kind: 'value', value: n }
}

const DEFAULT_BOUNDS_ROUNDS = '20'
const DEFAULT_BOUNDS_DAYS = '7'

// S2 UAT finding: these MUST mirror the server's real caps
// (pkg/plan/plan.go maxPlanTitleRunes / maxPlanGoalRunes) — the Goal textarea
// previously allowed 4000 chars client-side while the server 400s anything
// over 2000 ("plan validation: goal must be 2000 characters or fewer"), so a
// 2500-char goal was silently accepted by the UI and only rejected on submit.
const TITLE_MAX_LEN = 200
const GOAL_MAX_LEN = 2000

const INITIAL_FORM: FormState = {
  title: '',
  goal: '',
  description: '',
  ownerAgentId: '__none__',
  dod: [],
  boundsRounds: '',
  boundsDays: '',
}

function formFromPlan(plan: Plan): FormState {
  return {
    title: plan.title,
    goal: plan.goal ?? '',
    description: plan.description ?? '',
    ownerAgentId: plan.owner_agent_id,
    dod: plan.dod ?? [],
    boundsRounds: plan.bounds?.plan_judge_max_rounds != null ? String(plan.bounds.plan_judge_max_rounds) : '',
    boundsDays: plan.bounds?.idle_expiry_days != null ? String(plan.bounds.idle_expiry_days) : '',
  }
}

/**
 * Create/Edit Plan slide-over (ADR-049 FR-083/084, US-10 AS-4/5). Approve is
 * confirm-on-success (SD-C4) — on a `400` the per-task criteria-missing
 * errors render inline and the plan does NOT optimistically transition.
 */
export function CreatePlanSlideOver({ open, onOpenChange, workspaceId, plan }: CreatePlanSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const username = useAuthStore((s) => s.username)
  const isEdit = plan != null

  const [form, setForm] = useState<FormState>(plan ? formFromPlan(plan) : INITIAL_FORM)
  const [titleError, setTitleError] = useState('')
  // S2 UAT finding: owner_agent_id is server-required but previously had no
  // client-side validation at all — a submit with the default '__none__'
  // selection fired a doomed request that 400'd invisibly (toast covered by
  // the slide-over footer). Validated the same way Title already is.
  const [ownerError, setOwnerError] = useState('')
  const [boundsRoundsError, setBoundsRoundsError] = useState('')
  const [boundsDaysError, setBoundsDaysError] = useState('')
  const [approveErrors, setApproveErrors] = useState<PlanApproveTaskError[] | null>(null)
  const [approveErrorMessage, setApproveErrorMessage] = useState('')

  useEffect(() => {
    if (open) {
      setForm(plan ? formFromPlan(plan) : INITIAL_FORM)
      setTitleError('')
      setOwnerError('')
      setBoundsRoundsError('')
      setBoundsDaysError('')
      setApproveErrors(null)
      setApproveErrorMessage('')
    }
  }, [open, plan])

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const { teamIds, isLoading: teamLoading, isError: teamError } = useWorkspaceTeamIds(workspaceId)

  function buildDod(): AcceptanceCriterion[] | undefined {
    return form.dod.length > 0 ? form.dod : undefined
  }

  /** Create-only: whatever the two rendered inputs hold, or nothing. */
  function buildCreateBounds(): PlanUpdateBounds | undefined {
    const bounds: PlanUpdateBounds = {}
    const rounds = readBoundsInput(form.boundsRounds)
    if (rounds.kind === 'value') bounds.plan_judge_max_rounds = rounds.value
    const days = readBoundsInput(form.boundsDays)
    if (days.kind === 'value') bounds.idle_expiry_days = days.value
    return Object.keys(bounds).length > 0 ? bounds : undefined
  }

  /**
   * Edit-only: the CHANGED-FIELDS-ONLY update body.
   *
   * This form renders inputs for a strict subset of what a Plan carries, so
   * every field it re-asserts on save is a field it is asserting on stale,
   * incomplete knowledge. Diffing against the plan the user actually opened
   * fixes three live defects at once:
   *
   *  1. `owner_agent_id` and `dod` are FROZEN once a plan leaves `draft`
   *     (`pkg/gateway/rest_plans.go` handlePlanPut — the freeze check is
   *     `req.Dod != nil || req.OwnerAgentId != nil`, i.e. PRESENCE, not a
   *     value comparison). The form always sent `owner_agent_id`, so editing
   *     the title of ANY approved/running/done/failed plan 409'd
   *     unconditionally. Unchanged fields are now absent, so they no longer
   *     trip a freeze they were never trying to break.
   *  2. `goal`/`description`/`dod` were sent as `undefined` when emptied, and
   *     `JSON.stringify` drops undefined keys — so the backend's
   *     presence-checked patch (`if patch.Goal != nil`) never saw them and
   *     CLEARING any of the three was a silent no-op that still toasted
   *     "Plan updated". A field the user emptied now differs from its stored
   *     value and is sent explicitly as `''` / `[]`, which the store does
   *     apply.
   *  3. `bounds` is merged field-by-field server-side, so an absent key keeps
   *     its stored value. Sending only the keys the user actually changed is
   *     what makes that merge correct rather than merely tolerated: the two
   *     supervision overrides this form does not render are preserved because
   *     they are not mentioned, not because we echoed a stale copy of them
   *     back. See the "no echo" note on the Bounds fieldset below.
   */
  function buildUpdateBody(existing: Plan): PlanUpdateBody {
    const body: PlanUpdateBody = {}

    const title = form.title.trim()
    if (title !== existing.title) body.title = title

    const goal = form.goal.trim()
    if (goal !== (existing.goal ?? '')) body.goal = goal

    const description = form.description.trim()
    if (description !== (existing.description ?? '')) body.description = description

    if (form.ownerAgentId !== existing.owner_agent_id) body.owner_agent_id = form.ownerAgentId

    if (!deepEqual(form.dod, existing.dod ?? [])) body.dod = form.dod

    const bounds: PlanUpdateBounds = {}
    const rounds = readBoundsInput(form.boundsRounds)
    if (rounds.kind === 'value' && rounds.value !== existing.bounds?.plan_judge_max_rounds) {
      bounds.plan_judge_max_rounds = rounds.value
    }
    const days = readBoundsInput(form.boundsDays)
    if (days.kind === 'value' && days.value !== existing.bounds?.idle_expiry_days) {
      bounds.idle_expiry_days = days.value
    }
    if (Object.keys(bounds).length > 0) body.bounds = bounds

    return body
  }

  const saveMutation = useMutation({
    mutationFn: (vars: SaveVars) => {
      if (vars.kind === 'update') {
        return updatePlan(plan!.id, vars.body)
      }
      return createPlan({
        workspace_id: workspaceId,
        title: form.title.trim(),
        goal: form.goal.trim() || undefined,
        description: form.description.trim() || undefined,
        owner_agent_id: form.ownerAgentId,
        dod: buildDod(),
        bounds: buildCreateBounds(),
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      addToast({ message: isEdit ? 'Plan updated' : 'Plan created', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to save plan'
      addToast({ message: msg, variant: 'error' })
    },
  })

  // Approve/Execute (SD-C4): confirm-on-success only — a 400 never
  // transitions the badge; the per-task error list renders inline instead
  // of a toast. ADR-052 G2: POST /approve (`executePlan`), never PUT.
  const approveMutation = useMutation({
    mutationFn: () => executePlan(plan!.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: plansQueryKeys.list(workspaceId) })
      setApproveErrors(null)
      setApproveErrorMessage('')
      addToast({ message: 'Plan approved', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err) => {
      if (isApiError(err) && err.status === 400) {
        const taskErrors = parsePlanApproveTaskErrors(err.body)
        setApproveErrors(taskErrors)
        setApproveErrorMessage(taskErrors ? '' : err.userMessage)
        return
      }
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to approve plan'
      setApproveErrors(null)
      setApproveErrorMessage(msg)
    },
  })

  function handleSubmit() {
    // Validate every required field before firing the request (mirrors the
    // Title check that already existed) — batched so both errors show at
    // once rather than the user fixing one and re-clicking to discover the
    // next. Owner agent is server-required (400 invalid owner_agent_id) but
    // defaults to the unselected '__none__' sentinel, so it needs the same
    // guard Title has always had.
    let hasError = false
    if (!form.title.trim()) {
      setTitleError('Title is required')
      hasError = true
    } else {
      setTitleError('')
    }
    if (form.ownerAgentId === '__none__' || !form.ownerAgentId.trim()) {
      setOwnerError('Owner agent is required')
      hasError = true
    } else {
      setOwnerError('')
    }

    // Bounds inputs are `integer, minimum: 1` on the wire. Two failure modes
    // that were previously SILENT are now blocked inline rather than being
    // coerced (a fractional value was truncated by `parseInt`) or dropped (an
    // emptied override was simply omitted, and the server's field-by-field
    // bounds merge keeps an absent field's stored value — so the save
    // succeeded, the toast said "Plan updated", and the override the user
    // had just cleared was still there).
    const boundsChecks = [
      {
        read: readBoundsInput(form.boundsRounds),
        stored: plan?.bounds?.plan_judge_max_rounds,
        label: 'Max rounds',
        set: setBoundsRoundsError,
      },
      {
        read: readBoundsInput(form.boundsDays),
        stored: plan?.bounds?.idle_expiry_days,
        label: 'Idle-expiry days',
        set: setBoundsDaysError,
      },
    ]
    for (const check of boundsChecks) {
      if (check.read.kind === 'invalid') {
        check.set(`${check.label} must be a whole number of 1 or more`)
        hasError = true
      } else if (isEdit && check.read.kind === 'blank' && check.stored != null) {
        check.set(
          `${check.label} can be changed but not cleared here — enter a value of 1 or more, ` +
            `or Cancel to leave the current override (${check.stored}) in place`,
        )
        hasError = true
      } else {
        check.set('')
      }
    }

    if (hasError) return

    if (isEdit) {
      const body = buildUpdateBody(plan!)
      // PlanUpdateRequest is `minProperties: 1` — an empty body is a schema
      // violation, not a no-op. Report the real outcome instead of firing a
      // request that cannot succeed or toasting a change that never happened.
      if (Object.keys(body).length === 0) {
        addToast({ message: 'No changes to save', variant: 'default' })
        onOpenChange(false)
        return
      }
      saveMutation.mutate({ kind: 'update', body })
      return
    }
    saveMutation.mutate({ kind: 'create' })
  }

  function handleApprove() {
    setApproveErrors(null)
    setApproveErrorMessage('')
    approveMutation.mutate()
  }

  const isPending = saveMutation.isPending
  const canApprove = isEdit && plan!.state === 'draft'

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col p-0">
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>{isEdit ? 'Edit plan' : 'New plan'}</SheetTitle>
        </SheetHeader>

        <div className="flex flex-col flex-1 gap-5 px-6 py-4 overflow-y-auto">
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between gap-2">
              <Label htmlFor="cp-title" className="text-[var(--color-secondary)]">
                Title <span className="text-[var(--color-error)]">*</span>
              </Label>
              <span
                className={cn(
                  'text-[10px]',
                  form.title.length >= TITLE_MAX_LEN ? 'text-[var(--color-error)]' : 'text-[var(--color-muted)]',
                )}
              >
                {form.title.length}/{TITLE_MAX_LEN}
                {form.title.length >= TITLE_MAX_LEN ? ' — max length reached' : ''}
              </span>
            </div>
            <Input
              id="cp-title"
              value={form.title}
              onChange={(e) => { setForm((s) => ({ ...s, title: e.target.value })); setTitleError('') }}
              placeholder="v1.0 Launch"
              autoFocus
              maxLength={TITLE_MAX_LEN}
              aria-invalid={!!titleError}
              aria-describedby={titleError ? 'cp-title-error' : undefined}
            />
            {titleError && <p id="cp-title-error" className="text-xs text-[var(--color-error)]">{titleError}</p>}
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between gap-2">
              <Label htmlFor="cp-goal" className="text-[var(--color-secondary)]">Goal</Label>
              <span
                className={cn(
                  'text-[10px]',
                  form.goal.length >= GOAL_MAX_LEN ? 'text-[var(--color-error)]' : 'text-[var(--color-muted)]',
                )}
              >
                {form.goal.length}/{GOAL_MAX_LEN}
                {form.goal.length >= GOAL_MAX_LEN ? ' — max length reached' : ''}
              </span>
            </div>
            <Textarea
              id="cp-goal"
              value={form.goal}
              onChange={(e) => setForm((s) => ({ ...s, goal: e.target.value }))}
              placeholder="Plain-prose objective the plan judge evaluates against when the DoD is empty…"
              rows={3}
              maxLength={GOAL_MAX_LEN}
              className="text-xs"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cp-desc" className="text-[var(--color-secondary)]">Description</Label>
            <Textarea
              id="cp-desc"
              value={form.description}
              onChange={(e) => setForm((s) => ({ ...s, description: e.target.value }))}
              placeholder="Optional free-form description"
              rows={2}
              maxLength={2000}
              className="text-xs"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">
              Owner agent <span className="text-[var(--color-error)]">*</span>
            </Label>
            <SmartSelect
              value={form.ownerAgentId}
              onValueChange={(v) => { setForm((s) => ({ ...s, ownerAgentId: v })); setOwnerError('') }}
              placeholder={teamLoading ? 'Loading team…' : 'Select an owner'}
              disabled={teamLoading}
              triggerClassName="h-9 text-sm"
              ariaLabel="Owner agent"
              items={[
                { value: '__none__', label: 'Select an owner', className: 'text-xs' },
                ...buildTaskAssigneeItems(agents, {
                  teamScope: teamIds ? { kind: 'scoped', ids: teamIds } : { kind: 'unscoped' },
                  currentAssigneeId: form.ownerAgentId,
                }),
              ]}
            />
            {ownerError && <p id="cp-owner-error" className="text-xs text-[var(--color-error)]">{ownerError}</p>}
            {teamError && (
              <p className="text-xs text-[var(--color-muted)]">Team list unavailable — showing all agents</p>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Definition of Done</Label>
            <AcceptanceCriteriaEditor
              criteria={form.dod}
              onChange={(dod) => setForm((s) => ({ ...s, dod }))}
              currentAuthor={{ kind: 'user', id: username ?? 'operator' }}
              emptyHint="No DoD criteria — the plan judge will evaluate against the goal/title instead (soft tier). Every member task still needs its own criteria before Approve."
            />
          </div>

          {/*
            Bounds renders TWO of the plan's bounds overrides. The others
            (`supervision_turn_timeout_seconds`, `supervision_max_attempts`)
            are deliberately not rendered and — just as deliberately — not
            echoed back on save.

            Echoing them was considered and rejected. `PlanUpdateRequest.bounds`
            is `additionalProperties: false`, so a key this SPA does not know
            about cannot be carried through it at all; the only keys a
            round-trip could ever preserve are known-but-unrendered ones. For
            those, re-asserting a value read when the slide-over opened is a
            lost update (the plan here is a snapshot held in
            `WorkspaceTasksTab`'s local state, not live cache), and it would
            make the server's field-by-field bounds merge behaviourally
            identical to the replace it replaced — leaving the merge path
            unexercised by the primary client. Absence is the preservation
            mechanism; see `buildUpdateBody`.
          */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Bounds</Label>
            <div className="flex items-start gap-2">
              <div className="flex-1 flex flex-col gap-1">
                <Input
                  aria-label="Plan judge max rounds"
                  type="number"
                  min={1}
                  value={form.boundsRounds}
                  onChange={(e) => { setForm((s) => ({ ...s, boundsRounds: e.target.value })); setBoundsRoundsError('') }}
                  placeholder={DEFAULT_BOUNDS_ROUNDS}
                  className="text-xs"
                  aria-invalid={!!boundsRoundsError}
                  aria-describedby={boundsRoundsError ? 'cp-bounds-rounds-error' : undefined}
                />
                <span className="text-[10px] text-[var(--color-muted)]">Max rounds (default {DEFAULT_BOUNDS_ROUNDS})</span>
                {boundsRoundsError && (
                  <p id="cp-bounds-rounds-error" className="text-xs text-[var(--color-error)]">{boundsRoundsError}</p>
                )}
              </div>
              <div className="flex-1 flex flex-col gap-1">
                <Input
                  aria-label="Idle expiry days"
                  type="number"
                  min={1}
                  value={form.boundsDays}
                  onChange={(e) => { setForm((s) => ({ ...s, boundsDays: e.target.value })); setBoundsDaysError('') }}
                  placeholder={DEFAULT_BOUNDS_DAYS}
                  className="text-xs"
                  aria-invalid={!!boundsDaysError}
                  aria-describedby={boundsDaysError ? 'cp-bounds-days-error' : undefined}
                />
                <span className="text-[10px] text-[var(--color-muted)]">Idle-expiry days (default {DEFAULT_BOUNDS_DAYS})</span>
                {boundsDaysError && (
                  <p id="cp-bounds-days-error" className="text-xs text-[var(--color-error)]">{boundsDaysError}</p>
                )}
              </div>
            </div>
          </div>

          {canApprove && (
            <div className="flex flex-col gap-1.5 rounded-md border border-[var(--color-border)] p-3">
              <div className="flex items-center justify-between gap-2">
                <p className="text-xs font-medium text-[var(--color-secondary)]">Approve this plan</p>
                <Button
                  type="button"
                  size="sm"
                  className="h-7 text-xs bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
                  onClick={handleApprove}
                  disabled={approveMutation.isPending}
                >
                  {approveMutation.isPending ? 'Approving…' : 'Approve'}
                </Button>
              </div>
              {approveErrorMessage && (
                <p className="text-xs text-[var(--color-error)]">{approveErrorMessage}</p>
              )}
              {approveErrors && approveErrors.length > 0 && (
                <div className="text-xs text-[var(--color-error)]" data-testid="plan-approve-task-errors">
                  <p className="font-medium">Cannot approve — the following tasks are missing criteria:</p>
                  <ul className="list-disc list-inside mt-1 space-y-0.5">
                    {approveErrors.map((e) => (
                      <li key={e.task_id}>{e.title ?? e.task_id}: {e.reason}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>

        <SheetFooter className="flex-row gap-2 px-6 py-4 flex-shrink-0">
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={isPending}
            className="flex-1"
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={handleSubmit}
            disabled={isPending}
            className="flex-1 bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
          >
            {isPending ? 'Saving…' : isEdit ? 'Save' : 'Create'}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
