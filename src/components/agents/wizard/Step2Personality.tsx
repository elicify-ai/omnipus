// Step2Personality — wizard step ② (Personality).
//
// Per spec §5.3-§5.5: soul (required for all three types — for External it's
// passed as CLI prompt content), heartbeat body + enabled + interval (Main
// only), voice (Main only — routed through `<VoiceProviderSub>` so the widget
// auto-switches between dropdown / free-text / disabled based on Settings →
// Voice provider detection).
//
// The Heartbeat body + enabled + interval sub-fields use the spec §4.9
// conditional: heartbeat body shows only when non-empty (Main only),
// heartbeat_enabled checkbox toggles persistence, heartbeat_interval number
// input appears when heartbeat_enabled is checked.
//
// W4 testids emitted per the plan's UI table:
//   wizard-soul, wizard-heartbeat (Main only), wizard-voice (Main only)

import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { VoiceProviderSub } from '../voice-provider-sub'
import type { StepProps } from './types'

export function Step2Personality({ payload, setField, initialType }: StepProps) {
  const isWorker = initialType !== 'Main'
  const soulLabel = isWorker ? 'Soul / task prompt' : 'Soul'

  return (
    <>
      <div className="space-y-2">
        <label htmlFor="wizard-soul" className="text-sm font-medium">
          {soulLabel} <span className="text-[var(--color-error)]" aria-label="required">*</span>
        </label>
        <Textarea
          id="wizard-soul"
          data-testid="wizard-soul"
          value={payload.soul}
          onChange={(e) => setField('soul', e.target.value)}
          rows={10}
          placeholder="You are a focused research assistant. You answer concisely..."
          aria-required="true"
        />
      </div>
      {!isWorker && (
        <div className="space-y-2" data-testid="wizard-heartbeat">
          <label className="text-sm font-medium">Heartbeat (Main only)</label>
          <Input
            value={payload.heartbeat ?? ''}
            onChange={(e) => setField('heartbeat', e.target.value)}
            placeholder="Periodic instruction body"
          />
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              // Heartbeat can only be "enabled" when there is a body —
              // server-side an enabled heartbeat without a body is
              // nonsense, so we disable the checkbox until the user
              // types one.
              disabled={!payload.heartbeat}
              checked={!!payload.heartbeat && (payload.heartbeat_enabled ?? false)}
              onChange={(e) => setField('heartbeat_enabled', e.target.checked)}
            />
            <span>Enable periodic heartbeat</span>
          </label>
          {payload.heartbeat_enabled && (
            <Input
              type="number"
              value={payload.heartbeat_interval ?? 1800}
              onChange={(e) => {
                // Guard against NaN (user clears the field, types "e",
                // etc.) and clamp to the server's min(60). Without this
                // the wire sees heartbeat_interval=NaN and the server
                // returns a generic 400.
                const raw = e.target.value
                if (raw === '') {
                  setField('heartbeat_interval', 1800)
                  return
                }
                const n = Number(raw)
                if (!Number.isFinite(n)) return
                setField('heartbeat_interval', Math.max(60, Math.floor(n)))
              }}
              min={60}
              placeholder="Interval in seconds (default 1800)"
            />
          )}
        </div>
      )}
      {!isWorker && (
        <div className="space-y-2" data-testid="wizard-voice">
          <label className="text-sm font-medium">Voice (Main only)</label>
          {/* VoiceProviderSub auto-detects the configured TTS provider and
              renders dropdown / free-text / disabled — see
              voice-provider-sub.tsx for the switch logic. Empty value is
              a legal "use engine default" state; we round-trip undefined
              to undefined on the wire (see CreateAgentModal payloadTo….). */}
          <VoiceProviderSub
            value={payload.voice ?? null}
            onChange={(v) => setField('voice', v)}
          />
          <p className="text-xs text-[var(--color-muted)]">
            See Settings → Voice to configure providers.
          </p>
        </div>
      )}
    </>
  )
}

export default Step2Personality