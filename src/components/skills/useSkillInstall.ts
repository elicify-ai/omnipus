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
import { useUiStore } from '@/store/ui'
import type { InstallOutcome } from './InstallReview'

const POLL_MS = 1000

/** The spec's success toast (UI states, Install review). */
export function installedToast(name: string): string {
  return `Installed ${name}. It is set to Ask: an agent needs your yes to use it unless Auto-approve is on. ` +
    "No agent is granted it yet; grant it on an agent's Skills tab."
}
const CONFIRM_WINDOW_MS = 60_000

function errorReason(err: unknown): string {
  if (isApiError(err)) return serverErrorText(err.body) ?? err.userMessage
  return err instanceof Error ? err.message : String(err)
}

/** Where the reviewed skill comes from. // not-wire-format */
type InstallSource = Pick<SkillInstallRequest, 'upload_id' | 'slug'>

export function useSkillInstall(currentRevision: (name: string) => string | undefined) {
  const client = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [review, setReview] = useState<SkillInstallReview | null>(null)
  const [source, setSource] = useState<InstallSource | null>(null)
  const [outcome, setOutcome] = useState<InstallOutcome | undefined>(undefined)
  const [stageError, setStageError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  /** True while an upload is being staged for review ("Checking the skill…"). */
  const [staging, setStaging] = useState(false)
  /** Bumped by a Cancel while staging, so a late review is discarded. */
  const stagingRun = useRef(0)
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
          addToast({ message: installedToast(reviewed.name), variant: 'success' })
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
  }, [client, addToast])

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
    setStaging(true)
    const run = ++stagingRun.current
    const cancelled = () => run !== stagingRun.current || !alive.current
    try {
      const uploaded = await uploadFiles(generateId(), [file])
      const ref = uploaded.files[0]?.ref
      if (!ref) throw new Error('The upload returned no file to review.')
      const staged = await stageSkillInstallReview({ upload_id: ref })
      if (cancelled()) {
        // Cancelled while staging: the late review is thrown away, never offered.
        discardSkillInstallReview(staged.review_id).catch(() => { /* expires on its own */ })
        return
      }
      setSource({ upload_id: ref })
      setReview(staged)
    } catch (err) {
      if (!cancelled()) setStageError(`Could not open ${file.name} for review: ${errorReason(err).replace(/\.+$/, '')}.`)
    } finally {
      if (!cancelled()) {
        setBusy(false)
        setStaging(false)
      }
    }
  }, [])

  /** Cancel while the skill is being staged (C-13): nothing is installed. */
  const cancelStaging = useCallback(() => {
    stagingRun.current++
    setStaging(false)
    setBusy(false)
    setCancelNotice('Nothing was installed.')
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

  return { review, outcome, busy, staging, cancelling, cancelNotice, stageError, upload, install, retry, cancel, cancelStaging, close }
}
