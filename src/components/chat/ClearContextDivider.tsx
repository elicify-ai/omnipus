// A saved /clear marker separates context segments. The reached boundary's
// Show earlier messages action belongs to HistoryBoundary, not to every marker.
import { Separator } from '@/components/ui/separator'

export function ClearContextDivider({ messageId, content }: { messageId: string; content: string }) {
  return (
    <div data-message-role="system" data-message-id={messageId}>
      <div data-testid="clear-context-divider" className="flex items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]">
        <div className="min-w-0 flex-1"><Separator /></div>
        <span className="whitespace-nowrap text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{content}</span>
        <div className="min-w-0 flex-1"><Separator /></div>
      </div>
    </div>
  )
}
