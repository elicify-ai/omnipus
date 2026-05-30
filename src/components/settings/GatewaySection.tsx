import { useState, useEffect, useRef, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Copy, ArrowsClockwise, CheckCircle, CaretDown, CaretRight } from '@phosphor-icons/react'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SmartSelect } from '@/components/ui/smart-select'
import { Badge } from '@/components/ui/badge'
import { Separator } from '@/components/ui/separator'
import { fetchConfig, updateConfig, rotateGatewayToken, fetchGatewayStatus, fetchAgents, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAutoSave } from '@/hooks/useAutoSave'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'

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
  const [authMode, setAuthMode] = useState<'none' | 'token'>('none')
  const [hotReload, setHotReload] = useState(false)
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
    setAuthMode(config.gateway.auth_mode)
    setHotReload(config.gateway.hot_reload ?? false)
    setLogLevel(config.gateway.log_level ?? 'info')
    setDefaultAgentId(config.agents?.defaults?.default_agent_id ?? '')
  }, [config])

  const gatewayFormData = useMemo(() => ({
    bind_address: bindAddress,
    port,
    auth_mode: authMode,
    hot_reload: hotReload,
    log_level: logLevel,
    default_agent_id: defaultAgentId,
  }), [bindAddress, port, authMode, hotReload, logLevel, defaultAgentId])

  const { status: saveStatus, error: saveError } = useAutoSave(
    gatewayFormData,
    async () => {
      await updateConfig({
        gateway: {
          bind_address: bindAddress,
          port: parseInt(port, 10),
          auth_mode: authMode,
          hot_reload: hotReload,
          log_level: logLevel,
        },
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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-headline font-bold text-base text-[var(--color-secondary)]">Gateway</h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            Configure how the gateway listens. Restart required for changes to take effect.
          </p>
        </div>
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        {/* Bind address */}
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Bind address</p>
            <p className="text-xs text-[var(--color-muted)]">Where the gateway listens</p>
          </div>
          <SmartSelect
            value={bindAddress}
            onValueChange={(v) => { markDirty(); setBindAddress(v) }}
            triggerClassName="w-[160px] h-8 text-xs font-mono"
            items={[
              { value: '127.0.0.1', label: '127.0.0.1 (localhost only)' },
              { value: '0.0.0.0', label: '0.0.0.0 (all interfaces)' },
            ]}
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

        {/* Auth mode */}
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Auth mode</p>
            <p className="text-xs text-[var(--color-muted)]">Require a bearer token for API access</p>
          </div>
          <SmartSelect
            value={authMode}
            onValueChange={(v) => { markDirty(); setAuthMode(v as 'none' | 'token') }}
            triggerClassName="w-[120px] h-8 text-xs"
            items={[
              { value: 'none', label: 'None' },
              { value: 'token', label: 'Bearer token' },
            ]}
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
        {authMode === 'token' && (
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
        )}

        {/* Hot reload */}
        <div className="flex items-center justify-between pt-2 border-t border-[var(--color-border)]">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">Hot reload</p>
            <p className="text-xs text-[var(--color-muted)]">Reload config without restarting the gateway</p>
          </div>
          <Switch
            checked={hotReload}
            onCheckedChange={(v) => { markDirty(); setHotReload(v) }}
          />
        </div>

        {/* Log level */}
        <div className="flex items-center justify-between">
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
