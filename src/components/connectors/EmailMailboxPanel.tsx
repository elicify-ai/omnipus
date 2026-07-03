/**
 * EmailMailboxPanel — slide-over for configuring the email mailbox account.
 *
 * Email is a TOOL (not a channel). The mailbox is per-(agent, workspace), cap-1
 * in 0.1.0. The panel lets the user pick the owning agent + workspace, enter
 * IMAP/SMTP credentials, and see the "heartbeat + Board tasks" explainer.
 *
 * Secret field (password): write-only. If the backend returns no password value
 * (which it never does — it is credential-store-routed), the field shows an
 * empty placeholder so the user can set or rotate the credential. A "(stored)"
 * hint appears when the GET response includes password_ref, indicating that a
 * credential is already saved. The field never pre-fills with a real secret.
 *
 * M11 (remediation-decisions.md §M11, 0.1.0 scope):
 *   - One mailbox, belonging to the Assistant agent in My Workspace
 *   - IMAP/SMTP host+port, username, password (secret)
 *   - No inbox UI (v0.2); unhandled mail surfaces as Board tasks
 */

import { useState, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Eye,
  EyeSlash,
  FloppyDisk,
  Info,
  Envelope,
} from '@phosphor-icons/react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  findConfiguredMailbox,
  saveAgentMailbox,
  deleteAgentMailbox,
  fetchAgents,
  fetchWorkspaces,
  isWorker,
  isApiError,
} from '@/lib/api'
import type { MailboxConfigureRequest } from '@/lib/api'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { useUiStore } from '@/store/ui'

interface EmailMailboxPanelProps {
  open: boolean
  onOpenChange: (open: boolean) => void
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
}: {
  value: string
  onChange: (val: string) => void
  hasStoredCredential: boolean
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
        aria-describedby="mailbox-password-help"
      />
      <button
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
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs font-medium text-[var(--color-secondary)]">
        {label}
        {required && <span className="text-[var(--color-error)] ml-0.5">*</span>}
      </Label>
      {children}
      {error && (
        <p className="text-[10px] text-[var(--color-error)]">{error}</p>
      )}
      {!error && helpText && (
        <p id={helpId} className="text-[10px] text-[var(--color-muted)] leading-relaxed">
          {helpText}
        </p>
      )}
    </div>
  )
}

function validate(form: MailboxFormState): FieldErrors {
  const errors: FieldErrors = {}
  if (!form.agent_id) errors.agent_id = 'Owning agent is required'
  if (!form.workspace_id) errors.workspace_id = 'Workspace is required'
  if (!form.username.trim()) errors.username = 'Email address is required'
  if (!form.imap_host.trim()) errors.imap_host = 'IMAP server hostname is required'
  if (!form.smtp_host.trim()) errors.smtp_host = 'SMTP server hostname is required'
  return errors
}

