// useSkillInstall — upload → review → install → confirmed outcome (Skills &
// Tools C-13, D15).
//
// The install answer can be lost while the publish still completes, so the
// outcome comes from the install status: read within 1000 ms of the install,
// then once a second, for at most 60 seconds after it. Only an installed
// state whose installed_revision is the reviewed revision counts as
// installed. Retry reads the status again; it never installs again.

import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { SkillCategory, SkillInstallRequest, SkillInstallReview, SkillInstallReviewStatus } from '@/lib/api/generated/openapi-types'
import { discardSkillInstallReview, fetchSkillInstallStatus, installReviewedSkill, stageSkillInstallReview, uploadFiles } from '@/lib/api'
import { serverErrorText } from '@/lib/api/permissions'
import { isApiError } from '@/lib/api-error'
import { generateId } from '@/lib/constants'
import type { InstallOutcome } from './InstallReview'

const POLL_MS = 1000
const CONFIRM_WINDOW_MS = 60_000

function errorReason(err: unknown): string {
  if (isApiError(err)) return serverErrorText(err.body) ?? err.userMessage
  return err instanceof Error ? err.message : String(err)
}

/** Where the reviewed skill comes from. // not-wire-format */
type InstallSource = Pick<SkillInstallRequest, 'upload_id' | 'slug'>

export function useSkillInstall(currentRevision: (name: string) => string | undefined) {
  const client = useQueryClient()
  const [review, setReview] = useState<SkillInstallReview | null>(null)
  const [source, setSource] = useState<InstallSource | null>(null)
  const [outcome, setOutcome] = useState<InstallOutcome | undefined>(undefined)
  const [stageError, setStageError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [cancelling, setCancelling] = useState(false)
  /** What Cancel reported: the review is gone and nothing was installed. */
  const [cancelNotice, setCancelNotice] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const deadline = useRef(0)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      clearTimeout(timer.current)
    }
  }, [])

  /** Applies one status answer; true when nothing more is to be read. */
  const apply = useCallback((status: SkillInstallReviewStatus, reviewed: SkillInstallReview): boolean => {
    switch (status.state) {
      case 'installed':
        if (status.installed_revision === reviewed.revision) {
          setOutcome({ kind: 'installed' })
          void client.invalidateQueries({ queryKey: ['skills'] })
        } else {
          // Installed, but not what was reviewed: never call it success.
          setOutcome({ kind: 'unknown' })
        }
        return true
      case 'failed':
        setOutcome({ kind: 'not_installed', reason: status.reason })
        return true
      case 'expired':
      case 'cancelled':
        setOutcome({ kind: 'not_installed' })
        return true
      default:
        return false
    }
  }, [client])

  const poll = useCallback((reviewed: SkillInstallReview) => {
    if (!alive.current) return
    if (Date.now() >= deadline.current) {
      setOutcome({ kind: 'unknown' })
      return
    }
    fetchSkillInstallStatus(reviewed.review_id).then(
      (status) => {
        if (!alive.current || apply(status, reviewed)) return
        timer.current = setTimeout(() => poll(reviewed), POLL_MS)
      },
      () => { if (alive.current) setOutcome({ kind: 'unknown' }) },
    )
  }, [apply])

  const upload = useCallback(async (file: File) => {
    setStageError(null)
    setCancelNotice(null)
    setOutcome(undefined)
    setBusy(true)
    try {
      const uploaded = await uploadFiles(generateId(), [file])
      const ref = uploaded.files[0]?.ref
      if (!ref) throw new Error('The upload returned no file to review.')
      const staged = await stageSkillInstallReview({ upload_id: ref })
      setSource({ upload_id: ref })
      setReview(staged)
    } catch (err) {
      setStageError(`Could not open ${file.name} for review: ${errorReason(err).replace(/\.+$/, '')}.`)
    } finally {
      setBusy(false)
    }
  }, [])

  const install = useCallback(async (category: SkillCategory) => {
    if (!review || !source) return
    const revision = review.replaces_existing ? currentRevision(review.name) : undefined
    const body: SkillInstallRequest = { ...source, review_id: review.review_id, category, ...(revision ? { revision } : {}) }
    setBusy(true)
    deadline.current = Date.now() + CONFIRM_WINDOW_MS
    try {
      await installReviewedSkill(body)
      setOutcome({ kind: 'confirming' })
      poll(review)
    } catch (err) {
      if (isApiError(err) && err.status > 0) {
        setOutcome({ kind: 'not_installed', reason: errorReason(err) })
      } else {
        // No answer: the publish may still have happened. Read the status.
        setOutcome({ kind: 'checking' })
        poll(review)
      }
    } finally {
      setBusy(false)
    }
  }, [review, source, currentRevision, poll])

  const retry = useCallback(() => {
    if (!review) return
    clearTimeout(timer.current)
    fetchSkillInstallStatus(review.review_id).then(
      (status) => { if (alive.current && !apply(status, review)) setOutcome({ kind: 'unknown' }) },
      () => { if (alive.current) setOutcome({ kind: 'unknown' }) },
    )
  }, [review, apply])

  const close = useCallback(() => {
    clearTimeout(timer.current)
    setReview(null)
    setSource(null)
    setOutcome(undefined)
  }, [])

  /**
   * Cancel before Install (C-13): discards the staged copy. Nothing was
   * published either way; a 404 means the review had already expired. Any
   * other failure is said, because the staged copy may still be there until
   * it expires.
   */
  const cancel = useCallback(async () => {
    if (!review) return
    setCancelling(true)
    try {
      await discardSkillInstallReview(review.review_id)
      if (alive.current) setCancelNotice('Nothing was installed.')
    } catch (err) {
      if (!alive.current) return
      if (isApiError(err) && err.status === 404) {
        setCancelNotice('Nothing was installed.')
      } else {
        setCancelNotice(`Nothing was installed. The staged copy was not discarded: ${errorReason(err).replace(/\.+$/, '')}. It is removed when the review expires.`)
      }
    } finally {
      if (alive.current) {
        setCancelling(false)
        setReview(null)
        setSource(null)
        setOutcome(undefined)
      }
    }
  }, [review])

  return { review, outcome, busy, cancelling, cancelNotice, stageError, upload, install, retry, cancel, close }
}
