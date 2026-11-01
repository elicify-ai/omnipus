// ChannelOutboundSection — the "Starting messages" section of a connection's
// Configure panel (channels spec FR-026, S-80..S-83, T-6b).
//
// It decides whether the agent may only answer on this connection or may also
// start messages to the listed people and groups. It has its own Save, which
// calls the dedicated PUT /channels/{id}/outbound with the revision from its
// GET; the generic Configure save never carries `outbound` (FR-023).
//
// Rendered by ChannelConfigPanel (src/components/skills/), which owns the rest
// of the Configure form.

import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { ErrorState } from '@/components/ui/error-state'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Textarea } from '@/components/ui/textarea'
import {
  fetchChannelOutbound,
  readOutboundRejection,
  setChannelOutbound,
} from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import type {
  ChannelOutboundRead,
  ChannelOutboundRejection,
  ChannelOutboundWriteResponse,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'

type Policy = ChannelOutboundRead['outbound']['policy']
type TargetError = NonNullable<ChannelOutboundRejection['target_errors']>[number]

// Starting messages works on Discord and Telegram only (spec T-1; the GET's
// `start_supported` is true for exactly these). Other platforms show the
// "not supported yet" text without reading the setting.
const START_PLATFORMS = new Set(['discord', 'telegram'])

const OPTION_HELP: Record<Policy, string> = {
  reply_only: 'The agent answers messages that arrive here. It never starts a conversation.',
  agent_may_start: 'The agent may also start messages, but only to the people and groups listed below.',
}
const FIELD_LABEL = 'People and groups the agent may message'
const FIELD_HELP = "One id per line, like the allowed senders: a person's user id (a direct message) or a group's chat id. No @names, no *. Up to 20."
const PLATFORM_HELP: Record<string, string> = {
  discord: "A person's id sends a direct message; a channel or thread id posts there. Adding a Discord id needs the connection running.",
  telegram: 'A positive id is a person (they must have started your bot first); a negative id is a group or channel; a forum topic is written chat_id/topic_id.',
}
const NOT_RUNNING_DISCORD = 'This connection is not running. You can remove ids or switch to Only answer; adding a Discord id needs it running.'

// S-81: each 409 is handled by its rejection code, never by one stale-form text.
const STALE_TEXT = 'These settings changed since you opened them. The current values are shown; check them and save again.'
const CONNECT_FIRST_TEXT = 'Connect the channel first to add Discord ids.'
const SAVED_LIVE_MISMATCH_TEXT = 'An earlier change to this connection did not finish applying, so nothing was saved. Finish applying it first (any successful save of this connection, or disable and enable it), then save the list again.'
const UNREADABLE_REFUSAL_TEXT = 'The server refused this save, but its answer could not be read. Close and reopen this panel to see the current values, then try again.'

// Used only when a refused entry arrives without its own reason text.
const TARGET_CODE_TEXT: Record<TargetError['code'], string> = {
  invalid_id: 'this is not a valid id',
  wildcard_not_allowed: '* is not allowed; list each id',
  username_not_allowed: 'use the numeric id, not a name',
  wrong_platform: 'this id belongs to another platform',
  not_found: 'this id was not found',
  unsupported_channel_kind: 'messages cannot be started in this kind of channel',
  cannot_message_people: 'this connection cannot message people directly',
  lookup_unavailable: 'this id could not be checked right now',
}

// Stored targets are canonical `<platform>:<id>`; the field shows bare ids.
function bareTarget(target: string, platform: string): string {
  const prefix = `${platform}:`
  return target.startsWith(prefix) ? target.slice(prefix.length) : target
}

function targetsText(read: Pick<ChannelOutboundRead, 'outbound'>, platform: string): string {
  return read.outbound.targets.map((t) => bareTarget(t, platform)).join('\n')
}

// The entries sent, plus each entry's 1-based line in the field, so a refused
// entry's `index` (its position in the request) points at the right line even
// when blank lines were skipped.
function entriesOf(text: string): { targets: string[]; lines: number[] } {
  const targets: string[] = []
  const lines: number[] = []
  text.split('\n').forEach((line, i) => {
    const entry = line.trim()
    if (entry === '') return
    targets.push(entry)
    lines.push(i + 1)
  })
  return { targets, lines }
}

interface Refusal {
  message: string | null
  omittedCount: number
  items: { line: number; reason: string }[]
}

const NO_REFUSAL: Refusal = { message: null, omittedCount: 0, items: [] }

// One announced message (role="alert", so screen readers read it at once),
// aligned with the section's text instead of centred like a page state.
function Notice({ message }: { message: string }) {
  return <ErrorState message={message} className="items-start py-0" />
}

export interface ChannelOutboundSectionProps {
  channelId: string
  channelName: string
}

export function ChannelOutboundSection({ channelId, channelName }: ChannelOutboundSectionProps) {
  const platform = channelId.split('.')[0]
  if (!START_PLATFORMS.has(platform)) return <UnsupportedSection channelName={channelName} />
  return <OutboundSettings channelId={channelId} channelName={channelName} platform={platform} />
}

// The titled section. While the setting loads only a placeholder is shown, so
// the section never appears with its controls missing.
function SectionFrame({ headingId, children }: { headingId: string; children: ReactNode }) {
  return (
    <section
      aria-labelledby={headingId}
      className="pt-[var(--space-2)] border-t border-[var(--color-border)] space-y-[var(--space-2-5)]"
    >
      <h3
        id={headingId}
        className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)] uppercase tracking-wider"
      >
        Starting messages
      </h3>
      {children}
    </section>
  )
}

