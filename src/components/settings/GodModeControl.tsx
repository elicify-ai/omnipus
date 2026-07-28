/**
 * GodModeControl — Settings → Gateway god-mode switch (O14).
 *
 * God-mode is the single global "bypass-permissions" switch. When ON it:
 *   - flips every agent's tool permissions from "ask" → "allow" (no prompts),
 *   - disables the kernel sandbox (full host filesystem + syscalls),
 *   - opens outbound network egress,
 *   - turns off the shell guard / deny-patterns.
 * Audit logging, the prompt-guard, and rate limiting STAY ON.
 *
 * Because it removes capability restraints globally, flipping it ALWAYS requires
 * password step-up — the same reAuth / X-Reauth-Token flow used by tool-policies
 * and integrations. We stage the desired state, open ReAuthDialog, and only fire
 * the write (with the minted consent token) once the password is confirmed.
 *
 * Live state is read from GET /api/v1/gateway/god-mode (GodModeStatus:
 * { enabled, available, supported, persisted }). `supported` is build support
 * (false only on a nogodmode build) — the toggle is clickable whenever
 * `supported` is true, even if `available` is currently false. `available`
 * means this BOOT was already authorized (via --allow-god-mode or a prior UI
 * enable + restart); it gates whether the override is live right now, not
 * whether the switch can be flipped. `persisted` is the raw config intent
 * (sandbox.god_mode), read directly and NOT gated by `available` — it is what
 * this component's switch and toggle logic bind to (D1/D19), because
 * `enabled` (== available && persisted) collapses "never armed" and "armed
 * via the UI, pending restart" to the same false value, which made an armed-
 * pending switch render as OFF with no way to disarm it.
 *
 * The write goes through setGodMode() in api.ts (POST /api/v1/gateway/god-mode),
 * which returns { enabled, restart_required }. When enabling from a boot that
 * was not yet authorized, the config write succeeds but the override has no
 * live effect until the gateway restarts — restart_required=true signals
 * exactly that, and we open GatewayRestartModal so the operator can restart
 * immediately (or defer via the existing pending-restart banner). Disabling
 * always applies live (restart_required is always false for a disable), so
 * the restart modal never opens on disable.
 */

import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Warning, ShieldCheck, SpinnerGap } from '@phosphor-icons/react'
import { fetchGodMode, setGodMode, getErrorMessage } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { ReAuthDialog } from './ReAuthDialog'
import { GatewayRestartModal } from './GatewayRestartModal'

