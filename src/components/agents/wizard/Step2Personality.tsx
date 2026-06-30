// Step2Personality — wizard step ② (Personality).
//
// Per spec FR-017 / US-5.AC3: heartbeat fields have been removed from this
// step — heartbeat is now workspace-scoped (member_configs) and NOT set at
// agent-creation time.
//
// Per spec FR-026 / US-10: the soul field now offers an Upload .md control
// (parity with AgentProfile's UploadButton) so operators can upload a
// SOUL.md file instead of pasting its contents.
//
// Remaining fields: soul (required for all types), voice (Main only).
//
// W4 testids:
//   wizard-soul, wizard-voice (Main only), wizard-soul-upload

import { useRef } from 'react'
import { Textarea } from '@/components/ui/textarea'
import { VoiceProviderSub } from '../voice-provider-sub'
import { UploadSimple } from '@phosphor-icons/react'
import { useUiStore } from '@/store/ui'
import type { StepProps } from './types'

export function Step2Personality({ payload, setField, initialType }: StepProps) {
  const isWorker = initialType !== 'Main'
  const soulLabel = isWorker ? 'Soul / task prompt' : 'Soul'
  const addToast = useUiStore((s) => s.addToast)
  // Keep a stable ref for the file input to avoid DOM leak across re-renders.
  const fileInputRef = useRef<HTMLInputElement>(null)

  function handleSoulUpload() {
    // Programmatic file input — same pattern as AgentProfile's UploadButton
    // (FR-026 / US-10). Accepts .md/.markdown/.txt per the spec.
    const input = document.createElement('input')
    input.type = 'file'
    input.accept = '.md,.markdown,.txt'
    input.onchange = (e) => {
      const file = (e.target as HTMLInputElement).files?.[0]
      if (!file) return
      if (file.size > 1_000_000) {
        addToast({
          message: `File too large (${(file.size / 1_000_000).toFixed(1)}MB). Max 1MB for markdown files.`,
          variant: 'error',
        })
        return
      }
      const reader = new FileReader()
      reader.onload = () => {
        setField('soul', reader.result as string)
      }
      reader.onerror = () => {
        addToast({
          message: `Failed to read ${file.name}: ${reader.error?.message ?? 'unknown error'}`,
          variant: 'error',
        })
      }
      reader.readAsText(file)
    }
    input.click()
    // Stash on the ref so tests can query it if needed (no-op for the real flow).
    fileInputRef.current = input
  }

  return (
    <>
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <label htmlFor="wizard-soul" className="text-sm font-medium">
            {soulLabel}{' '}
            <span className="text-[var(--color-error)]" aria-label="required">*</span>
          </label>
          {/* FR-026 / US-10: Upload .md control — parity with AgentProfile */}
          <button
            type="button"
            data-testid="wizard-soul-upload"
            onClick={handleSoulUpload}
            className="h-7 px-2 text-xs rounded border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex items-center gap-1"
          >
            <UploadSimple size={12} />
            Upload .md
          </button>
        </div>
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
