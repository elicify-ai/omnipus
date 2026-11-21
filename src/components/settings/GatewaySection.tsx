/**
 * GatewaySection — Settings → Gateway tab.
 *
 * FR-107: token auth is the only mode — there is NO auth_mode control. The
 *   legacy auth_mode field was removed entirely (backend never had it; the
 *   frontend default-coerced it to "none"). Do NOT re-add a "none" mode.
 * FR-107b: a loud persistent banner warns the operator when dev_mode_bypass=true
 *   — the only setting that actually disables authentication.
 * US-B2 / #328:
 * - bind_address 0.0.0.0 wrapped in RiskySettingControl (safe = '127.0.0.1').
 * - Standing badge derives from persisted config values (fetchConfig → ['config'] query).
 * - Badge clears on save of the safe value (queryClient.invalidateQueries clears it).
 * Hot-reload is always on (FR-106); the toggle is removed.
 *
 * US-4 / FR-008 (docs/internal/specs/preview-on-main-listener-spec.md, ADR-044):
 * a live "Preview" toggle bound to `gateway.preview_enabled` (default ON).
 * This goes through the standard `updateConfig({ gateway: { preview_enabled } })`
 * path — `frontendToRawConfig` guards each gateway field with `!== undefined`,
 * so a partial update containing only `preview_enabled` never clobbers
 * bind_address/port/log_level, and the bind/port/log_level autosave below
 * (which never sets `preview_enabled`) never clobbers the toggle either.
 */

import { useCallback, useMemo, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, ArrowsClockwise, CheckCircle, CaretDown, CaretRight, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'
import { fetchConfig, updateConfig, rotateGatewayToken, fetchGatewayStatus, getErrorMessage, type Config } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { usePendingRestart } from '@/hooks/restart'
import { SaveBarPanel, SettingRow } from './SaveBar'
import { toWriteResponse } from './saveOutcome'
import { useSaveBar, type SaveBarField, type SaveBarValue } from './useSaveBar'
import { GatewayRestartModal } from './GatewayRestartModal'
import { settingsRow } from './settingsSections'

// ── Risky-control copy bundles (US-B2) ────────────────────────────────────────

const LOCAL_ONLY = '127.0.0.1'
const ALL_INTERFACES = '0.0.0.0'
const BIND_LABELS: Record<string, string> = { [LOCAL_ONLY]: 'Localhost only', [ALL_INTERFACES]: 'All interfaces' }
const LOG_LEVELS = ['debug', 'info', 'warn', 'error']

function validatePort(v: SaveBarValue): string | null {
  const n = typeof v === 'number' ? v : Number(v)
  if (v === '' || !Number.isInteger(n) || n < 1024 || n > 65535) return 'Enter a whole number from 1,024 to 65,535.'
  return null
}

/** The write body's dotted keys mapped onto the gateway config object. */
function gatewayBody(body: Record<string, SaveBarValue>): Record<string, unknown> {
  const g: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(body)) g[k.replace(/^gateway\./, '')] = v
  return g
}

// ── UnauthenticatedBanner (FR-107b) ───────────────────────────────────────────

interface UnauthBannerProps {
  devModeBypass: boolean
}

// The banner warns ONLY about the genuinely-insecure state: dev_mode_bypass=true,
// which makes the gateway accept any request as admin. The legacy `auth_mode`
// field is NOT consulted — token auth is the only mode (FR-107), so an unset
// auth_mode (serialized as "none") does NOT disable auth; access is still gated
// by the configured user + bearer token. Keying the banner off auth_mode==="none"
// produced a false "unauthenticated access" alarm on every normal install.
function UnauthenticatedBanner({ devModeBypass }: UnauthBannerProps) {
  if (!devModeBypass) return null
  return (
    <div
      role="alert"
      data-testid="unauth-banner"
      className="flex items-start gap-[var(--space-2-5)] rounded-lg border border-[var(--color-error)]/60 bg-[var(--color-error)]/10 px-[var(--space-3)] py-[var(--space-2-5)]"
    >
      <Warning size={18} weight="fill" className="shrink-0 mt-[var(--space-0-5)] text-[var(--color-error)]" />
      <div className="space-y-[var(--space-1)]">
        <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-error)]">
          Unauthenticated access is enabled
        </p>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]/80">
          Anyone who can reach this server has admin access (dev_mode_bypass is enabled in
          config.json). To fix this, set
          <code className="mx-[var(--space-1)] font-mono text-[length:var(--type-caption-size)] bg-[var(--color-error)]/10 px-[var(--space-1)] rounded">
            gateway.dev_mode_bypass: false
          </code>
          in config.json, then restart the gateway.
        </p>
      </div>
    </div>
  )
}

