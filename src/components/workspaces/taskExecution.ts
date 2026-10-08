import type { Task } from '@/lib/api'

// This is display math over generated Task fields, never a new wire shape.
export const DEFAULT_TASK_MAX_ATTEMPTS = 3

export function goalLoopStatusLabel(
  task: Pick<Task, 'attempt_count' | 'effective_max_attempts' | 'max_attempts' | 'status'> & Partial<Pick<Task, 'judge_rounds' | 'goal_max_rounds'>>,
  paused: boolean,
): string | null {
  const running = task.status === 'in_progress'
  const attemptsUsed = task.attempt_count ?? 0
  const triesLimit = task.goal_max_rounds ?? 0
  const triesUsed = task.judge_rounds ?? 0
  const showTries = triesLimit > 0 && triesUsed > 0
  const parts: string[] = []
  if (attemptsUsed > 0 || (running && showTries)) {
    const max = task.effective_max_attempts ?? task.max_attempts ?? DEFAULT_TASK_MAX_ATTEMPTS
    parts.push(`attempt ${running ? attemptsUsed + 1 : attemptsUsed} of ${max}`)
  }
  if (showTries) {
    const current = running ? Math.min(triesUsed + 1, triesLimit) : triesUsed
    parts.push(`try ${current} of ${triesLimit}`)
  }
  return parts.length === 0 ? null : `${parts.join(' · ')}${paused ? ' · paused' : ''}`
}

/** Live execution time uses started_at→now; terminal time uses the server's
 * completed_at. Never substitute created_at, updated_at or a guessed end.
 */
export function taskExecutionSeconds(task: Pick<Task, 'status' | 'started_at' | 'completed_at'>, now: number): number | null {
  const start = task.started_at ? Date.parse(task.started_at) : NaN
  const end = task.status === 'in_progress' ? now
    : (task.status === 'done' || task.status === 'failed') && task.completed_at ? Date.parse(task.completed_at) : NaN
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null
  return Math.floor((end - start) / 1000)
}

export function executionTimeLabel(seconds: number): string {
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainder = seconds % 60
  if (hours > 0) return `${hours}h ${String(minutes).padStart(2, '0')}m ${String(remainder).padStart(2, '0')}s`
  if (minutes > 0) return `${minutes}m ${String(remainder).padStart(2, '0')}s`
  return `${remainder}s`
}
