// SecretRequestCard — the secure secret card (omnipus-system-agent US-6/US-7,
// FR-061, FR-062, FR-071, FR-074, FR-075, FR-083, FR-109; spec §6.3 row
// "Secret card", §6.7, §6.8; BDD-6.1, 6.5, 6.7, 12.9; F:D10).
//
// An agent asks for one to eight secret values. The card shows who asks,
// why, and exactly where each value will be used (the server's recipient
// display; for an agent-private variable, the owning workspace and agent and
// promise wording A). The values live ONLY in this card's own inputs: they
// go straight into the authenticated submit, are never put in a store, a
// draft or a log, are cleared on every terminal state and when the card
// leaves the screen, and are never re-sent automatically. No password
// re-entry (FR-083).
//
// States: pending (editable), submitting, rejected (server refused the
// values — still editable), submission unknown (the answer was lost — the
// card reads the request's true state, never re-sending), and the terminal
// states submitted ("Saved" with the reference names), cancelled, stopped,
// interrupted ("Request unavailable") and refused. A committed 503 adds a
// persistent warning that the agent's next step is not confirmed.
//
// Keyboard (FR-109, BDD-12.9): focus starts on the first field; Enter in a
// field moves to the next one and submits only from the last; keystrokes
// inside the card never reach the chat composer; errors are tied to their
// field and announced; a terminal state is announced and focus returns to
// the composer.
import { useCallback, useContext, useEffect, useRef, useState } from 'react'
import { QueryClientContext } from '@tanstack/react-query'
import { Key, LockKey } from '@phosphor-icons/react'
import type { Agent, Workspace } from '@/lib/api'
import { getSecretRequest, submitSecretRequest, workspacesQueryKeys } from '@/lib/api'
import type { SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'
import { isApiError } from '@/lib/api-error'
import {
  cardsForSession,
  isTerminalSecretState,
  useSecretRequestStore,
  type SecretRequestEntry,
} from '@/store/secretRequests'
import { useSessionStore } from '@/store/session'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

const RECIPIENT_KIND_LABEL: Record<SessionStateSecretRequest['recipient']['kind'], string> = {
  provider_key: 'AI provider key',
  mcp_env: 'Connector (MCP server) setting',
  channel_credential: 'Chat channel credential',
  mailbox_password: 'Mailbox password',
  agent_private: 'Private variable',
  unbound: 'Stored in Settings',
}

/** Promise wording A (FA:OQ-1, §6.7) for the agent-private kind. */
export const PRIVATE_SECRET_PROMISE =
  'Never typed into the chat, never in tool arguments, hidden in output where possible; a command you let the agent run can still reveal it.'

/** The promise for the integration kinds (provider, connector, channel, mailbox). */
export const INTEGRATION_SECRET_PROMISE =
  'The value never appears in the chat and can only be used for the destination shown here.'

const MIN_BYTES = 4
const MAX_BYTES = 16_384
const MAX_BODY_BYTES = 65_536

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

/** FR-071: 4–16,384 bytes of UTF-8 without NUL. Returns the error, or null. */
export function secretValueError(value: string): string | null {
  if (value.length === 0) return 'Enter a value.'
  if (value.includes('\u0000')) return 'The value contains a character that cannot be stored.'
  const bytes = byteLength(value)
  if (bytes < MIN_BYTES) return 'Too short — at least 4 characters.'
  if (bytes > MAX_BYTES) return 'Too long — at most 16,384 bytes.'
  return null
}

function focusComposer(): void {
  const composer = document.querySelector<HTMLElement>('[data-testid="chat-input"]')
  composer?.focus()
}

/** Thread-tail mount: the cards rooted in the chat that is open. */
export function SecretRequestThreadTail() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const byId = useSecretRequestStore((s) => s.byId)
  const cards = cardsForSession(byId, activeSessionId)
  if (cards.length === 0) return null
  return (
    <div className="mx-auto w-full max-w-3xl px-[var(--space-3)]" data-testid="secret-request-thread-tail">
      <div className="max-h-[60vh] overflow-y-auto">
        {cards.map((entry) => (
          <SecretRequestCard key={entry.request.request_id} entry={entry} />
        ))}
      </div>
    </div>
  )
}

