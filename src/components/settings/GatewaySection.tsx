/**
 * GatewaySection — Settings → Gateway tab.
 *
 * FR-107: auth_mode UI option removed (token auth is the only mode; backend
 *   capability preserved — do NOT re-add the 'none' control).
 * FR-107b: when backend reports auth_mode=none OR dev_mode_bypass=true, a
 *   loud persistent banner warns the operator.
 * US-B2 / #328:
 * - bind_address 0.0.0.0 wrapped in RiskySettingControl (safe = '127.0.0.1').
 * - Standing badge derives from persisted config values (fetchConfig → ['config'] query).
 * - Badge clears on save of the safe value (queryClient.invalidateQueries clears it).
 * Hot-reload is always on (FR-106); the toggle is removed.
 */

import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, ArrowsClockwise, CheckCircle, CaretDown, CaretRight, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SmartSelect } from '@/components/ui/smart-select'
import { Badge } from '@/components/ui/badge'
import { Separator } from '@/components/ui/separator'
import { fetchConfig, updateConfig, rotateGatewayToken, fetchGatewayStatus, fetchAgents, isApiError, type Config } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { RiskySettingControl } from '@/components/shared/RiskySettingControl'

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
  const [defaultAgentId, setDefaultAgentId] = useState('')

  const { data: agents } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  useEffect(() => {
    if (!config) return
    if (isDirtyRef.current) return
    setBindAddress(config.gateway.bind_address)
    setPort(config.gateway.port.toString())
    setLogLevel(config.gateway.log_level ?? 'info')
    setDefaultAgentId(config.agents?.defaults?.default_agent_id ?? '')
  }, [config])

  const gatewayFormData = useMemo(() => ({
    bind_address: bindAddress,
    port,
    log_level: logLevel,
    default_agent_id: defaultAgentId,
  }), [bindAddress, port, logLevel, defaultAgentId])

  const { status: saveStatus, error: saveError } = useAutoSave(
    gatewayFormData,
    async () => {
      // FR-107: auth_mode is intentionally not sent — the UI no longer controls it.
      // FR-106: hot_reload is intentionally not sent — always-on backend-side.
      // We cast to satisfy the Partial<Config> signature; frontendToRawConfig
      // guards each field with `!== undefined` so absent fields are skipped.
      await updateConfig({
        gateway: {
          bind_address: bindAddress,
          port: parseInt(port, 10),
          log_level: logLevel,
        } as unknown as Config['gateway'],
        agents: {
          defaults: {
            default_agent_id: defaultAgentId || undefined,
          },
        },
      })
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['config'] })
    },
    { disabled: !config },
  )

  const { mutate: doRotate, isPending: isRotating } = useMutation({
    mutationFn: rotateGatewayToken,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['config'] })
      addToast({ message: 'Gateway token rotated', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Token rotation failed', variant: 'error' }),
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

        {/* Default Agent */}
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Default Agent</p>
            <p className="text-xs text-[var(--color-muted)]">The agent that handles messages when no specific routing applies</p>
          </div>
          <SmartSelect
            value={defaultAgentId || '__none__'}
            onValueChange={(v) => { markDirty(); setDefaultAgentId(v === '__none__' ? '' : v) }}
            triggerClassName="w-[180px] h-8 text-xs"
            placeholder="(none set)"
            items={[
              { value: '__none__', label: '(none set)' },
              ...(agents ?? []).map((a) => ({ value: a.id, label: a.name })),
            ]}
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

      {/* Status */}
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
        <span>Listening on {bindAddress}:{port}</span>
      </div>

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
    </div>
  )
}
