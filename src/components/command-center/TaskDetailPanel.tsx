import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { fetchAgents, fetchSubtasks, updateTask, startTask, isApiError } from '@/lib/api'
import type { Task } from '@/lib/api'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { SmartSelect } from '@/components/ui/smart-select'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { useUiStore } from '@/store/ui'
import {
  Play,
  Copy,
  PencilSimple,
  Check,
  Robot,
  X,
  ChatCircle,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// ── Status config ──────────────────────────────────────────────────────────────

const STATUS_OPTIONS: { value: Task['status']; label: string; color: string }[] = [
  { value: 'queued',    label: 'Queued',    color: 'text-blue-400' },
  { value: 'assigned',  label: 'Assigned',  color: 'text-purple-400' },
  { value: 'running',   label: 'Running',   color: 'text-yellow-400' },
  { value: 'completed', label: 'Completed', color: 'text-green-400' },
  { value: 'failed',    label: 'Failed',    color: 'text-red-400' },
]

const PRIORITY_CONFIG: Record<number, { label: string; color: string }> = {
  1: { label: 'P1 — Critical',  color: 'text-red-400' },
  2: { label: 'P2 — High',      color: 'text-orange-400' },
  3: { label: 'P3 — Medium',    color: 'text-yellow-400' },
  4: { label: 'P4 — Low',       color: 'text-blue-400' },
  5: { label: 'P5 — Minimal',   color: 'text-[var(--color-muted)]' },
}

// ── Status badge for subtask list ──────────────────────────────────────────────

const STATUS_BADGE: Record<string, string> = {
  queued:    'text-blue-400 bg-blue-400/10',
  assigned:  'text-purple-400 bg-purple-400/10',
  running:   'text-yellow-400 bg-yellow-400/10',
  completed: 'text-green-400 bg-green-400/10',
  failed:    'text-red-400 bg-red-400/10',
}

// ── Helpers ───────────────────────────────────────────────────────────────────


// ── Props ──────────────────────────────────────────────────────────────────────

interface TaskDetailPanelProps {
  task: Task | null
  onClose: () => void
  onTaskSelect?: (task: Task) => void
}

// ── Component ──────────────────────────────────────────────────────────────────

export function TaskDetailPanel({ task, onClose, onTaskSelect }: TaskDetailPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  // Prompt editing state — only for queued tasks
  const [editingPrompt, setEditingPrompt] = useState(false)
  const [promptDraft, setPromptDraft] = useState('')

  useEffect(() => {
    setPromptDraft(task?.prompt ?? '')
    setEditingPrompt(false)
  }, [task?.id, task?.prompt])

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const { data: subtasks = [] } = useQuery({
    queryKey: ['subtasks', task?.id],
    queryFn: () => fetchSubtasks(task!.id),
    enabled: task != null,
  })

  const { mutate: doUpdate } = useMutation({
    mutationFn: (data: Partial<Task>) => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, data)
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tasks'] }),
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update task', variant: 'error' }),
  })

  const { mutate: doStart, isPending: isStarting } = useMutation({
    mutationFn: () => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return startTask(task.id)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks'] })
      addToast({ message: 'Task started.', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to start task', variant: 'error' }),
  })

  const handleSavePrompt = () => {
    const trimmed = promptDraft.trim()
    if (trimmed !== (task?.prompt ?? '').trim()) {
      doUpdate({ prompt: trimmed })
    }
    setEditingPrompt(false)
  }

  const handleCopyResult = async () => {
    if (!task?.result) return
    try {
      await navigator.clipboard.writeText(task.result)
      addToast({ message: 'Result copied to clipboard.', variant: 'success' })
    } catch {
      addToast({ message: 'Failed to copy to clipboard.', variant: 'error' })
    }
  }

  const handleCopyPath = async (path: string) => {
    try {
      await navigator.clipboard.writeText(path)
      addToast({ message: 'Path copied.', variant: 'success' })
    } catch {
      addToast({ message: 'Failed to copy path.', variant: 'error' })
    }
  }

  const formatDate = (iso?: string) => {
    if (!iso) return '—'
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(iso))
  }

  const isQueued = task?.status === 'queued'
  const showResult = task?.status === 'completed' || task?.status === 'failed'

  return (
    <Sheet open={task != null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-[380px] sm:w-[460px] overflow-y-auto">
        <SheetHeader className="mb-5">
          <SheetTitle className="pr-6 leading-snug">{task?.title ?? ''}</SheetTitle>
        </SheetHeader>

        {task && (
          <div className="space-y-5">

            {/* Prompt section */}
            <Field label="Prompt / Instructions">
              {editingPrompt ? (
                <div className="space-y-1.5">
                  <Textarea
                    value={promptDraft}
                    onChange={(e) => setPromptDraft(e.target.value)}
                    className="text-xs min-h-[80px] font-mono"
                    autoFocus
                  />
                  <div className="flex gap-1.5">
                    <Button
                      size="sm"
                      className="h-6 px-2 text-[10px] gap-1"
                      onClick={handleSavePrompt}
                    >
                      <Check size={10} weight="bold" /> Save
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-6 px-2 text-[10px] gap-1"
                      onClick={() => {
                        setPromptDraft(task.prompt)
                        setEditingPrompt(false)
                      }}
                    >
                      <X size={10} /> Cancel
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="relative group">
                  <pre className="text-xs font-mono text-[var(--color-secondary)] bg-[var(--color-surface-2)] rounded-md p-3 whitespace-pre-wrap break-words leading-relaxed">
                    {task.prompt || <span className="text-[var(--color-muted)]">No prompt set.</span>}
                  </pre>
                  {isQueued && (
                    <button
                      type="button"
                      onClick={() => setEditingPrompt(true)}
                      className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)]"
                      aria-label="Edit prompt"
                    >
                      <PencilSimple size={12} />
                    </button>
                  )}
                </div>
              )}
            </Field>

            {/* Priority */}
            <Field label="Priority">
              <SmartSelect
                value={String(task.priority)}
                onValueChange={(val) => doUpdate({ priority: parseInt(val, 10) })}
                disabled={!isQueued}
                triggerClassName="h-8 text-xs"
                items={[1, 2, 3, 4, 5].map((p) => ({
                  value: String(p),
                  label: PRIORITY_CONFIG[p]?.label ?? `P${p}`,
                  className: cn('text-xs', PRIORITY_CONFIG[p]?.color),
                }))}
              />
            </Field>

            {/* Status */}
            <Field label="Status">
              <SmartSelect
                value={task.status}
                onValueChange={(val) => doUpdate({ status: val as Task['status'] })}
                triggerClassName="h-8 text-xs"
                items={STATUS_OPTIONS.map((o) => ({
                  value: o.value,
                  label: o.label,
                  className: cn('text-xs', o.color),
                }))}
              />
            </Field>

            {/* Agent */}
            <Field label="Agent">
              <SmartSelect
                value={task.agent_id ?? '__none__'}
                onValueChange={(val) =>
                  doUpdate({ agent_id: val === '__none__' ? undefined : val })
                }
                placeholder="Unassigned"
                triggerClassName="h-8 text-xs"
                items={[
                  { value: '__none__', label: 'Unassigned', className: 'text-xs' },
                  ...agents.map((a) => ({ value: a.id, label: a.name, className: 'text-xs' })),
                ]}
              />
            </Field>

            {/* Start button — queued tasks only */}
            {isQueued && (
              <Button
                className="w-full gap-2 text-xs h-8"
                onClick={() => doStart()}
                disabled={isStarting}
              >
                <Play size={13} weight="fill" />
                {isStarting ? 'Starting...' : 'Start Task'}
              </Button>
            )}

            {/* Open in Chat — available once the task has a session.
                Navigate directly to the session route so its loader calls
                setActiveSession + setAttachedContext before first render.
                This avoids the race where navigating to '/' mounts
                RootChatScreen which unconditionally calls startNewSession(),
                wiping the activeSessionId the attach had just set. */}
            {task.session_id && (
              <Button
                variant="outline"
                size="sm"
                className="w-full gap-2 text-xs h-8"
                onClick={() => {
                  void navigate({
                    to: '/sessions/$sessionId',
                    params: { sessionId: task.session_id! },
                  })
                  onClose()
                }}
              >
                <ChatCircle size={13} />
                Open in Chat
              </Button>
            )}

            {/* Result section — completed or failed */}
            {showResult && task.result && (
              <Field label="Result">
                <div className="relative">
                  <pre className="text-xs font-mono text-[var(--color-secondary)] bg-[var(--color-surface-2)] rounded-md p-3 max-h-[200px] overflow-y-auto whitespace-pre-wrap break-words leading-relaxed">
                    {task.result}
                  </pre>
                  <button
                    type="button"
                    onClick={handleCopyResult}
                    className="absolute top-2 right-2 flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)] transition-colors"
                    aria-label="Copy result"
                  >
                    <Copy size={11} /> Copy
                  </button>
                </div>
              </Field>
            )}

            {/* Artifacts section */}
            {(task.artifacts?.length ?? 0) > 0 && (
              <Field label="Artifacts">
                <div className="space-y-1">
                  {task.artifacts!.map((path) => (
                    <div
                      key={path}
                      className="flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs"
                    >
                      <span className="flex-1 font-mono text-[var(--color-secondary)] truncate">{path}</span>
                      <button
                        type="button"
                        onClick={() => handleCopyPath(path)}
                        className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
                        aria-label={`Copy path: ${path}`}
                      >
                        <Copy size={11} />
                      </button>
                    </div>
                  ))}
                </div>
              </Field>
            )}

            {/* Sub-tasks section */}
            {subtasks.length > 0 && (
              <Field label={`Sub-tasks (${subtasks.length})`}>
                <div className="space-y-1">
                  {subtasks.map((sub) => (
                    <button
                      key={sub.id}
                      type="button"
                      onClick={() => onTaskSelect?.(sub)}
                      className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs hover:bg-[var(--color-surface-1)] transition-colors text-left"
                    >
                      <Badge
                        variant="outline"
                        className={cn('text-[9px] px-1 py-0 shrink-0 border-0', STATUS_BADGE[sub.status] ?? '')}
                      >
                        {sub.status}
                      </Badge>
                      <span className="flex-1 text-[var(--color-secondary)] truncate">{sub.title}</span>
                      {sub.agent_name && (
                        <span className="shrink-0 text-[var(--color-muted)] flex items-center gap-0.5">
                          <Robot size={10} /> {sub.agent_name}
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              </Field>
            )}

            {/* Metadata */}
            <div className="pt-2 border-t border-[var(--color-border)] space-y-1.5">
              {task.created_by && (
                <MetaRow label="Created by" value={task.created_by} />
              )}
              <MetaRow label="Created" value={formatDate(task.created_at)} />
              <MetaRow label="Started" value={formatDate(task.started_at)} />
              <MetaRow label="Completed" value={formatDate(task.completed_at)} />
              <MetaRow label="Trigger" value={task.trigger_type} />
            </div>

          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

// ── Field ──────────────────────────────────────────────────────────────────────

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">{label}</p>
      {children}
    </div>
  )
}

// ── MetaRow ────────────────────────────────────────────────────────────────────

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="text-[var(--color-muted)] w-[80px] shrink-0">{label}</span>
      <span className="text-[var(--color-secondary)]">{value}</span>
    </div>
  )
}
