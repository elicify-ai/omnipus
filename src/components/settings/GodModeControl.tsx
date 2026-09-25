/**
 * GodModeControl — Settings → Gateway god-mode switch (O14).
 *
 * God-mode is the single global "bypass-permissions" switch. When ON it:
 *   - flips every agent's tool permissions from "ask" → "allow" (no prompts),
 *   - disables the kernel sandbox's filesystem confinement (full host
 *     filesystem) and network port controls for the shell's child process —
 *     seccomp's syscall filter stays installed process-wide and cannot be
 *     removed per-child, so it still applies,
 *   - opens outbound network egress (no network pre-flight, ADR-092 D8).
 * Audit logging, the prompt-guard, and rate limiting STAY ON — and operator
 * `command_rules` deny entries (ADR-092 D3) still refuse a matching command,
 * since they are enforced inside the shell tool, downstream of this floor.
 * The shell's own outside-workspace write refusal also survives — it just
 * never prompts under god mode, it still hard-denies.
 *
 * Because it removes capability restraints globally, flipping it ALWAYS asks for
 * a confirmation first (ADR-0008 ruling 6) — a dialog that names the change and
 * asks whether you meant it, not a password. We stage the desired state, open
 * the step-up gate (ReAuthDialog in local mode, ConfirmDialog in platform
 * mode — ADR-0010 WP3), and only fire the write once the operator confirms.
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
import { Button } from '@/components/ui/button'
import { useDevModeBypassKnown } from '@/hooks/useDevModeBypassKnown'
import { isBypassUnavailable } from '@/hooks/useGodModeLiveStatus'
import { useStepUp } from './useStepUp'
import { isReAuthCancelled } from './useReAuthGate'
import { GatewayRestartModal } from './GatewayRestartModal'

export function GodModeControl() {
  const { addToast } = useUiStore()
  const stepUp = useStepUp()
  const queryClient = useQueryClient()

  // GET /api/v1/gateway/god-mode always 503s under dev_mode_bypass, so skip
  // the doomed request once dev_mode_bypass (shared ['app-state'] cache
  // entry, same key AppShell already fetches — see useDevModeBypassKnown)
  // says it's on, rather than firing it and explaining the error away
  // afterward. (isBypassUnavailable's full rationale lives in
  // useGodModeLiveStatus, which shares it.)
  const { known: devModeBypassKnown, resolved: appStateResolved } = useDevModeBypassKnown()

  const { data: godMode, isLoading: godModeIsLoading, isError, error } = useQuery({
    queryKey: ['god-mode'],
    queryFn: fetchGodMode,
    enabled: appStateResolved && !devModeBypassKnown,
  })
  const bypassUnavailable = devModeBypassKnown || isBypassUnavailable(error)
  // Still resolving whether god-mode is even reachable: either appState
  // itself hasn't answered yet, or it has (bypass confirmed off) and the
  // now-enabled god-mode query is in flight. Once bypass is confirmed on,
  // this reads false immediately — nothing is "loading", it's just
  // unavailable, exactly like a completed fetch that hit the real 503 did
  // before this fix.
  const isLoading = !appStateResolved || (!devModeBypassKnown && godModeIsLoading)

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
  // `bypassUnavailable` must not collapse into this either, now that it can
  // be known WITHOUT the query ever running (isError stays false in that
  // case) — otherwise this would show "compiled out" side by side with the
  // "unavailable while bypass is active" note below on every bypass install.
  const knownUnsupported = !isLoading && !isError && !bypassUnavailable && !supported

  const { mutateAsync: applyChangeAsync, isPending: isSaving } = useMutation({
    // The consent token exists only in local (password) mode; confirm mode
    // calls with no token, exactly as before.
    mutationFn: ({ next, token }: { next: boolean; token?: string }) =>
      token === undefined ? setGodMode(next) : setGodMode(next, token),
    onSuccess: (data, { next }) => {
      // Refresh the read-side state so the banner + toggle reflect reality.
      queryClient.invalidateQueries({ queryKey: ['god-mode'] })
      // The toggle also changes per-agent effective sandbox/tool behaviour.
      queryClient.invalidateQueries({ queryKey: ['config'] })
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      // A pending-restart entry for sandbox.god_mode(_allowed) also appears in
      // the generic banner; refresh it so it's in sync with our own modal.
      queryClient.invalidateQueries({ queryKey: ['pending-restart'] })
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
      addToast({
        message: getErrorMessage(err, 'Could not change god-mode'),
        variant: 'error',
      })
    },
  })

  // stage runs a change through the step-up gate (ADR-0010 WP3): ReAuthDialog
  // and a replayed consent token in local mode, ConfirmDialog with no token in
  // platform mode. The gate is staged BEFORE the POST fires, so "cancel" means
  // nothing was sent and the staged state is simply dropped.
  function stage(next: boolean) {
    void stepUp
      .gate((token) => applyChangeAsync({ next, token }), {
        title: next ? 'Enable god mode?' : 'Disable god mode?',
        body: next
          ? 'Agents stop asking permission before anything, and the sandbox around their code is switched off. You can turn this back off at any time.'
          : 'Agents go back to asking permission, and the sandbox around their code is switched back on.',
        confirmLabel: next ? 'Enable god mode' : 'Disable god mode',
      })
      .catch((err: unknown) => {
        // A cancelled gate sent nothing — stay silent. A real failure already
        // toasted once via applyChangeAsync's own onError; do not toast twice.
        if (isReAuthCancelled(err)) return
      })
  }

  // requestToggle stages the desired state through the gate. Gated on
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
    stage(!persisted)
  }

  // requestDisarm always stages `false`, regardless of the current state —
  // it is the explicit "Cancel authorization" affordance shown in the S1
  // (armed, pending restart) banner. Disabling is unconditionally permitted
  // by the backend (fail-safe), so this never needs to check availability.
  function requestDisarm() {
    if (isSaving) return
    stage(false)
  }

  const busy = isSaving

  return (
    <div className="space-y-[var(--space-2-5)]">
      <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">
        Danger zone
      </h3>

      <div
        data-testid="god-mode-control"
        className={[
          'rounded-lg border px-[var(--space-3)] py-[var(--space-3)] transition-colors',
          // Danger styling keys on `persisted`, NOT `enabled` — same signal the
          // switch itself binds to (see requestToggle) and the same one the
          // Security Health check uses server-side (rest.go's god-mode-armed
          // issue triggers on cfg.Sandbox.GodMode).
          //
          // `enabled` means "live in THIS process" and is false in state S1
          // (authorized, pending restart). Keying the card on it produced a red
          // ON switch sitting inside a calm, muted card — a milder replay of the
          // exact switch-vs-banner contradiction the D1 fix existed to remove.
          // S1 is one restart away from disabling the kernel sandbox and
          // egress restrictions for every agent, so it must read as
          // dangerous, not as reassuring.
          persisted
            ? 'border-[var(--color-error)]/60 bg-[var(--color-error)]/10'
            : 'border-[var(--color-error)]/30 bg-[var(--color-surface-1)]',
        ].join(' ')}
      >
        <div className="flex items-start justify-between gap-[var(--space-3)]">
          <div className="min-w-0 space-y-[var(--space-1)]">
            <div className="flex items-center gap-[var(--space-2)]">
              <Warning
                size={16}
                weight="fill"
                // Keyed on `persisted` for the same reason as the card border
                // above: S1 (armed, pending restart) must not render as calm.
                className={persisted ? 'text-[var(--color-error)]' : 'text-[var(--color-warning)]'}
              />
              <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">God-mode</p>
            </div>
            <div id="god-mode-consequence-copy">
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
                Removes <strong className="text-[var(--color-secondary)]">all permission prompts</strong> and
                disables the kernel sandbox and outbound-network restrictions for every agent. Configured
                deny rules still refuse a matching command. Audit logging, the prompt-guard, and rate
                limiting stay on.
              </p>
              <p className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                <ShieldCheck size={12} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
                Changing this requires re-typing your password.
              </p>
            </div>
            {/* Dynamic status notes — kept mounted (even when empty) and marked
                aria-live so a note appearing/changing is announced reliably,
                rather than depending on the element itself being inserted. At
                most one of the three conditions below is ever true at once. */}
            <div id="god-mode-status-note" aria-live="polite">
              {bypassUnavailable && (
                <p
                  data-testid="god-mode-bypass-unavailable-note"
                  className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic"
                >
                  Not available while development-mode bypass is active.
                </p>
              )}
              {isError && !bypassUnavailable && (
                <p
                  data-testid="god-mode-fetch-error-note"
                  className="text-[length:var(--type-caption-size)] text-[var(--color-error)]"
                >
                  Could not fetch god-mode status — gateway may be offline. The state shown here may
                  be stale.
                </p>
              )}
              {knownUnsupported && (
                <p
                  data-testid="god-mode-unavailable-note"
                  className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic"
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
                <div className="space-y-[var(--space-1)]">
                  <p
                    data-testid="god-mode-restart-note"
                    className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] italic"
                  >
                    Authorized but not yet active — restart the gateway to activate god-mode.
                  </p>
                  {/* D1/D19: explicit disarm affordance — an operator must be
                      able to cancel a pending authorization without waiting
                      for (or triggering) a restart. Replays the existing
                      setGodMode(false, token) re-auth flow; no new endpoint. */}
                  <Button
                    type="button"
                    variant="link"
                    data-testid="god-mode-cancel-authorization"
                    disabled={busy || isLoading}
                    onClick={requestDisarm}
                    className="text-[length:var(--type-caption-size)] font-medium disabled:opacity-40"
                  >
                    Cancel authorization
                  </Button>
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
          {/* Kept on Button (not the catalogued Switch): the checked-state
              track color here is danger red (--color-error), not the
              Switch's accent gold, and the thumb hosts a busy spinner — both
              are behaviour Switch doesn't express. Button with the same
              role="switch"/aria-checked semantics preserves the D1/D19
              contract exactly (see file header). */}
          <Button
            type="button"
            variant="ghost"
            role="switch"
            aria-checked={persisted}
            aria-label="God-mode"
            aria-describedby="god-mode-consequence-copy god-mode-status-note"
            data-testid="god-mode-toggle"
            disabled={knownUnsupported || busy || isLoading}
            onClick={requestToggle}
            className={[
              'relative h-6 w-11 shrink-0 rounded-full p-0 transition-colors',
              'disabled:opacity-40',
              persisted
                ? 'bg-[var(--color-error)] hover:bg-[var(--color-error)]'
                : 'bg-[var(--color-surface-3)] hover:bg-[var(--color-surface-3)]',
            ].join(' ')}
          >
            <span
              className={[
                'inline-flex items-center justify-center h-5 w-5 rounded-full bg-[var(--color-secondary)] shadow transition-transform',
                persisted ? 'translate-x-[22px]' : 'translate-x-0.5',
              ].join(' ')}
            >
              {busy && <SpinnerGap size={11} className="animate-spin text-[var(--color-error)]" />}
            </span>
          </Button>
        </div>
      </div>

      {/* The step-up dialogs (ReAuthDialog in local mode, ConfirmDialog in
          platform mode) — rendered once; only the one matching the mode ever opens. */}
      {stepUp.dialogs}

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
