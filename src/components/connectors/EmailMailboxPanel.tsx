/**
 * EmailMailboxPanel — slide-over for configuring an agent's email mailbox
 * account.
 *
 * Email is a TOOL (not a channel). A mailbox belongs to exactly one
 * (agent, workspace) PAIR — the same agent can hold a different mailbox in
 * each workspace it belongs to (different roles, different inboxes). The
 * parent (ConnectorsScreen) owns the mailbox roster and passes the edit
 * target as the `mailbox` prop: a `Mailbox` object to edit, or `null`/undefined
 * to create a new one. The panel lets the user pick the owning agent +
 * workspace, enter IMAP/SMTP credentials, remove the mailbox, and see the
 * "heartbeat + Board tasks" explainer.
 *
 * In edit mode, both the agent select and the workspace select remain
 * changeable — changing either is a MOVE: the target identity is the pair,
 * so on save we create/update the NEW (agent, workspace) mailbox FIRST, then
 * delete the OLD one. A failed save leaves the old mailbox fully intact
 * (never delete-then-save); a failed delete-after-save still counts as a
 * successful move but surfaces a distinct warning toast — the new mailbox is
 * saved, and the orphaned old pair must be removed manually from the list.
 * Because credentials are keyed per (agent, workspace) pair server-side and
 * never transfer, a move also requires the password to be re-entered.
 *
 * Secret field (password): write-only. The backend never returns the password
 * value — the Mailbox wire type carries only a `configured: boolean` flag
 * (true when a stored credential resolves in the credential store). When
 * `configured` is true and no move is pending, the password placeholder shows
 * a "(stored — enter a new value to rotate)" hint; the field itself always
 * starts empty and never pre-fills with a real secret.
 *
 * M11 (remediation-decisions.md §M11, superseded by the multi-mailbox
 * follow-up — every (agent, workspace) pair may own a mailbox, not just one
 * system-wide):
 *   - IMAP/SMTP host+port, username, password (secret)
 *   - No inbox UI (v0.2); unhandled mail surfaces as Board tasks in that workspace
 */

import { useState, useEffect, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Eye,
  EyeSlash,
  FloppyDisk,
  Info,
  Envelope,
  Trash,
  Warning,
} from '@phosphor-icons/react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  saveAgentMailbox,
  deleteAgentMailbox,
  fetchAgents,
  fetchWorkspace,
  fetchWorkspaces,
  isWorker,
  isApiError,
} from '@/lib/api'
import type { Mailbox, MailboxConfigureRequest } from '@/lib/api'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { useUiStore } from '@/store/ui'
import { logError } from '@/lib/telemetry'

interface EmailMailboxPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Edit target. `null`/undefined opens the panel in create mode. */
  mailbox?: Mailbox | null
  /**
   * The full mailbox roster (ConnectorsScreen's `['agent-mailboxes']` query) —
   * used to block creating, or moving into, an (agent, workspace) pair that
   * already owns a mailbox. PUT is an upsert server-side, so without this
   * guard a duplicate pair would silently overwrite the existing mailbox.
   */
  mailboxes?: Mailbox[]
}

// not-wire-format: result of the save mutation — whether the (agent,
// workspace) move's old-pair cleanup succeeded, distinguishing a full
// success from a partial one (new mailbox saved, old pair orphaned).
interface SaveMailboxResult {
  saved: Mailbox
  orphanedOldPair: boolean
}

// not-wire-format: form state for the email mailbox account panel
interface MailboxFormState {
  username: string
  password: string
  imap_host: string
  imap_port: string
  smtp_host: string
  smtp_port: string
  agent_id: string
  workspace_id: string
}

const EMPTY_FORM: MailboxFormState = {
  username: '',
  password: '',
  imap_host: '',
  imap_port: '',
  smtp_host: '',
  smtp_port: '',
  agent_id: '',
  workspace_id: '',
}

// not-wire-format: client-side validation errors for the mailbox form
interface FieldErrors {
  username?: string
  password?: string
  imap_host?: string
  smtp_host?: string
  agent_id?: string
  workspace_id?: string
}