export function GodModeControl() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data: godMode, isLoading, isError } = useQuery({
    queryKey: ['god-mode'],
    queryFn: fetchGodMode,
  })

  // The desired next state, staged while the step-up dialog is open. The write
  // only fires from onReAuthConfirmed once the consent token is minted.
  const [pendingNext, setPendingNext] = useState<boolean | null>(null)
  const [reauthOpen, setReauthOpen] = useState(false)
  // Opened when setGodMode reports restart_required=true (enabling from a
  // boot that was not yet authorized). Never opens for a disable.
  const [restartModalOpen, setRestartModalOpen] = useState(false)

  // NOTE: `godMode.enabled` is deliberately NOT read anywhere in this
  // component. It is a server-derived convenience (== available && persisted)
  // meaning "live in THIS process", and binding any UI to it is precisely what
  // caused D1/D19: it reads false in BOTH S0 (never armed) and S1 (armed,
  // pending restart), so the switch showed OFF while armed and requestToggle()
  // could only ever compute `!enabled` = true. Everything here binds to
  // `persisted` (intent) or `available` (this boot was authorized), which
  // together distinguish all the states this UI needs.
  const available = godMode?.available === true
  // persisted = the raw config intent (sandbox.god_mode), read directly and
  // NOT gated by availability (D1/D19). This is the field the switch's
  // visual state and the toggle logic must bind to — `enabled` collapses
  // both "never armed" (S0) and "armed via the UI, pending restart" (S1) to
  // false, which is exactly what made the switch look OFF while armed and
  // made requestToggle() compute `!enabled` = true forever (no way to
  // disarm from the UI).
  const persisted = godMode?.persisted === true
  // supported = build support (false only on a nogodmode build). Distinct
  // from `available` (this boot was already authorized) — the toggle is
  // clickable whenever the build supports god mode at all, since enabling is
  // exactly how an unauthorized boot GETS authorized (persist + restart).
  const supported = godMode?.supported === true
  // True only when the query genuinely SUCCEEDED and reported no build
  // support. `isError` must never collapse into this — a fetch failure
  // (gateway offline) is not the same fact as "god-mode compiled out", and
  // must not read (or behave, e.g. disabling the toggle) as if it were.
  const knownUnsupported = !isLoading && !isError && !supported

  const { mutate: applyChange, isPending: isSaving } = useMutation({
    mutationFn: ({ next, token }: { next: boolean; token: string }) => setGodMode(next, token),
    onSuccess: (data, { next }) => {
      // Refresh the read-side state so the banner + toggle reflect reality.
      queryClient.invalidateQueries({ queryKey: ['god-mode'] })
      // The toggle also changes per-agent effective sandbox/tool behaviour.
      queryClient.invalidateQueries({ queryKey: ['config'] })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      // A pending-restart entry for sandbox.god_mode(_allowed) also appears in
      // the generic banner; refresh it so it's in sync with our own modal.
      queryClient.invalidateQueries({ queryKey: ['pending-restart'] })
      setPendingNext(null)
      if (data.restart_required) {
        // Enabled, but not yet active in this process — surface the restart
        // modal so the operator can activate immediately (or defer via
        // "Later", same as any other restart-gated setting).
        addToast({ message: 'God-mode authorized — restart the gateway to activate it', variant: 'error' })
        setRestartModalOpen(true)
      } else {
        addToast({
          message: next ? 'God-mode enabled' : 'God-mode disabled',
          variant: next ? 'error' : 'success',
        })
      }
    },
    onError: (err: unknown) => {
      setPendingNext(null)
      addToast({
        message: getErrorMessage(err, 'Could not change god-mode'),
        variant: 'error',
      })
    },
  })

  // requestToggle stages the desired state and opens the step-up dialog. The PUT
  // fires from onReAuthConfirmed once the password is verified. Gated on
  // `knownUnsupported` — blocks only once a successful, non-loading fetch has
  // confirmed god-mode is unsupported; a still-loading or failed fetch must
  // NOT block the toggle, since those are "unknown" states, not a confirmed
  // "unsupported" one. NOT gated on `available` — enabling from an
  // unauthorized boot is exactly the UI-driven enablement flow.
  //
  // D1: negates `persisted`, not `enabled`. `enabled` is always false while
  // `available` is false (S0 and S1 both), so negating it would compute
  // `true` forever once armed — an operator could arm god-mode but could
  // never reach the toggle-driven disarm path from the UI. `persisted`
  // reflects the actual config intent the switch controls.
  function requestToggle() {
    if (knownUnsupported || isSaving) return
    setPendingNext(!persisted)
    setReauthOpen(true)
  }

  // requestDisarm always stages `false`, regardless of the current state —
  // it is the explicit "Cancel authorization" affordance shown in the S1
  // (armed, pending restart) banner. Disabling is unconditionally permitted
  // by the backend (fail-safe), so this never needs to check availability.
  function requestDisarm() {
    if (isSaving) return
    setPendingNext(false)
    setReauthOpen(true)
  }

  function onReAuthConfirmed(token: string) {
    if (pendingNext === null) return
    applyChange({ next: pendingNext, token })
  }

  const busy = isSaving || reauthOpen

  return (
    <div className="space-y-3">
      <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">
        Danger zone
      </h3>

      <div
        data-testid="god-mode-control"
        className={[
          'rounded-lg border px-4 py-3.5 transition-colors',
          // Danger styling keys on `persisted`, NOT `enabled` — same signal the
          // switch itself binds to (see requestToggle) and the same one the
          // Security Health check uses server-side (rest.go's god-mode-armed
          // issue triggers on cfg.Sandbox.GodMode).
          //
          // `enabled` means "live in THIS process" and is false in state S1
          // (authorized, pending restart). Keying the card on it produced a red
          // ON switch sitting inside a calm, muted card — a milder replay of the
          // exact switch-vs-banner contradiction the D1 fix existed to remove.
          // S1 is one restart away from disabling the kernel sandbox, egress
          // restrictions and the shell guard for every agent, so it must read as
          // dangerous, not as reassuring.
          persisted
            ? 'border-[var(--color-error)]/60 bg-[var(--color-error)]/10'
            : 'border-[var(--color-error)]/30 bg-[var(--color-surface-1)]',
        ].join(' ')}
      >
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 space-y-1.5">
            <div className="flex items-center gap-2">
              <Warning
                size={16}
                weight="fill"
                // Keyed on `persisted` for the same reason as the card border
                // above: S1 (armed, pending restart) must not render as calm.
                className={persisted ? 'text-[var(--color-error)]' : 'text-amber-400'}
              />
              <p className="text-sm font-semibold text-[var(--color-secondary)]">God-mode</p>
            </div>
            <div id="god-mode-consequence-copy">
              <p className="text-xs text-[var(--color-muted)] leading-relaxed">
                Removes <strong className="text-[var(--color-secondary)]">all permission prompts</strong> and
                disables the kernel sandbox, outbound-network restrictions, and the shell guard for every agent.
                Audit logging, the prompt-guard, and rate limiting stay on.
              </p>
              <p className="flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]">
                <ShieldCheck size={12} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
                Changing this requires re-typing your password.
              </p>
            </div>
            {/* Dynamic status notes — kept mounted (even when empty) and marked
                aria-live so a note appearing/changing is announced reliably,
                rather than depending on the element itself being inserted. At
                most one of the three conditions below is ever true at once. */}
            <div id="god-mode-status-note" aria-live="polite">
              {isError && (
                <p
                  data-testid="god-mode-fetch-error-note"
                  className="text-[11px] text-[var(--color-error)]"
                >
                  Could not fetch god-mode status — gateway may be offline. The state shown here may
                  be stale.
                </p>
              )}
              {knownUnsupported && (
                <p
                  data-testid="god-mode-unavailable-note"
                  className="text-[11px] text-[var(--color-muted)] italic"
                >
                  Not available in this build — god-mode support is compiled out.
                </p>
              )}
              {/* D19: gated strictly on persisted && !available — true only
                  in real S1 (armed via the UI, pending restart), never in S0
                  (never armed: persisted=false). Previously gated on
                  `supported && !available`, which is also true for a
                  never-touched fresh install and falsely claimed it was
                  "authorized but not yet active". */}
              {persisted && !available && !isLoading && (
                <div className="space-y-1">
                  <p
                    data-testid="god-mode-restart-note"
                    className="text-[11px] text-[var(--color-muted)] italic"
                  >
                    Authorized but not yet active — restart the gateway to activate god-mode.
                  </p>
                  {/* D1/D19: explicit disarm affordance — an operator must be
                      able to cancel a pending authorization without waiting
                      for (or triggering) a restart. Replays the existing
                      setGodMode(false, token) re-auth flow; no new endpoint. */}
                  <button tabIndex={0}
                    type="button"
                    data-testid="god-mode-cancel-authorization"
                    disabled={busy || isLoading}
                    onClick={requestDisarm}
                    className="text-[11px] font-medium text-[var(--color-accent)] hover:underline disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    Cancel authorization
                  </button>
                </div>
              )}
            </div>
          </div>

          {/* Switch — accessible role=switch, danger styling when ON.
              D1: bound to `persisted` (the config intent this control
              actually governs), not `enabled` (whether it's live in THIS
              process). Binding to `enabled` made an armed-but-pending-
              restart switch render as OFF, and made requestToggle()'s
              `!enabled` negation re-arm instead of disarm on every click. */}
          <button tabIndex={0}
            type="button"
            role="switch"
            aria-checked={persisted}
            aria-label="God-mode"
            aria-describedby="god-mode-consequence-copy god-mode-status-note"
            data-testid="god-mode-toggle"
            disabled={knownUnsupported || busy || isLoading}
            onClick={requestToggle}
            className={[
              'relative shrink-0 inline-flex h-6 w-11 items-center rounded-full transition-colors',
              'disabled:opacity-40 disabled:cursor-not-allowed',
              persisted ? 'bg-[var(--color-error)]' : 'bg-[var(--color-surface-3)]',
            ].join(' ')}
          >
            <span
              className={[
                'inline-flex items-center justify-center h-5 w-5 rounded-full bg-white shadow transition-transform',
                persisted ? 'translate-x-[22px]' : 'translate-x-0.5',
              ].join(' ')}
            >
              {busy && <SpinnerGap size={11} className="animate-spin text-[var(--color-error)]" />}
            </span>
          </button>
        </div>
      </div>

      {/* Step-up password modal — always shown before a god-mode change. */}
      <ReAuthDialog
        open={reauthOpen}
        onOpenChange={(next) => {
          setReauthOpen(next)
          // Dialog dismissed without confirming — drop the staged change.
          if (!next) setPendingNext(null)
        }}
        title="Confirm your password to change god-mode"
        description={
          pendingNext
            ? 'God-mode removes all permission prompts and disables the kernel sandbox. Re-type your password to enable it.'
            : 'Re-type your password to disable god-mode and restore the previous protections.'
        }
        onConfirmed={onReAuthConfirmed}
      />

      {/* O14: shown only when enabling persisted authorization but the
          override has no live effect yet in this process (restart_required
          from the POST response). Disabling never opens this — it always
          applies live. */}
      <GatewayRestartModal
        open={restartModalOpen}
        onClose={() => setRestartModalOpen(false)}
      />
    </div>
  )
}

