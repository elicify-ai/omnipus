// DraftLink.tsx — the create_email_draft tool card (email-mail-view-spec.md
// §17/D12): the agent created an email draft; the human approves/sends it
// from the Mail panel. The card states the outcome and hands the human the
// "Open draft" affordance, which navigates IN PLACE to the draft (§17):
// the chat_link's hash carries /workspaces/{ws}/mail?mailbox&folder&message
// — openMailDeepLink sets the hash, the /mail route stub consumes the
// params into the panel intent and opens the Mail panel (leave-gated), then
// lands on chat. When no link was derivable (no derivable public_url), the
// card shows the stated reason instead — never a dead button.
import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { Button } from '@/components/ui/button'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { openMailDeepLink } from '@/components/workspaces/mail/mailDeepLink'
import { mailUidRef } from '@/lib/api/mail'
import { useUiStore } from '@/store/ui'
import { stripUntrustedContentWrapper } from '@/lib/untrustedToolContent'

interface CreateEmailDraftArgs {
  workspace_id?: string
  agent_id?: string
  to?: string
  subject?: string
  body_markdown?: string
}

interface CreateEmailDraftResult {
  created?: boolean
  message_id?: string
  uid?: number
  uidvalidity?: number
  chat_link?: string | null
  chat_link_reason?: string | null
}

function parseResult(result: unknown): CreateEmailDraftResult {
  if (!result) return {}
  if (typeof result === 'string') {
    const unwrapped = stripUntrustedContentWrapper(result)
    if (unwrapped === null) {
      return { created: undefined }
    }
    try {
      return JSON.parse(unwrapped) as CreateEmailDraftResult
    } catch {
      return {}
    }
  }
  if (typeof result === 'object') return result as CreateEmailDraftResult
  return {}
}

function currentWorkspaceId(): string | null {
  // The chat surface is always a workspace tab: /#/workspaces/{id}/chat.
  // Parse the hash — the workspace param lives there under hash routing.
  const hash = window.location.hash
  const match = /#\/workspaces\/([^/]+)\/chat/.exec(hash)
  return match === null ? null : (match[1] as string)
}

export function DraftLinkBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
}: {
  toolName: string
  args: CreateEmailDraftArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (
    !shouldRenderToolCall(toolName, args as unknown as Record<string, unknown>, verboseChatEnabled, !!isError)
  ) {
    return null
  }

  const statusConfig: ToolBadgeStatusConfig = isRunning
    ? getToolBadgeStatusConfig('running', { size: 12 })
    : isCancelled
    ? getToolBadgeStatusConfig('cancelled', { size: 12, cancelledVariant: 'muted' })
    : isError
    ? getToolBadgeStatusConfig('error', { size: 12 })
    : getToolBadgeStatusConfig('success', { size: 12 })

  const parsed = parseResult(result)
  const hasResult = result != null
  const chatLink = typeof parsed.chat_link === 'string' && parsed.chat_link !== '' ? parsed.chat_link : null
  const detail = args.subject ?? '(no subject)'

  const handleOpenDraft = () => {
    if (chatLink !== null && openMailDeepLink(chatLink)) return
    // Fallback: derive the in-app path from the result ids themselves.
    const ws = args.workspace_id ?? currentWorkspaceId()
    const agent = args.agent_id
    if (ws !== null && agent !== undefined && parsed.uid !== undefined && parsed.uidvalidity !== undefined) {
      const messageRef = mailUidRef(parsed.uidvalidity, parsed.uid)
      window.location.hash = `/workspaces/${ws}/mail?mailbox=${encodeURIComponent(agent)}&folder=drafts&message=${encodeURIComponent(messageRef)}`
      return
    }
    useUiStore.getState().addToast({ message: 'No draft link available.', variant: 'warning' })
  }

  return (
    // Flat text-line design (mirrors BrowserNavigateBlock): no border, no
    // surface fill — the row is transparent on the thread. DisclosureRow is
    // the catalogued expand/collapse control (design-system rule 14).
    <div className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      <div className="flex w-full items-center gap-[var(--space-2)]">
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={setExpanded}
          expandable={hasResult}
          data-testid="draft-link-toggle"
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-muted)] shrink-0">create_email_draft</span>
          <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[length:var(--type-caption-size)]">
            {detail}
          </span>
          <span className="text-[var(--color-muted)] shrink-0">{statusConfig.label}</span>
        </DisclosureRow>

        {!isRunning && !isError && parsed.created === true && (
          <Button
            type="button"
            variant="link"
            onClick={handleOpenDraft}
            aria-label="Open draft"
            title="Open this draft in the Mail panel"
            className="shrink-0 flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)]"
          >
            <span>Open draft</span>
          </Button>
        )}
      </div>

      {/* Detail panel — indented left-accent block (mirrors
          BrowserNavigateBlock's detail region). Carries the draft recipients
          and the chat_link_reason when no link was derivable (D12): the
          card states WHY there is nothing to open instead of rendering a
          dead button. */}
      {expanded && hasResult && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] pl-[var(--space-2-5)] py-[var(--space-1)] space-y-[var(--space-2)]">
          {args.to !== undefined && (
            <div>
              <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] font-mono break-all">to: {args.to}</span>
            </div>
          )}
          {typeof parsed.chat_link_reason === 'string' && parsed.chat_link_reason !== '' && (
            <div>
              <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-all">
                no link: {parsed.chat_link_reason}
              </span>
            </div>
          )}
          {parsed.created !== true && (
            <div>
              <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">draft was not created</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// Draft created (US-8/§17): the card hands the human the "Open draft"
// affordance; the navigation is IN PLACE (Mail panel over the chat), not a
// new tab. Registered in OmnipusRuntimeProvider alongside the other tool
// UIs. toolName is registered under both spellings, dot and underscore,
// mirroring BrowserNavigateUI's dual registration.
export const DraftLinkUI = makeAssistantToolUI<CreateEmailDraftArgs, unknown>({
  toolName: 'create_email_draft',
  render: ({ args, result, status, isError }) => (
    <DraftLinkBlock
      toolName="create_email_draft"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

export const CreateEmailDraftUI = makeAssistantToolUI<CreateEmailDraftArgs, unknown>({
  toolName: 'create-email-draft',
  render: ({ args, result, status, isError }) => (
    <DraftLinkBlock
      toolName="create-email-draft"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

/** Both spellings share one lazy boundary in OmnipusRuntimeProvider. */
export function DraftLinkToolUIs() {
  return (
    <>
      <DraftLinkUI />
      <CreateEmailDraftUI />
    </>
  )
}