export function SecretRequestCard({ entry }: { entry: SecretRequestEntry }) {
  const { request } = entry
  const terminal = isTerminalSecretState(request.state)
  // Was the person inside the card? Then a terminal state hands focus back to
  // the composer (FR-109). Tracked here because the form that held focus is
  // gone by the time the terminal record renders.
  const focusInside = useRef(false)
  useEffect(() => {
    if (terminal && focusInside.current) {
      focusInside.current = false
      focusComposer()
    }
  }, [terminal])
  return (
    <section
      onFocus={() => { focusInside.current = true }}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) focusInside.current = false
      }}
      data-testid="secret-request-card"
      data-state={entry.localPhase ?? request.state}
      aria-label={`Secret requested by ${request.acting_agent_name}`}
      // Keystrokes typed here never reach the composer or any global shortcut.
      onKeyDown={(event) => event.stopPropagation()}
      className="my-[var(--space-2)] rounded-[var(--radius-medium)] border border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-3)] text-[length:var(--type-body-compact-size)]"
    >
      <CardHeader request={request} />
      {terminal ? (
        <TerminalRecord entry={entry} />
      ) : (
        <PendingForm entry={entry} />
      )}
    </section>
  )
}

function useNames(request: SessionStateSecretRequest): { workspace: string; agent: string } | null {
  // Names from the shared caches when present (never a request); ids otherwise.
  const client = useContext(QueryClientContext)
  if (!request.owner) return null
  const workspaces = client?.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' })) ?? []
  const agents = client?.getQueryData<Agent[]>(['agents']) ?? []
  return {
    workspace: workspaces.find((w) => w.id === request.owner?.workspace_id)?.name ?? request.owner.workspace_id,
    agent: agents.find((a) => a.id === request.owner?.agent_id)?.name ?? request.owner.agent_id,
  }
}

function CardHeader({ request }: { request: SessionStateSecretRequest }) {
  const owner = useNames(request)
  const isPrivate = request.recipient.kind === 'agent_private'
  return (
    <div className="space-y-[var(--space-2)]">
      <div className="flex items-center gap-[var(--space-2)]">
        <LockKey size={16} weight="duotone" className="shrink-0 text-[var(--color-accent)]" aria-hidden="true" />
        <p className="font-medium text-[var(--color-secondary)]" data-testid="secret-request-asker">
          {request.acting_agent_name} needs a secret
          {request.origin_label ? <span className="font-[var(--font-weight-regular)] text-[var(--color-muted)]"> · {request.origin_label}</span> : null}
        </p>
      </div>
      <p className="text-[var(--color-secondary)]" data-testid="secret-request-purpose">{request.purpose}</p>
      <div className="border-l-2 border-[var(--color-border)] pl-[var(--space-2-5)]" data-testid="secret-request-recipient">
        <p className="text-[length:var(--type-caption-size)] uppercase tracking-wider text-[var(--color-muted)]">
          Used only for · {RECIPIENT_KIND_LABEL[request.recipient.kind]}
        </p>
        {request.recipient.lines.map((line) => (
          <p key={line} className="break-words text-[var(--color-secondary)]">{line}</p>
        ))}
        {owner && (
          <p className="text-[var(--color-secondary)]" data-testid="secret-request-owner">
            Belongs to {owner.agent} in {owner.workspace}
          </p>
        )}
      </div>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid="secret-request-promise">
        {isPrivate ? PRIVATE_SECRET_PROMISE : INTEGRATION_SECRET_PROMISE}
      </p>
    </div>
  )
}

/** A 403 from the CSRF check: the submit never reached the handler. */
function isCsrfRejection(error: unknown): boolean {
  return isApiError(error) && error.status === 403 && /csrf/i.test(error.body ?? '')
}

