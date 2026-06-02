import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { FloppyDisk } from '@phosphor-icons/react'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  fetchAgents,
  createSchedule,
  updateSchedule,
  isApiError,
} from '@/lib/api'
import type { Schedule, ScheduleCreate, ScheduleUpdate } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'

interface ScheduleFormSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  // When set, the form edits an existing schedule; otherwise it creates a new one.
  schedule?: Schedule | null
  // Pre-fill the owner (used by the agent profile "New schedule" button).
  defaultOwnerAgentId?: string
}

type TriggerKind = 'at' | 'every' | 'cron'
type SessionMode = 'isolated' | 'continue' | 'main'
type EveryUnit = 'minutes' | 'hours' | 'days'

interface FormState {
  ownerAgentId: string
  name: string
  triggerKind: TriggerKind
  atLocal: string // datetime-local string
  everyValue: string
  everyUnit: EveryUnit
  cronExpr: string
  message: string
  sessionMode: SessionMode
  deliver: boolean
  timeoutSeconds: string
  enabled: boolean
}

const UNIT_MS: Record<EveryUnit, number> = {
  minutes: 60_000,
  hours: 3_600_000,
  days: 86_400_000,
}

function initialState(schedule: Schedule | null | undefined, defaultOwner?: string): FormState {
  if (schedule) {
    const t = schedule.trigger
    let everyValue = ''
    let everyUnit: EveryUnit = 'minutes'
    if (t.kind === 'every' && t.every_ms) {
      if (t.every_ms % UNIT_MS.days === 0) {
        everyValue = String(t.every_ms / UNIT_MS.days)
        everyUnit = 'days'
      } else if (t.every_ms % UNIT_MS.hours === 0) {
        everyValue = String(t.every_ms / UNIT_MS.hours)
        everyUnit = 'hours'
      } else {
        everyValue = String(Math.round(t.every_ms / UNIT_MS.minutes))
        everyUnit = 'minutes'
      }
    }
    return {
      ownerAgentId: schedule.owner_agent_id,
      name: schedule.name,
      triggerKind: t.kind,
      atLocal: t.at_ms ? toLocalInput(t.at_ms) : '',
      everyValue,
      everyUnit,
      cronExpr: t.cron_expr ?? '',
      message: schedule.message,
      sessionMode: schedule.session_mode,
      deliver: schedule.deliver,
      timeoutSeconds: schedule.timeout_seconds ? String(schedule.timeout_seconds) : '',
      enabled: schedule.enabled,
    }
  }
  return {
    ownerAgentId: defaultOwner ?? '',
    name: '',
    triggerKind: 'every',
    atLocal: '',
    everyValue: '60',
    everyUnit: 'minutes',
    cronExpr: '',
    message: '',
    sessionMode: 'isolated',
    deliver: false,
    timeoutSeconds: '',
    enabled: true,
  }
}

