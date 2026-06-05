/**
 * SandboxProfileSelector — per-agent sandbox profile radio control.
 *
 * #335 (US-D3): plain-language labels, Recommended pill on 'none' (inherit
 * global default), kernel/Landlock wording in description, type-the-name
 * confirm for 'off' (existing, unchanged), and a standing warning badge
 * when a WIDENING profile (workspace+net or off) is active.
 *
 * F-G14: a shell-deny pattern hardens the agent — it does NOT trigger the
 * badge. Only the sandbox profile widening triggers it.
 *
 * Five profiles: none | workspace | workspace+net | host | off
 *
 * The "off" profile requires:
 *   1. god_mode_available=true  (build flag)
 *   2. god_mode_opted_in=true   (operator boot flag)
 * If either is false, the "off" radio is disabled with a tooltip explaining why.
 *
 * When the user selects "off", a confirmation dialog appears that requires
 * typing the exact agent display name before the selection commits.
 */

import { useState } from 'react'
import { Warning } from '@phosphor-icons/react'
import type { SandboxProfile } from '@/lib/api'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// ── Profile metadata ──────────────────────────────────────────────────────────

interface ProfileMeta {
  label: string
  desc: string
  recommended?: boolean
  /** True when selecting this profile widens the agent's attack surface. */
  widened?: boolean
}

const PROFILE_META: Record<SandboxProfile, ProfileMeta> = {
  none: {
    label: 'Use global default',
    desc: 'Inherits the sandbox setting from the global Security configuration. Recommended for most agents.',
    recommended: true,
  },
  workspace: {
    label: 'Workspace only',
    desc: 'Kernel-enforced (Landlock) file access limited to the agent workspace directory. No outbound network.',
  },
  'workspace+net': {
    label: 'Workspace + internet access',
    desc: 'Kernel-enforced (Landlock) file access limited to the workspace directory. Outbound network is permitted.',
    widened: true,
  },
  host: {
    label: 'Full host enforcement',
    desc: 'Landlock applied across the full host filesystem — equivalent to the global enforce mode.',
  },
  off: {
    label: 'Off (no isolation)',
    desc: 'No kernel boundary. The agent operates directly on the host system. Requires --allow-god-mode.',
    widened: true,
  },
}

const PROFILE_ORDER: SandboxProfile[] = ['none', 'workspace', 'workspace+net', 'host', 'off']

/** Profiles where the agent's access is widened relative to the standard workspace profile. */
const WIDENED_PROFILES = new Set<SandboxProfile>(['workspace+net', 'off'])

// ── Disabled tooltip ──────────────────────────────────────────────────────────

function DisabledTooltip({ reason }: { reason: string }) {
  const [visible, setVisible] = useState(false)
  return (
    <span className="relative inline-block">
      <button
        type="button"
        className="ml-2 text-[10px] text-[var(--color-muted)] underline decoration-dotted cursor-default focus:outline-none"
        onMouseEnter={() => setVisible(true)}
        onMouseLeave={() => setVisible(false)}
        onFocus={() => setVisible(true)}
        onBlur={() => setVisible(false)}
        tabIndex={0}
        aria-label="Why is this disabled?"
      >
        why?
      </button>
      {visible && (
        <span
          role="tooltip"
          className="absolute bottom-full left-0 mb-1 z-50 w-72 rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 py-1.5 text-[10px] text-[var(--color-muted)] shadow-lg pointer-events-none whitespace-normal"
        >
          {reason}
        </span>
      )}
    </span>
  )
}

// ── Props ─────────────────────────────────────────────────────────────────────

interface Props {
  value: SandboxProfile | undefined
  agentName: string
  godModeAvailable: boolean
  godModeOptedIn: boolean
  onChange: (next: SandboxProfile) => void
}

// ── Component ─────────────────────────────────────────────────────────────────