function usePendingSubmit(entry: SecretRequestEntry, clearValues: () => void) {
  const store = useSecretRequestStore
  const requestId = entry.request.request_id

  const checkState = useCallback(async () => {
    store.getState().setLocalPhase(requestId, 'unknown')
    try {
      store.getState().applyState(await getSecretRequest(requestId))
    } catch {
      // Still unknown: the card keeps saying so and offers "Check again".
    }
  }, [requestId, store])

  const send = useCallback(
    async (body: { values: Record<string, string> } | { cancel: true }) => {
      store.getState().setLocalPhase(requestId, 'submitting')
      // FR-075 (founder, 2026-10-14): the typed values leave the inputs the
      // moment they are handed to the one submit — whatever its outcome
      // (saved, refused, lost) they are never kept for a second attempt.
      clearValues()
      try {
        const outcome = await submitSecretRequest(requestId, body)
        store.getState().applyState(outcome.state, outcome.kind)
      } catch (error) {
        if (classifyMutationFailure(error) === 'uncertain') {
          await checkState()
          return
        }
        const message = isCsrfRejection(error)
          // Never replayed automatically (secrets.ts): the person sends again.
          ? 'Not sent — send again.'
          : isApiError(error) && error.status === 404
            ? 'This request is no longer available.'
            : isApiError(error) && error.status === 413
              ? 'The values are too long altogether.'
              : `Not saved: ${mutationFailureMessage(error)}`
        store.getState().setLocalPhase(requestId, 'rejected', message)
      }
    },
    [checkState, clearValues, requestId, store],
  )

  return { send, checkState }
}

function PendingForm({ entry }: { entry: SecretRequestEntry }) {
  const { request, localPhase, localMessage, checkRequired } = entry
  const [values, setValues] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string | null>(null)
  const inputs = useRef<Array<HTMLInputElement | null>>([])
  // Every input node this card ever rendered — kept so a value can be wiped
  // even from a control React has already detached (FR-075).
  const everyInput = useRef(new Set<HTMLInputElement>())
  const clearValues = useCallback(() => {
    setValues({})
    for (const node of everyInput.current) node.value = ''
  }, [])
  // Leaving the screen (terminal state, navigation, unmount) wipes the inputs.
  useEffect(() => {
    const nodes = everyInput.current
    return () => {
      for (const node of nodes) node.value = ''
      nodes.clear()
    }
  }, [])
  const { send, checkState } = usePendingSubmit(entry, clearValues)
  const busy = localPhase === 'submitting'
  const unknown = localPhase === 'unknown'
  // K1 / FR-071: a card holds 1–8 fields. Anything else is malformed and is
  // never actionable (only Cancel stays available).
  const malformed = request.fields.length < 1 || request.fields.length > 8

  // Focus starts on the first field (FR-109).
  useEffect(() => { inputs.current[0]?.focus() }, [])
  // A pending request a newer snapshot no longer lists: read its true state.
  useEffect(() => { if (checkRequired) void checkState() }, [checkRequired, checkState])

  const submit = () => {
    if (malformed) return
    const nextErrors: Record<string, string> = {}
    for (const field of request.fields) {
      const error = secretValueError(values[field.key] ?? '')
      if (error) nextErrors[field.key] = error
    }
    setErrors(nextErrors)
    const firstBad = request.fields.findIndex((field) => nextErrors[field.key])
    if (firstBad >= 0) {
      inputs.current[firstBad]?.focus()
      return
    }
    const body = { values: Object.fromEntries(request.fields.map((field) => [field.key, values[field.key] ?? ''])) }
    if (byteLength(JSON.stringify(body)) > MAX_BODY_BYTES) {
      setFormError('The values are too long altogether.')
      return
    }
    setFormError(null)
    void send(body)
  }

  const onFieldKeyDown = (index: number) => (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'Enter') return
    event.preventDefault()
    if (index < request.fields.length - 1) inputs.current[index + 1]?.focus()
    else submit()
  }

  return (
    <div className="mt-[var(--space-2-5)] space-y-[var(--space-2-5)]">
      {request.fields.map((field, index) => (
        <Field key={field.key} label={field.label} error={errors[field.key]} required>
          <Input
            ref={(node) => {
              inputs.current[index] = node
              if (node) everyInput.current.add(node)
            }}
            type="password"
            autoComplete="new-password"
            spellCheck={false}
            disabled={busy || unknown}
            value={values[field.key] ?? ''}
            data-testid={`secret-request-field-${field.key}`}
            onChange={(event) => {
              const value = event.target.value
              setValues((prev) => ({ ...prev, [field.key]: value }))
              if (errors[field.key]) setErrors((prev) => ({ ...prev, [field.key]: '' }))
            }}
            onKeyDown={onFieldKeyDown(index)}
          />
        </Field>
      ))}
      <div role="status" aria-live="polite" className="text-[length:var(--type-caption-size)]" data-testid="secret-request-status">
        {busy && <span className="text-[var(--color-muted)]">Submitting…</span>}
        {unknown && <span className="text-[var(--color-warning)]">Submission unknown — checking with the server. Your values will not be sent again.</span>}
        {localPhase === 'rejected' && localMessage && <span className="text-[var(--color-error)]">{localMessage}</span>}
      </div>
      {/* A whole-card problem is an alert, like a field error (FR-109). */}
      {(formError || malformed) && (
        <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]" data-testid="secret-request-form-error">
          {malformed ? 'This request cannot be filled in — it asks for no fields or too many. The agent can ask again.' : formError}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        {unknown ? (
          <Button type="button" variant="outline" size="sm" onClick={() => void checkState()} data-testid="secret-request-check">
            Check again
          </Button>
        ) : (
          <Button type="button" size="sm" disabled={busy || malformed} onClick={submit} data-testid="secret-request-submit">
            <Key size={13} className="mr-[var(--space-1)]" aria-hidden="true" />
            Save secret
          </Button>
        )}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={busy || unknown}
          onClick={() => void send({ cancel: true })}
          data-testid="secret-request-cancel"
        >
          Cancel
        </Button>
      </div>
    </div>
  )
}

