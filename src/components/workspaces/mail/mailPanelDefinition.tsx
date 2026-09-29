// mailPanelDefinition.tsx — Mail's side-panel registration payload
// (side-panel-shell-spec.md §8.1), registered in the shell's production
// registry (src/components/panel-shell/registry.tsx).
//
// Final shared shell contract (SP-38/R11, D48/D49): full screen is a
// SHELL-owned feature — the shell owns the chrome-less `#/panel/mail` route
// and drives it through `fullScreen.toSearch`/`fromSearch` (a pure context
// <-> query-object codec), never through a panel-owned `expandTarget` or
// `window.open`. Mail's only job is to report its CURRENT selection back to
// the shell via `registerExpandContext`, so the shell can build that route
// from whatever mailbox/folder/message is on screen right now. D48: the
// list-above-preview vs. side-by-side layout choice comes from the shell's
// `presentation` prop alone, never from Mail's own state.
import { useCallback, useEffect, useRef } from 'react'
import type {
  PanelContentProps,
  PanelContext,
  PanelDefinition,
  WorkspacePanelContext,
} from '@/components/panel-shell/types'
import { lazy } from 'react'
import type { MailPanelLocation } from './MailPanel'
import {
  confirmDiscardMailEdits,
  isMailEditorDirty,
} from './mailUnsavedGuard'

// Lazy, like the shell's Library/Browser entries: the registry is imported
// by the AppShell eagerly, so the mail panel's code must ride the same
// on-open dynamic chunk path instead of the shell's eager one.
const MailPanel = lazy(async () => {
  const module = await import('./MailPanel')
  return { default: module.MailPanel }
})

const MAIL_FOLDER_VALUES = ['inbox', 'sent', 'drafts'] as const
type MailFolder = (typeof MAIL_FOLDER_VALUES)[number]

/** Explicit-null marker for a query value: the shell's route search only
 * carries strings, so `null` (explicitly no mailbox / no folder / no open
 * message) must be distinguishable from the key being absent entirely
 * (no directive at all — SP-23's "undefined" posture). Every real value in
 * these three fields is non-empty by construction (mailbox/agent ids,
 * the closed folder enum, and the opaque `uid:…`/`mid:…` message-ref
 * format), so the empty string can never collide with a real one. */
const NULL_MARKER = ''

function encodeNullable(value: string | null | undefined): string | undefined {
  if (value === undefined) return undefined
  return value === null ? NULL_MARKER : value
}

function decodeNullable(value: string): string | null {
  return value === NULL_MARKER ? null : value
}

/** Mail's `fullScreen` context <-> search codec (§8.1). Only Mail's own
 * `WorkspacePanelContext` fields are read/written — the shell never opens
 * Mail's full-screen route with a `BrowserPanelContext`. */
function mailToSearch(context: PanelContext): Record<string, string> {
  const ctx = context as WorkspacePanelContext
  const search: Record<string, string> = {}
  if (ctx.workspaceId !== undefined) search.workspace = ctx.workspaceId
  const mailbox = encodeNullable(ctx.mailboxId)
  if (mailbox !== undefined) search.mailbox = mailbox
  const folder = encodeNullable(ctx.folder)
  if (folder !== undefined) search.folder = folder
  const message = encodeNullable(ctx.messageRef)
  if (message !== undefined) search.message = message
  return search
}

function mailFromSearch(search: Record<string, unknown>): PanelContext | null {
  const context: WorkspacePanelContext = {}

  if ('workspace' in search) {
    const value = search.workspace
    if (typeof value !== 'string') return null
    context.workspaceId = value
  }

  if ('mailbox' in search) {
    const value = search.mailbox
    if (typeof value !== 'string') return null
    context.mailboxId = decodeNullable(value)
  }

  if ('folder' in search) {
    const value = search.folder
    if (typeof value !== 'string') return null
    if (value === NULL_MARKER) {
      context.folder = null
    } else if ((MAIL_FOLDER_VALUES as readonly string[]).includes(value)) {
      context.folder = value as MailFolder
    } else {
      return null
    }
  }

  if ('message' in search) {
    const value = search.message
    if (typeof value !== 'string') return null
    context.messageRef = decodeNullable(value)
  }

  return context
}

function MailPanelContent({ context, presentation, close, registerExpandContext }: PanelContentProps) {
  const workspaceContext = context as WorkspacePanelContext
  const locationRef = useRef<MailPanelLocation>({
    mailboxId: workspaceContext.mailboxId ?? null,
    folder: workspaceContext.folder ?? 'inbox',
    messageRef: workspaceContext.messageRef ?? null,
  })

  // Read fresh from the ref on every call — registered ONCE, so the same
  // getter keeps answering with Mail's live selection for as long as the
  // panel stays mounted, and unregisters (null) on unmount.
  const getCurrentContext = useCallback((): PanelContext => ({
    workspaceId: workspaceContext.workspaceId,
    mailboxId: locationRef.current.mailboxId,
    folder: locationRef.current.folder as MailFolder,
    messageRef: locationRef.current.messageRef,
  }), [workspaceContext.workspaceId])

  useEffect(() => {
    registerExpandContext(getCurrentContext)
    return () => registerExpandContext(null)
  }, [registerExpandContext, getCurrentContext])

  return (
    <MailPanel
      workspaceId={workspaceContext.workspaceId ?? ''}
      mailboxId={workspaceContext.mailboxId}
      layout={presentation === 'fullscreen' ? 'split' : 'stacked'}
      onReturnToChat={presentation === 'fullscreen' ? close : undefined}
      initialFolder={workspaceContext.folder ?? undefined}
      initialMessageRef={workspaceContext.messageRef ?? undefined}
      onLocationChange={(location) => { locationRef.current = location }}
    />
  )
}

/** The §8.1 PanelDefinition for the Mail panel. */
export const mailPanelDefinition: PanelDefinition = {
  id: 'mail',
  title: 'Mail',
  // The shell's own <Suspense> boundary hosts the lazy chunk ("Loading
  // Mail…" fallback) — same as Library/Browser.
  content: MailPanelContent,
  fullScreen: {
    toSearch: mailToSearch,
    fromSearch: mailFromSearch,
  },
  // CRIT-001: unsaved compose text (MailComposeDialog) or unsaved
  // draft-editor text (MailPreviewPane's edit mode) guards leaving, same
  // shared leave gate as Library's editor.
  beforeLeave: confirmDiscardMailEdits,
  beforeLeaveRequired: isMailEditorDirty,
}