function UnsupportedSection({ channelName }: { channelName: string }) {
  const headingId = useId()
  return (
    <SectionFrame headingId={headingId}>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
        {`Starting messages is not supported yet on ${channelName}. The agent only answers here.`}
      </p>
    </SectionFrame>
  )
}

function OutboundSettings({
  channelId,
  channelName,
  platform,
}: {
  channelId: string
  channelName: string
  platform: string
}) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const queryKey = ['channel-outbound', channelId]
  const { data: read, isLoading, isError, refetch } = useQuery({
    queryKey,
    queryFn: () => fetchChannelOutbound(channelId),
  })

  const [policy, setPolicy] = useState<Policy>('reply_only')
  const [text, setText] = useState('')
  const [revision, setRevision] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const [refusal, setRefusal] = useState<Refusal>(NO_REFUSAL)
  // True while the operator has unsaved edits: a background re-read must not
  // replace what they typed. A deliberate re-read (stale_revision) does.
  const dirtyRef = useRef(false)
  const fieldErrorId = useId()
  const headingId = useId()

  function hydrate(next: Pick<ChannelOutboundRead, 'outbound' | 'revision'>) {
    dirtyRef.current = false
    setPolicy(next.outbound.policy)
    setText(targetsText(next, platform))
    setRevision(next.revision)
  }

  useEffect(() => {
    if (read && !dirtyRef.current) hydrate(read)
  }, [read])

  const { mutate: save, isPending: saving } = useMutation({
    mutationFn: (entries: { targets: string[]; lines: number[] }) =>
      setChannelOutbound(channelId, { policy, targets: entries.targets, expected_revision: revision }),
    onMutate: () => {
      setNotice(null)
      setRefusal(NO_REFUSAL)
    },
    onSuccess: (written: ChannelOutboundWriteResponse) => {
      hydrate(written)
      if (read) queryClient.setQueryData<ChannelOutboundRead>(queryKey, { ...read, outbound: written.outbound, revision: written.revision })
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      // T-6b: saved and in force, but other settings were not refreshed.
      if (written.warning) addToast({ message: written.warning, variant: 'warning' })
      else addToast({ message: 'Starting messages saved.', variant: 'success' })
    },
    onError: async (error: unknown, entries) => {
      const decoded = readOutboundRejection(error, channelId)
      if (decoded.kind === 'invalid') {
        setNotice(UNREADABLE_REFUSAL_TEXT)
        return
      }
      if (decoded.kind === 'none') {
        setNotice(isApiError(error) ? error.userMessage : error instanceof Error ? error.message : 'The setting could not be saved. Try again.')
        return
      }
      const { rejection } = decoded
      switch (rejection.code) {
        case 'stale_revision': {
          // Show the current values instead of overwriting a concurrent change.
          const reread = await refetch()
          if (reread.data) hydrate(reread.data)
          setNotice(STALE_TEXT)
          return
        }
        case 'connect_first':
          setNotice(CONNECT_FIRST_TEXT)
          return
        case 'saved_live_mismatch':
          setNotice(SAVED_LIVE_MISMATCH_TEXT)
          return
        default:
          setRefusal({
            message: rejection.error,
            omittedCount: rejection.omitted_count ?? 0,
            items: (rejection.target_errors ?? []).map((item) => ({
              line: entries.lines[item.index] ?? item.index + 1,
              reason: item.reason ?? TARGET_CODE_TEXT[item.code],
            })),
          })
      }
    },
  })

  if (isLoading) {
    return (
      <div
        aria-busy="true"
        className="pt-[var(--space-2)] border-t border-[var(--color-border)]"
      >
        <div className="h-10 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
      </div>
    )
  }
  if (isError || !read) {
    return (
      <SectionFrame headingId={headingId}>
        <Notice message={`Couldn't load the Starting messages setting for ${channelName}. Check your connection, then close and reopen this panel.`} />
      </SectionFrame>
    )
  }
  if (!read.start_supported) return <UnsupportedSection channelName={channelName} />

  const notRunning = read.availability.state !== 'running'
  const sectionMessage = notice ?? refusal.message
  const descriptionFor = (value: Policy) => `${headingId}-${value}-help`

  return (
    <SectionFrame headingId={headingId}>
      <div className="space-y-[var(--space-2-5)]">
        <RadioGroup
          aria-labelledby={headingId}
          orientation="vertical"
          value={policy}
          onValueChange={(value) => {
            dirtyRef.current = true
            setPolicy(value as Policy)
          }}
          disabled={saving}
        >
          <RadioGroupItem value="reply_only" aria-describedby={descriptionFor('reply_only')}>
            Only answer
          </RadioGroupItem>
          <p id={descriptionFor('reply_only')} className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
            {OPTION_HELP.reply_only}
          </p>
          <RadioGroupItem value="agent_may_start" aria-describedby={descriptionFor('agent_may_start')}>
            Answer and also start messages
          </RadioGroupItem>
          <p id={descriptionFor('agent_may_start')} className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
            {OPTION_HELP.agent_may_start}
          </p>
        </RadioGroup>

        {platform === 'discord' && notRunning && (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-warning)] leading-relaxed">
            {NOT_RUNNING_DISCORD}
          </p>
        )}

        {refusal.omittedCount > 0 && (
          <Notice message={`+${refusal.omittedCount} more entries were rejected; fix the ones shown, then save again.`} />
        )}

        {policy === 'agent_may_start' && (
          <Field
            label={FIELD_LABEL}
            description={
              <>
                <p>{FIELD_HELP}</p>
                {PLATFORM_HELP[platform] && <p>{PLATFORM_HELP[platform]}</p>}
              </>
            }
          >
            <Textarea
              value={text}
              rows={4}
              aria-describedby={refusal.items.length > 0 ? fieldErrorId : undefined}
              aria-invalid={refusal.items.length > 0 ? true : undefined}
              onChange={(event) => {
                dirtyRef.current = true
                setText(event.target.value)
              }}
            />
          </Field>
        )}

        {refusal.items.length > 0 && (
          <div id={fieldErrorId} className="space-y-[var(--space-1)]">
            {refusal.items.map((item, i) => (
              <Notice key={`${item.line}-${i}`} message={`Line ${item.line}: ${item.reason}`} />
            ))}
          </div>
        )}

        {sectionMessage && <Notice message={sectionMessage} />}

        <Button
          type="button"
          variant="outline"
          className="w-full"
          disabled={saving}
          onClick={() => save(entriesOf(text))}
        >
          Save
        </Button>
      </div>
    </SectionFrame>
  )
}