const TERMINAL_TEXT: Record<Exclude<SessionStateSecretRequest['state'], 'pending'>, string> = {
  submitted: 'Saved',
  cancelled: 'Cancelled — nothing was stored.',
  stopped: 'Stopped — nothing was stored.',
  interrupted: 'Request unavailable — the gateway restarted before anything was stored. The agent can ask again.',
  refused: 'Refused — nothing was stored.',
}

function TerminalRecord({ entry }: { entry: SecretRequestEntry }) {
  const { request } = entry
  const state = request.state as Exclude<SessionStateSecretRequest['state'], 'pending'>
  return (
    <div className="mt-[var(--space-2-5)] flex flex-wrap items-center gap-[var(--space-2)]">
      <p role="status" aria-live="polite" className="flex-1 text-[var(--color-secondary)]" data-testid="secret-request-result">
        <span className={state === 'submitted' ? 'font-medium text-[color:var(--color-success)]' : undefined}>{TERMINAL_TEXT[state]}</span>
        {state === 'submitted' && request.reference_names && request.reference_names.length > 0 && (
          <span className="text-[var(--color-muted)]"> · stored as {request.reference_names.join(', ')}</span>
        )}
      </p>
      {!entry.continuationUnconfirmed && (
        <Button type="button" variant="ghost" size="sm" onClick={() => {
          useSecretRequestStore.getState().dismiss(request.request_id)
          focusComposer()
        }}>
          Dismiss
        </Button>
      )}
      {entry.continuationUnconfirmed && (
        <ErrorState
          className="w-full items-start py-[var(--space-0)]"
          message="Saved, but the agent's next step is not confirmed. Reopen this chat to retry. Your values will not be sent again."
        />
      )}
    </div>
  )
}
