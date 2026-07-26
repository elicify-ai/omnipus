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
import type { AcceptanceCriterion, Plan, PlanApproveTaskError } from '@/lib/api'
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
  const [approveErrors, setApproveErrors] = useState<PlanApproveTaskError[] | null>(null)
  const [approveErrorMessage, setApproveErrorMessage] = useState('')

  useEffect(() => {
    if (open) {
      setForm(plan ? formFromPlan(plan) : INITIAL_FORM)
      setTitleError('')
      setOwnerError('')
      setApproveErrors(null)
      setApproveErrorMessage('')
    }
  }, [open, plan])

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const { teamIds, isLoading: teamLoading, isError: teamError } = useWorkspaceTeamIds(workspaceId)

  function buildDod(): AcceptanceCriterion[] | undefined {
    return form.dod.length > 0 ? form.dod : undefined
  }

  function buildBounds(): { plan_judge_max_rounds?: number; idle_expiry_days?: number } | undefined {
    const rounds = parseInt(form.boundsRounds, 10)
    const days = parseInt(form.boundsDays, 10)
    const bounds: { plan_judge_max_rounds?: number; idle_expiry_days?: number } = {}
    if (Number.isFinite(rounds) && form.boundsRounds.trim() !== '') bounds.plan_judge_max_rounds = rounds
    if (Number.isFinite(days) && form.boundsDays.trim() !== '') bounds.idle_expiry_days = days
    return Object.keys(bounds).length > 0 ? bounds : undefined
  }

  const saveMutation = useMutation({
    mutationFn: () => {
      const ownerId = form.ownerAgentId === '__none__' ? undefined : form.ownerAgentId
      if (isEdit) {
        return updatePlan(plan!.id, {
          title: form.title.trim(),
          goal: form.goal.trim() || undefined,
          description: form.description.trim() || undefined,
          owner_agent_id: ownerId,
          dod: buildDod(),
          bounds: buildBounds(),
        })
      }
      return createPlan({
        workspace_id: workspaceId,
        title: form.title.trim(),
        goal: form.goal.trim() || undefined,
        description: form.description.trim() || undefined,
        owner_agent_id: ownerId ?? '',
        dod: buildDod(),
        bounds: buildBounds(),
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
    if (hasError) return
    saveMutation.mutate()
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

          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Bounds</Label>
            <div className="flex items-center gap-2">
              <div className="flex-1 flex flex-col gap-1">
                <Input
                  aria-label="Plan judge max rounds"
                  type="number"
                  min={1}
                  value={form.boundsRounds}
                  onChange={(e) => setForm((s) => ({ ...s, boundsRounds: e.target.value }))}
                  placeholder={DEFAULT_BOUNDS_ROUNDS}
                  className="text-xs"
                />
                <span className="text-[10px] text-[var(--color-muted)]">Max rounds (default {DEFAULT_BOUNDS_ROUNDS})</span>
              </div>
              <div className="flex-1 flex flex-col gap-1">
                <Input
                  aria-label="Idle expiry days"
                  type="number"
                  min={1}
                  value={form.boundsDays}
                  onChange={(e) => setForm((s) => ({ ...s, boundsDays: e.target.value }))}
                  placeholder={DEFAULT_BOUNDS_DAYS}
                  className="text-xs"
                />
                <span className="text-[10px] text-[var(--color-muted)]">Idle-expiry days (default {DEFAULT_BOUNDS_DAYS})</span>
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