export function GatewaySection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [copied, setCopied] = useState(false)
  const [remoteAccessOpen, setRemoteAccessOpen] = useState(false)
  const [restartModalOpen, setRestartModalOpen] = useState(false)
  const [rotateConfirmOpen, setRotateConfirmOpen] = useState(false)
  const [bindConfirmOpen, setBindConfirmOpen] = useState(false)
  const bindResolver = useRef<((ok: boolean) => void) | null>(null)
  const { entries: pendingEntries } = usePendingRestart()

  const { data: config, isLoading, isError: isConfigError, refetch: refetchConfig } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
  })

  const { isSuccess: isOnline } = useQuery({
    queryKey: ['gateway-status'],
    queryFn: fetchGatewayStatus,
    retry: false,
  })

  // Every Gateway value waits for Save changes (FR-010, #346: no auto-save).
  const fields = useMemo<SaveBarField[]>(() => {
    const g = config?.gateway
    return [
      {
        key: 'gateway.bind_address',
        label: 'Bind address',
        value: g?.bind_address ?? LOCAL_ONLY,
        kind: 'select',
        options: [LOCAL_ONLY, ALL_INTERFACES],
        description: 'Which network interfaces the gateway listens on. All interfaces asks you to confirm first.',
        restart: true,
      },
      {
        key: 'gateway.port',
        label: 'Port',
        value: g?.port ?? 8080,
        kind: 'number',
        description: '1024 to 65535.',
        restart: true,
        validate: validatePort,
      },
      {
        key: 'gateway.preview_enabled',
        label: 'Preview server',
        value: g?.preview_enabled ?? true,
        kind: 'switch',
        description: 'Serves pages your agents build so you can open them. Takes effect as soon as it is saved.',
      },
      {
        key: 'gateway.log_level',
        label: 'Log level',
        value: g?.log_level ?? 'info',
        kind: 'select',
        options: LOG_LEVELS,
        description: 'How much detail the gateway writes to its log.',
      },
    ]
  }, [config])

  // FR-016: "Listen on all interfaces?" runs before any write; Cancel stores nothing.
  const confirmBindAll = useCallback(
    (changed: SaveBarField[], drafts: Record<string, SaveBarValue>) => {
      const bind = changed.find((f) => f.key === 'gateway.bind_address')
      if (!bind || drafts['gateway.bind_address'] !== ALL_INTERFACES) return Promise.resolve(true)
      return new Promise<boolean>((resolve) => {
        bindResolver.current = resolve
        setBindConfirmOpen(true)
      })
    },
    [],
  )
  const settleBind = (ok: boolean) => {
    const r = bindResolver.current
    bindResolver.current = null
    setBindConfirmOpen(false)
    r?.(ok)
  }

  const state = useSaveBar({
    section: 'gateway',
    fields,
    onSave: (body) =>
      toWriteResponse(() => updateConfig({ gateway: gatewayBody(body) as unknown as Config['gateway'] })),
    readBack: async (key) => {
      const fresh = await fetchConfig()
      return (fresh.gateway as unknown as Record<string, unknown>)[key.replace(/^gateway\./, '')]
    },
    preSave: confirmBindAll,
    onSaved: () => void queryClient.invalidateQueries({ queryKey: ['config'] }),
  })

  const { mutate: doRotate, isPending: isRotating } = useMutation({
    mutationFn: rotateGatewayToken,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['config'] })
      addToast({ message: 'The gateway token was rotated. Apps and programs must sign in with the new one.', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'The gateway token was not rotated'), variant: 'error' }),
  })

  const copyToken = async () => {
    const token = config?.gateway.token
    if (!token) return
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      addToast({ message: 'The token could not be copied. Select it and copy it by hand.', variant: 'error' })
    }
  }

  // FR-065: loading text with a spinner and Skeleton cards until the reads
  // answer; both stop moving under reduced motion (FR-075).
  if (isLoading) {
    return (
      <div className="space-y-[var(--space-2)]" aria-busy="true" data-testid="gateway-loading">
        <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          <Spinner decorative />
          Loading Gateway…
        </div>
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-[var(--space-8)] w-full" />
        ))}
      </div>
    )
  }

  if (isConfigError || !config) {
    return (
      <Card className="border-[var(--color-error)]/40 p-[var(--space-3)] space-y-[var(--space-2-5)]">
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">The gateway settings could not be loaded.</p>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          Nothing can be saved here until they load, so no setting is overwritten with a default.
        </p>
        <Button size="sm" variant="outline" onClick={() => refetchConfig()}>
          Retry
        </Button>
      </Card>
    )
  }

  const portEntry = pendingEntries.find((e) => e.key === 'gateway.port')
  const bindEntry = pendingEntries.find((e) => e.key === 'gateway.bind_address' || e.key === 'gateway.host')
  const runningPort = portEntry ? String(portEntry.applied_value) : String(config.gateway.port)
  const runningBind = bindEntry ? String(bindEntry.applied_value) : config.gateway.bind_address
  const devModeBypass = config.gateway.dev_mode_bypass === true
  const [bindField, portField, previewField, logField] = fields

  return (
    <div className="space-y-[var(--space-4)]">
      {/* FR-107b: persistent unauthenticated-access banner (dev_mode_bypass only) */}
      <UnauthenticatedBanner devModeBypass={devModeBypass} />

      <div className="flex items-center gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        {isOnline ? (
          <Badge variant="success" className="gap-[var(--space-1)] text-[length:var(--type-caption-size)]">Online</Badge>
        ) : (
          <Badge variant="error" className="gap-[var(--space-1)] text-[length:var(--type-caption-size)]">Offline</Badge>
        )}
        <span>
          Listening on <span className="font-mono">{runningBind}:{runningPort}</span>
        </span>
      </div>

      <section aria-label="Network" className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Network</h3>
        <Card className="px-[var(--space-3)]">
          <SettingRow state={state} field={bindField}>
            {({ id, fieldId, value, setValue, describedBy }) => (
              <select tabIndex={0}
                id={id}
                data-settings-field={fieldId}
                value={String(value ?? LOCAL_ONLY)}
                onChange={(e) => setValue(e.target.value)}
                aria-describedby={describedBy}
                className="flex h-9 w-full max-w-xs rounded-md border border-[var(--color-border)] bg-transparent px-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]"
              >
                {[LOCAL_ONLY, ALL_INTERFACES].map((o) => (
                  <option key={o} value={o}>
                    {BIND_LABELS[o]} ({o})
                  </option>
                ))}
              </select>
            )}
          </SettingRow>
          <SettingRow state={state} field={portField} />
        </Card>
      </section>

      <section aria-label="Access" className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Access</h3>
        <Card className="p-[var(--space-3)] space-y-[var(--space-2)]">
          <div className="flex flex-wrap items-center justify-between gap-[var(--space-2)]" {...settingsRow('Gateway token')}>
            <div className="min-w-0">
              <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Gateway token</p>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Apps and programs use it to sign in. Rotating it signs them out.</p>
            </div>
            <div className="flex gap-[var(--space-2)]">
              <Button variant="outline" size="sm" onClick={copyToken} disabled={!config.gateway.token} aria-label="Copy gateway token">
                {copied ? <CheckCircle size={11} className="text-[var(--color-success)]" /> : <Copy size={11} />}
                {copied ? 'Copied' : 'Copy'}
              </Button>
              <Button variant="outline" size="sm" onClick={() => setRotateConfirmOpen(true)} disabled={isRotating} aria-label="Rotate gateway token">
                <ArrowsClockwise size={11} className={isRotating ? 'animate-spin motion-reduce:animate-none' : ''} />
                Rotate…
              </Button>
            </div>
          </div>
          {config.gateway.token && (
            <p className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)] truncate">
              {'••••••••••••'}{config.gateway.token.slice(-4)}
            </p>
          )}
        </Card>
      </section>

      <section aria-label="Other" className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Other</h3>
        <Card className="px-[var(--space-3)]">
          <SettingRow state={state} field={previewField} />
          <SettingRow state={state} field={logField} />
        </Card>
      </section>

      <SaveBarPanel {...state.bar} />

      <section aria-label="Restart" className="space-y-[var(--space-2)]">
        <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Restart</h3>
        <Card className="p-[var(--space-3)] flex flex-wrap items-center justify-between gap-[var(--space-2)]" {...settingsRow('Restart gateway')}>
          <div className="min-w-0">
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Restart gateway</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Applies saved changes that need a restart. Running agents pause for a few seconds.
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={() => setRestartModalOpen(true)}>
            Restart gateway…
          </Button>
        </Card>
      </section>

      {/* Remote Access */}
      <section {...settingsRow('Remote access')}>
        <Button
          variant="ghost"
          className="h-auto w-full justify-start gap-[var(--space-2)] rounded-none p-0 py-[var(--space-1)] text-left font-[var(--font-weight-regular)] hover:bg-transparent"
          onClick={() => setRemoteAccessOpen((v) => !v)}
          type="button"
          aria-expanded={remoteAccessOpen}
          aria-controls="remote-access-panel"
        >
          {remoteAccessOpen ? (
            <CaretDown size={12} className="text-[var(--color-muted)]" />
          ) : (
            <CaretRight size={12} className="text-[var(--color-muted)]" />
          )}
          <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-muted)] uppercase tracking-wider">Remote Access</h3>
        </Button>

        {remoteAccessOpen && (
          <div id="remote-access-panel" className="mt-[var(--space-2-5)] space-y-[var(--space-3)]">
            {/* Tailscale */}
            <Card className="p-[var(--space-3)] space-y-[var(--space-2)]">
              <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Tailscale</p>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                Install Tailscale on this machine and your client device. Once connected, access Omnipus via your Tailscale IP:
              </p>
              <code className="block text-[length:var(--type-utility-xs-size)] font-mono bg-[var(--color-primary)] border border-[var(--color-border)] rounded px-[var(--space-2-5)] py-[var(--space-2)] text-[var(--color-accent)]">
                http://&lt;tailscale-ip&gt;:{runningPort}
              </code>
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                Ensure the gateway bind address is set to <span className="font-mono">0.0.0.0</span> or your Tailscale IP to accept remote connections.
              </p>
            </Card>

            {/* SSH tunnel */}
            <Card className="p-[var(--space-3)] space-y-[var(--space-2)]">
              <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">SSH Tunnel</p>
              <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                Forward the gateway port to your local machine over SSH:
              </p>
              <code className="block text-[length:var(--type-utility-xs-size)] font-mono bg-[var(--color-primary)] border border-[var(--color-border)] rounded px-[var(--space-2-5)] py-[var(--space-2)] text-[var(--color-accent)] break-all">
                ssh -L {runningPort}:localhost:{runningPort} user@your-server
              </code>
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                Then open <span className="font-mono">http://localhost:{runningPort}</span> in your browser.
              </p>
            </Card>
          </div>
        )}
      </section>

      <ConfirmDialog
        open={rotateConfirmOpen}
        onOpenChange={setRotateConfirmOpen}
        title="Rotate the gateway token?"
        description="Every app and program that uses the token must sign in again with the new one."
        confirmLabel="Rotate token"
        onConfirm={() => {
          setRotateConfirmOpen(false)
          doRotate()
        }}
      />
      <ConfirmDialog
        open={bindConfirmOpen}
        onOpenChange={(open) => {
          if (!open) settleBind(false)
        }}
        title="Listen on all interfaces?"
        description="Anyone on your network can then reach the sign-in page of this server. Keep a strong password and the gateway token secret."
        confirmLabel="Listen on all interfaces"
        cancelLabel="Keep localhost only"
        emphasis="cancel"
        onConfirm={() => settleBind(true)}
      />
      <GatewayRestartModal open={restartModalOpen} onClose={() => setRestartModalOpen(false)} />
    </div>
  )
}