function PasswordField({
  value,
  onChange,
  hasStoredCredential,
  ariaDescribedBy,
  ariaInvalid,
}: {
  value: string
  onChange: (val: string) => void
  hasStoredCredential: boolean
  ariaDescribedBy?: string
  ariaInvalid?: boolean
}) {
  const [visible, setVisible] = useState(false)
  return (
    <div className="relative">
      <Input
        id="mailbox-password"
        data-testid="mailbox-password"
        type={visible ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={hasStoredCredential ? '(stored — enter a new value to rotate)' : 'App password or IMAP password'}
        className="pr-9 font-mono text-xs"
        autoComplete="new-password"
        aria-describedby={ariaDescribedBy}
        aria-invalid={ariaInvalid || undefined}
      />
      <button tabIndex={0}
        type="button"
        onClick={() => setVisible((v) => !v)}
        className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
        aria-label={visible ? 'Hide password' : 'Show password'}
      >
        {visible ? <EyeSlash size={13} /> : <Eye size={13} />}
      </button>
    </div>
  )
}

// FIELD_ROW_ERROR_ID / describedByFor: the validation-error association
// triple shared with ChannelConfigPanel's ChannelFieldRow — (a) the error <p>
// owns a stable id, (b) the control's aria-describedby points at it when an
// error is present, else at the help paragraph when one renders (never
// dangling), (c) aria-invalid on the control itself (wired at each call site,
// since FieldRow only owns the label/help/error chrome, not the control).
function fieldRowErrorId(id: string): string {
  return `error-${id}`
}

function describedByFor(id: string, helpId: string | undefined, error: string | undefined): string | undefined {
  if (error) return fieldRowErrorId(id)
  return helpId
}

function FieldRow({
  id,
  label,
  required,
  error,
  children,
  helpId,
  helpText,
}: {
  id: string
  label: string
  required?: boolean
  error?: string
  children: React.ReactNode
  helpId?: string
  helpText?: string
}) {
  return (
    <div className="space-y-1.5" id={`fieldrow-${id}`}>
      <Label htmlFor={id} className="text-xs font-medium text-[var(--color-secondary)]">
        {label}
        {required && <span className="text-[var(--color-error)] ml-0.5">*</span>}
      </Label>
      {children}
      {error && (
        <p id={fieldRowErrorId(id)} role="alert" className="text-[10px] text-[var(--color-error)]">{error}</p>
      )}
      {!error && helpText && (
        <p id={helpId} className="text-[10px] text-[var(--color-muted)] leading-relaxed">
          {helpText}
        </p>
      )}
    </div>
  )
}

/**
 * True when the form's currently-selected (agent, workspace) pair differs
 * from the seeded `mailbox`'s pair — i.e. Save would MOVE the mailbox rather
 * than update it in place. False in create mode (no `mailbox`) and false
 * while either id is still unselected (the initial/empty form state is not
 * a "move" — there is nothing yet to move away from).
 */
function isMoveTarget(form: MailboxFormState, mailbox: Mailbox | null | undefined): boolean {
  if (!mailbox) return false
  if (form.agent_id === '' || form.workspace_id === '') return false
  return mailbox.agent_id !== form.agent_id || mailbox.workspace_id !== form.workspace_id
}

function validate(
  form: MailboxFormState,
  mailbox: Mailbox | null | undefined,
  mailboxes: Mailbox[],
): FieldErrors {
  const errors: FieldErrors = {}
  if (!form.agent_id) errors.agent_id = 'Owning agent is required'
  if (!form.workspace_id) errors.workspace_id = 'Workspace is required'
  if (!form.username.trim()) errors.username = 'Email address is required'
  if (!form.imap_host.trim()) errors.imap_host = 'IMAP server hostname is required'
  if (!form.smtp_host.trim()) errors.smtp_host = 'SMTP server hostname is required'

  // Credentials are keyed per (agent, workspace) pair server-side — a move
  // can never carry the old password over, so re-entering it is mandatory.
  if (isMoveTarget(form, mailbox) && !form.password.trim()) {
    errors.password =
      'Moving a mailbox to a different agent or workspace requires re-entering the password — stored credentials do not transfer'
  }

  // PUT is an upsert server-side — without this guard, creating (or moving
  // into) a pair that already owns a mailbox would silently overwrite it.
  // Excludes the mailbox's OWN pair so a plain (non-move) edit of an
  // existing mailbox never trips on itself.
  if (form.agent_id && form.workspace_id) {
    const targetIsOwnPair =
      mailbox != null && mailbox.agent_id === form.agent_id && mailbox.workspace_id === form.workspace_id
    const targetTaken =
      !targetIsOwnPair &&
      mailboxes.some((mb) => mb.agent_id === form.agent_id && mb.workspace_id === form.workspace_id)
    if (targetTaken) {
      errors.agent_id = 'That agent already has a mailbox in this workspace — edit it from the list instead'
    }
  }

  return errors
}

// Visual field order (top to bottom, after the Workspace/Owning-agent swap —
// item 5) — used to focus the FIRST invalid field after a blocked Save so a
// keyboard/screen-reader user lands directly on what needs fixing, in tab
// order rather than validate()'s internal insertion order. Maps each
// FieldErrors key to the FieldRow `id` used at its call site.
const FIELD_ROW_ID: Record<keyof FieldErrors, string> = {
  workspace_id: 'mailbox-workspace',
  agent_id: 'mailbox-agent',
  username: 'mailbox-username',
  password: 'mailbox-password',
  imap_host: 'mailbox-imap-host',
  smtp_host: 'mailbox-smtp-host',
}
const FIELD_VISUAL_ORDER: (keyof FieldErrors)[] = [
  'workspace_id',
  'agent_id',
  'username',
  'password',
  'imap_host',
  'smtp_host',
]

function focusFirstInvalidField(errors: FieldErrors) {
  const firstKey = FIELD_VISUAL_ORDER.find((k) => errors[k] !== undefined)
  if (!firstKey) return
  requestAnimationFrame(() => {
    const container = document.getElementById(`fieldrow-${FIELD_ROW_ID[firstKey]}`)
    container?.querySelector<HTMLElement>('input, button, textarea, select')?.focus()
  })
}

export function EmailMailboxPanel({ open, onOpenChange, mailbox, mailboxes = [] }: EmailMailboxPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const isDirtyRef = useRef(false)
  const [form, setForm] = useState<MailboxFormState>(EMPTY_FORM)
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({})
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const { data: agents = [], isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })

  const { data: workspaces = [], isError: workspacesError } = useQuery({
    queryKey: ['workspaces'],
    queryFn: () => fetchWorkspaces(),
    enabled: open,
  })

  // Populate the form from the edit target (the parent owns the mailbox
  // roster and fetches it — this panel no longer queries ['agent-mailboxes']
  // itself). `mailbox` is null/undefined in create mode. Skip repopulating
  // while the user has unsaved edits (e.g. a background refetch of the
  // parent's list mid-edit) — the panel is always fully closed and reopened
  // between editing two different mailboxes, which resets isDirtyRef below.
  // `open` is a dependency ON PURPOSE: reopening in create mode keeps
  // `mailbox` at null (null → null never re-fires a [mailbox]-only effect),
  // which left the PREVIOUS session's form — including a typed password —
  // on screen (live-UAT find, 2026-07-03). Keying on `open` re-populates on
  // every open; the close effect below has already reset isDirtyRef.
  useEffect(() => {
    if (!open) return
    if (isDirtyRef.current) return
    if (!mailbox) {
      setForm(EMPTY_FORM)
      return
    }
    setForm({
      username: mailbox.username ?? '',
      // password is NEVER returned by the backend — always start blank
      password: '',
      imap_host: mailbox.imap_host ?? '',
      imap_port: mailbox.imap_port != null ? String(mailbox.imap_port) : '',
      smtp_host: mailbox.smtp_host ?? '',
      smtp_port: mailbox.smtp_port != null ? String(mailbox.smtp_port) : '',
      agent_id: mailbox.agent_id,
      workspace_id: mailbox.workspace_id,
    })
  }, [open, mailbox])

  // Reset on close so the next open repopulates from scratch — including the
  // form itself, so a typed (never-persisted) password does not linger in
  // memory or reappear on the next create-mode open.
  useEffect(() => {
    if (!open) {
      isDirtyRef.current = false
      setFieldErrors({})
      setForm(EMPTY_FORM)
      setShowDeleteConfirm(false)
      setDeleteError(null)
    }
  }, [open])

  function setField<K extends keyof MailboxFormState>(key: K, value: MailboxFormState[K]) {
    isDirtyRef.current = true
    setForm((prev) => ({ ...prev, [key]: value }))
    // Clear the error for this field when the user edits it
    if (key in fieldErrors) {
      setFieldErrors((prev) => {
        const next = { ...prev }
        delete next[key as keyof FieldErrors]
        return next
      })
    }
  }

  const { mutate: doSave, isPending: saving } = useMutation({
    mutationFn: async (): Promise<SaveMailboxResult> => {
      const errors = validate(form, mailbox, mailboxes)
      if (Object.keys(errors).length > 0) {
        setFieldErrors(errors)
        focusFirstInvalidField(errors)
        throw new Error('Please fill in all required fields')
      }

      // Build the request — only include password when the user entered one.
      // An omitted password leaves the stored credential untouched (write-only;
      // never clear on empty submit). Both ids ride in the PUT path — the
      // request body carries no workspace_id member.
      const req: MailboxConfigureRequest = {
        enabled: true,
        username: form.username.trim(),
        imap_host: form.imap_host.trim(),
        smtp_host: form.smtp_host.trim(),
      }
      if (form.imap_port !== '') req.imap_port = Number(form.imap_port)
      if (form.smtp_port !== '') req.smtp_port = Number(form.smtp_port)
      if (form.password !== '') req.password = form.password

      // The mailbox endpoint is keyed by the (agent, workspace) PAIR
      // (PUT /agents/{id}/mailboxes/{workspaceId}) — there is no rename.
      // Changing either the owning agent or the workspace is a MOVE: save the
      // mailbox under the NEW pair FIRST, then delete the OLD pair. A failed
      // save must leave the old mailbox fully intact — never delete-then-save.
      const moving = isMoveTarget(form, mailbox)
      const saved = await saveAgentMailbox(form.agent_id, form.workspace_id, req)

      if (moving && mailbox) {
        try {
          await deleteAgentMailbox(mailbox.agent_id, mailbox.workspace_id)
        } catch (err) {
          // The new mailbox is saved; only the old pair's cleanup failed —
          // this is still a successful save. Surface a distinct warning
          // instead of failing the whole operation; the operator can remove
          // the orphaned old pair manually from the list.
          logError({
            event: 'mailboxMoveOldPairDeleteFailed',
            agentId: mailbox.agent_id,
            workspaceId: mailbox.workspace_id,
            message: err instanceof Error ? err.message : String(err),
          })
          return { saved, orphanedOldPair: true }
        }
      }
      return { saved, orphanedOldPair: false }
    },
    onSuccess: (result) => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      if (result.orphanedOldPair) {
        addToast({
          message:
            'Mailbox saved, but the old mailbox could not be removed — delete it manually from the list.',
          variant: 'warning',
        })
      } else {
        addToast({ message: 'Mailbox account saved', variant: 'success' })
      }
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      if (err instanceof Error && err.message === 'Please fill in all required fields') return
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed',
        variant: 'error',
      })
    },
    onSettled: () => {
      // Refetch the roster on BOTH success and failure so a failed move/save
      // never leaves ConnectorsScreen showing stale mailbox state.
      queryClient.invalidateQueries({ queryKey: ['agent-mailboxes'] })
    },
  })

  const { mutate: doDelete, isPending: deleting } = useMutation({
    mutationFn: () => {
      if (!mailbox) return Promise.reject(new Error('No mailbox to remove'))
      return deleteAgentMailbox(mailbox.agent_id, mailbox.workspace_id)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agent-mailboxes'] })
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      setDeleteError(null)
      setShowDeleteConfirm(false)
      addToast({ message: 'Mailbox removed', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      // Keep the confirm dialog open and show the error inline — same pattern
      // as ConnectorsScreen's channel-instance DeleteConfirmDialog — so a
      // failed destructive delete never silently looks like it succeeded.
      const message = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to remove mailbox'
      setDeleteError(message)
      addToast({ message, variant: 'error' })
    },
  })

  // Whether a stored credential already exists (wire `configured` flag: the
  // password ref is set AND resolves in the credential store). False while a
  // move is pending — the credential is keyed to the OLD pair and will not
  // carry over, so the "(stored)" hint would be misleading.
  const hasStoredCredential = !isMoveTarget(form, mailbox) && Boolean(mailbox?.configured)

  // Non-worker agents only — email mailbox owner must be a conversational agent.
  // ADR-033 (operator-decided): the owning agent must be a core_team member
  // of the selected workspace — mirror CreateChannelSheet's ADR-029 pattern:
  // fetch the selected workspace's detail and filter the agent roster by its
  // team. No workspace selected → no agents offered (workspace picks first).
  const { data: workspaceDetail } = useQuery({
    queryKey: ['workspace', form.workspace_id],
    queryFn: () => fetchWorkspace(form.workspace_id),
    enabled: open && form.workspace_id !== '',
  })
  const coreTeam = workspaceDetail?.core_team

  const nonWorkerAgents = agents.filter((a) => !isWorker(a))
  const mainAgents = (() => {
    if (!form.workspace_id) return []
    if (!coreTeam) return nonWorkerAgents // detail loading or roster absent — don't block on it
    return nonWorkerAgents.filter((a) => coreTeam.includes(a.id))
  })()

  // A workspace change can invalidate the current agent selection (the agent
  // may not be a member of the new workspace's team) — clear it so the
  // membership rule can't be bypassed by selecting agent-then-workspace.
  useEffect(() => {
    if (!form.agent_id || !coreTeam) return
    if (!coreTeam.includes(form.agent_id)) {
      setForm((prev) => ({ ...prev, agent_id: '' }))
    }
  }, [coreTeam, form.agent_id])

  const agentItems = [
    { value: '__none__', label: '(select an agent)' },
    ...mainAgents.map((a) => ({ value: a.id, label: a.name })),
  ]

  const workspaceItems = [
    { value: '__none__', label: '(select a workspace)' },
    ...workspaces.map((w) => ({ value: w.id, label: w.name })),
  ]

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-0)] border-[var(--color-border)] overflow-y-auto p-0"
        aria-describedby="mailbox-panel-desc"
      >
        <SheetHeader className="px-6 pr-14">
          <SheetTitle className="flex items-center gap-2">
            <Envelope size={16} weight="duotone" />
            Email Mailbox Account
          </SheetTitle>
        </SheetHeader>
        <SheetDescription
          id="mailbox-panel-desc"
          className="text-xs text-[var(--color-muted)] leading-relaxed px-6 pt-3"
        >
          Configure an IMAP/SMTP mailbox for the owning agent. The agent reads
          its inbox on heartbeat and routes unhandled mail to Board tasks.
        </SheetDescription>

        <div className="px-6 pt-5 space-y-5">
          {/* Explainer callout */}
          <div
            className="flex gap-2 p-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]"
            role="note"
            aria-label="How the email mailbox works"
          >
            <Info size={13} className="text-[var(--color-accent)] shrink-0 mt-0.5" />
            <p className="text-[11px] text-[var(--color-muted)] leading-relaxed">
              The agent works this inbox on heartbeat. Mail it cannot fully handle
              becomes a Board task. A dedicated Email tab (inbox view) arrives in v0.2.
            </p>
          </div>

          {/* Ownership */}
          <div className="space-y-4 pb-3 border-b border-[var(--color-border)]">
            <h3 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
              Ownership
            </h3>

            {/* Workspace precedes Owning agent (item 5): the agent list is
                gated on the chosen workspace (core_team membership), so tab
                order must match task order — pick the workspace first. */}
            <FieldRow
              id="mailbox-workspace"
              label="Workspace"
              required
              error={fieldErrors.workspace_id}
              helpId="mailbox-workspace-help"
              helpText="The workspace this mailbox belongs to. Unhandled mail becomes Board tasks in that workspace."
            >
              {workspacesError ? (
                <p className="text-xs text-[var(--color-error)]">Could not load workspaces.</p>
              ) : (
                <SmartSelect
                  value={form.workspace_id || '__none__'}
                  onValueChange={(v) => setField('workspace_id', v === '__none__' ? '' : v)}
                  placeholder="(select a workspace)"
                  ariaLabel="Workspace"
                  items={workspaceItems}
                />
              )}
            </FieldRow>

            <FieldRow
              id="mailbox-agent"
              label="Owning agent"
              required
              error={fieldErrors.agent_id}
              helpId="mailbox-agent-help"
              helpText="The agent whose email identity this mailbox represents — only the selected workspace’s team members are listed (a mailbox’s unhandled mail becomes Board tasks in its workspace). Every (agent, workspace) pair can have its own mailbox."
            >
              {agentsError ? (
                <p className="text-xs text-[var(--color-error)]">Could not load agents.</p>
              ) : (
                <SmartSelect
                  value={form.agent_id || '__none__'}
                  onValueChange={(v) => setField('agent_id', v === '__none__' ? '' : v)}
                  placeholder={form.workspace_id ? '(select an agent)' : '(select a workspace first)'}
                  ariaLabel="Owning agent"
                  items={agentItems}
                />
              )}
            </FieldRow>
          </div>

          {/* Credentials */}
          <div className="space-y-4">
            <h3 className="text-xs font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
              Credentials
            </h3>

            <FieldRow
              id="mailbox-username"
              label="Email Address"
              required
              error={fieldErrors.username}
              helpId="mailbox-username-help"
              helpText="The email address the agent sends and receives from."
            >
              <Input
                id="mailbox-username"
                data-testid="mailbox-username"
                type="email"
                value={form.username}
                onChange={(e) => setField('username', e.target.value)}
                placeholder="bot@example.com"
                className="text-xs"
                autoComplete="email"
                aria-describedby={describedByFor('mailbox-username', 'mailbox-username-help', fieldErrors.username)}
                aria-invalid={fieldErrors.username ? true : undefined}
              />
            </FieldRow>

            <FieldRow
              id="mailbox-password"
              label="Password"
              error={fieldErrors.password}
              helpId="mailbox-password-help"
              helpText="IMAP/SMTP app password — stored encrypted, never shown. Leave blank to keep the current credential."
            >
              <PasswordField
                value={form.password}
                onChange={(v) => setField('password', v)}
                hasStoredCredential={hasStoredCredential}
                ariaDescribedBy={describedByFor('mailbox-password', 'mailbox-password-help', fieldErrors.password)}
                ariaInvalid={Boolean(fieldErrors.password)}
              />
            </FieldRow>

            <FieldRow
              id="mailbox-imap-host"
              label="IMAP Host"
              required
              error={fieldErrors.imap_host}
              helpId="mailbox-imap-host-help"
              helpText="IMAP server hostname — TLS required (port 993)."
            >
              <Input
                id="mailbox-imap-host"
                data-testid="mailbox-imap-host"
                type="text"
                value={form.imap_host}
                onChange={(e) => setField('imap_host', e.target.value)}
                placeholder="imap.gmail.com"
                className="text-xs"
                aria-describedby={describedByFor('mailbox-imap-host', 'mailbox-imap-host-help', fieldErrors.imap_host)}
                aria-invalid={fieldErrors.imap_host ? true : undefined}
              />
            </FieldRow>

            <FieldRow
              id="mailbox-smtp-host"
              label="SMTP Host"
              required
              error={fieldErrors.smtp_host}
              helpId="mailbox-smtp-host-help"
              helpText="SMTP server hostname for outbound mail (STARTTLS port 587 or SMTPS port 465)."
            >
              <Input
                id="mailbox-smtp-host"
                data-testid="mailbox-smtp-host"
                type="text"
                value={form.smtp_host}
                onChange={(e) => setField('smtp_host', e.target.value)}
                placeholder="smtp.gmail.com"
                className="text-xs"
                aria-describedby={describedByFor('mailbox-smtp-host', 'mailbox-smtp-host-help', fieldErrors.smtp_host)}
                aria-invalid={fieldErrors.smtp_host ? true : undefined}
              />
            </FieldRow>
          </div>

          {/* Advanced: ports */}
          <AdvancedDisclosure>
            <div className="space-y-4">
              <FieldRow
                id="mailbox-imap-port"
                label="IMAP Port"
                helpId="mailbox-imap-port-help"
                helpText="Default 993 (IMAPS). Override only if your server uses a non-standard port."
              >
                <Input
                  id="mailbox-imap-port"
                  type="number"
                  value={form.imap_port}
                  onChange={(e) => setField('imap_port', e.target.value)}
                  placeholder="993"
                  className="text-xs"
                  aria-describedby="mailbox-imap-port-help"
                />
              </FieldRow>

              <FieldRow
                id="mailbox-smtp-port"
                label="SMTP Port"
                helpId="mailbox-smtp-port-help"
                helpText="Default 587 (STARTTLS) or 465 (SMTPS). Override only if your server uses a non-standard port."
              >
                <Input
                  id="mailbox-smtp-port"
                  type="number"
                  value={form.smtp_port}
                  onChange={(e) => setField('smtp_port', e.target.value)}
                  placeholder="587"
                  className="text-xs"
                  aria-describedby="mailbox-smtp-port-help"
                />
              </FieldRow>
            </div>
          </AdvancedDisclosure>

          {/* Actions */}
          <div className="flex flex-col gap-2 pt-2 border-t border-[var(--color-border)]">
            <Button
              className="w-full gap-1.5"
              onClick={() => doSave()}
              disabled={saving || deleting}
            >
              <FloppyDisk size={13} />
              {saving ? 'Saving…' : 'Save Mailbox'}
            </Button>
            {mailbox && (
              <Button
                type="button"
                variant="destructive"
                className="w-full gap-1.5"
                onClick={() => setShowDeleteConfirm(true)}
                disabled={deleting || saving}
                data-testid="mailbox-delete-btn"
              >
                <Trash size={13} />
                Remove Mailbox
              </Button>
            )}
          </div>
        </div>
      </SheetContent>

      {/* Remove-mailbox confirmation — same AlertDialog pattern as
          ConnectorsScreen's channel-instance Delete: Cancel first in DOM/tab
          order (Radix focuses it by default), consequence text, inline error
          on failure so the dialog stays open rather than silently succeeding. */}
      <AlertDialog
        open={showDeleteConfirm}
        onOpenChange={(next) => {
          if (!next) setDeleteError(null)
          setShowDeleteConfirm(next)
        }}
      >
        <AlertDialogContent data-testid="mailbox-delete-confirm-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle className="font-headline text-[var(--color-secondary)]">
              Remove mailbox
            </AlertDialogTitle>
            <AlertDialogDescription className="text-sm text-[var(--color-muted)]">
              This will permanently remove the mailbox for{' '}
              <span className="font-mono text-xs font-semibold text-[var(--color-secondary)]">
                {form.username || (mailbox?.username ?? 'this agent')}
              </span>{' '}
              including its stored IMAP/SMTP credentials. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>

          {deleteError && (
            <div
              className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2"
              data-testid="mailbox-delete-error"
              role="alert"
            >
              <Warning size={14} className="text-red-400 mt-0.5 shrink-0" />
              <p className="text-xs text-red-400">{deleteError}</p>
            </div>
          )}

          <AlertDialogFooter>
            <AlertDialogCancel
              disabled={deleting}
              data-testid="mailbox-delete-cancel-btn"
            >
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={deleting}
              onClick={() => doDelete()}
              className="bg-red-600 hover:bg-red-700 text-white"
              data-testid="mailbox-delete-confirm-btn"
            >
              {deleting ? 'Removing…' : 'Remove'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Sheet>
  )
}
