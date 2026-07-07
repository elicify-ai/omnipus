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
 */

import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, ArrowsClockwise, CheckCircle, CaretDown, CaretRight, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SmartSelect } from '@/components/ui/smart-select'
import { Badge } from '@/components/ui/badge'
import { Separator } from '@/components/ui/separator'
import { fetchConfig, updateConfig, rotateGatewayToken, fetchGatewayStatus, getErrorMessage, type Config } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { RiskySettingControl } from '@/components/shared/RiskySettingControl'
import { usePendingRestart, PENDING_RESTART_QUERY_KEY } from '@/store/restart'
import { GatewayRestartModal } from './GatewayRestartModal'
import { GodModeControl, GodModeActiveBanner } from './GodModeControl'

// ── Risky-control copy bundles (US-B2) ────────────────────────────────────────

const BIND_ADDRESS_COPY = {
  dialogTitle: 'Listen on all network interfaces?',
  dialogDescription:
    'Setting bind address to 0.0.0.0 makes the gateway reachable from any network interface, not just your local machine. If this server is on a public network, other devices may be able to reach your Omnipus — make sure login is required and your firewall is configured.',
  confirmLabel: 'Listen on all interfaces anyway',
  cancelLabel: 'Keep localhost only (safer)',
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
      className="flex items-start gap-3 rounded-lg border border-[var(--color-error)]/60 bg-[var(--color-error)]/10 px-4 py-3"
    >
      <Warning size={18} weight="fill" className="shrink-0 mt-0.5 text-[var(--color-error)]" />
      <div className="space-y-1">
        <p className="text-sm font-semibold text-[var(--color-error)]">
          Unauthenticated access is enabled
        </p>
        <p className="text-xs text-[var(--color-error)]/80">
          Anyone who can reach this server has admin access (dev_mode_bypass is enabled in
          config.json). To fix this, set
          <code className="mx-1 font-mono text-[10px] bg-[var(--color-error)]/10 px-1 rounded">
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
  // O4: restart modal state — shown ONCE when a save creates new pending changes.
  // Subsequent visits to this tab while the same changes are pending show the
  // persistent badge/banner in the section below instead of re-popping the modal.
  const [restartModalOpen, setRestartModalOpen] = useState(false)
  // Track the last save count that triggered the modal so we only show it once
  // per save action, not on every tab revisit while the change is pending.
  const saveModalShownRef = useRef(0)
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

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [bindAddress, setBindAddress] = useState('127.0.0.1')
  const [port, setPort] = useState('8080')
  const [logLevel, setLogLevel] = useState('info')

  useEffect(() => {
    if (!config) return
    if (isDirtyRef.current) return
    const newBind = config.gateway.bind_address
    const newPort = config.gateway.port.toString()
    setBindAddress(newBind)
    setPort(newPort)
    setLogLevel(config.gateway.log_level ?? 'info')
    // Sync prev refs to the loaded config values so the first autosave
    // comparison starts from the real persisted value, not the useState default.
    // This prevents the modal from re-opening on tab revisits while a restart
    // is pending (the initial useEffect would otherwise look like a port change).
    if (prevPortRef.current === null) prevPortRef.current = newPort
    if (prevBindRef.current === null) prevBindRef.current = newBind
  }, [config])

  const gatewayFormData = useMemo(() => ({
    bind_address: bindAddress,
    port,
    log_level: logLevel,
  }), [bindAddress, port, logLevel])

  // O4: track which gateway fields are restart-gated so we can show the modal
  // after saving. port and bind_address require a restart; log_level does not.
  // These are initialised to null so that the first useEffect sync (which
  // populates state from the fetched config) does NOT trigger the changed-flag
  // and open the modal on every tab visit while a restart is pending.
  const prevPortRef = useRef<string | null>(null)
  const prevBindRef = useRef<string | null>(null)

  const { status: saveStatus, error: saveError } = useAutoSave(
    gatewayFormData,
    async () => {
      // FR-107: token auth is the only mode — there is no auth_mode field to send.
      // FR-106: hot_reload is intentionally not sent — always-on backend-side.
      // We cast to satisfy the Partial<Config> signature; frontendToRawConfig
      // guards each field with `!== undefined` so absent fields are skipped.
      // null means config hasn't loaded yet — treat as no change to avoid
      // spurious modal pops on the initial config→state sync.
      const portChanged = prevPortRef.current !== null && port !== prevPortRef.current
      const bindChanged = prevBindRef.current !== null && bindAddress !== prevBindRef.current

      await updateConfig({
        gateway: {
          bind_address: bindAddress,
          port: parseInt(port, 10),
          log_level: logLevel,
        } as unknown as Config['gateway'],
      })
      isDirtyRef.current = false
      prevPortRef.current = port
      prevBindRef.current = bindAddress
      queryClient.invalidateQueries({ queryKey: ['config'] })
      // O4: if a restart-gated field changed, refresh pending list and show modal
      // ONCE for this save event. The saveModalShownRef counter ensures the modal
      // only pops on the save that created the pending change, not on subsequent
      // tab visits while the change is still pending (the persistent badge below
      // serves that role).
      if (portChanged || bindChanged) {
        await queryClient.invalidateQueries({ queryKey: [...PENDING_RESTART_QUERY_KEY] })
        saveModalShownRef.current++
        setRestartModalOpen(true)
      }
    },
    { disabled: !config },
  )

  // O4: always-available "Restart gateway" handler.
  const handleManualRestart = useCallback(() => {
    setRestartModalOpen(true)
  }, [])

  const { mutate: doRotate, isPending: isRotating } = useMutation({
    mutationFn: rotateGatewayToken,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['config'] })
      addToast({ message: 'Gateway token rotated', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: getErrorMessage(err, 'Token rotation failed'), variant: 'error' }),
  })

  const copyToken = async () => {
    const token = config?.gateway.token
    if (!token) return
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      addToast({ message: 'Could not copy token to clipboard', variant: 'error' })
    }
  }

  if (isLoading) return <div className="text-sm text-[var(--color-muted)]">Loading...</div>

  if (isConfigError) {
    return (
      <div className="rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-surface-1)] p-4 space-y-3">
        <p className="text-sm text-[var(--color-error)]">Failed to load gateway configuration.</p>
        <p className="text-xs text-[var(--color-muted)]">
          Save is disabled until the configuration is loaded successfully to prevent overwriting real settings with defaults.
        </p>
        <Button size="sm" variant="outline" onClick={() => refetchConfig()}>
          Retry
        </Button>
      </div>
    )
  }

  // US-B2: badges derive from the PERSISTED config values (not local draft state).
  const persistedBindAddress = config?.gateway.bind_address ?? '127.0.0.1'

  // FR-107b: the only genuinely-unauthenticated state is dev_mode_bypass=true.
  // (auth_mode is legacy/vestigial — token auth is enforced regardless of it.)
  const devModeBypass = config?.gateway.dev_mode_bypass === true

  return (
    <div className="space-y-6">
      {/* O14: persistent god-mode-active banner (shown whenever god-mode is on). */}
      <GodModeActiveBanner />

      {/* FR-107b: persistent unauthenticated-access banner (dev_mode_bypass only) */}
      <UnauthenticatedBanner devModeBypass={devModeBypass} />

      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Gateway</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Configure how the gateway listens. Restart required for port changes.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        {/* Bind address — risky when 0.0.0.0 (US-B2) */}
        <div className="space-y-2">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Bind address</p>
            <p className="text-xs text-[var(--color-muted)]">Which network interfaces the gateway listens on</p>
          </div>
          <RiskySettingControl
            options={[
              { value: '127.0.0.1', label: 'Localhost only (127.0.0.1)' },
              { value: '0.0.0.0', label: 'All interfaces (0.0.0.0)' },
            ]}
            currentValue={persistedBindAddress}
            selectedValue={bindAddress}
            safeValue="127.0.0.1"
            copy={BIND_ADDRESS_COPY}
            onConfirm={(v) => { markDirty(); setBindAddress(v) }}
            onSelectSafe={(v) => { markDirty(); setBindAddress(v) }}
          />
        </div>

        {/* Port */}
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Port</p>
          </div>
          <Input
            type="number"
            min="1024"
            max="65535"
            value={port}
            onChange={(e) => { markDirty(); setPort(e.target.value) }}
            className="w-24 h-8 text-xs font-mono"
          />
        </div>

        {/* Token management */}
        <div className="pt-2 border-t border-[var(--color-border)] space-y-2">
          <div className="flex items-center justify-between">
            <p className="text-sm text-[var(--color-secondary)]">Gateway token</p>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                className="h-7 px-2 gap-1 text-xs"
                onClick={copyToken}
                disabled={!config?.gateway.token}
              >
                {copied ? <CheckCircle size={11} className="text-[var(--color-success)]" /> : <Copy size={11} />}
                {copied ? 'Copied' : 'Copy'}
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-7 px-2 gap-1 text-xs"
                onClick={() => doRotate()}
                disabled={isRotating}
              >
                <ArrowsClockwise size={11} className={isRotating ? 'animate-spin' : ''} />
                Rotate
              </Button>
            </div>
          </div>
          {config?.gateway.token && (
            <p className="font-mono text-[10px] text-[var(--color-muted)] truncate">
              {config.gateway.token.slice(0, 20)}...
            </p>
          )}
        </div>

        {/* Log level */}
        <div className="flex items-center justify-between border-t border-[var(--color-border)] pt-2">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Log level</p>
            <p className="text-xs text-[var(--color-muted)]">Verbosity of gateway logs</p>
          </div>
          <SmartSelect
            value={logLevel}
            onValueChange={(v) => { markDirty(); setLogLevel(v) }}
            triggerClassName="w-[120px] h-8 text-xs"
            items={[
              { value: 'debug', label: 'Debug' },
              { value: 'info', label: 'Info' },
              { value: 'warn', label: 'Warn' },
              { value: 'error', label: 'Error' },
            ]}
          />
        </div>
      </div>

      {/* O4 honest status — show the *running* value (applied_value from pending
          entries) vs the *saved* value separately when a restart is pending. */}
      <div className="space-y-2">
        <div className="flex items-center gap-2 text-xs text-[var(--color-muted)]">
          {isOnline ? (
            <Badge variant="success" className="gap-1 text-[10px]">
              <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-success)]" /> Online
            </Badge>
          ) : (
            <Badge variant="error" className="gap-1 text-[10px]">
              <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-error)]" /> Offline
            </Badge>
          )}
          {(() => {
            // Find pending entries for gateway.port and gateway.bind_address.
            const portEntry = pendingEntries.find((e) => e.key === 'gateway.port')
            const bindEntry = pendingEntries.find((e) => e.key === 'gateway.bind_address')
            const runningPort = portEntry ? String(portEntry.applied_value) : port
            const runningBind = bindEntry ? String(bindEntry.applied_value) : bindAddress
            const savedPort = portEntry ? String(portEntry.persisted_value) : port
            const savedBind = bindEntry ? String(bindEntry.persisted_value) : bindAddress
            const hasPending = Boolean(portEntry || bindEntry)
            if (hasPending) {
              return (
                <span>
                  Running on{' '}
                  <span className="font-mono">{runningBind}:{runningPort}</span>
                  {' '}—{' '}
                  <span style={{ color: 'var(--color-warning)' }}>
                    saved <span className="font-mono">{savedBind}:{savedPort}</span>, restart to apply
                  </span>
                </span>
              )
            }
            return <span>Listening on <span className="font-mono">{bindAddress}:{port}</span></span>
          })()}
        </div>

        {/* O4: persistent "Restart gateway" maintenance control — always available
            so operators can trigger a graceful restart at any time. Also shows a
            notice + changed keys when a restart is pending. */}
        <div
          className={`rounded-lg border px-4 py-3 space-y-2 ${
            pendingEntries.length > 0 ? '' : 'border-[var(--color-border)] bg-[var(--color-surface-1)]'
          }`}
          style={
            pendingEntries.length > 0
              ? {
                  borderColor: 'color-mix(in srgb, var(--color-warning) 40%, transparent)',
                  backgroundColor: 'color-mix(in srgb, var(--color-warning) 10%, transparent)',
                }
              : undefined
          }
        >
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              {pendingEntries.length > 0 ? (
                <>
                  <p className="text-sm font-semibold" style={{ color: 'var(--color-warning)' }}>
                    {pendingEntries.length} change{pendingEntries.length !== 1 ? 's' : ''} pending — restart to apply
                  </p>
                  <div className="mt-1 space-y-0.5">
                    {pendingEntries.map((e) => (
                      <p
                        key={e.key}
                        className="text-[11px] font-mono"
                        style={{ color: 'color-mix(in srgb, var(--color-warning) 70%, transparent)' }}
                      >
                        {e.key}: {String(e.applied_value)} → {String(e.persisted_value)}
                      </p>
                    ))}
                  </div>
                </>
              ) : (
                <p className="text-sm text-[var(--color-secondary)]">Restart gateway</p>
              )}
              <p className="text-xs text-[var(--color-muted)] mt-0.5">
                {pendingEntries.length > 0
                  ? 'A graceful restart will drain in-flight requests before applying saved settings.'
                  : 'Trigger a graceful restart for maintenance or to apply saved settings.'}
              </p>
            </div>
            <Button
              variant={pendingEntries.length > 0 ? 'default' : 'outline'}
              size="sm"
              onClick={handleManualRestart}
              className="shrink-0"
            >
              <ArrowsClockwise size={13} className="mr-1.5" />
              Restart
            </Button>
          </div>
        </div>
      </div>

      <Separator />

      {/* O14: God-mode switch (danger zone) — step-up-gated global override. */}
      <GodModeControl />

      <Separator />

      {/* Remote Access */}
      <section>
        <button
          className="flex items-center gap-2 w-full text-left py-1"
          onClick={() => setRemoteAccessOpen((v) => !v)}
          type="button"
        >
          {remoteAccessOpen ? (
            <CaretDown size={12} className="text-[var(--color-muted)]" />
          ) : (
            <CaretRight size={12} className="text-[var(--color-muted)]" />
          )}
          <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Remote Access</h3>
        </button>

        {remoteAccessOpen && (
          <div className="mt-3 space-y-4">
            {/* Tailscale */}
            <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-2">
              <p className="text-sm font-semibold text-[var(--color-secondary)]">Tailscale</p>
              <p className="text-xs text-[var(--color-muted)]">
                Install Tailscale on this machine and your client device. Once connected, access Omnipus via your Tailscale IP:
              </p>
              <code className="block text-xs font-mono bg-[var(--color-primary)] border border-[var(--color-border)] rounded px-3 py-2 text-[var(--color-accent)]">
                http://&lt;tailscale-ip&gt;:{port}
              </code>
              <p className="text-[10px] text-[var(--color-muted)]">
                Ensure the gateway bind address is set to <span className="font-mono">0.0.0.0</span> or your Tailscale IP to accept remote connections.
              </p>
            </div>

            {/* SSH tunnel */}
            <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-2">
              <p className="text-sm font-semibold text-[var(--color-secondary)]">SSH Tunnel</p>
              <p className="text-xs text-[var(--color-muted)]">
                Forward the gateway port to your local machine over SSH:
              </p>
              <code className="block text-xs font-mono bg-[var(--color-primary)] border border-[var(--color-border)] rounded px-3 py-2 text-[var(--color-accent)] break-all">
                ssh -L {port}:localhost:{port} user@your-server
              </code>
              <p className="text-[10px] text-[var(--color-muted)]">
                Then open <span className="font-mono">http://localhost:{port}</span> in your browser.
              </p>
            </div>
          </div>
        )}
      </section>

      {/* O4: restart modal — shows when a restart-gated setting is saved or
          when the operator clicks the persistent "Restart" button. */}
      <GatewayRestartModal
        open={restartModalOpen}
        onClose={() => setRestartModalOpen(false)}
      />
    </div>
  )
}
