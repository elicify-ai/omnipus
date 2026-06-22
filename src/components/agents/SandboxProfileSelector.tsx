/**
 * SandboxProfileSelector — per-agent sandbox profile radio control.
 *
 * O13 (LOCKED 2026-06-20):
 *   - The per-agent picker enum is `workspace / workspace+net / host` (plus the
 *     UI-only "Use global default" inherit marker). `off` has been REMOVED — the
 *     only way to run with no sandbox is the global god-mode switch in
 *     Settings → Gateway, which is a global override (not a per-agent choice).
 *   - Editable for ALL agents, including locked core agents — a core agent's
 *     "locked" status does NOT extend to its sandbox profile.
 *   - subagent_3p hides sandbox entirely (handled by the caller, not here).
 *
 * #335 (US-D3): plain-language labels, Recommended pill on the inherit marker,
 * kernel/Landlock wording in descriptions, and a standing warning badge when a
 * WIDENING profile (workspace+net) is active.
 *
 * F-G14: a shell-deny pattern hardens the agent — it does NOT trigger the
 * badge. Only the sandbox profile widening triggers it.
 */

import { Warning } from '@phosphor-icons/react'
import type { SandboxProfile } from '@/lib/api'

// ── Profile metadata ──────────────────────────────────────────────────────────

interface ProfileMeta {
  label: string
  desc: string
  recommended?: boolean
  /** True when selecting this profile widens the agent's attack surface. */
  widened?: boolean
}

// The picker excludes 'off' (O13) — "no sandbox" is only the global god-mode
// switch. 'none' remains the UI-only "inherit global default" marker.
type PickerProfile = Exclude<SandboxProfile, 'off'>

const PROFILE_META: Record<PickerProfile, ProfileMeta> = {
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
}

const PROFILE_ORDER: PickerProfile[] = ['none', 'workspace', 'workspace+net', 'host']

/** Profiles where the agent's access is widened relative to the standard workspace profile. */
const WIDENED_PROFILES = new Set<PickerProfile>(['workspace+net'])

// ── Props ─────────────────────────────────────────────────────────────────────

interface Props {
  value: SandboxProfile | undefined
  agentName: string
  onChange: (next: PickerProfile) => void
}

// ── Component ─────────────────────────────────────────────────────────────────

export function SandboxProfileSelector({ value, agentName, onChange }: Props) {
  // 'off' can still arrive on the wire from a legacy agent config; surface it as
  // the inherit default in the picker (the user can then pick an enforced
  // profile). It is never re-selectable here.
  const effective: PickerProfile = value && value !== 'off' ? value : 'none'

  // F-G14 (#335): widened badge when workspace+net is active.
  // Shell-deny patterns harden — they do NOT trigger this badge.
  const showWideningBadge = WIDENED_PROFILES.has(effective)

  function handleSelect(profile: PickerProfile) {
    if (profile === effective) return
    onChange(profile)
  }

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
          const isSelected = effective === profile

          return (
            <label
              key={profile}
              className={[
                'flex items-start gap-2 p-2 rounded-md border transition-colors cursor-pointer',
                isSelected
                  ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                  : 'border-[var(--color-border)] hover:bg-[var(--color-surface-2)]',
              ].join(' ')}
            >
              <input
                type="radio"
                name={`sandbox-profile-${agentName}`}
                value={profile}
                checked={isSelected}
                onChange={() => handleSelect(profile)}
                className="mt-0.5 accent-[var(--color-accent)]"
                aria-label={`Sandbox profile: ${meta.label}`}
                data-testid={`sandbox-profile-radio-${profile}`}
              />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1.5 flex-wrap">
                  <p className="text-sm font-medium text-[var(--color-secondary)]">{meta.label}</p>
                  {/* #335: Recommended pill on the inherit marker */}
                  {meta.recommended && (
                    <span className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-emerald-500/20 text-emerald-400 border border-emerald-500/40">
                      Recommended
                    </span>
                  )}
                </div>
                <p className="text-xs text-[var(--color-muted)] leading-snug">{meta.desc}</p>
              </div>
            </label>
          )
        })}
      </fieldset>
    </>
  )
}

export type { Props as SandboxProfileSelectorProps, PickerProfile }
