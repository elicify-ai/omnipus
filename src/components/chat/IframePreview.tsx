/**
 * IframePreview — chat preview-LINK renderer for the `web_serve` tool result
 * (dev + static modes), and the legacy replay aliases ServeWorkspaceUI /
 * RunInWorkspaceUI which delegate through makeWebServeUI (which wraps
 * WebServeBlock, the canonical caller for both).
 *
 * ADR-044 (preview-on-main-listener) retires the embedded same-origin iframe
 * entirely. `/preview/` now shares the SPA's own gateway listener — there is
 * no separate preview port/origin — so ANY iframe embed here would be
 * same-origin with the SPA. That is exactly the isolation dilemma ADR-044's
 * D6 rules out: a sandboxed same-origin iframe can't hold the previewed
 * app's own login (allow-same-origin must be stripped to prevent full SPA
 * compromise, which breaks localStorage/cookie access inside the frame); an
 * un-sandboxed one could reach `window.parent`. Neither is acceptable, so
 * this component no longer mounts an iframe at all:
 *
 *   - the preview renders as a plain clickable link (`target="_blank"`) —
 *     the user opens it in their OWN top-level browser tab, a full,
 *     unsandboxed origin where the previewed app can log in normally;
 *   - the agent reviews/demonstrates the running app via the built-in
 *     browser live panel (ADR-038) — a screencast process with no SPA
 *     session, so it raises no isolation dilemma either.
 *
 * Spec: docs/internal/specs/preview-on-main-listener-spec.md — FR-016,
 * FR-017, US-9. Warmup (dev-mode "is the server up yet?" detection) is kept,
 * but driven by a plain same-origin `fetch(..., { method: 'HEAD' })` poll
 * instead of a hidden probe iframe — `/preview/` sharing the SPA's origin
 * means this fetch is no longer cross-origin, so it needs no CORS opt-in
 * from the dev server (an improvement over the old two-port model).
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, ArrowSquareOut, Copy, Globe } from '@phosphor-icons/react'
import { resolvePreviewHref } from '@/lib/preview-url'
import { isSafeHref } from '@/lib/url-safe'
import type { ServeWorkspaceResult, RunInWorkspaceResult } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// ── Types ─────────────────────────────────────────────────────────────────────

/**
 * Discriminated union for IframePreview.
 *
 * kind='web_serve' and kind='serve_workspace' both render a static-file
 * preview with no warmup. kind='run_in_workspace' renders a dev-server
 * preview with the warmup state machine. The 'serve_workspace' and
 * 'run_in_workspace' literals are preserved because WebServeBlock passes
 * them to map the web_serve tool's effectiveKind onto this component's
 * behaviour; they are not tool names in the context of current sessions.
 */
export type IframePreviewProps =
  | { kind: 'serve_workspace'; result: ServeWorkspaceResult | null; warmupTimeoutSeconds?: number }
  | { kind: 'run_in_workspace'; result: RunInWorkspaceResult | null; warmupTimeoutSeconds?: number }
  | { kind: 'web_serve'; result: ServeWorkspaceResult | null; warmupTimeoutSeconds?: number }

// Internal warmup state machine phases.
type WarmupPhase = 'starting' | 'probing' | 'ready' | 'error'

// Default port for a protocol when window.location.port is empty (browsers
// omit .port for the scheme's default port).
function defaultPortForProtocol(protocol: string): number {
  if (protocol === 'https:') return 443
  return 80
}

// ── Link-only fallback (for a raw/unvalidated legacy `url`) ───────────────────

function LinkOnlyFallback({ href, label }: { href: string; label: string }) {
  const safe = isSafeHref(href)

  return (
    <div data-testid="preview-link-fallback" className="mt-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs">
      <span className="text-[var(--color-muted)]">{label}: </span>
      {safe ? (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 font-mono break-all"
        >
          {href}
        </a>
      ) : (
        <span className="text-[var(--color-muted)] italic">
          Cannot render link — invalid scheme:{' '}
          <code className="font-mono text-[10px] bg-[var(--color-surface-2)] px-1 rounded break-all">
            {href}
          </code>
        </span>
      )}
    </div>
  )
}

// ── Error block ───────────────────────────────────────────────────────────────