/**
 * GodModeActiveBanner — persistent indicator shown at the top of the Gateway
 * section (and anywhere god-mode visibility matters) while god-mode is active.
 * Reads the live state from AppState; renders nothing when god-mode is off.
 */
export function GodModeActiveBanner() {
  const { data: godMode, isError } = useQuery({
    queryKey: ['god-mode'],
    queryFn: fetchGodMode,
  })

  const enabled = godMode?.enabled === true

  // Genuinely off (query succeeded and reported enabled=false) — nothing to
  // warn about. NB: this must NOT be reached on a fetch failure — `enabled`
  // collapses to false when `godMode` is undefined, which is indistinguishable
  // from "really off" unless we check `isError` too. A transport error must
  // never silently look like "sandboxing is definitely on" — that is exactly
  // the moment an operator most needs a signal, not silence.
  if (!enabled && !isError) return null

  if (isError) {
    return (
      <div
        role="alert"
        data-testid="god-mode-status-unknown-banner"
        className="flex items-start gap-3 rounded-lg border border-amber-500/60 bg-amber-500/10 px-4 py-3"
      >
        <Warning size={18} weight="fill" className="shrink-0 mt-0.5 text-amber-400" />
        <div className="space-y-1">
          <p className="text-sm font-semibold text-amber-400">God-mode status unavailable</p>
          <p className="text-xs text-amber-400/80">
            Could not fetch god-mode status from the gateway — it may be offline. If god-mode was
            previously active, sandboxing may still be disabled right now and this banner cannot
            confirm it either way. Check your connection and reload.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div
      role="alert"
      data-testid="god-mode-active-banner"
      className="flex items-start gap-3 rounded-lg border border-[var(--color-error)]/60 bg-[var(--color-error)]/10 px-4 py-3"
    >
      <Warning size={18} weight="fill" className="shrink-0 mt-0.5 text-[var(--color-error)]" />
      <div className="space-y-1">
        <p className="text-sm font-semibold text-[var(--color-error)]">God-mode is active</p>
        <p className="text-xs text-[var(--color-error)]/80">
          All permission prompts are bypassed and the kernel sandbox, network restrictions, and shell guard are
          disabled for every agent. Audit logging, the prompt-guard, and rate limiting remain on. Turn god-mode
          off below to restore the previous protections.
        </p>
      </div>
    </div>
  )
}
