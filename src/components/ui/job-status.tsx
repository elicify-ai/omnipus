import { useEffect, useRef, useState } from 'react'

import { cn } from '@/lib/utils'
import { resolvedTokens } from '@/design-system/tokens'

import { Button } from './button'
import { Progress } from './progress'

export type JobState = 'queued' | 'running' | 'progress' | 'paused' | 'failed' | 'complete' | 'cancelled'

export interface JobStatusProps {
  jobKey?: string | number
  status: JobState
  label: string
  progress?: number | null
  max?: number
  lastProgressAt?: number | string | Date
  message?: string
  onCancel?: () => void
  onRetry?: () => void
  onBackground?: () => void
  className?: string
}

const escalationMs = Number.parseFloat(resolvedTokens['motion.loading.escalation'])

const labels: Record<JobState, string> = {
  queued: 'Queued', running: 'Running', progress: 'In progress', paused: 'Paused',
  failed: 'Failed', complete: 'Complete', cancelled: 'Cancelled',
}

function timestamp(value: JobStatusProps['lastProgressAt']): number | null {
  const parsed = value instanceof Date ? value.getTime() : typeof value === 'string' ? Date.parse(value) : value
  return typeof parsed === 'number' && Number.isFinite(parsed) ? parsed : null
}

function normalizedProgress(value: JobStatusProps['progress']): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function JobStatus({
  jobKey, status, label, progress, max = 100, lastProgressAt, message,
  onCancel, onRetry, onBackground, className,
}: JobStatusProps) {
  const [politeAnnouncement, setPoliteAnnouncement] = useState('')
  const [failureAnnouncement, setFailureAnnouncement] = useState('')
  const active = status === 'queued' || status === 'running' || status === 'progress' || status === 'paused'
  const validProgress = typeof progress === 'number' && Number.isFinite(progress)
    && Number.isFinite(max) && max > 0 && progress >= 0 && progress <= max
  const lastKnown = useRef<{ value: number; max: number } | null>(null)

  const reportedTimestamp = timestamp(lastProgressAt)
  const stallClock = useRef({ reportedTimestamp, start: reportedTimestamp !== null && reportedTimestamp <= Date.now() ? reportedTimestamp : Date.now() })
  const previousProgress = useRef(progress)
  const previousIdentity = useRef(jobKey)
  const previousStatus = useRef(status)
  const restarted = previousIdentity.current !== jobKey
    || (!['queued', 'running', 'progress', 'paused'].includes(previousStatus.current) && active)
  const progressChanged = !restarted && normalizedProgress(progress) !== normalizedProgress(previousProgress.current)
  const timestampChanged = reportedTimestamp !== stallClock.current.reportedTimestamp
  const stallStart = restarted || progressChanged
    ? Date.now()
    : timestampChanged
      ? reportedTimestamp !== null && reportedTimestamp <= Date.now() ? reportedTimestamp : Date.now()
      : stallClock.current.start
  const [stalled, setStalled] = useState(active && Date.now() - stallStart >= escalationMs)

  useEffect(() => {
    previousIdentity.current = jobKey
    previousStatus.current = status
    previousProgress.current = progress
    stallClock.current = { reportedTimestamp, start: stallStart }
    if (restarted) lastKnown.current = null
    if (validProgress) lastKnown.current = { value: progress, max }
  }, [jobKey, max, progress, reportedTimestamp, restarted, stallStart, status, validProgress])

  useEffect(() => {
    if (!active) { setStalled(false); return }
    const remaining = Math.max(0, escalationMs - (Date.now() - stallStart))
    setStalled(remaining === 0)
    if (remaining === 0) return
    const timer = setTimeout(() => setStalled(true), remaining)
    return () => clearTimeout(timer)
  }, [active, stallStart, status, progress])

  const retained = !restarted && lastKnown.current?.max === max ? lastKnown.current.value : null
  const shownProgress = validProgress ? progress : retained
  const percentage = shownProgress === null || !Number.isFinite(max) || max <= 0 || shownProgress > max
    ? null
    : Math.round((shownProgress / max) * 100)
  const canCancel = onCancel && ['queued', 'running', 'progress', 'paused'].includes(status)
  const canRetry = onRetry && (status === 'failed' || status === 'cancelled')
  const canBackground = onBackground && ['queued', 'running', 'progress', 'paused'].includes(status)
  const showStalled = active && stalled
  const stalledCopy = status === 'paused'
    ? shownProgress === null ? 'Paused; progress is unavailable.' : 'Paused; no progress reported recently.'
    : shownProgress === null ? 'Still working; progress is unavailable.' : 'No progress reported recently.'

  useEffect(() => {
    if (status === 'failed') {
      setPoliteAnnouncement('')
      setFailureAnnouncement(message ? `Failed. ${message}` : 'Failed')
      return
    }
    setFailureAnnouncement('')
    setPoliteAnnouncement(showStalled ? `${labels[status]}. ${stalledCopy}` : labels[status])
  }, [message, showStalled, stalledCopy, status])

  return (
    <section className={cn('space-y-[var(--space-2-5)] rounded-lg border border-[var(--color-border)] p-[var(--space-3)]', className)} aria-label={label}>
      <span data-job-announcement="polite" className="sr-only" role="status" aria-live="polite" aria-atomic="true">{politeAnnouncement}</span>
      <span data-job-announcement="assertive" className="sr-only" role="alert" aria-atomic="true">{failureAnnouncement}</span>
      <div className="flex items-center justify-between gap-[var(--space-2-5)]">
        <div><p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{label}</p><p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{labels[status]}</p></div>
        {percentage !== null ? <span className="text-[length:var(--type-utility-xs-size)] tabular-nums text-[var(--color-secondary)]">{percentage}%</span> : null}
      </div>
      {(status === 'running' || status === 'progress') ? <Progress value={shownProgress} max={max} label={`${label} progress`} /> : null}
      {message ? <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{message}</p> : null}
      {showStalled ? <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]">{stalledCopy}</p> : null}
      {(canCancel || canRetry || canBackground) ? <div className="flex flex-wrap gap-[var(--space-2)]">
        {canCancel ? <Button variant="outline" size="sm" onClick={onCancel}>Cancel</Button> : null}
        {canRetry ? <Button size="sm" onClick={onRetry}>Retry</Button> : null}
        {canBackground ? <Button variant="ghost" size="sm" onClick={onBackground}>Run in background</Button> : null}
      </div> : null}
    </section>
  )
}
