// Shared pieces of the Export & Import wizards (epic #1094, slice B1): the
// focused step heading, byte formatting, the address helpers and the two
// "we cannot tell what happened" screens.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from '@tanstack/react-router'
import type { ExportImportSearch } from '@/routes/_app/export-import'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { PACKAGE_TEXT } from '@/lib/api/packages'

export const EXPORT_STEPS = ['Choose agent', 'Review', 'Download']
export const IMPORT_STEPS = ['Choose file', 'Preview', 'Confirm', 'Result']

/** The address of the Export & Import page; every wizard step lives in its search. */
export const EXPORT_IMPORT_PATH = '/export-import'

/**
 * A step's heading. Focus moves to it when the step mounts (a heading
 * focusable by script only), so a screen reader announces the new step
 * (spec "Accessibility and Keyboard").
 */
export function StepHeading({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLHeadingElement>(null)
  useEffect(() => {
    ref.current?.focus()
  }, [])
  return (
    <h2
      ref={ref}
      tabIndex={-1}
      className="font-headline text-[length:var(--type-section-title-size)] font-semibold text-[var(--color-secondary)]"
    >
      {children}
    </h2>
  )
}

/** An inline error next to the action that failed (role alert through the error Callout). */
export function InlineError({ text }: { text: string | null | undefined }) {
  if (!text) return null
  return (
    <Callout variant="error">
      <CalloutDescription>{text}</CalloutDescription>
    </Callout>
  )
}

/** A polite live region for busy states ("Preparing the review…"). */
export function BusyNotice({ children }: { children: ReactNode }) {
  return (
    <p aria-live="polite" className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
      {children}
    </p>
  )
}

/** Byte sizes as the spec shows them: "500 B", "2.0 KB", "1.5 MB". */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * A stable key for an address search (key order and unset keys do not
 * matter), so a wizard can tell its own navigation from an address change
 * that comes from outside (a link, Back, a reload).
 */
export function searchKey(search: object): string {
  return JSON.stringify(
    Object.entries(search)
      .filter(([, value]) => value !== undefined)
      .sort(([a], [b]) => a.localeCompare(b)),
  )
}

/**
 * A wizard's view, kept in local state and in the address. The view starts
 * from the address and follows it only when the address changes from
 * outside; `go` shows the next view at once and writes its address, so a
 * reload lands on the same step.
 */
export function useAddressView<V>(search: ExportImportSearch, seed: (search: ExportImportSearch) => V) {
  const navigate = useNavigate()
  const [view, setView] = useState<V>(() => seed(search))
  const key = searchKey(search)
  const lastKey = useRef(key)
  useEffect(() => {
    if (lastKey.current === key) return
    lastKey.current = key
    setView(seed(search))
    // Only an address change re-seeds the view; `search` and `seed` are read through it.
  }, [key])
  const go = useCallback(
    (next: V, nextSearch: ExportImportSearch) => {
      setView(next)
      lastKey.current = searchKey(nextSearch)
      void navigate({ to: EXPORT_IMPORT_PATH, search: nextSearch })
    },
    [navigate],
  )
  /** Shows a view whose address a step writes itself. */
  const show = useCallback((next: V, nextSearch: ExportImportSearch) => {
    setView(next)
    lastKey.current = searchKey(nextSearch)
  }, [])
  return { view, setView, go, show }
}

// A one-shot notice for the landing, set by a wizard just before it leaves
// for the landing (the upload was cancelled but its temporary files stay).
let landingNotice: string | undefined

export function setLandingNotice(text: string | undefined): void {
  landingNotice = text
}

export function peekLandingNotice(): string | undefined {
  return landingNotice
}

/** Opens the Team panel of a workspace's chat (the Team lives there, `?panel=team`). */
export function useOpenTeam(): (workspaceId: string) => void {
  const navigate = useNavigate()
  return (workspaceId: string) => {
    void navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId }, search: { panel: 'team' } })
  }
}

interface UnclearOutcomeProps {
  /** The workspace the tab record names, when it names one. */
  workspaceId?: string
  onStartOver: () => void
}

/**
 * The Uncertain screen: a cancel answer that proves nothing (404, 409, lost
 * or unreadable). Open Team only when the record names a workspace;
 * otherwise the hint. Start over sends nothing.
 */
export function ImportUncertain({ workspaceId, onStartOver }: UnclearOutcomeProps) {
  return (
    <UnclearOutcome text={PACKAGE_TEXT.uncertain} workspaceId={workspaceId} onStartOver={onStartOver} />
  )
}

/**
 * The Outcome-unknown screen: Import was sent and no authoritative answer
 * arrived. Never offers Import again and never cancels the upload.
 */
export function ImportOutcomeUnknown({ workspaceId, onStartOver }: UnclearOutcomeProps) {
  return (
    <UnclearOutcome text={PACKAGE_TEXT.unknownApply} workspaceId={workspaceId} onStartOver={onStartOver} />
  )
}

function UnclearOutcome({ text, workspaceId, onStartOver }: UnclearOutcomeProps & { text: string }) {
  const openTeam = useOpenTeam()
  return (
    <div className="flex flex-col gap-[var(--space-3)]">
      <Callout variant="warning">
        <CalloutDescription>{text}</CalloutDescription>
      </Callout>
      {!workspaceId && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          check the Team of the workspace you chose
        </p>
      )}
      <div className="flex flex-wrap gap-[var(--space-2)]">
        {workspaceId && (
          <Button variant="outline" onClick={() => openTeam(workspaceId)}>
            Open Team
          </Button>
        )}
        <Button onClick={onStartOver}>Start over</Button>
      </div>
    </div>
  )
}
