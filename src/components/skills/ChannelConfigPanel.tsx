import { useState, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Eye,
  EyeSlash,
  FloppyDisk,
  Play,
  Lightning,
  Warning,
  CheckCircle,
  Clock,
} from '@phosphor-icons/react'
import { QRCodeSVG } from 'qrcode.react'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  fetchChannelConfig,
  configureChannel,
  enableChannel,
  testChannel,
  getChannelRouting,
  setChannelRouting,
  fetchAgents,
  isApiError,
} from '@/lib/api'
import { getChannelFields, type ChannelField } from '@/lib/channel-fields'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useWhatsAppPairingStore, type WhatsAppPairingState } from '@/store/whatsappPairing'

interface ChannelConfigPanelProps {
  channelId: string
  channelName: string
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * Capability flag from the channel's ChannelEntry (#299): whether this server
   * build can run native WhatsApp (whatsmeow). Only `false` gates the WhatsApp
   * linked-device QR pairing UI; `undefined`/`true` are treated as available so
   * the QR renders by default ("the UI must only offer what the binary can do").
   */
  nativeAvailable?: boolean
}

function PasswordField({
  field,
  value,
  onChange,
}: {
  field: ChannelField
  value: string
  onChange: (val: string) => void
}) {
  const [visible, setVisible] = useState(false)
  return (
    <div className="relative">
      <Input
        id={`field-${field.key}`}
        type={visible ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={field.placeholder}
        className="pr-9 font-mono text-xs"
        autoComplete="off"
      />
      <button
        type="button"
        onClick={() => setVisible((v) => !v)}
        className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
        aria-label={visible ? 'Hide' : 'Show'}
      >
        {visible ? <EyeSlash size={13} /> : <Eye size={13} />}
      </button>
    </div>
  )
}

// WhatsAppPairingBody renders the inner QR/status block for the linked-device
// notice (#283). Extracted from WhatsAppNativeNotice to keep the status ladder a
// flat switch rather than a nested ternary.
function WhatsAppPairingBody({ pairing }: { pairing?: WhatsAppPairingState }) {
  // The QR special-case wins over status alone: only render it when we actually
  // have a code to show.
  if (pairing?.status === 'code' && pairing.qr) {
    return (
      <>
        {/* QR must sit on a light background to scan reliably in dark mode. */}
        <div data-testid="whatsapp-qr" className="rounded-md bg-white p-3">
          <QRCodeSVG value={pairing.qr} size={184} level="L" />
        </div>
        <p className="text-xs text-[var(--color-secondary)] text-center">
          Scan with{' '}
          <span className="font-medium">WhatsApp → Linked Devices → Link a Device</span>. The code
          refreshes automatically.
        </p>
      </>
    )
  }

  switch (pairing?.status) {
    case 'linked':
      return (
        <div className="flex items-center gap-2 text-[var(--color-success)]">
          <CheckCircle size={16} weight="fill" />
          <p className="text-xs font-medium">Linked successfully.</p>
        </div>
      )
    case 'timeout':
      return (
        <div className="flex items-center gap-2 text-[var(--color-muted)]">
          <Clock size={14} />
          <p className="text-xs">The QR code expired — a fresh one will appear shortly…</p>
        </div>
      )
    case 'error':
      return (
        <div className="flex items-center gap-2 text-[var(--color-error)]">
          <Warning size={14} weight="fill" />
          <p className="text-xs">Pairing error: {pairing?.message || 'unknown'}.</p>
        </div>
      )
    default:
      return (
        <div className="flex items-center gap-2 text-[var(--color-muted)]">
          <Clock size={14} />
          <p className="text-xs text-center">
            Enable and save, then a QR code will appear here to scan with WhatsApp.
          </p>
        </div>
      )
  }
}

// WhatsAppNativeNotice renders the live linked-device pairing QR + status in the
// browser (#283), fed by the whatsapp_pairing WS frame. The native channel emits
// under channel_id "whatsapp_native". Replaces the old "check the gateway
// terminal" text — no terminal access required.
function WhatsAppNativeNotice() {
  const pairing = useWhatsAppPairingStore((s) => s.byChannel['whatsapp_native'])
  const clear = useWhatsAppPairingStore((s) => s.clear)
  const isConnected = useConnectionStore((s) => s.isConnected)

  // #283 (Option B): tell the gateway this connection is viewing WhatsApp
  // pairing so the QR is delivered only here, not broadcast to every admin tab.
  // Re-subscribe whenever the socket (re)connects while the panel is open —
  // per-connection interest is lost across a reconnect.
  useEffect(() => {
    if (!isConnected) return
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: 'whatsapp_native',
      active: true,
    })
  }, [isConnected])

  // On unmount (panel closed): unsubscribe and drop the QR/pairing secret from
  // the store so it doesn't linger in memory past the pairing flow (#283).
  useEffect(
    () => () => {
      useConnectionStore.getState().connection?.send({
        type: 'whatsapp_pairing_subscribe',
        channel_id: 'whatsapp_native',
        active: false,
      })
      clear('whatsapp_native')
    },
    [clear],
  )

  return (
    <div className="space-y-2 mt-1">
      <div className="flex flex-col items-center gap-3 p-4 rounded-lg bg-[var(--color-surface-1)] border border-[var(--color-border)]">
        <WhatsAppPairingBody pairing={pairing} />
      </div>
      <div className="flex gap-2 p-3 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-error)]/30">
        <Warning size={14} className="text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" />
        <p className="text-xs text-[var(--color-muted)]">
          WhatsApp native mode stores sessions locally. The gateway must keep running for the session
          to stay active.
        </p>
      </div>
    </div>
  )
}

