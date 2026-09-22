/**
 * McpServerModal — Add / Edit MCP Server slide-out.
 *
 * FR-110 / US-7: converted from Dialog to Sheet (slide-out), matching
 * ChannelConfigPanel. The Sheet uses Radix DialogPrimitive under the hood,
 * which provides native focus-trap, ESC dismiss, and focus-restore on close
 * (a11y F-16 — all satisfied by the primitive; no hand-wiring needed).
 *
 * Replaces the raw transport dropdown with a two-mode picker:
 *   - "Local program" (stdio): requires Command; Args, Env, Env file under
 *     AdvancedDisclosure. Follows the RiskySettingControl *pattern* (AlertDialog
 *     confirmation + standing badge) but hand-rolls it inline rather than reusing
 *     the shared component — the two-button mode picker needs a custom layout that
 *     the shared component's single-slot API cannot express without coupling.
 *   - "Network address" (sse/http): requires a URL; rejects non-https on
 *     non-loopback hosts; shows an inline SSRF caution for RFC1918/link-local
 *     literal addresses (heuristic — the real guard is backend F-G07). Also
 *     supports Headers key/value editor under AdvancedDisclosure.
 *
 * G8: edit mode — pass `initialServer` to pre-populate all fields; the modal
 * title becomes "Edit MCP server" and the submit path calls updateMcpServer
 * with only the changed fields.
 *
 * G9: new inputs — Headers (sse/http), Env file (stdio).
 *
 * Issues #336, #356.
 */

import { useState, useEffect } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Warning, Globe, Terminal, Plus, Trash } from '@phosphor-icons/react'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
} from '@/components/ui/sheet'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { addMcpServer, updateMcpServer, isApiError, type McpServer, type McpServerUpdate } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface McpServerModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** When provided the modal opens in edit mode, pre-populated from this server. */
  initialServer?: McpServer
}

type ConnectMode = 'local' | 'network'

/**
 * Best-effort literal-host heuristic: returns true when the URL's hostname
 * looks like a private, loopback, link-local, or mDNS address.
 *
 * Scope and limitations:
 * - Only inspects the literal hostname string — NO DNS resolution is performed.
 *   A hostname like "internal.corp.example.com" is not flagged even if it
 *   resolves to a private IP.
 * - IPv6 coverage: loopback (::1), ULA (fc00::/7), link-local (fe80::/10),
 *   and IPv4-mapped loopback (::ffff:127.x.x.x). Bracket notation is stripped.
 * - This is a UI hint only. The backend (F-G07) is the authoritative SSRF guard
 *   and must not be bypassed regardless of what this function returns.
 */
function isInternalAddress(url: string): boolean {
  try {
    const parsed = new URL(url)
    // Strip surrounding brackets from IPv6 addresses (URL.hostname preserves them)
    const host = parsed.hostname.replace(/^\[|\]$/g, '').toLowerCase()
    if (host === 'localhost' || host.endsWith('.local')) return true
    // IPv4 loopback (127.0.0.0/8)
    if (/^127\./.test(host)) return true
    // RFC1918 private ranges
    if (/^10\./.test(host)) return true
    if (/^192\.168\./.test(host)) return true
    // 172.16.0.0/12
    const m = host.match(/^172\.(\d{1,3})\./)
    if (m && parseInt(m[1], 10) >= 16 && parseInt(m[1], 10) <= 31) return true
    // IPv4 link-local (169.254.0.0/16)
    if (/^169\.254\./.test(host)) return true
    // IPv6 loopback (::1)
    if (host === '::1') return true
    // IPv6 ULA (fc00::/7 — starts with fc or fd)
    if (/^f[cd]/i.test(host)) return true
    // IPv6 link-local (fe80::/10 — starts with fe8, fe9, fea, feb)
    if (/^fe[89ab]/i.test(host)) return true
    // IPv4-mapped IPv6 loopback (::ffff:127.x.x.x)
    if (/^::ffff:127\./i.test(host)) return true
    return false
  } catch {
    return false
  }
}