function ErrorBlock({
  message,
  href,
  onRetry,
}: {
  message: string
  href?: string
  onRetry?: () => void
}) {
  return (
    <div className="mt-2 rounded-md border border-[var(--color-error)]/30 bg-[var(--color-error)]/5 px-3 py-2 text-xs space-y-1.5">
      <p className="text-[var(--color-error)]">{message}</p>
      {href && (
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 font-mono break-all block"
        >
          {href}
        </a>
      )}
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          aria-label="Retry warmup"
          className="mt-1 flex items-center gap-1 px-2 py-1 rounded border border-[var(--color-border)] text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors"
        >
          <ArrowsClockwise size={11} />
          Retry
        </button>
      )}
    </div>
  )
}

// ── Warmup placeholder ────────────────────────────────────────────────────────

// `toolName` here is the IframePreviewProps.kind discriminator label
// passed down from the parent — NOT a current tool name. The literal
// 'run_in_workspace' selects dev-mode placeholder copy and is the
// IframePreview kind for dev mode regardless of which tool produced
// the result (the canonical tool is `web_serve` dev mode).
function WarmupPlaceholder({ toolName }: { toolName: string }) {
  return (
    <div
      aria-live="polite"
      className="flex items-center gap-2 px-4 py-6 text-sm text-[var(--color-muted)]"
    >
      <span className="flex gap-1">
        <span
          className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce"
          style={{ animationDelay: '0ms' }}
        />
        <span
          className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce"
          style={{ animationDelay: '150ms' }}
        />
        <span
          className="w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-bounce"
          style={{ animationDelay: '300ms' }}
        />
      </span>
      <span>
        Starting {toolName === 'run_in_workspace' ? 'dev server' : 'preview'}…
      </span>
    </div>
  )
}

// ── Main component ────────────────────────────────────────────────────────────

