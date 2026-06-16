/**
 * McpServerModal — Add MCP Server slide-out.
 *
 * FR-110 / US-7: converted from Dialog to Sheet (slide-out), matching
 * ChannelConfigPanel. The Sheet uses Radix DialogPrimitive under the hood,
 * which provides native focus-trap, ESC dismiss, and focus-restore on close
 * (a11y F-16 — all satisfied by the primitive; no hand-wiring needed).
 *
 * Replaces the raw transport dropdown with a two-mode picker:
 *   - "Local program" (stdio): requires Command; Args, Env, Command under
 *     AdvancedDisclosure. Follows the RiskySettingControl *pattern*
 *     (AlertDialog confirmation + standing badge) but hand-rolls it inline
 *     rather than reusing the shared component — the two-button mode picker
 *     needs a custom layout that the shared component's single-slot API
 *     cannot express without coupling.
 *   - "Network address" (sse/http): requires a URL; rejects non-https on
 *     non-loopback hosts; shows an inline SSRF caution for RFC1918/link-local
 *     literal addresses (heuristic — the real guard is backend F-G07).
 *
 * Issues #336, #356.
 */

import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Warning, Globe, Terminal } from '@phosphor-icons/react'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
} from '@/components/ui/sheet'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { addMcpServer, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface McpServerModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
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

export function McpServerModal({ open, onOpenChange }: McpServerModalProps) {
  const queryClient = useQueryClient()
  const { addToast } = useUiStore()

  const [name, setName] = useState('')
  const [mode, setMode] = useState<ConnectMode>('network')

  // local-program fields
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [env, setEnv] = useState('')

  // network-address fields
  const [url, setUrl] = useState('')

  // stdio safety gate: pendingLocal means user clicked "local program" but hasn't confirmed
  const [pendingLocal, setPendingLocal] = useState(false)
  // confirmedLocal: user has confirmed they want stdio (standing badge shows while true)
  const [confirmedLocal, setConfirmedLocal] = useState(false)

  const { mutate: doAdd, isPending } = useMutation({
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
        return addMcpServer({
          name: trimmedName,
          command: command.trim(),
          args: args.trim()
            ? args.split(',').map((a) => a.trim()).filter(Boolean)
            : undefined,
          transport: 'stdio',
          env: Object.keys(envObj).length > 0 ? envObj : undefined,
        })
      } else {
        // Network: send url field — backend stores it as cfg.URL so the MCP manager
        // (pkg/mcp/manager.go ConnectServer) can connect via StreamableClientTransport.Endpoint.
        return addMcpServer({
          name: trimmedName,
          url: url.trim(),
          transport: 'sse',
        })
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      addToast({ message: 'MCP server added', variant: 'success' })
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

  function handleClose() {
    setName('')
    setMode('network')
    setCommand('')
    setArgs('')
    setEnv('')
    setUrl('')
    setPendingLocal(false)
    setConfirmedLocal(false)
    onOpenChange(false)
  }

  function handleModeSelect(selected: ConnectMode) {
    if (selected === mode) return
    if (selected === 'local') {
      // Opening stdio requires confirmation
      setPendingLocal(true)
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

  const networkUrlValid = url.trim().length > 0 && isValidUrlScheme(url.trim())
  const networkUrlIsInternal = url.trim().length > 0 && isInternalAddress(url.trim())
  const networkUrlBadScheme =
    url.trim().length > 0 && !isValidUrlScheme(url.trim())

  const canSubmit =
    name.trim().length > 0 &&
    (mode === 'local'
      ? command.trim().length > 0
      : networkUrlValid)

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
          className="w-full sm:max-w-md flex flex-col overflow-y-auto"
          data-testid="mcp-sheet"
        >
          <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
            <SheetTitle>Add MCP Server</SheetTitle>
            <SheetDescription>
              Connect a Model Context Protocol server to extend agent capabilities.{' '}
              <a
                href="https://modelcontextprotocol.io/docs"
                target="_blank"
                rel="noopener noreferrer"
                className="underline text-[var(--color-accent)] hover:opacity-80"
              >
                Learn more
              </a>
            </SheetDescription>
          </SheetHeader>

          <div className="flex-1 py-4 space-y-4">
            {/* Server name */}
            <div className="space-y-1">
              <label htmlFor="mcp-name" className="text-xs text-[var(--color-muted)]">Name</label>
              <Input
                id="mcp-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-mcp-server"
                className="text-sm"
              />
            </div>

            {/* Connect mode */}
            <div className="space-y-2" role="group" aria-labelledby="mcp-connect-mode-label">
              <span id="mcp-connect-mode-label" className="block text-xs text-[var(--color-muted)]">Connect via</span>
              <div className="flex gap-2">
                <button
                  type="button"
                  data-testid="mode-network"
                  aria-pressed={mode === 'network'}
                  onClick={() => handleModeSelect('network')}
                  className={[
                    'flex items-center gap-1.5 px-3 py-2 rounded-md border text-xs font-medium flex-1 justify-center transition-colors',
                    mode === 'network'
                      ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
                      : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
                  ].join(' ')}
                >
                  <Globe size={13} />
                  A network address
                </button>
                <button
                  type="button"
                  data-testid="mode-local"
                  aria-pressed={mode === 'local'}
                  onClick={() => handleModeSelect('local')}
                  className={[
                    'flex items-center gap-1.5 px-3 py-2 rounded-md border text-xs font-medium flex-1 justify-center transition-colors',
                    mode === 'local'
                      ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
                      : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
                  ].join(' ')}
                >
                  <Terminal size={13} />
                  A local program
                </button>
              </div>

              {/* Standing badge: shown while local-program mode is active */}
              {mode === 'local' && confirmedLocal && (
                <div
                  className="flex items-center gap-1.5 text-[11px] text-amber-400"
                  data-testid="stdio-standing-badge"
                  role="status"
                >
                  <Warning size={13} weight="fill" />
                  <span>Runs a local program on your server</span>
                </div>
              )}
            </div>

            {/* Network mode: URL field */}
            {mode === 'network' && (
              <div className="space-y-1">
                <label htmlFor="mcp-url" className="text-xs text-[var(--color-muted)]">
                  Server URL
                </label>
                <Input
                  id="mcp-url"
                  data-testid="network-url"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  placeholder="https://mcp.example.com/sse"
                  className="text-sm font-mono"
                />
                {networkUrlBadScheme && (
                  <p className="text-[11px] text-[var(--color-error)]">
                    Use https:// (or http:// for localhost only).
                  </p>
                )}
                {networkUrlIsInternal && networkUrlValid && (
                  <div
                    className="flex items-start gap-1.5 text-[11px] text-amber-400"
                    data-testid="ssrf-caution"
                    role="status"
                  >
                    <Warning size={13} weight="fill" className="mt-0.5 shrink-0" />
                    <span>
                      This URL points to an internal or private address. Connecting to
                      internal services may expose sensitive data (SSRF risk). The
                      backend enforces the authoritative guard.
                    </span>
                  </div>
                )}
              </div>
            )}

            {/* Local program mode: command / args / env under AdvancedDisclosure */}
            {mode === 'local' && (
              <AdvancedDisclosure
                title="Command &amp; environment"
                summary="command, args, env variables"
                defaultOpen
              >
                <div className="space-y-3">
                  <div className="space-y-1">
                    <label htmlFor="mcp-command" className="text-xs text-[var(--color-muted)]">
                      Command
                    </label>
                    <Input
                      id="mcp-command"
                      data-testid="local-command"
                      value={command}
                      onChange={(e) => setCommand(e.target.value)}
                      placeholder="npx @example/mcp-server"
                      className="text-sm font-mono"
                    />
                  </div>

                  <div className="space-y-1">
                    <label htmlFor="mcp-args" className="text-xs text-[var(--color-muted)]">
                      Args (comma-separated, optional)
                    </label>
                    <Input
                      id="mcp-args"
                      value={args}
                      onChange={(e) => setArgs(e.target.value)}
                      placeholder="--port, 3000, --verbose"
                      className="text-sm font-mono"
                    />
                  </div>

                  <div className="space-y-1">
                    <label htmlFor="mcp-env" className="text-xs text-[var(--color-muted)]">
                      Environment variables (KEY=value, one per line, optional)
                    </label>
                    <textarea
                      id="mcp-env"
                      value={env}
                      onChange={(e) => setEnv(e.target.value)}
                      placeholder={"API_KEY=abc123\nDEBUG=true"}
                      rows={3}
                      className="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs font-mono text-[var(--color-secondary)] placeholder:text-[var(--color-muted)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] resize-none"
                    />
                  </div>
                </div>
              </AdvancedDisclosure>
            )}
          </div>

          <SheetFooter className="pt-4 border-t border-[var(--color-border)]">
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
              onClick={() => doAdd()}
              disabled={!canSubmit || isPending}
            >
              {isPending ? 'Adding...' : 'Add server'}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {/* Stdio safety confirmation dialog */}
      <AlertDialog
        open={pendingLocal}
        onOpenChange={(o) => {
          if (!o) handleCancelStdio()
        }}
      >
        <AlertDialogContent data-testid="stdio-confirm-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>This runs a program on your server</AlertDialogTitle>
            <AlertDialogDescription>
              A local-program MCP server launches an executable directly on the
              machine running the Omnipus gateway. Only connect servers you trust.
              The program will have access to the gateway process environment.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={handleCancelStdio}>
              Use a network address instead
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={handleConfirmStdio}
              className="bg-amber-600/20 text-amber-400 border border-amber-500/40 hover:bg-amber-600/30"
              data-testid="stdio-confirm-accept"
            >
              I understand, continue
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