/**
 * Returns true if the URL scheme is acceptable.
 * https is always accepted; http is only accepted for loopback/localhost.
 */
function isValidUrlScheme(url: string): boolean {
  try {
    const parsed = new URL(url)
    if (parsed.protocol === 'https:') return true
    if (parsed.protocol === 'http:') {
      const host = parsed.hostname.toLowerCase()
      return (
        host === 'localhost' ||
        /^127\./.test(host) ||
        host === '::1' ||
        host === '[::1]'
      )
    }
    return false
  } catch {
    return false
  }
}

/** A single key/value row in the headers editor. */
interface KVRow {
  key: string
  value: string
}

/** Converts KVRow[] to a Record<string,string>, dropping empty keys. */
function rowsToRecord(rows: KVRow[]): Record<string, string> | undefined {
  const result: Record<string, string> = {}
  for (const row of rows) {
    if (row.key.trim()) {
      result[row.key.trim()] = row.value
    }
  }
  return Object.keys(result).length > 0 ? result : undefined
}

/** Infers the initial ConnectMode from a server's transport. */
function transportToMode(transport: 'stdio' | 'sse' | 'http' | undefined): ConnectMode {
  return transport === 'stdio' ? 'local' : 'network'
}

/**
 * Invalidate every cache an MCP server mutation can affect, beyond the
 * server list itself. Adding/editing/removing a server now reconciles the
 * live MCP manager (connect/disconnect + tool registration) synchronously
 * server-side, which changes what the central tool registry and per-agent
 * tool lists report — so the policy editors (Settings → Security,
 * ToolsAndPermissions) and the Skills screen's own tools tab must refetch
 * too, or they keep showing stale (pre-reconcile) tool counts / MCP
 * sections.
 */
function invalidateMcpToolCaches(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
  queryClient.invalidateQueries({ queryKey: ['tools-builtin'] }) // SecuritySection global editor
  queryClient.invalidateQueries({ queryKey: ['registry-tools'] }) // ToolsAndPermissions per-agent editor
  queryClient.invalidateQueries({ queryKey: ['agent-tools'] }) // prefix match — every agent
  queryClient.invalidateQueries({ queryKey: ['tools'] }) // SkillsScreen's own tools tab
}

