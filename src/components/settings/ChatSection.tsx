/**
 * ChatSection — Settings → Chat tab.
 *
 * Local, per-device chat display preferences. Currently a single toggle:
 * "Verbose chat", which reveals every tool call in the transcript, including
 * background dispatches, status polls, and internal tool-loading calls that
 * are hidden by default.
 *
 * This is a pure client-side display preference (backed by
 * useChatPreferencesStore, persisted to localStorage). It does not cross the
 * gateway/API boundary, does not call any REST endpoint, and does not affect
 * what is stored in the session transcript — it only changes what is
 * rendered on this device.
 */

import { ChatCircleText } from '@phosphor-icons/react'
import { Switch } from '@/components/ui/switch'
import { useChatPreferencesStore } from '@/store/chatPreferences'

export function ChatSection(): React.ReactElement {
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const setVerboseChatEnabled = useChatPreferencesStore((s) => s.setVerboseChatEnabled)

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-2">
        <ChatCircleText size={18} className="text-[var(--color-secondary)]" />
        <h2 className="text-sm font-semibold text-[var(--color-secondary)]">Chat display</h2>
      </div>

      {/* Verbose chat card */}
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3">
        {/* Toggle row */}
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--color-secondary)]">Verbose chat</p>
            <p className="text-xs text-[var(--color-muted)] mt-0.5">
              Show every tool call in the transcript, including background dispatches,
              status polls, and internal tool-loading calls that are hidden by default.
              This is also the only way to see delegation cards and their step-level
              detail inline in the thread, rather than in the Activity panel alone.
            </p>
          </div>
          <Switch
            checked={verboseChatEnabled}
            onCheckedChange={setVerboseChatEnabled}
            aria-label="Verbose chat"
            data-testid="chat-verbose-switch"
          />
        </div>

        {/* Helper text */}
        <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
          This is a local, per-device display preference — it is not synced across
          devices and does not change what is stored in the session transcript.
        </p>
      </div>
    </div>
  )
}
