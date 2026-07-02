import { useState, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Eye,
  EyeSlash,
  FloppyDisk,
  Play,
  Lightning,
  Warning,
} from '@phosphor-icons/react'
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
  fetchWorkspaces,
  fetchWorkspace,
  isWorker,
  isApiError,
} from '@/lib/api'
import { getChannelFields, type ChannelField } from '@/lib/channel-fields'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { useUiStore } from '@/store/ui'
import { WHATSAPP_CHANNEL_ID } from './whatsappChannelId'
import { WhatsAppNativeNotice } from './WhatsAppNativeNotice'

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
  /**
   * Whether the channel is currently enabled. Used to gate the WhatsApp QR
   * pairing UI: whatsmeow only starts the QR handshake after the channel is
   * enabled, so we show an informational prompt when enabled is false/undefined
   * rather than starting the 15-second QR timeout with no chance of a QR arriving.
   */
  enabled?: boolean
}

// #326: Map raw snake_case API field keys to human-readable labels using the
// channel descriptor. E.g. "bot_token" → "Bot Token".
// Dev-mode: emits console.warn for any snake_case token that looks like a field
// name but has no mapping — surfacing descriptor/backend drift early.
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

  const unmapped: string[] = []

  // Replace every snake_case token that matches a known field key; collect
  // candidates that look like field identifiers but have no mapping for
  // observability (dev-mode only, never in production log spam).
  const result = raw.replace(/\b([a-z][a-z0-9_]*)\b/g, (match) => {
    if (labelMap[match]) return labelMap[match]
    // Only flag tokens that look like field names (contain underscore or are
    // long enough to plausibly be an api key), not common English words.
    if (match.includes('_') || match.length > 12) {
      unmapped.push(match)
    }
    return match
  })

  if (import.meta.env.DEV && unmapped.length > 0) {
    console.warn(
      '[ChannelConfigPanel] mapErrorsToHumanLabels: unmapped snake_case tokens in error message — add to channel descriptor or check for backend/descriptor drift:',
      unmapped,
      '| raw message:', raw,
    )
  }

  return result
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

// HelperLink renders the helpText and optional helpLink for a channel field.
// LOCAL component — single consumer (ChannelConfigPanel); see spec §2, M-11.
// Rendered as a one-liner under the input.
function HelperLink({ field }: { field: ChannelField }) {
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

// ── Dotted-key helpers ────────────────────────────────────────────────────────
//
// Both functions support exactly one level of nesting (a.b), which is the
// only depth used in the channel descriptor today.  Deep nesting (a.b.c)
// would require a recursive implementation — extend when needed.

/**
 * Return a shallow-merged copy of `obj` with `dottedKey` set to `value`.
 * "group_trigger.mention_only" → obj["group_trigger"]["mention_only"] = value.
 * Flat keys are set directly.
 */
function setNested(
  obj: Record<string, unknown>,
  dottedKey: string,
  value: unknown,
): Record<string, unknown> {
  if (!dottedKey.includes('.')) {
    return { ...obj, [dottedKey]: value }
  }
  const dot = dottedKey.indexOf('.')
  const parent = dottedKey.slice(0, dot)
  const child = dottedKey.slice(dot + 1)
  return {
    ...obj,
    [parent]: {
      ...((obj[parent] as Record<string, unknown>) ?? {}),
      [child]: value,
    },
  }
}

/**
 * Read `dottedKey` from `obj`.
 * "group_trigger.mention_only" → (obj["group_trigger"] ?? {})["mention_only"].
 * Flat keys are read directly.
 */
function getNested(obj: Record<string, unknown>, dottedKey: string): unknown {
  if (!dottedKey.includes('.')) return obj[dottedKey]
  const dot = dottedKey.indexOf('.')
  const parent = dottedKey.slice(0, dot)
  const child = dottedKey.slice(dot + 1)
  return (obj[parent] as Record<string, unknown> | undefined)?.[child]
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

      <HelperLink field={field} />
    </div>
  )
}