export function McpServerModal({ open, onOpenChange, initialServer }: McpServerModalProps) {
  const queryClient = useQueryClient()
  const { addToast } = useUiStore()

  const editMode = initialServer !== undefined

  const [name, setName] = useState('')
  const [mode, setMode] = useState<ConnectMode>('network')

  // local-program fields
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [env, setEnv] = useState('')
  const [envFile, setEnvFile] = useState('')

  // network-address fields
  const [url, setUrl] = useState('')
  const [headerRows, setHeaderRows] = useState<KVRow[]>([{ key: '', value: '' }])

  // stdio safety gate: pendingLocal means user clicked "local program" but hasn't confirmed
  const [pendingLocal, setPendingLocal] = useState(false)
  // confirmedLocal: user has confirmed they want stdio (standing badge shows while true)
  const [confirmedLocal, setConfirmedLocal] = useState(false)

  // Populate fields when initialServer changes (edit mode open).
  useEffect(() => {
    if (open && initialServer) {
      setName(initialServer.name)
      const m = transportToMode(initialServer.transport)
      setMode(m)
      if (m === 'local') {
        setConfirmedLocal(true)
      }
      // Pre-fill non-secret config fields (#437). env/headers VALUES are never
      // returned (secrets), so the inputs stay blank — "leave blank to keep
      // current". The set keys are surfaced read-only next to each editor below
      // via initialServer.env_keys / header_names ("currently set: …").
      setCommand(initialServer.command ?? '')
      setUrl(initialServer.url ?? '')
      setArgs((initialServer.args ?? []).join(', '))
      setEnvFile(initialServer.env_file ?? '')
    } else if (!open) {
      // Reset all state on close
      setName('')
      setMode('network')
      setCommand('')
      setArgs('')
      setEnv('')
      setEnvFile('')
      setUrl('')
      setHeaderRows([{ key: '', value: '' }])
      setPendingLocal(false)
      setConfirmedLocal(false)
    }
  }, [open, initialServer])

  const { mutate: doAdd, isPending: isAdding } = useMutation({
    mutationFn: () => {
      const trimmedName = name.trim()
      if (mode === 'local') {
        // Parse env key=value pairs
        const envObj: Record<string, string> = {}
        env.split('\n').forEach((line) => {
          const eq = line.indexOf('=')
          if (eq > 0) {
            envObj[line.slice(0, eq).trim()] = line.slice(eq + 1).trim()
          }
        })
        const envFileTrimmed = envFile.trim() || undefined
        return addMcpServer({
          name: trimmedName,
          command: command.trim(),
          args: args.trim()
            ? args.split(',').map((a) => a.trim()).filter(Boolean)
            : undefined,
          transport: 'stdio',
          env: Object.keys(envObj).length > 0 ? envObj : undefined,
          env_file: envFileTrimmed,
        })
      } else {
        // Network: send url field — backend stores it as cfg.URL so the MCP manager
        // (pkg/mcp/manager.go ConnectServer) can connect via StreamableClientTransport.Endpoint.
        const headers = rowsToRecord(headerRows)
        return addMcpServer({
          name: trimmedName,
          url: url.trim(),
          transport: 'sse',
          headers,
        })
      }
    },
    onSuccess: (server) => {
      invalidateMcpToolCaches(queryClient)
      // Config write can succeed while the live connect does not — 'error' (bad
      // command/URL, auth, network) and 'disconnected' (e.g. MCP globally
      // disabled, not yet reconciled) both mean "not actually usable yet".
      // Reflect that honestly instead of a blanket "added"; the plain success
      // toast is reserved for a proven live connection.
      if (server.status !== 'connected') {
        addToast({
          message: 'Server added, but not currently connected — check the command/URL and use Test.',
          variant: 'warning',
        })
      } else {
        addToast({ message: 'MCP server added', variant: 'success' })
      }
      handleClose()
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err)
          ? err.userMessage
          : err instanceof Error
          ? err.message
          : 'Failed to add MCP server',
        variant: 'error',
      }),
  })

  const { mutate: doPatch, isPending: isPatching } = useMutation({
    mutationFn: () => {
      if (!initialServer) throw new Error('No server to edit')
      // Name is immutable on PATCH — McpServerUpdate has no `name` field
      // (additionalProperties:false), so renaming requires delete + re-add.
      // Fields left blank are NOT sent: the backend merges (omitted = preserved),
      // so a blank input keeps the current value (it does not clear it).
      const patch: McpServerUpdate = {}
      if (mode === 'local') {
        const envObj: Record<string, string> = {}
        env.split('\n').forEach((line) => {
          const eq = line.indexOf('=')
          if (eq > 0) {
            envObj[line.slice(0, eq).trim()] = line.slice(eq + 1).trim()
          }
        })
        if (command.trim()) patch.command = command.trim()
        if (args.trim()) {
          patch.args = args.split(',').map((a) => a.trim()).filter(Boolean)
        }
        if (Object.keys(envObj).length > 0) patch.env = envObj
        const envFileTrimmed = envFile.trim()
        if (envFileTrimmed) patch.env_file = envFileTrimmed
      } else {
        if (url.trim()) patch.url = url.trim()
        const headers = rowsToRecord(headerRows)
        if (headers) patch.headers = headers
      }
      return updateMcpServer(initialServer.id, patch)
    },
    onSuccess: (server) => {
      invalidateMcpToolCaches(queryClient)
      if (server.status !== 'connected') {
        addToast({
          message: 'Server updated, but not currently connected — check the command/URL and use Test.',
          variant: 'warning',
        })
      } else {
        addToast({ message: 'MCP server updated', variant: 'success' })
      }
      handleClose()
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err)
          ? err.userMessage
          : err instanceof Error
          ? err.message
          : 'Failed to update MCP server',
        variant: 'error',
      }),
  })

  const isPending = isAdding || isPatching

  function handleClose() {
    onOpenChange(false)
  }

  function handleModeSelect(selected: ConnectMode) {
    if (selected === mode) return
    if (selected === 'local') {
      // Opening stdio requires confirmation (skip if already confirmed in edit mode)
      if (confirmedLocal) {
        setMode('local')
      } else {
        setPendingLocal(true)
      }
    } else {
      setMode('network')
      setConfirmedLocal(false)
    }
  }

  function handleConfirmStdio() {
    setMode('local')
    setConfirmedLocal(true)
    setPendingLocal(false)
  }

  function handleCancelStdio() {
    setPendingLocal(false)
    // Always revert to network when user cancels the stdio confirm
    setMode('network')
  }

  function handleAddHeaderRow() {
    setHeaderRows((prev) => [...prev, { key: '', value: '' }])
  }

  function handleRemoveHeaderRow(idx: number) {
    setHeaderRows((prev) => prev.filter((_, i) => i !== idx))
  }

  function handleHeaderRowChange(idx: number, field: 'key' | 'value', val: string) {
    setHeaderRows((prev) => prev.map((row, i) => i === idx ? { ...row, [field]: val } : row))
  }

  const networkUrlValid = url.trim().length > 0 && isValidUrlScheme(url.trim())
  const networkUrlIsInternal = url.trim().length > 0 && isInternalAddress(url.trim())
  const networkUrlBadScheme =
    url.trim().length > 0 && !isValidUrlScheme(url.trim())

  const canSubmit =
    name.trim().length > 0 &&
    (mode === 'local'
      ? command.trim().length > 0
      : networkUrlValid)

  // In edit mode, name is display-only (immutable via PATCH), so it does NOT
  // gate submission — canSubmitEdit ignores it. A blank URL is allowed (means
  // "keep the current URL"); a non-blank URL must still be valid.
  const canSubmitEdit = editMode
    ? (mode === 'local' ? true : networkUrlValid || url.trim().length === 0)
    : canSubmit

  return (
    <>
      {/*
        Sheet — FR-110 / US-7.
        Focus-trap, ESC dismiss, and focus-restore are provided natively by
        Radix DialogPrimitive (the Sheet primitive is built on it).
        a11y F-16: all three behaviours are satisfied without any custom wiring.
      */}
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent
          side="right"
          className="w-full sm:max-w-md flex flex-col overflow-y-auto p-0"
          data-testid="mcp-sheet"
        >
          <SheetHeader className="px-[var(--space-4)] pr-[var(--space-7)]">
            <SheetTitle>{editMode ? 'Edit MCP server' : 'Add MCP Server'}</SheetTitle>
          </SheetHeader>
          <SheetDescription className="px-[var(--space-4)] pt-[var(--space-2-5)]">
            {editMode
              ? 'Update the configuration for this MCP server.'
              : <>Connect a Model Context Protocol server to extend agent capabilities.{' '}
                <a tabIndex={0}
                  href="https://modelcontextprotocol.io/docs"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline text-[var(--color-accent)] hover:opacity-80"
                >
                  Learn more
                </a></>
            }
          </SheetDescription>

          <div className="flex-1 px-[var(--space-4)] py-[var(--space-3)] space-y-[var(--space-3)]">
            {/* Server name */}
            <div className="space-y-[var(--space-2)]">
              <Label htmlFor="mcp-name">Name</Label>
              {editMode ? (
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] px-[var(--space-1)] py-[var(--space-2)]">{name}</p>
              ) : (
                <Input
                  id="mcp-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="my-mcp-server"
                  className="text-[length:var(--type-body-compact-size)]"
                  autoFocus
                />
              )}
            </div>

            {/* Connect mode */}
            <div className="space-y-[var(--space-2)]">
              <span id="mcp-connect-mode-label" className="block text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Connect via</span>
              <SegmentedControl
                aria-labelledby="mcp-connect-mode-label"
                value={mode}
                onValueChange={(v) => handleModeSelect(v as ConnectMode)}
                className="flex w-full"
              >
                <SegmentedControlItem
                  value="network"
                  data-testid="mode-network"
                  className="flex-1 justify-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)]"
                >
                  <Globe size={13} />
                  A network address
                </SegmentedControlItem>
                <SegmentedControlItem
                  value="local"
                  data-testid="mode-local"
                  className="flex-1 justify-center gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)]"
                >
                  <Terminal size={13} />
                  A local program
                </SegmentedControlItem>
              </SegmentedControl>

              {/* Standing badge: shown while local-program mode is active */}
              {mode === 'local' && confirmedLocal && (
                <div
                  className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"
                  data-testid="stdio-standing-badge"
                  role="status"
                >
                  <Warning size={13} weight="fill" />
                  <span>Runs a local program on your server</span>
                </div>
              )}
            </div>

            {/* Network mode: URL field + Headers (G9) */}
            {mode === 'network' && (
              <>
                <div className="space-y-[var(--space-2)]">
                  <Label htmlFor="mcp-url">
                    Server URL
                  </Label>
                  <Input
                    id="mcp-url"
                    data-testid="network-url"
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                    placeholder="https://mcp.example.com/sse"
                    className="text-[length:var(--type-body-compact-size)] font-mono"
                  />
                  {networkUrlBadScheme && (
                    <p className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">
                      Use https:// (or http:// for localhost only).
                    </p>
                  )}
                  {networkUrlIsInternal && networkUrlValid && (
                    <div
                      className="flex items-start gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"
                      data-testid="ssrf-caution"
                      role="status"
                    >
                      <Warning size={13} weight="fill" className="mt-[var(--space-0-5)] shrink-0" />
                      <span>
                        This URL points to an internal or private address. Connecting to
                        internal services may expose sensitive data (SSRF risk). The
                        backend enforces the authoritative guard.
                      </span>
                    </div>
                  )}
                </div>

                {/* G9: Headers key/value editor (sse/http only) */}
                <AdvancedDisclosure
                  title="Advanced"
                  summary="headers"
                  defaultOpen={(initialServer?.header_names?.length ?? 0) > 0}
                >
                  <div className="space-y-[var(--space-2-5)]">
                    <div className="space-y-[var(--space-1)]">
                      <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">HTTP headers (optional)</span>
                      {(initialServer?.header_names?.length ?? 0) > 0 && (
                        <p
                          className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
                          data-testid="header-names-set"
                        >
                          Currently set (values hidden — re-enter to replace):{' '}
                          {initialServer!.header_names!.join(', ')}
                        </p>
                      )}
                      <div className="space-y-[var(--space-1)]">
                        {headerRows.map((row, idx) => (
                          <div key={idx} className="flex gap-[var(--space-1)] items-center">
                            <Input
                              value={row.key}
                              onChange={(e) => handleHeaderRowChange(idx, 'key', e.target.value)}
                              placeholder="Header-Name"
                              className="text-[length:var(--type-utility-xs-size)] font-mono flex-1"
                              data-testid={`header-key-${idx}`}
                            />
                            <Input
                              value={row.value}
                              onChange={(e) => handleHeaderRowChange(idx, 'value', e.target.value)}
                              placeholder="value"
                              className="text-[length:var(--type-utility-xs-size)] font-mono flex-1"
                              data-testid={`header-value-${idx}`}
                            />
                            {headerRows.length > 1 && (
                              <IconButton
                                type="button"
                                size="sm"
                                variant="ghost"
                                onClick={() => handleRemoveHeaderRow(idx)}
                                className="h-auto w-auto p-[var(--space-1)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)] shrink-0"
                                aria-label={`Remove header ${row.key.trim() || `row ${idx + 1}`}`}
                              >
                                <Trash size={13} />
                              </IconButton>
                            )}
                          </div>
                        ))}
                        <Button
                          type="button"
                          variant="link"
                          onClick={handleAddHeaderRow}
                          className="gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                          data-testid="add-header-row"
                        >
                          <Plus size={11} /> Add header
                        </Button>
                      </div>
                    </div>

                  </div>
                </AdvancedDisclosure>
              </>
            )}

            {/* Local program mode: command / args / env / env_file (G9) */}
            {mode === 'local' && (
              <AdvancedDisclosure
                title="Command &amp; environment"
                summary="command, args, env variables"
                defaultOpen
              >
                <div className="space-y-[var(--space-2-5)]">
                  <div className="space-y-[var(--space-2)]">
                    <Label htmlFor="mcp-command">
                      Command
                    </Label>
                    <Input
                      id="mcp-command"
                      data-testid="local-command"
                      value={command}
                      onChange={(e) => setCommand(e.target.value)}
                      placeholder="npx @example/mcp-server"
                      className="text-[length:var(--type-body-compact-size)] font-mono"
                    />
                  </div>

                  <div className="space-y-[var(--space-2)]">
                    <Label htmlFor="mcp-args">
                      Args (comma-separated, optional)
                    </Label>
                    <Input
                      id="mcp-args"
                      value={args}
                      onChange={(e) => setArgs(e.target.value)}
                      placeholder="--port, 3000, --verbose"
                      className="text-[length:var(--type-body-compact-size)] font-mono"
                    />
                  </div>

                  <div className="space-y-[var(--space-2)]">
                    <Label htmlFor="mcp-env">
                      Environment variables (KEY=value, one per line, optional)
                    </Label>
                    {(initialServer?.env_keys?.length ?? 0) > 0 && (
                      <p
                        className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
                        data-testid="env-keys-set"
                      >
                        Currently set (values hidden — re-enter to replace):{' '}
                        {initialServer!.env_keys!.join(', ')}
                      </p>
                    )}
                    <textarea tabIndex={0}
                      id="mcp-env"
                      value={env}
                      onChange={(e) => setEnv(e.target.value)}
                      placeholder={"API_KEY=abc123\nDEBUG=true"}
                      rows={3}
                      className="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-secondary)] placeholder:text-[var(--color-muted)] focus:outline-none resize-none"
                    />
                  </div>

                  {/* G9: Env file (stdio only) */}
                  <div className="space-y-[var(--space-2)]">
                    <Label htmlFor="mcp-env-file">
                      Env file path (optional)
                    </Label>
                    <Input
                      id="mcp-env-file"
                      data-testid="env-file"
                      value={envFile}
                      onChange={(e) => setEnvFile(e.target.value)}
                      placeholder="/etc/omnipus/mcp-server.env"
                      className="text-[length:var(--type-utility-xs-size)] font-mono"
                    />
                  </div>

                </div>
              </AdvancedDisclosure>
            )}
          </div>

          <SheetFooter className="pt-[var(--space-3)] border-t border-[var(--color-border)]">
            <Button
              variant="outline"
              size="sm"
              onClick={handleClose}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              data-testid="submit-add"
              onClick={() => editMode ? doPatch() : doAdd()}
              disabled={!(editMode ? canSubmitEdit : canSubmit) || isPending}
            >
              {isPending
                ? (editMode ? 'Saving...' : 'Adding...')
                : (editMode ? 'Save changes' : 'Add server')
              }
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Stdio safety confirmation dialog — follows the RiskySettingControl
          pattern (see file header): `emphasis="cancel"` favors the safe
          "network address" path as the primary action, de-emphasizing the
          "run a local program" confirm. */}
      <ConfirmDialog
        open={pendingLocal}
        onOpenChange={(o) => {
          if (!o) handleCancelStdio()
        }}
        title="This runs a program on your server"
        description="A local-program MCP server launches an executable directly on the machine running the Omnipus gateway. Only connect servers you trust. The program will have access to the gateway process environment."
        cancelLabel="Use a network address instead"
        confirmLabel="I understand, continue"
        emphasis="cancel"
        onConfirm={handleConfirmStdio}
      />
    </>
  )
}
