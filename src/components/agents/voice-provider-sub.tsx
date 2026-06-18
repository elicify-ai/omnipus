// voice-provider-sub.tsx — Dynamic widget switch for the per-agent voice
// field. Renders dropdown / free-text / disabled based on the
// detectVoiceProvider() result.
//
// W5 of agent-form-requirements delivery. Co-located per the codebase
// convention (ExecutorSelector.tsx / WorkerCard.tsx) — the spec's §8.3 calls
// for .ts but the codebase uses .tsx for React components.
//
// Per spec §4.10 + §6.5: built-in Main agents have an EDITABLE Voice field
// (per F-14 resolution: voice is operator-tunable on built-ins because it's a
// runtime TTS config, not identity). Workers (Subagent / Subagent (External))
// do NOT render this field at all — they never have voice.

import * as React from 'react'
import { useEffect, useState } from 'react'

import {
  detectVoiceProvider,
  bumpVoiceProviderCacheVersion,
  type VoiceProviderDetectResult,
} from '@/lib/agents/voice-provider-detect'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export interface VoiceProviderSubProps {
  /** Current voice value (the persisted agent.voice). */
  value: string | null | undefined
  /** Called with the new value on change. */
  onChange: (next: string) => void
  /** Disable the widget (e.g. on built-in agents where voice is locked by spec §6.5).
   *  F-14 resolution overrides this for built-in Main: voice is editable there. */
  disabled?: boolean
  /** Tooltip / aria-label for the disabled state. */
  disabledReason?: string
}

export function VoiceProviderSub({
  value,
  onChange,
  disabled,
  disabledReason,
}: VoiceProviderSubProps) {
  const [result, setResult] = useState<VoiceProviderDetectResult | null>(null)

  // Fetch the provider config on mount; re-fetch on cache-bump events.
  useEffect(() => {
    let cancelled = false

    async function load() {
      const r = await detectVoiceProvider()
      if (!cancelled) setResult(r)
    }
    void load()

    function onChangeEvent() {
      bumpVoiceProviderCacheVersion()
      void load()
    }
    // Settings → Voice publishes this event when the operator changes the
    // provider (per spec §4.10.1).
    window.addEventListener('voice-provider-change', onChangeEvent)
    return () => {
      cancelled = true
      window.removeEventListener('voice-provider-change', onChangeEvent)
    }
  }, [])

  // Disabled state — either explicitly disabled by the caller OR the
  // provider reports disabled.
  if (disabled || result?.mode === 'disabled') {
    return (
      <Input
        data-testid="voice-field"
        value={value ?? ''}
        disabled
        placeholder={result?.reason ?? disabledReason ?? 'Voice disabled'}
        title={result?.reason ?? disabledReason}
        onChange={() => {
          /* locked */
        }}
      />
    )
  }

  // Still loading → render a disabled placeholder so the form layout doesn't jump.
  if (!result) {
    return (
      <Input
        data-testid="voice-field"
        value={value ?? ''}
        disabled
        placeholder="Detecting voice provider…"
      />
    )
  }

  if (result.mode === 'enum' && result.voices && result.voices.length > 0) {
    return (
      <Select
        value={value ?? ''}
        onValueChange={onChange}
        disabled={disabled}
      >
        <SelectTrigger data-testid="voice-field">
          <SelectValue placeholder="Select voice" />
        </SelectTrigger>
        <SelectContent>
          {result.voices.map((v) => (
            <SelectItem key={v} value={v}>
              {v}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    )
  }

  // Free-text mode (provider has no enum, or empty enum).
  return (
    <Input
      data-testid="voice-field"
      value={value ?? ''}
      onChange={(e) => onChange(e.target.value)}
      placeholder="e.g. alloy"
      disabled={disabled}
    />
  )
}

export default VoiceProviderSub