export function ChannelConfigPanel({
  channelId,
  channelName,
  open,
  onOpenChange,
  nativeAvailable,
  enabled,
}: ChannelConfigPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const fields = getChannelFields(channelId)

  const isDirtyRef = useRef(false)
  const markDirty = () => { isDirtyRef.current = true }

  const [formValues, setFormValues] = useState<Record<string, unknown>>({})
  const [testResult, setTestResult] = useState<{ success: boolean; message: string } | null>(null)
  // Tracks whether Save & Enable was just clicked in this panel session. Needed because
  // the `enabled` prop comes from the parent's stale state after Save & Enable keeps the
  // dialog open — without this, the panel would keep showing the enable-prompt even after
  // the channel was just enabled in this session.
  const [wasJustEnabled, setWasJustEnabled] = useState(false)

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
  // workspace selector state — empty string = no workspace chosen
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<string>('')

  useEffect(() => {
    if (routing === undefined) return
    setSelectedAgentId(routing.default_agent_id ?? '__none__')
    setSelectedWorkspaceId(routing.workspace_id ?? '')
  }, [routing])

  // workspace list — always loaded when panel is open (non-webchat)
  const { data: workspaces = [] } = useQuery({
    queryKey: ['workspaces', { status: 'active' }],
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    enabled: open && !isWebchat,
  })

  // selected workspace detail — needed for core_team membership
  const {
    data: selectedWorkspace,
    isError: workspaceLoadError,
    isLoading: workspaceDetailLoading,
  } = useQuery({
    queryKey: ['workspaces', selectedWorkspaceId],
    queryFn: () => fetchWorkspace(selectedWorkspaceId),
    enabled: open && !isWebchat && selectedWorkspaceId !== '',
  })

  // Bound flow: agent picker lists only the selected workspace's core_team (non-workers)
  // Unbound flow (no workspace): all agents minus workers (legacy behaviour preserved)
  const coreTeam = selectedWorkspace?.core_team ?? null
  const filteredAgents = (() => {
    const nonWorkers = agents.filter((a) => !isWorker(a))
    if (!selectedWorkspaceId) return nonWorkers
    if (workspaceDetailLoading || workspaceLoadError) return [] // workspace loading or errored
    if (!coreTeam) return [] // workspace loaded but core_team absent
    return nonWorkers.filter((a) => coreTeam.includes(a.id))
  })()

  // Bound flow: a workspace is selected (FR-001). Agent is required but not yet chosen.
  const isBoundFlow = selectedWorkspaceId !== ''

  const { mutate: doSaveRouting } = useMutation({
    mutationFn: (payload: { agentId: string | undefined; workspaceId: string | undefined }) =>
      setChannelRouting(channelId, {
        default_agent_id: payload.agentId,
        workspace_id: payload.workspaceId || undefined,
      }),
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
  // Reset transient state when the dialog closes so next open starts fresh.
  useEffect(() => {
    if (!open) {
      setWasJustEnabled(false)
      setSelectedWorkspaceId('')
      setSelectedAgentId('__none__')
    }
  }, [open])

  // useRef (not useState) — timer ID mutation must not trigger re-render
  const routingDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => {
    if (routingDebounceRef.current !== null) clearTimeout(routingDebounceRef.current)
  }, [])
  const doSaveRoutingDebounced = (agentId: string | undefined, workspaceId: string | undefined) => {
    if (routingDebounceRef.current) clearTimeout(routingDebounceRef.current)
    routingDebounceRef.current = setTimeout(() => {
      routingDebounceRef.current = null
      doSaveRouting({ agentId, workspaceId })
    }, 400)
  }

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
      setTestResult(null)
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
      setTestResult(null)
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      queryClient.invalidateQueries({ queryKey: ['channel-config', channelId] })
      // #358: channels with a live pairing flow (WhatsApp native) must keep the panel
      // open after enable so the QR — pushed over the whatsapp_pairing WS frame once the
      // backend starts the channel — can render in WhatsAppNativeNotice. Closing here
      // unmounts the notice and drops its subscription before any QR frame arrives, which
      // is why "Enable & Save" never showed a code. Other channels have no pairing step.
      // Uses the REST-facing id ('whatsapp') that channel.id carries from GET /channels.
      const hasPairingFlow = channelId === WHATSAPP_CHANNEL_ID
      addToast({
        message: hasPairingFlow
          ? 'Channel enabled — scan the QR code below to link your device'
          : 'Channel configured and enabled',
        variant: 'success',
      })
      if (hasPairingFlow) {
        setWasJustEnabled(true)
      } else {
        onOpenChange(false)
      }
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
    setFormValues((prev) => setNested(prev, key, value))
  }

  function getValue(key: string): unknown {
    return getNested(formValues, key)
  }

  // #324 — on method switch: clear the deselected group's field values so a
  // stale secret cannot be resurrected on switch-back (M-13 / F-G08).
  function handleGChatMethodSwitch(method: GChatAuthMethod) {
    if (method === gChatAuthMethod) return
    markDirty()

    const clearGroup = method === 'webhook' ? 'service_account' : 'webhook'
    const fieldsToClear = fields.filter((f) => f.authGroup === clearGroup)

    setFormValues((prev) => {
      let next = { ...prev }
      for (const f of fieldsToClear) {
        next = setNested(next, f.key, '')
      }
      return next
    })
    setGChatAuthMethod(method)
  }

  // #324 — build the payload, explicitly clearing deselected authGroup fields.
  //
  // SECURITY: must NOT omit/delete deselected fields — the backend configureChannel
  // is a deep-merge (pkg/gateway/rest.go ~4549-4561). An absent field is left
  // untouched, so a previously-stored service_account_json_ref would survive in
  // config.json + the credential store after switching to Webhook. Instead, we
  // send an explicit '' for each deselected field so the backend's clear path
  // (rest.go ~4532-4537) fires: presence with empty string → clear the _ref and
  // delete the stored credential.
  function buildSubmitPayload(): Record<string, unknown> {
    if (!isGoogleChat) return formValues

    let payload = { ...formValues }
    const deselectedGroup = gChatAuthMethod === 'webhook' ? 'service_account' : 'webhook'
    const fieldsToClear = fields.filter((f) => f.authGroup === deselectedGroup)
    // Send '' (not delete) so the backend detects the clear and revokes any
    // stored credential for this field. Dotted keys do not appear in GChat
    // descriptors but handle them defensively for future-proofing.
    for (const f of fieldsToClear) {
      payload = setNested(payload, f.key, '')
    }
    return payload
  }

  // WhatsApp is always native (whatsmeow) — the legacy bridge + the `use_native`
  // toggle were removed, so there is no user-facing native toggle. The
  // linked-device QR pairing UI is still capability-gated (#299): on a lite/stub
  // build the backend reports native_available:false on the whatsapp ChannelEntry,
  // and we must NOT show a QR that can never pair. Only `false` gates;
  // `undefined`/`true` default to available.
  // Uses the REST-facing id ('whatsapp') that channel.id carries from GET /channels.
  const isWhatsApp = channelId === WHATSAPP_CHANNEL_ID
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

            {/* Advanced fields — collapsed under the shared AdvancedDisclosure (#323). */}
            {advancedFields.length > 0 && (
              <AdvancedDisclosure>
                <div className="space-y-4">
                  {advancedFields.map((field) => (
                    <ChannelFieldRow
                      key={field.key}
                      field={field}
                      getValue={getValue}
                      setValue={setValue}
                    />
                  ))}
                </div>
              </AdvancedDisclosure>
            )}

            {/* WhatsApp is always native (whatsmeow): show the live linked-device
                QR pairing UI (#283) — but only when the server build can run
                native WhatsApp. On a lite/stub build (native_available:false) show
                a hint instead of a QR that can never pair (#299).
                When the channel is not yet enabled, show an informational prompt
                instead of starting the 15-second QR timeout: whatsmeow only
                generates a QR after the channel is enabled. */}
            {isWhatsApp &&
              (whatsAppNativeUnavailable ? (
                <p
                  data-testid="native-unavailable-hint"
                  className="text-xs text-[var(--color-muted)] leading-relaxed mt-1 p-3 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-border)]"
                >
                  WhatsApp requires the native build (whatsmeow); this server build
                  doesn&apos;t include it, so linked-device pairing is unavailable.
                </p>
              ) : (enabled || wasJustEnabled) ? (
                <WhatsAppNativeNotice />
              ) : (
                <p
                  data-testid="whatsapp-enable-prompt"
                  className="text-xs text-[var(--color-muted)] leading-relaxed mt-1 p-3 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-border)]"
                >
                  Save &amp; Enable WhatsApp to start pairing. Once enabled, the QR code will appear here automatically.
                </p>
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
                  <div className="space-y-3">
                    {/* Workspace selector (US-1 / FR-001) */}
                    <div className="space-y-1.5">
                      <Label
                        htmlFor="routing-workspace-select"
                        className="text-xs font-medium text-[var(--color-secondary)]"
                      >
                        Workspace
                      </Label>
                      <div data-testid="routing-workspace-select">
                        <SmartSelect
                          value={selectedWorkspaceId || '__none__'}
                          onValueChange={(v) => {
                            const next = v === '__none__' ? '' : v
                            const wasbound = selectedWorkspaceId !== ''
                            setSelectedWorkspaceId(next)
                            // Changing workspace clears the agent selection (member set changed)
                            setSelectedAgentId('__none__')
                            if (next === '') {
                              // Unbind: user selected "No workspace" on a previously-bound
                              // channel — persist immediately with no workspace_id or
                              // default_agent_id so the backend clears the binding (FR-001).
                              if (wasbound) {
                                doSaveRoutingDebounced(undefined, undefined)
                              }
                              // If already unbound, nothing changed — no PUT needed.
                            }
                            // Bound flow: do not persist until agent is also chosen (FR-001).
                          }}
                          placeholder="No workspace (global default routing)"
                          items={[
                            { value: '__none__', label: 'No workspace (global default routing)' },
                            ...workspaces.map((ws) => ({ value: ws.id, label: ws.name })),
                          ]}
                        />
                      </div>
                      <p className="text-[10px] text-[var(--color-muted)] leading-relaxed">
                        Bind this channel to a workspace. Once bound, only that workspace&apos;s member agents are eligible.
                      </p>
                    </div>

                    {/* Agent selector (US-2 / FR-002) — disabled until workspace chosen */}
                    <div className="space-y-1.5">
                      <Label className="text-xs font-medium text-[var(--color-secondary)]">
                        Default agent
                      </Label>
                      {agentsError ? (
                        <p className="text-xs text-[var(--color-error)]">
                          Couldn&apos;t load agent list.
                        </p>
                      ) : isBoundFlow && workspaceLoadError ? (
                        // Workspace detail fetch failed — show a distinct, actionable error.
                        // Distinct from the loading/empty-core_team states (Finding #1).
                        <p
                          data-testid="routing-workspace-load-error"
                          className="text-xs text-[var(--color-error)]"
                        >
                          Couldn&apos;t load workspace members — try again.{' '}
                          <button
                            type="button"
                            className="underline hover:no-underline"
                            onClick={() =>
                              queryClient.invalidateQueries({ queryKey: ['workspaces', selectedWorkspaceId] })
                            }
                          >
                            Retry
                          </button>
                        </p>
                      ) : isBoundFlow && coreTeam !== null && coreTeam.length === 0 ? (
                        // FR-009: empty core_team — can't select anything
                        <p
                          data-testid="routing-empty-core-team-hint"
                          className="text-xs text-[var(--color-error)]"
                        >
                          Add a member to this workspace first.
                        </p>
                      ) : (
                        <div data-testid="routing-agent-select">
                          <SmartSelect
                            value={selectedAgentId}
                            disabled={!isBoundFlow}
                            onValueChange={(v) => {
                              const next = v === '__none__' ? undefined : v
                              setSelectedAgentId(v)
                              if (isBoundFlow) {
                                if (next) {
                                  // Both workspace and agent are chosen — persist (MAJ-001)
                                  doSaveRoutingDebounced(next, selectedWorkspaceId)
                                }
                                // If bound but no agent yet, do not persist (FR-004)
                              } else {
                                // Unbound flow: persist on agent change (legacy auto-persist)
                                doSaveRoutingDebounced(next, undefined)
                              }
                            }}
                            placeholder={isBoundFlow ? 'Select an agent from this workspace' : '(Global default)'}
                            items={
                              isBoundFlow
                                ? // Bound flow: no "(Global default)" option (FR-003)
                                  filteredAgents.map((a) => ({ value: a.id, label: a.name }))
                                : [
                                    // Unbound flow: "(Global default)" is valid (legacy path)
                                    { value: '__none__', label: '(Global default)' },
                                    ...filteredAgents.map((a) => ({ value: a.id, label: a.name })),
                                  ]
                            }
                          />
                        </div>
                      )}

                      {/* FR-004 / US-3 AC-1: hint when workspace chosen but no agent selected.
                          Not shown during a workspace load error (separate error state handles that). */}
                      {isBoundFlow && !workspaceLoadError && (selectedAgentId === '__none__' || selectedAgentId === '') && !(coreTeam !== null && coreTeam.length === 0) && (
                        <p
                          data-testid="routing-agent-required-hint"
                          className="text-xs text-[var(--color-error)]"
                        >
                          Select an agent from this workspace to enable routing.
                        </p>
                      )}

                      {/* Unbound flow helper text */}
                      {!isBoundFlow && (
                        <p className="text-[10px] text-[var(--color-muted)] leading-relaxed">
                          Which agent handles inbound messages on this channel. &quot;(Global default)&quot; falls back to the globally-configured default agent.
                        </p>
                      )}
                    </div>

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