function toLocalInput(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function ScheduleFormSheet({
  open,
  onOpenChange,
  schedule,
  defaultOwnerAgentId,
}: ScheduleFormSheetProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const isEdit = Boolean(schedule)

  const [form, setForm] = useState<FormState>(() => initialState(schedule, defaultOwnerAgentId))

  function setValue<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })

  const { mutate: doSave, isPending: saving } = useMutation({
    mutationFn: () => {
      const trigger = buildTrigger(form)
      const timeout = form.timeoutSeconds === '' ? undefined : Number(form.timeoutSeconds)
      if (isEdit && schedule) {
        const body: ScheduleUpdate = {
          name: form.name,
          owner_agent_id: form.ownerAgentId,
          trigger,
          message: form.message,
          deliver: form.deliver,
          session_mode: form.sessionMode,
          timeout_seconds: timeout,
          enabled: form.enabled,
        }
        return updateSchedule(schedule.id, body)
      }
      const body: ScheduleCreate = {
        name: form.name,
        owner_agent_id: form.ownerAgentId,
        trigger,
        message: form.message,
        deliver: form.deliver,
        session_mode: form.sessionMode,
        timeout_seconds: timeout,
        enabled: form.enabled,
      }
      return createSchedule(body)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['schedules'] })
      addToast({ message: isEdit ? 'Schedule updated' : 'Schedule created', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed', variant: 'error' }),
  })

  const canSave = form.ownerAgentId !== '' && form.name.trim() !== '' && form.message.trim() !== ''

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            {isEdit ? 'Edit schedule' : 'New schedule'}
          </SheetTitle>
        </SheetHeader>

        <div className="pt-5 space-y-5">
          {/* Owner */}
          <Field label="Owner agent" htmlFor="schedule-owner" required>
            <SmartSelect
              value={form.ownerAgentId}
              onValueChange={(v) => setValue('ownerAgentId', v)}
              placeholder="Select an agent"
              items={agents.map((a) => ({ value: a.id, label: a.name }))}
            />
          </Field>

          {/* Name */}
          <Field label="Name" htmlFor="schedule-name" required>
            <Input
              id="schedule-name"
              value={form.name}
              onChange={(e) => setValue('name', e.target.value)}
              placeholder="Daily PR summary"
              className="text-xs"
            />
          </Field>

          {/* Trigger kind */}
          <Field label="Trigger" htmlFor="schedule-trigger-kind">
            <SmartSelect
              value={form.triggerKind}
              onValueChange={(v) => setValue('triggerKind', v as TriggerKind)}
              items={[
                { value: 'at', label: 'Once' },
                { value: 'every', label: 'Every (interval)' },
                { value: 'cron', label: 'Cron expression' },
              ]}
            />
          </Field>

          {form.triggerKind === 'at' && (
            <Field label="Run at" htmlFor="schedule-at">
              <Input
                id="schedule-at"
                type="datetime-local"
                value={form.atLocal}
                onChange={(e) => setValue('atLocal', e.target.value)}
                className="text-xs"
              />
            </Field>
          )}

          {form.triggerKind === 'every' && (
            <Field label="Interval" htmlFor="schedule-every-value">
              <div className="flex gap-2">
                <Input
                  id="schedule-every-value"
                  type="number"
                  min={1}
                  value={form.everyValue}
                  onChange={(e) => setValue('everyValue', e.target.value)}
                  className="text-xs flex-1"
                />
                <div className="w-32">
                  <SmartSelect
                    value={form.everyUnit}
                    onValueChange={(v) => setValue('everyUnit', v as EveryUnit)}
                    items={[
                      { value: 'minutes', label: 'minutes' },
                      { value: 'hours', label: 'hours' },
                      { value: 'days', label: 'days' },
                    ]}
                  />
                </div>
              </div>
            </Field>
          )}

          {form.triggerKind === 'cron' && (
            <Field label="Cron expression" htmlFor="schedule-cron">
              <Input
                id="schedule-cron"
                value={form.cronExpr}
                onChange={(e) => setValue('cronExpr', e.target.value)}
                placeholder="0 18 * * *"
                className="font-mono text-xs"
              />
            </Field>
          )}

          {/* Message */}
          <Field label="Message" htmlFor="schedule-message" required>
            <Textarea
              id="schedule-message"
              value={form.message}
              onChange={(e) => setValue('message', e.target.value)}
              placeholder="Summarize today's merged PRs in one line."
              className="text-xs resize-none h-24"
            />
          </Field>

          {/* Session mode */}
          <Field label="Session mode" htmlFor="schedule-session-mode">
            <SmartSelect
              value={form.sessionMode}
              onValueChange={(v) => setValue('sessionMode', v as SessionMode)}
              items={[
                { value: 'isolated', label: 'Isolated (fresh session per run)' },
                { value: 'continue', label: 'Continue (persistent session)' },
                { value: 'main', label: 'Main (owner main session)' },
              ]}
            />
          </Field>

          {/* Delivery */}
          <div className="flex items-center justify-between">
            <div>
              <Label className="text-xs font-medium text-[var(--color-secondary)]">Delivery</Label>
              <p className="text-[10px] text-[var(--color-muted)]">
                {form.deliver ? 'Send the message directly to the channel.' : 'The agent processes it.'}
              </p>
            </div>
            <Switch
              checked={form.deliver}
              onCheckedChange={(checked) => setValue('deliver', checked)}
              aria-label="Send directly to channel"
            />
          </div>

          {/* Timeout */}
          <Field label="Timeout (seconds)" htmlFor="schedule-timeout">
            <Input
              id="schedule-timeout"
              type="number"
              min={0}
              value={form.timeoutSeconds}
              onChange={(e) => setValue('timeoutSeconds', e.target.value)}
              placeholder="0 = use global default"
              className="text-xs"
            />
          </Field>

          {/* Enabled */}
          <div className="flex items-center justify-between">
            <Label className="text-xs font-medium text-[var(--color-secondary)]">Enabled</Label>
            <Switch
              checked={form.enabled}
              onCheckedChange={(checked) => setValue('enabled', checked)}
              aria-label="Enabled"
            />
          </div>

          {/* Actions */}
          <div className="pt-2 border-t border-[var(--color-border)]">
            <Button
              className="w-full gap-1.5"
              onClick={() => doSave()}
              disabled={!canSave || saving}
            >
              <FloppyDisk size={13} />
              {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Create schedule'}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}

function buildTrigger(form: FormState) {
  switch (form.triggerKind) {
    case 'at':
      return {
        kind: 'at' as const,
        at_ms: form.atLocal ? new Date(form.atLocal).getTime() : undefined,
      }
    case 'every':
      return {
        kind: 'every' as const,
        every_ms: form.everyValue ? Number(form.everyValue) * UNIT_MS[form.everyUnit] : undefined,
      }
    case 'cron':
      return {
        kind: 'cron' as const,
        cron_expr: form.cronExpr || undefined,
      }
  }
}

function Field({
  label,
  htmlFor,
  required,
  children,
}: {
  label: string
  htmlFor: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs font-medium text-[var(--color-secondary)]">
        {label}
        {required && <span className="text-[var(--color-error)] ml-0.5">*</span>}
      </Label>
      {children}
    </div>
  )
}
