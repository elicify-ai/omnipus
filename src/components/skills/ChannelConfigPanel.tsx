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
  ArrowsClockwise,
  Spinner,
} from '@phosphor-icons/react'
import { QRCodeSVG } from 'qrcode.react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
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

// #326: Map raw snake_case API field keys to human-readable labels using the
// channel descriptor. E.g. "bot_token" → "Bot Token".
function mapErrorsToHumanLabels(
  fields: ChannelField[],
  raw: string,
): string {
  // Build a lookup from key → label for this channel.
  const labelMap: Record<string, string> = {}
  for (const f of fields) {
    // Support flat key and dotted key (e.g. "group_trigger.mention_only").
    labelMap[f.key] = f.label
    // Also map the leaf part of dotted keys (e.g. "mention_only" → label).
    if (f.key.includes('.')) {
      const leaf = f.key.split('.').pop()
      if (leaf) labelMap[leaf] = f.label
    }
  }

  // Replace every snake_case token that matches a known field key.
  return raw.replace(/\b([a-z][a-z0-9_]*)\b/g, (match) => {
    return labelMap[match] ?? match
  })
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
        aria-describedby={field.helpText ? `help-${field.key}` : undefined}
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

// HelperText renders the helpText and optional helpLink for a channel field.
// Rendered as a one-liner under the input.
// TODO #323: extract HelperLink + AdvancedDisclosure after Wave 0
function FieldHelper({ field }: { field: ChannelField }) {
  if (!field.helpText && !field.helpLink) return null
  return (
    <p id={`help-${field.key}`} className="text-[10px] text-[var(--color-muted)] leading-relaxed">
      {field.helpText}
      {field.helpText && field.helpLink && ' '}
      {field.helpLink && (
        <a
          href={field.helpLink.url}
          target="_blank"
          rel="noopener noreferrer"
          className="underline text-[var(--color-accent)] hover:text-[var(--color-accent)]/80 transition-colors"
        >
          {field.helpLink.label}
        </a>
      )}
    </p>
  )
}

// WhatsAppPairingBody renders the inner QR/status block for the linked-device
// notice (#283 / US-C3). Implements the full 5-state machine by real wire names:
// waiting | code | linked | timeout | error
function WhatsAppPairingBody({
  pairing,
  onRetry,
}: {
  pairing?: WhatsAppPairingState
  onRetry?: () => void
}) {
  // waiting: no frame yet, or explicit waiting state — show spinner
  if (!pairing || pairing.status === 'waiting') {
    return (
      <div className="flex items-center gap-2 text-[var(--color-muted)]">
        <Spinner size={14} className="animate-spin" />
        <p className="text-xs">Generating your QR code&hellip;</p>
      </div>
    )
  }

  // code: QR delivered — show scannable QR + exact Linked Devices steps
  if (pairing.status === 'code' && pairing.qr) {
    return (
      <>
        {/* QR must sit on a light background to scan reliably in dark mode. */}
        <div data-testid="whatsapp-qr" className="rounded-md bg-white p-3">
          <QRCodeSVG value={pairing.qr} size={184} level="L" />
        </div>
        <p className="text-xs text-[var(--color-secondary)] text-center">
          Open <span className="font-medium">WhatsApp</span> on your phone, go to{' '}
          <span className="font-medium">Settings &rarr; Linked Devices &rarr; Link a Device</span>,
          then scan this code. It refreshes every 20s.
        </p>
      </>
    )
  }

  switch (pairing.status) {
    case 'linked':
      return (
        <div className="flex items-center gap-2 text-[var(--color-success)]">
          <CheckCircle size={16} weight="fill" />
          <p className="text-xs font-medium">Linked successfully.</p>
        </div>
      )
    case 'timeout':
      return (
        <div className="flex flex-col items-center gap-3">
          <div className="flex items-center gap-2 text-[var(--color-muted)]">
            <Clock size={14} />
            <p className="text-xs">QR expired &mdash; tap to get a fresh one.</p>
          </div>
          {onRetry && (
            <button
              type="button"
              onClick={onRetry}
              data-testid="whatsapp-retry"
              className="flex items-center gap-1.5 text-xs text-[var(--color-accent)] hover:text-[var(--color-accent)]/80 transition-colors"
            >
              <ArrowsClockwise size={13} />
              Retry
            </button>
          )}
        </div>
      )
    case 'error':
      return (
        <div className="flex flex-col items-center gap-3">
          <div className="flex items-center gap-2 text-[var(--color-error)]">
            <Warning size={14} weight="fill" />
            <p className="text-xs">Pairing failed &mdash; tap to retry.</p>
          </div>
          {onRetry && (
            <button
              type="button"
              onClick={onRetry}
              data-testid="whatsapp-retry"
              className="flex items-center gap-1.5 text-xs text-[var(--color-accent)] hover:text-[var(--color-accent)]/80 transition-colors"
            >
              <ArrowsClockwise size={13} />
              Retry
            </button>
          )}
        </div>
      )
    default:
      // Fallback for any unexpected status — show spinner
      return (
        <div className="flex items-center gap-2 text-[var(--color-muted)]">
          <Spinner size={14} className="animate-spin" />
          <p className="text-xs">Generating your QR code&hellip;</p>
        </div>
      )
  }
}

// WhatsAppNativeNotice renders the live linked-device pairing QR + status in the
// browser (#283 / US-C3), fed by the whatsapp_pairing WS frame. The native channel
// emits under channel_id "whatsapp_native". Replaces the old "check the gateway
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

  function handleRetry() {
    // Clear the stale state and re-subscribe to trigger a fresh QR from the backend.
    clear('whatsapp_native')
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: 'whatsapp_native',
      active: false,
    })
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: 'whatsapp_native',
      active: true,
    })
  }

  return (
    <div className="space-y-2 mt-1">
      <div className="flex flex-col items-center gap-3 p-4 rounded-lg bg-[var(--color-surface-1)] border border-[var(--color-border)]">
        <WhatsAppPairingBody pairing={pairing} onRetry={handleRetry} />
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

// Google Chat auth method groups for the pick-one radio (#324 / US-C2).
type GChatAuthMethod = 'webhook' | 'service_account'

const GCHAT_AUTH_OPTIONS: Array<{ value: GChatAuthMethod; label: string; description: string }> = [
  {
    value: 'webhook',
    label: 'Webhook URL',
    description: 'Simplest setup — send messages to a Google Chat space via an incoming webhook.',
  },
  {
    value: 'service_account',
    label: 'Service account',
    description: 'Full bot mode — create spaces, post as a bot, receive events.',
  },
]

// ChannelFieldRow renders a single field with its label, input, and help text.
// The `descriptionId` is used as the aria-describedby target for the sheet.
function ChannelFieldRow({
  field,
  getValue,
  setValue,
}: {
  field: ChannelField
  getValue: (key: string) => unknown
  setValue: (key: string, value: unknown) => void
}) {
  return (
    <div className="space-y-1.5">
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
            aria-describedby={field.helpText ? `help-${field.key}` : undefined}
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
          aria-describedby={field.helpText ? `help-${field.key}` : undefined}
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
          aria-describedby={field.helpText ? `help-${field.key}` : undefined}
        />
      )}

      <FieldHelper field={field} />
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

  // #324 — Google Chat auth method picker. Switching method clears the other
  // group's useState value so a stale secret cannot be resurrected on switch-back (F-G08).
  const [gChatAuthMethod, setGChatAuthMethod] = useState<GChatAuthMethod>('webhook')

  const { data: currentConfig, isLoading } = useQuery({
    queryKey: ['channel-config', channelId],
    queryFn: () => fetchChannelConfig(channelId),
    enabled: open,
  })

  const isWebchat = channelId === 'webchat'
  const isGoogleChat = channelId === 'google-chat'

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

  // Populate form when config loads — skip if user has unsaved edits.
  // For google-chat, also detect which auth group is pre-configured.
  useEffect(() => {
    if (!currentConfig) return
    if (isDirtyRef.current) return
    setFormValues(currentConfig)
    // Detect pre-existing method on load: prefer service_account if any SA field is set.
    if (isGoogleChat) {
      const cfg = currentConfig as Record<string, unknown>
      if (cfg['service_account_json'] || cfg['service_account_file']) {
        setGChatAuthMethod('service_account')
      } else {
        setGChatAuthMethod('webhook')
      }
    }
  }, [currentConfig, isGoogleChat])

  const { mutate: doSave, isPending: saving } = useMutation({
    mutationFn: () => configureChannel(channelId, buildSubmitPayload()),
    onSuccess: () => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      queryClient.invalidateQueries({ queryKey: ['channel-config', channelId] })
      addToast({ message: 'Configuration saved', variant: 'success' })
    },
    onError: (err: unknown) => {
      const rawMsg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed'
      addToast({ message: mapErrorsToHumanLabels(fields, rawMsg), variant: 'error' })
    },
  })

  const { mutate: doSaveAndEnable, isPending: savingAndEnabling } = useMutation({
    mutationFn: async () => {
      await configureChannel(channelId, buildSubmitPayload())
      await enableChannel(channelId)
    },
    onSuccess: () => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      queryClient.invalidateQueries({ queryKey: ['channel-config', channelId] })
      addToast({ message: 'Channel configured and enabled', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      const rawMsg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to enable channel'
      addToast({ message: mapErrorsToHumanLabels(fields, rawMsg), variant: 'error' })
    },
  })

  const { mutate: doTest, isPending: testing } = useMutation({
    mutationFn: () => {
      // #326 — validate required fields client-side before hitting the API.
      const missingRequired = fields.filter((f) => {
        // Only check fields visible in the current context (authGroup filtering for GChat).
        if (isGoogleChat && f.authGroup && f.authGroup !== gChatAuthMethod) return false
        if (!f.required) return false
        const val = getValue(f.key)
        return val === undefined || val === null || val === ''
      })
      if (missingRequired.length > 0) {
        const labels = missingRequired.map((f) => f.label).join(', ')
        throw new Error(`Please fill in: ${labels}`)
      }
      return testChannel(channelId)
    },
    onSuccess: (result) => {
      setTestResult(result)
      if (result.success) {
        addToast({ message: 'Connection test passed', variant: 'success' })
      } else {
        const humanMsg = mapErrorsToHumanLabels(fields, result.message || 'Test failed')
        setTestResult({ success: false, message: humanMsg })
        addToast({ message: humanMsg, variant: 'error' })
      }
    },
    onError: (err: unknown) => {
      const rawMsg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Test failed'
      const humanMsg = mapErrorsToHumanLabels(fields, rawMsg)
      setTestResult({ success: false, message: humanMsg })
      addToast({ message: humanMsg, variant: 'error' })
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

  // #324 — on method switch: clear the deselected group's field values so a
  // stale secret cannot be resurrected on switch-back (M-13 / F-G08).
  function handleGChatMethodSwitch(method: GChatAuthMethod) {
    if (method === gChatAuthMethod) return
    markDirty()

    const clearGroup = method === 'webhook' ? 'service_account' : 'webhook'
    const fieldsToClear = fields.filter((f) => f.authGroup === clearGroup)

    setFormValues((prev) => {
      const next = { ...prev }
      for (const f of fieldsToClear) {
        if (f.key.includes('.')) {
          const [parent, child] = f.key.split('.')
          next[parent] = {
            ...((next[parent] as Record<string, unknown>) ?? {}),
            [child]: '',
          }
        } else {
          next[f.key] = ''
        }
      }
      return next
    })
    setGChatAuthMethod(method)
  }

  // #324 — build the payload, omitting fields from the deselected authGroup.
  function buildSubmitPayload(): Record<string, unknown> {
    if (!isGoogleChat) return formValues

    const payload = { ...formValues }
    const deselectedGroup = gChatAuthMethod === 'webhook' ? 'service_account' : 'webhook'
    const fieldsToOmit = fields.filter((f) => f.authGroup === deselectedGroup)
    for (const f of fieldsToOmit) {
      if (f.key.includes('.')) {
        const [parent, child] = f.key.split('.')
        const parentObj = (payload[parent] as Record<string, unknown> | undefined) ?? {}
        const { [child]: _removed, ...rest } = parentObj
        void _removed
        payload[parent] = rest
      } else {
        delete payload[f.key]
      }
    }
    return payload
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

  // #326 — a unique id for the sheet's aria-describedby pointing at the channel
  // description element ("How to connect" intro text).
  const descriptionId = `channel-config-desc-${channelId}`

  if (fields.length === 0) return null

  // Split fields into primary and advanced groups.
  const primaryFields = fields.filter((f) => !f.advanced)
  const advancedFields = fields.filter((f) => f.advanced)

  // For Google Chat, further split primary fields by authGroup for the picker.
  // Fields with no authGroup render unconditionally (e.g. space, allow_from in primary).
  const gChatGroupedFields = isGoogleChat
    ? primaryFields.filter((f) => f.authGroup === gChatAuthMethod || !f.authGroup)
    : null

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
        aria-describedby={descriptionId}
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            Configure {channelName}
          </SheetTitle>
          {/* #326 — aria-describedby target: the "How to connect" intro */}
          <SheetDescription
            id={descriptionId}
            className="text-xs text-[var(--color-muted)] leading-relaxed"
          >
            {isGoogleChat
              ? 'Choose how you want to connect Google Chat, then fill in the credentials below.'
              : isWhatsApp
              ? 'Link your WhatsApp account by scanning the QR code with your phone.'
              : `Enter your ${channelName} credentials to connect the bot.`}
          </SheetDescription>
        </SheetHeader>

        {isLoading ? (
          <div className="space-y-4 pt-6">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-10 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
            ))}
          </div>
        ) : (
          <div className="pt-5 space-y-5">
            {/* #324 — Google Chat auth method picker */}
            {isGoogleChat && (
              <div className="space-y-3">
                <p className="text-xs font-medium text-[var(--color-secondary)]">
                  How do you want to connect?
                </p>
                <div className="flex flex-col gap-2" role="radiogroup" aria-label="Connection method">
                  {GCHAT_AUTH_OPTIONS.map((opt) => (
                    <label
                      key={opt.value}
                      className={`flex items-start gap-3 p-3 rounded-md border cursor-pointer transition-colors ${
                        gChatAuthMethod === opt.value
                          ? 'border-[var(--color-accent)]/60 bg-[var(--color-accent)]/5'
                          : 'border-[var(--color-border)] bg-[var(--color-surface-2)] hover:border-[var(--color-border)]/80'
                      }`}
                    >
                      <input
                        type="radio"
                        name="gchat-auth-method"
                        value={opt.value}
                        checked={gChatAuthMethod === opt.value}
                        onChange={() => handleGChatMethodSwitch(opt.value)}
                        className="mt-0.5 accent-[var(--color-accent)]"
                        aria-label={opt.label}
                      />
                      <div>
                        <p className="text-xs font-medium text-[var(--color-secondary)]">{opt.label}</p>
                        <p className="text-[10px] text-[var(--color-muted)] mt-0.5">{opt.description}</p>
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            )}

            {/* Primary fields (not advanced); GChat applies authGroup filter */}
            {(isGoogleChat ? gChatGroupedFields! : primaryFields).map((field) => (
              <ChannelFieldRow
                key={field.key}
                field={field}
                getValue={getValue}
                setValue={setValue}
              />
            ))}

            {/* Advanced fields — collapsed under a disclosure (#323 will extract this) */}
            {/* TODO #323: extract HelperLink + AdvancedDisclosure after Wave 0 */}
            {advancedFields.length > 0 && (
              <AdvancedSection fields={advancedFields} getValue={getValue} setValue={setValue} />
            )}

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

            {/* Test result — pass/fail line (#326) */}
            {testResult && (
              <div
                role="status"
                aria-live="polite"
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
                Save &amp; Enable
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
                  title="Check the connection without saving"
                >
                  <Play size={13} weight="fill" />
                  {testing ? 'Testing…' : 'Test'}
                </Button>
              </div>
              {/* #326 — Test clarity: explain Test = check without saving */}
              <p className="text-[10px] text-[var(--color-muted)] text-center">
                Test checks your connection without saving any changes.
              </p>
            </div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

// AdvancedSection renders advanced fields under a collapsed disclosure.
// TODO #323: replace with AdvancedDisclosure primitive after Wave 0
function AdvancedSection({
  fields,
  getValue,
  setValue,
}: {
  fields: ChannelField[]
  getValue: (key: string) => unknown
  setValue: (key: string, value: unknown) => void
}) {
  const [open, setOpen] = useState(false)

  return (
    <div className="border-t border-[var(--color-border)] pt-3">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors w-full text-left"
        aria-expanded={open}
      >
        <span className="text-[10px] leading-none">{open ? '▾' : '▸'}</span>
        <span className="font-medium">Advanced</span>
        {!open && (
          <span className="ml-1 text-[10px] text-[var(--color-muted)]">
            ({fields.length} {fields.length === 1 ? 'option' : 'options'})
          </span>
        )}
      </button>
      {open && (
        <div className="mt-3 space-y-4">
          {fields.map((field) => (
            <ChannelFieldRow
              key={field.key}
              field={field}
              getValue={getValue}
              setValue={setValue}
            />
          ))}
        </div>
      )}
    </div>
  )
}