export function IframePreview(props: IframePreviewProps) {
  const { kind, result, warmupTimeoutSeconds } = props
  const isWarmupRequired = kind === 'run_in_workspace'
  // `toolName` is a kind-discriminator label fed to WarmupPlaceholder and
  // warmup-resolution logic, NOT a current tool name. The legacy names
  // (`serve_workspace`, `run_in_workspace`) are preserved here as mode tags
  // because the placeholder copy and warmup branching depend on them;
  // renaming would require touching every consumer.
  const toolName = kind === 'web_serve' ? 'serve_workspace' : kind
  const { addToast } = useUiStore()

  // Warmup state machine (dev mode only — static mode is 'ready' immediately).
  const [warmupPhase, setWarmupPhase] = useState<WarmupPhase>(
    isWarmupRequired ? 'starting' : 'ready'
  )
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const probeCountRef = useRef(0)

  // Guard against state updates after unmount (event-loop callbacks that
  // arrive after the interval cleanup may still reference closed-over
  // setters — this ref lets them exit early).
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const effectiveTimeout = warmupTimeoutSeconds ?? 60
  const maxProbes = Math.max(1, Math.floor(effectiveTimeout / 2))

  // Resolve the href from the tool result. window.location.* is read at
  // render time (tests stub `window.location` before rendering — matches
  // this component's long-standing convention).
  const hostname = window.location.hostname
  const protocol = window.location.protocol
  const origin = window.location.origin
  const port = window.location.port
    ? Number(window.location.port)
    : defaultPortForProtocol(protocol)

  const resolved = result
    ? resolvePreviewHref({ path: result.path, url: result.url, origin, hostname, port })
    : null
  const href = resolved && 'href' in resolved ? resolved.href : null

  // ── Warmup polling (same-origin HEAD fetch — no iframe, no CORS needed) ──

  const stopPolling = useCallback(() => {
    if (intervalRef.current !== null) {
      clearInterval(intervalRef.current)
      intervalRef.current = null
    }
  }, [])

  const probeOnce = useCallback((target: string) => {
    fetch(target, { method: 'HEAD' })
      .then((res) => {
        if (!mountedRef.current) return
        if (res.status >= 200 && res.status < 400) {
          stopPolling()
          setWarmupPhase('ready')
        }
        // Non-2xx/3xx (e.g. a 503 while the dev server is still starting):
        // not ready yet — the interval keeps polling.
      })
      .catch((err) => {
        // Network error — dev server not reachable yet; interval keeps polling.
        console.debug('preview.warmup_probe_failed', err)
      })
  }, [stopPolling])

  const startPolling = useCallback((target: string) => {
    stopPolling()
    probeCountRef.current = 0
    setWarmupPhase('probing')
    probeOnce(target)
    intervalRef.current = setInterval(() => {
      if (!mountedRef.current) return
      probeCountRef.current += 1
      if (probeCountRef.current >= maxProbes) {
        stopPolling()
        setWarmupPhase('error')
        console.warn('preview.warmup_timeout', { tool: toolName })
        return
      }
      probeOnce(target)
    }, 2000)
  }, [maxProbes, probeOnce, stopPolling, toolName])

  // Start polling once the href resolves (dev mode only).
  useEffect(() => {
    if (isWarmupRequired && href && warmupPhase === 'starting') {
      startPolling(href)
    }
    return () => stopPolling()
    // Run once per href resolution; re-triggered by the Retry handler below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [href])

  function handleRetry() {
    if (href) startPolling(href)
  }

  async function handleCopy() {
    if (!href) return
    try {
      await navigator.clipboard.writeText(href)
      addToast({
        message: 'Link copied. Anyone with this link can view the preview until it expires.',
        variant: 'success',
      })
    } catch (err) {
      console.warn('preview.copy_failed', err)
      if (err instanceof Error && err.name === 'NotAllowedError') {
        addToast({
          message: 'Clipboard access denied (HTTPS or user permission required)',
          variant: 'error',
        })
      } else {
        addToast({ message: 'Could not copy link to clipboard', variant: 'error' })
      }
    }
  }

  function handleOpen() {
    if (href) window.open(href, '_blank', 'noopener,noreferrer')
  }

  // ── Render guards ─────────────────────────────────────────────────────────

  // No result yet (tool still running).
  if (!result) {
    return (
      <div className="mt-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] text-xs px-3 py-2 text-[var(--color-muted)]">
        Waiting for {toolName}…
      </div>
    )
  }

  // Path validation failed on both `path` and `url` — fall back to the raw
  // legacy `url` (if any) so old/malformed transcripts still surface
  // something actionable rather than a dead end (FR-019 replay safety).
  if (!resolved || 'error' in resolved) {
    if (result.url) {
      return <LinkOnlyFallback href={result.url} label="Preview" />
    }
    return (
      <div className="mt-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] text-xs px-3 py-2 text-[var(--color-muted)]">
        Preview URL unavailable.
      </div>
    )
  }

  // Defence in depth: resolvePreviewHref only accepts http(s) inputs, but
  // re-check the final href's scheme before ever rendering it as a link.
  if (!href || !isSafeHref(href)) {
    return <LinkOnlyFallback href={href ?? ''} label="Preview" />
  }

  // Dev mode — still warming up.
  if (isWarmupRequired && (warmupPhase === 'starting' || warmupPhase === 'probing')) {
    return (
      <div className="mt-2 rounded-md border border-[var(--color-border)] overflow-hidden">
        <div data-testid="preview-warmup" className="bg-[var(--color-surface-1)] min-h-[80px] flex items-center justify-center">
          <WarmupPlaceholder toolName={toolName} />
        </div>
      </div>
    )
  }

  // Dev mode — warmup timed out.
  if (isWarmupRequired && warmupPhase === 'error') {
    return (
      <div className="mt-2 rounded-md border border-[var(--color-border)] overflow-hidden">
        <div className="bg-[var(--color-surface-1)] min-h-[80px] flex items-center justify-center p-4">
          <ErrorBlock
            message="Dev server did not respond in time. It may still be starting."
            href={href}
            onRetry={handleRetry}
          />
        </div>
      </div>
    )
  }

  // Ready — render the clickable preview link (US-9 / FR-016: link, never an
  // embedded iframe).
  return (
    <div data-testid="preview-link-block" className="mt-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden">
      <div className="flex items-center gap-1.5 px-2 py-1 bg-[var(--color-surface-2)] border-b border-[var(--color-border)]">
        <Globe size={12} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
        <span className="text-[10px] text-[var(--color-muted)] font-mono flex-1 truncate">
          {toolName}
        </span>

        <button
          type="button"
          onClick={handleOpen}
          aria-label="Open preview in new tab"
          title="Open in new tab"
          className="p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)] transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
        >
          <ArrowSquareOut size={13} />
        </button>

        <button
          type="button"
          onClick={handleCopy}
          aria-label="Copy preview link"
          title="Copy link"
          className="p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)] transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
        >
          <Copy size={13} />
        </button>
      </div>

      <div className="px-3 py-2">
        <a
          data-testid="preview-link"
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 font-mono text-xs break-all"
        >
          {href}
        </a>
        <p className="mt-1 text-[10px] text-[var(--color-muted)]">
          Opens in your browser. The agent can also show you this preview through its built-in browser panel.
        </p>
      </div>
    </div>
  )
}
