// RestartBanner — one banner on every Settings section while saved changes
// wait for a gateway restart (Settings redesign #1519, FR-035, FR-038,
// FR-039, FR-062). Reads GET /config/pending-restart (both reasons:
// restart_required and apply_failed). Show changes lists each entry's label,
// running value and saved value. A Callout (warning) with two Buttons — no
// new kit component.

import { useEffect, useRef, useState, type RefObject } from 'react'
import { ArrowClockwise } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import type { PendingRestartEntry } from '@/lib/api'

/** Visible labels for the dotted keys the gateway lists (FR-035). */
const KEY_LABELS: Record<string, string> = {
  'gateway.port': 'Port',
  'gateway.host': 'Bind address',
  'gateway.public_url': 'Public address',
  'gateway.log_level': 'Log level',
  'gateway.preview_enabled': 'Preview server',
  'sandbox.mode': 'Sandbox mode',
  'sandbox.filesystem_model': 'Filesystem model',
  'sandbox.workspace_path_guard': 'Shell workspace limit',
  'sandbox.allowed_paths': 'Allowed folders',
  'sandbox.audit_log': 'Audit log',
  'sandbox.prompt_injection_level': 'Defense level',
  'tools.exec.enable_proxy': 'Network proxy for shell commands',
  'session.dm_scope': 'Direct message scope',
  'tools.run_in_workspace.warmup_timeout_seconds': 'Workspace warm-up time limit',
}

export function pendingKeyLabel(key: string): string {
  if (KEY_LABELS[key]) return KEY_LABELS[key]
  if (key.startsWith('security.rate_limits')) return 'Rate limits'
  if (key.startsWith('context.')) return 'Limits: context and tool results'
  if (key.startsWith('sandbox.third_party_agents')) return 'Third-party agents'
  if (key.startsWith('security.platform_auth')) return 'Platform sign-in'
  return key
}

function show(v: unknown): string {
  if (v === null || v === undefined) return 'unknown'
  if (typeof v === 'string') return v || 'empty'
  return JSON.stringify(v)
}

/** One waiting change as the banner shows it (FR-035). */
export interface RestartBannerEntry { // not-wire-format: screen-side view of a pending-restart entry
  key: string
  label: string
  /** The value still in force; null when it is not known. */
  appliedValue: unknown
  /** The saved value, used after the restart. */
  persistedValue: unknown
  reason?: string
}

/** Maps the gateway's pending-restart list to the banner's entries. */
export function toBannerEntries(entries: readonly PendingRestartEntry[]): RestartBannerEntry[] {
  return entries.map((e) => ({
    key: e.key,
    label: pendingKeyLabel(e.key),
    appliedValue: e.applied_value,
    persistedValue: e.persisted_value,
    reason: e.reason,
  }))
}

export interface RestartBannerProps { // not-wire-format: React props
  entries: RestartBannerEntry[]
  /** The list could not be loaded (FR-039). */
  error?: boolean
  onRetry: () => void
  onRestartNow: () => void
  /** After Later in the "Restart now?" window, Restart now here takes focus (FR-035). */
  autofocusRestartNow?: boolean
  restartButtonRef?: RefObject<HTMLButtonElement | null>
  /** Landed on the new address after a port change (FR-063). */
  restartedNotice?: boolean
}

export function RestartBanner({
  entries,
  error = false,
  onRetry,
  onRestartNow,
  autofocusRestartNow = false,
  restartButtonRef,
  restartedNotice,
}: RestartBannerProps) {
  const [open, setOpen] = useState(false)
  const ownRef = useRef<HTMLButtonElement | null>(null)
  const hasEntries = entries.length > 0
  useEffect(() => {
    if (autofocusRestartNow && hasEntries && !error) ownRef.current?.focus()
  }, [autofocusRestartNow, hasEntries, error])
  const setRestartRef = (el: HTMLButtonElement | null) => {
    ownRef.current = el
    if (restartButtonRef) restartButtonRef.current = el
  }
  if (error) {
    return (
      <Callout variant="warning" className="mb-[var(--space-3)]" data-testid="settings-restart-banner-error">
        <CalloutTitle>The list of waiting changes could not be loaded</CalloutTitle>
        <CalloutDescription>Saved changes may still wait for a gateway restart. Retry to check.</CalloutDescription>
        <CalloutActions>
          <Button variant="outline" size="sm" onClick={onRetry}>
            <ArrowClockwise size={13} aria-hidden="true" />
            Retry
          </Button>
        </CalloutActions>
      </Callout>
    )
  }
  if (!entries.length) {
    if (restartedNotice) {
      return (
        <Callout variant="success" className="mb-[var(--space-3)]" data-testid="settings-restarted">
          <CalloutTitle>The gateway restarted on port {window.location.port || (window.location.protocol === 'https:' ? '443' : '80')}</CalloutTitle>
        </Callout>
      )
    }
    return null
  }
  const n = entries.length
  return (
    <Callout variant="warning" className="mb-[var(--space-3)]" data-testid="settings-restart-banner">
      <CalloutTitle>
        {n} saved {n === 1 ? 'change waits' : 'changes wait'} for a gateway restart
      </CalloutTitle>
      <CalloutDescription>Agents pause for a few seconds while the gateway restarts.</CalloutDescription>
      {open && (
        <ul id="settings-pending-list" className="mt-[var(--space-1)] space-y-[var(--space-0-5)] text-[length:var(--type-body-compact-size)]">
          {entries.map((e) => (
            <li key={e.key}>
              <span className="font-medium">{e.label}</span>: running <span>{show(e.appliedValue)}</span>, after the restart{' '}
              <span>{show(e.persistedValue)}</span> ·{' '}
              <span>{e.reason === 'apply_failed' ? 'saved, not applied' : 'needs a restart'}</span>
            </li>
          ))}
        </ul>
      )}
      <CalloutActions>
        <Button ref={setRestartRef} variant="outline" size="sm" onClick={onRestartNow}>
          Restart now
        </Button>
        <Button
          variant="ghost"
          size="sm"
          aria-expanded={open}
          aria-controls="settings-pending-list"
          onClick={() => setOpen((v) => !v)}
        >
          {open ? 'Hide changes' : 'Show changes'}
        </Button>
      </CalloutActions>
    </Callout>
  )
}