export function EmailMailboxPanel({ open, onOpenChange }: EmailMailboxPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const isDirtyRef = useRef(false)
  const [form, setForm] = useState<MailboxFormState>(EMPTY_FORM)
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({})

  const { data: agents = [], isError: agentsError, isSuccess: agentsLoaded } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })

  // The mailbox is per-agent (cap-1 in 0.1.0): probe the non-worker agents'
  // mailbox endpoints in parallel (404-tolerant) and use the first hit.
  // (The legacy GET /channels/email path is dead — "email" is not a channel
  // type, so the ADR-029 grammar gate rejects it.)
  const { data: mailbox, isLoading: configLoading } = useQuery({
    queryKey: ['agent-mailboxes'],
    queryFn: () => findConfiguredMailbox(agents.filter((a) => !isWorker(a)).map((a) => a.id)),
    enabled: open && agentsLoaded,
  })

  const { data: workspaces = [], isError: workspacesError } = useQuery({
    queryKey: ['workspaces'],
    queryFn: () => fetchWorkspaces(),
    enabled: open,
  })

  // Populate form from the configured mailbox; skip if user has unsaved edits.
  useEffect(() => {
    if (!mailbox) return
    if (isDirtyRef.current) return
    setForm({
      username: mailbox.username ?? '',
      // password is NEVER returned by the backend — always start blank
      password: '',
      imap_host: mailbox.imap_host ?? '',
      imap_port: mailbox.imap_port != null ? String(mailbox.imap_port) : '',
      smtp_host: mailbox.smtp_host ?? '',
      smtp_port: mailbox.smtp_port != null ? String(mailbox.smtp_port) : '',
      agent_id: mailbox.agent_id,
      workspace_id: mailbox.workspace_id ?? '',
    })
  }, [mailbox])

  // Reset dirty flag when the panel closes so the next open repopulates from server.
  useEffect(() => {
    if (!open) {
      isDirtyRef.current = false
      setFieldErrors({})
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
    mutationFn: async () => {
      const errors = validate(form)
      if (Object.keys(errors).length > 0) {
        setFieldErrors(errors)
        throw new Error('Please fill in all required fields')
      }

      // Build the request — only include password when the user entered one.
      // An omitted password leaves the stored credential untouched (write-only;
      // never clear on empty submit).
      const req: MailboxConfigureRequest = {
        enabled: true,
        workspace_id: form.workspace_id,
        username: form.username.trim(),
        imap_host: form.imap_host.trim(),
        smtp_host: form.smtp_host.trim(),
      }
      if (form.imap_port !== '') req.imap_port = Number(form.imap_port)
      if (form.smtp_port !== '') req.smtp_port = Number(form.smtp_port)
      if (form.password !== '') req.password = form.password

      // Cap-1: when the owning agent changes, delete the old agent's mailbox
      // first — the backend rejects a second enabled mailbox in the workspace.
      if (mailbox && mailbox.agent_id !== form.agent_id) {
        await deleteAgentMailbox(mailbox.agent_id)
      }
      return saveAgentMailbox(form.agent_id, req)
    },
    onSuccess: () => {
      isDirtyRef.current = false
      queryClient.invalidateQueries({ queryKey: ['agent-mailboxes'] })
      queryClient.invalidateQueries({ queryKey: ['channels'] })
      addToast({ message: 'Mailbox account saved', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      if (err instanceof Error && err.message === 'Please fill in all required fields') return
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed',
        variant: 'error',
      })
    },
  })

  // Whether a stored credential already exists (wire `configured` flag: the
  // password ref is set AND resolves in the credential store).
  const hasStoredCredential = Boolean(mailbox?.configured)

  // Non-worker agents only — email mailbox owner must be a conversational agent.
  const mainAgents = agents.filter((a) => !isWorker(a))

  const agentItems = [
    { value: '__none__', label: '(select an agent)' },
    ...mainAgents.map((a) => ({ value: a.id, label: a.name })),
  ]

  const workspaceItems = [
    { value: '__none__', label: '(select a workspace)' },
    ...workspaces.map((w) => ({ value: w.id, label: w.name })),
  ]

  const isLoading = configLoading && agentsLoaded

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
        aria-describedby="mailbox-panel-desc"
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)] flex items-center gap-2">
            <Envelope size={16} weight="duotone" />
            Email Mailbox Account
          </SheetTitle>
          <SheetDescription
            id="mailbox-panel-desc"
            className="text-xs text-[var(--color-muted)] leading-relaxed"
          >
            Configure an IMAP/SMTP mailbox for the owning agent. The agent reads
            its inbox on heartbeat and routes unhandled mail to Board tasks.
          </SheetDescription>
        </SheetHeader>

        {isLoading ? (
          <div className="space-y-4 pt-6">
            {[1, 2, 3, 4].map((i) => (
              <div key={i} className="h-10 rounded-md bg-[var(--color-surface-2)] animate-pulse" />
            ))}
          </div>
        ) : (
          <div className="pt-5 space-y-5">
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

              <FieldRow
                id="mailbox-agent"
                label="Owning agent"
                required
                error={fieldErrors.agent_id}
                helpId="mailbox-agent-help"
                helpText="The agent whose email identity this mailbox represents (one mailbox per agent for now)."
              >
                {agentsError ? (
                  <p className="text-xs text-[var(--color-error)]">Could not load agents.</p>
                ) : (
                  <SmartSelect
                    value={form.agent_id || '__none__'}
                    onValueChange={(v) => setField('agent_id', v === '__none__' ? '' : v)}
                    placeholder="(select an agent)"
                    items={agentItems}
                  />
                )}
              </FieldRow>

              <FieldRow
                id="mailbox-workspace"
                label="Workspace"
                required
                error={fieldErrors.workspace_id}
                helpId="mailbox-workspace-help"
                helpText="The workspace where this mailbox surfaces (Board tasks land here)."
              >
                {workspacesError ? (
                  <p className="text-xs text-[var(--color-error)]">Could not load workspaces.</p>
                ) : (
                  <SmartSelect
                    value={form.workspace_id || '__none__'}
                    onValueChange={(v) => setField('workspace_id', v === '__none__' ? '' : v)}
                    placeholder="(select a workspace)"
                    items={workspaceItems}
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
                  aria-describedby="mailbox-username-help"
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
                  aria-describedby="mailbox-imap-host-help"
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
                  aria-describedby="mailbox-smtp-host-help"
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
                disabled={saving}
              >
                <FloppyDisk size={13} />
                {saving ? 'Saving…' : 'Save Mailbox'}
              </Button>
            </div>
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