export function ChannelConfigPanel({
  channelId,
  channelName,
  open,
  onOpenChange,
  nativeAvailable,
}: ChannelConfigPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const fields = getChannelFields(channelId)

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [formValues, setFormValues] = useState<Record<string, unknown>>({})
  const [testResult, setTestResult] = useState<{ success: boolean; message: string } | null>(null)

  const { data: currentConfig, isLoading } = useQuery({
    queryKey: ['channel-config', channelId],
    queryFn: () => fetchChannelConfig(channelId),
    enabled: open,
  })

  const isWebchat = channelId === 'webchat'

  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open && !isWebchat,
  })

  const { data: routing, isError: routingError } = useQuery({
    queryKey: ['channel-routing', channelId],
    queryFn: () => getChannelRouting(channelId),
    enabled: open && !isWebchat,
  })

  const [selectedAgentId, setSelectedAgentId] = useState<string>('__none__')

  useEffect(() => {
    if (routing === undefined) return
    setSelectedAgentId(routing.default_agent_id ?? '__none__')
  }, [routing])

  const { mutate: doSaveRouting } = useMutation({
    mutationFn: (agentId: string | undefined) =>
      setChannelRouting(channelId, { default_agent_id: agentId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['channel-routing', channelId] })
      addToast({ message: 'Routing saved', variant: 'success' })
    },
    onError: (err: unknown) => {
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Routing save failed', variant: 'error' })
      // Revert the optimistic select by resyncing from server
      queryClient.invalidateQueries({ queryKey: ['channel-routing', channelId] })
    },
  })

  // Populate form when config loads — skip if user has unsaved edits
  useEffect(() => {
    if (!currentConfig) return
    if (isDirtyRef.current) return
    setFormValues(currentConfig)
  }, [currentConfig])

  const { mutate: doSave, isPending: saving } = useMutation({
    mutationFn: () => configureChannel(channelId, formValues),
    onSuccess: () => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      queryClient.invalidateQueries({ queryKey: ['channel-config', channelId] })
      addToast({ message: 'Configuration saved', variant: 'success' })
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed', variant: 'error' }),
  })

  const { mutate: doSaveAndEnable, isPending: savingAndEnabling } = useMutation({
    mutationFn: async () => {
      await configureChannel(channelId, formValues)
      await enableChannel(channelId)
    },
    onSuccess: () => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      queryClient.invalidateQueries({ queryKey: ['channel-config', channelId] })
      addToast({ message: 'Channel configured and enabled', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to enable channel', variant: 'error' }),
  })

  const { mutate: doTest, isPending: testing } = useMutation({
    mutationFn: () => testChannel(channelId),
    onSuccess: (result) => {
      setTestResult(result)
      if (result.success) {
        addToast({ message: 'Connection test passed', variant: 'success' })
      } else {
        addToast({ message: result.message || 'Test failed', variant: 'error' })
      }
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Test failed'
      setTestResult({ success: false, message: msg })
      addToast({ message: msg, variant: 'error' })
    },
  })

  function setValue(key: string, value: unknown) {
    markDirty()
    // Support nested keys like "group_trigger.mention_only"
    if (key.includes('.')) {
      const [parent, child] = key.split('.')
      setFormValues((prev) => ({
        ...prev,
        [parent]: {
          ...((prev[parent] as Record<string, unknown>) ?? {}),
          [child]: value,
        },
      }))
    } else {
      setFormValues((prev) => ({ ...prev, [key]: value }))
    }
  }

  function getValue(key: string): unknown {
    if (key.includes('.')) {
      const [parent, child] = key.split('.')
      const parentObj = formValues[parent] as Record<string, unknown> | undefined
      return parentObj?.[child]
    }
    return formValues[key]
  }

  // WhatsApp is always native (whatsmeow) — the legacy bridge + the `use_native`
  // toggle were removed, so there is no user-facing native toggle. The
  // linked-device QR pairing UI is still capability-gated (#299): on a lite/stub
  // build the backend reports native_available:false on the whatsapp ChannelEntry,
  // and we must NOT show a QR that can never pair. Only `false` gates;
  // `undefined`/`true` default to available.
  const isWhatsApp = channelId === 'whatsapp'
  const whatsAppNativeUnavailable = isWhatsApp && nativeAvailable === false

  const isBusy = saving || savingAndEnabling

  if (fields.length === 0) return null

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            Configure {channelName}
          </SheetTitle>
        </SheetHeader>

        {isLoading ? (
          <div className="space-y-4 pt-6">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-10 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
            ))}
          </div>
        ) : (
          <div className="pt-5 space-y-5">
            {fields.map((field) => (
              <div key={field.key} className="space-y-1.5">
                <Label
                  htmlFor={`field-${field.key}`}
                  className="text-xs font-medium text-[var(--color-secondary)]"
                >
                  {field.label}
                  {field.required && (
                    <span className="text-[var(--color-error)] ml-0.5">*</span>
                  )}
                </Label>

                {field.type === 'toggle' ? (
                  <div className="flex items-center gap-2 py-1">
                    <Switch
                      id={`field-${field.key}`}
                      checked={Boolean(getValue(field.key))}
                      onCheckedChange={(checked) => setValue(field.key, checked)}
                    />
                  </div>
                ) : field.type === 'password' ? (
                  <PasswordField
                    field={field}
                    value={String(getValue(field.key) ?? '')}
                    onChange={(val) => setValue(field.key, val)}
                  />
                ) : field.type === 'textarea' ? (
                  <Textarea
                    id={`field-${field.key}`}
                    value={String(getValue(field.key) ?? '')}
                    onChange={(e) => setValue(field.key, e.target.value)}
                    placeholder={field.placeholder}
                    className="font-mono text-xs resize-none h-20"
                  />
                ) : (
                  <Input
                    id={`field-${field.key}`}
                    type={field.type === 'number' ? 'number' : 'text'}
                    value={String(getValue(field.key) ?? '')}
                    onChange={(e) =>
                      setValue(
                        field.key,
                        field.type === 'number'
                          ? e.target.value === '' ? '' : Number(e.target.value)
                          : e.target.value,
                      )
                    }
                    placeholder={field.placeholder}
                    className="text-xs"
                  />
                )}

                {field.helpText && (
                  <p className="text-[10px] text-[var(--color-muted)] leading-relaxed">
                    {field.helpText}
                  </p>
                )}
              </div>
            ))}

            {/* WhatsApp is always native (whatsmeow): show the live linked-device
                QR pairing UI (#283) — but only when the server build can run
                native WhatsApp. On a lite/stub build (native_available:false) show
                a hint instead of a QR that can never pair (#299). */}
            {isWhatsApp &&
              (whatsAppNativeUnavailable ? (
                <p
                  data-testid="native-unavailable-hint"
                  className="text-xs text-[var(--color-muted)] leading-relaxed mt-1 p-3 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-border)]"
                >
                  WhatsApp requires the native build (whatsmeow); this server build
                  doesn&apos;t include it, so linked-device pairing is unavailable.
                </p>
              ) : (
                <WhatsAppNativeNotice />
              ))}

            {/* Routing — hidden for webchat (no agent-routing concept) */}
            {!isWebchat && (
              <div className="pt-2 border-t border-[var(--color-border)] space-y-3">
                <h3 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
                  Routing
                </h3>
                {routingError ? (
                  <p className="text-xs text-[var(--color-error)]">
                    Couldn&apos;t load routing — save may overwrite current setting.
                  </p>
                ) : (
                  <div className="space-y-1.5">
                    <Label className="text-xs font-medium text-[var(--color-secondary)]">
                      Default agent
                    </Label>
                    {agentsError ? (
                      <p className="text-xs text-[var(--color-error)]">
                        Couldn&apos;t load agent list.
                      </p>
                    ) : (
                      <SmartSelect
                        value={selectedAgentId}
                        onValueChange={(v) => {
                          const next = v === '__none__' ? undefined : v
                          setSelectedAgentId(v)
                          doSaveRouting(next)
                        }}
                        placeholder="(Global default)"
                        items={[
                          { value: '__none__', label: '(Global default)' },
                          ...agents.map((a) => ({ value: a.id, label: a.name })),
                        ]}
                      />
                    )}
                    <p className="text-[10px] text-[var(--color-muted)] leading-relaxed">
                      Which agent handles inbound messages on this channel. &quot;(Global default)&quot; falls back to the globally-configured default agent.
                    </p>
                  </div>
                )}
              </div>
            )}

            {/* Test result */}
            {testResult && (
              <div
                className={`flex gap-2 p-3 rounded-md border text-xs ${
                  testResult.success
                    ? 'bg-[var(--color-success)]/10 border-[var(--color-success)]/30 text-[var(--color-success)]'
                    : 'bg-[var(--color-error)]/10 border-[var(--color-error)]/30 text-[var(--color-error)]'
                }`}
              >
                {testResult.success ? (
                  <Lightning size={13} weight="fill" className="shrink-0 mt-0.5" />
                ) : (
                  <Warning size={13} weight="fill" className="shrink-0 mt-0.5" />
                )}
                {testResult.message}
              </div>
            )}

            {/* Actions */}
            <div className="flex flex-col gap-2 pt-2 border-t border-[var(--color-border)]">
              <Button
                className="w-full gap-1.5"
                onClick={() => doSaveAndEnable()}
                disabled={isBusy}
              >
                <Lightning size={13} weight="fill" />
                Save & Enable
              </Button>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  className="flex-1 gap-1.5"
                  onClick={() => doSave()}
                  disabled={isBusy}
                >
                  <FloppyDisk size={13} />
                  Save
                </Button>
                <Button
                  variant="outline"
                  className="flex-1 gap-1.5"
                  onClick={() => {
                    setTestResult(null)
                    doTest()
                  }}
                  disabled={testing || saving || savingAndEnabling}
                >
                  <Play size={13} weight="fill" />
                  {testing ? 'Testing…' : 'Test'}
                </Button>
              </div>
            </div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