export function SandboxProfileSelector({
  value,
  agentName,
  godModeAvailable,
  godModeOptedIn,
  onChange,
}: Props) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [confirmInput, setConfirmInput] = useState('')

  const effective: SandboxProfile = value ?? 'none'

  // F-G14 (#335): widened badge when workspace+net or off is active.
  // Shell-deny patterns harden — they do NOT trigger this badge.
  const showWideningBadge = WIDENED_PROFILES.has(effective)

  function handleSelect(profile: SandboxProfile) {
    if (profile === effective) return
    if (profile === 'off') {
      setConfirmInput('')
      setConfirmOpen(true)
      return
    }
    onChange(profile)
  }

  function handleConfirm() {
    if (confirmInput !== agentName) return
    setConfirmOpen(false)
    onChange('off')
  }

  function handleCancel() {
    setConfirmOpen(false)
    setConfirmInput('')
  }

  const godModeDisabledReason = !godModeAvailable
    ? 'Disabled in this build'
    : !godModeOptedIn
    ? 'Operator must start the gateway with --allow-god-mode'
    : null

  return (
    <>
      {/* #335: standing warning badge when a widening profile is active */}
      {showWideningBadge && (
        <div
          data-testid="sandbox-widening-badge"
          className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 mb-3"
        >
          <Warning size={14} className="text-amber-400 shrink-0 mt-0.5" weight="fill" />
          <p className="text-[11px] text-amber-300 leading-relaxed">
            This profile widens the agent's access beyond the workspace boundary.
            Review the agent's tool policies and shell deny patterns before enabling
            unattended runs.
          </p>
        </div>
      )}

      <fieldset className="space-y-2">
        <legend className="sr-only">Sandbox profile</legend>
        {PROFILE_ORDER.map((profile) => {
          const meta = PROFILE_META[profile]
          const isOffDisabled = profile === 'off' && godModeDisabledReason !== null
          const isSelected = effective === profile

          return (
            <label
              key={profile}
              className={[
                'flex items-start gap-2 p-2 rounded-md border transition-colors',
                isOffDisabled
                  ? 'opacity-50 cursor-not-allowed border-[var(--color-border)]'
                  : isSelected
                  ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5 cursor-pointer'
                  : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)] cursor-pointer',
              ].join(' ')}
            >
              <input
                type="radio"
                name={`sandbox-profile-${agentName}`}
                value={profile}
                checked={isSelected}
                disabled={isOffDisabled}
                onChange={() => handleSelect(profile)}
                className="mt-0.5 accent-[var(--color-accent)]"
                aria-label={`Sandbox profile: ${meta.label}`}
                data-testid={`sandbox-profile-radio-${profile}`}
              />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1.5 flex-wrap">
                  <p className="text-sm font-medium text-[var(--color-secondary)]">{meta.label}</p>
                  {/* #335: Recommended pill on 'none' */}
                  {meta.recommended && (
                    <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-emerald-500/20 text-emerald-400 border border-emerald-500/40">
                      Recommended
                    </span>
                  )}
                  {isOffDisabled && (
                    <DisabledTooltip reason={godModeDisabledReason!} />
                  )}
                </div>
                <p className="text-xs text-[var(--color-muted)] leading-snug">{meta.desc}</p>
              </div>
            </label>
          )
        })}
      </fieldset>

      {/* Confirmation dialog for "off" — unchanged from previous version */}
      <Dialog open={confirmOpen} onOpenChange={(open) => { if (!open) handleCancel() }}>
        <DialogContent className="sm:max-w-md bg-[var(--color-surface-1)] border-[var(--color-border)]">
          <DialogHeader>
            <DialogTitle className="text-[var(--color-secondary)]">
              Disable sandbox for {agentName}?
            </DialogTitle>
            <DialogDescription className="text-[var(--color-muted)] text-sm">
              This agent will run with no kernel boundary. Anything it does — including via{' '}
              <code className="font-mono text-[var(--color-secondary)]">workspace.shell</code> — affects the host
              system directly.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <p className="text-xs text-[var(--color-muted)]">
              Type <strong className="text-[var(--color-secondary)] font-mono">{agentName}</strong> to confirm:
            </p>
            <Input
              value={confirmInput}
              onChange={(e) => setConfirmInput(e.target.value)}
              placeholder={agentName}
              className="font-mono text-sm"
              data-testid="sandbox-off-confirm-input"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === 'Enter' && confirmInput === agentName) handleConfirm()
              }}
            />
          </div>
          <DialogFooter className="gap-2">
            <Button
              size="sm"
              variant="ghost"
              onClick={handleCancel}
              className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="default"
              onClick={handleConfirm}
              disabled={confirmInput !== agentName}
              className="bg-[var(--color-error)] text-white hover:bg-[var(--color-error)]/90 disabled:opacity-40"
              data-testid="sandbox-off-confirm-submit"
            >
              Disable sandbox
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

export type { Props as SandboxProfileSelectorProps }
