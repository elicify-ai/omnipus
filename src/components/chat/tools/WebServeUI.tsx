/**
 * WebServeUI — AssistantUI tool component for the `web_serve` tool.
 *
 * Handles both static-serve and dev-server modes based on the `kind` field
 * in the tool result. Also used as the canonical implementation backing the
 * back-compat replay aliases ServeWorkspaceUI (serve_workspace) and
 * RunInWorkspaceUI (run_in_workspace) — those components are kept only so
 * old transcripts replay correctly; all new sessions use WebServeUI directly.
 *
 * Spec: FR-008 / FR-008a / FR-010 / FR-011 / FR-012 / FR-013 / FR-014 / FR-015 / FR-019.
 * Also: docs/internal/specs/preview-on-main-listener-spec.md FR-016/FR-017 (US-9) —
 * `/preview/` now shares the SPA's own gateway listener (ADR-044), so BOTH
 * modes render as a clickable link via IframePreview — never an embedded
 * same-origin iframe.
 *
 * kind="static": Globe icon + path label, preview link renders immediately (no warmup).
 * kind="dev":    Terminal icon + command + port label, warmup state machine, then
 *                a preview link (same-origin HEAD-poll warmup, no iframe).
 *                Default grace period is 60 s (tools.run_in_workspace
 *                .warmup_timeout_seconds in config.json). The config key retains
 *                the pre-unification name for back-compat with deployed configs.
 *
 * The toolName passed to makeWebServeUI selects which tool name the component
 * registers under, allowing the same component factory to cover:
 *   web_serve        (canonical)
 *   serve_workspace  (back-compat replay alias)
 *   run_in_workspace (back-compat replay alias)
 */

import { makeAssistantToolUI } from '@assistant-ui/react'
import { Globe, Terminal } from '@phosphor-icons/react'
import { type ServeWorkspaceResult as ServeWorkspaceIframeResult, type RunInWorkspaceResult as RunInWorkspaceIframeResult } from '@/lib/api'
import { hasPreviewShape } from '@/lib/preview-url'
import { isCancelledStatus } from '@/lib/toolStatusConfig'
import { IframePreview } from '../IframePreview'
import { PreviewToolHeader } from './PreviewToolHeader'

// ── Result types ──────────────────────────────────────────────────────────────

/**
 * The result shape emitted by the `web_serve` tool.
 *
 * `kind` discriminates between the two modes. Back-compat: when replaying a
 * legacy `serve_workspace` or `run_in_workspace` transcript, `kind` may be
 * absent; we infer mode from the presence of `command` / `port` fields.
 */
export interface WebServeResult {
  /** Discriminator for the two modes. */
  kind?: 'static' | 'dev'
  /**
   * The preview URL. Since ADR-044 (preview-on-main-listener), the backend
   * builds this from the canonical gateway origin — e.g.
   * "https://pod.example.com/preview/<agent>/<token>/" or
   * "http://localhost:<gateway.port>/preview/<agent>/<token>/" — the SAME
   * origin the SPA itself is served from. Legacy/replay transcripts may
   * still carry a relative path here (e.g. "/preview/<agent>/<token>/");
   * IframePreview's URL resolution (resolvePreviewHref) handles both shapes.
   */
  url: string
  /** ISO-8601 token expiry timestamp. */
  expires_at: string
  /** Dev-mode: the command that was executed (e.g. "vite dev"). */
  command?: string
  /** Dev-mode: the local port the dev server is listening on. */
  port?: number
  /** Static-mode: the workspace path that was served. */
  path?: string
}

interface WebServeArgs {
  path?: string
  command?: string
  port?: number
  duration_seconds?: number
}

/**
 * Infer effective kind from result, falling back to presence of command/port
 * for legacy transcript replay where `kind` was not emitted.
 */
function inferKind(result: WebServeResult): 'static' | 'dev' {
  if (result.kind === 'static' || result.kind === 'dev') return result.kind
  // Legacy back-compat: run_in_workspace results have command + port
  if (typeof result.command === 'string' && typeof result.port === 'number') return 'dev'
  return 'static'
}

function isWebServeResult(value: unknown): value is WebServeResult {
  if (!value || typeof value !== 'object') return false
  const v = value as Record<string, unknown>
  // New web_serve shape: has url + expires_at
  if (typeof v.url === 'string' && typeof v.expires_at === 'string') return true
  // Legacy serve_workspace / run_in_workspace shape: hasPreviewShape checks path + url
  return (
    hasPreviewShape(value) &&
    typeof (value as Record<string, unknown>).expires_at === 'string'
  )
}

// ── Block component ───────────────────────────────────────────────────────────

// ── Malformed result block (B1.3e) ────────────────────────────────────────────

/**
 * Rendered when `isWebServeResult` rejects the tool result. Shows the raw JSON
 * in a collapsible details element so power users can debug without crashing the
 * rest of the chat. Does NOT throw — the ErrorBoundary wrapping ChatScreen is
 * not invoked.
 */
function MalformedResultBlock({ raw }: { raw: unknown }) {
  let rawJson: string
  try {
    rawJson = JSON.stringify(raw, null, 2)
  } catch {
    rawJson = String(raw)
  }
  return (
    // Flat text-line design (ticket "Tool components in chat", P2): the old
    // rounded/bordered/backgrounded error card is a border-l-2 left accent
    // instead — matches GenericToolCall.tsx's marshal-error/delegation-denied
    // banners. This is the only frame WebServeUI itself owns; PreviewToolHeader
    // (used above by WebServeBlock) was restyled separately (commit 48325168).
    <div data-testid="webserve-malformed-block" className="mt-2 border-l-2 border-[var(--color-error)]/40 pl-2.5 py-1 text-xs space-y-1.5">
      <p className="text-[var(--color-error)]">
        web_serve tool returned a malformed result — cannot render preview.
      </p>
      <details className="mt-1">
        <summary tabIndex={0} className="cursor-pointer text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors">
          Show raw result
        </summary>
        <pre className="mt-1.5 text-[var(--color-muted)] font-mono text-[10px] overflow-auto max-h-40 whitespace-pre-wrap break-all">
          {rawJson}
        </pre>
      </details>
    </div>
  )
}

export function WebServeBlock({
  args,
  result,
  isRunning,
  isError,
  isCancelled,
  toolName,
}: {
  args: WebServeArgs
  result: unknown
  isRunning: boolean
  /**
   * Issue #617: the tool call's real error outcome. Previously
   * PreviewToolHeader was handed `hasResult={typedResult !== null}` as a
   * stand-in for "did it fail" — an accidental proxy for a genuinely
   * different question ("did the result parse against the WebServeResult
   * schema"). A malformed-but-successful payload showed "Failed"; a real
   * failure that happened to return a well-formed payload showed "Done".
   * `isError` now carries the actual outcome from the tool-call part /
   * store ToolCall.status, independent of whether `result` parses.
   *
   * F5: required (nullable), not optional. An omitted optional prop is
   * `undefined` → falsy → renders success, silently reopening the exact bug
   * this field exists to fix. Making it `boolean | undefined` (required)
   * forces every call site to pass it explicitly.
   */
  isError: boolean | undefined
  /**
   * F2: the tool call's real cancellation outcome — WebServeBlock has no
   * `status` object to derive this from the way BrowserToolReplayBlock/
   * GenericToolCall do (both consult `isCancelledStatus(status)`), so it is
   * threaded explicitly. Same required-nullable rationale as `isError`.
   */
  isCancelled: boolean | undefined
  toolName: string
}) {
  // B1.3e: when the type guard rejects the result and the tool is no longer
  // running, render the malformed block instead of crashing or rendering nothing.
  // We allow null result while running (tool not done yet — normal state).
  if (result !== null && result !== undefined && !isRunning && !isWebServeResult(result)) {
    return <MalformedResultBlock raw={result} />
  }

  const typedResult = isWebServeResult(result) ? result : null
  const effectiveKind = typedResult ? inferKind(typedResult) : null

  // For dev mode: derive command and port from result or args
  const command = typedResult?.command ?? args.command ?? ''
  const port = typedResult?.port ?? args.port

  // For static mode: derive path label from result or args
  const pathLabel = typedResult?.path ?? args.path

  const isDevMode = effectiveKind === 'dev' ||
    (effectiveKind === null && (typeof args.command === 'string' || typeof args.port === 'number'))

  const portChip =
    isDevMode && port !== undefined ? (
      <span className="text-[var(--color-muted)] font-mono">:{port}</span>
    ) : undefined

  // IframePreview kind: map to the existing discriminated union.
  // The string literals 'serve_workspace' / 'run_in_workspace' are
  // IframePreviewProps.kind discriminators — mode tags, NOT current tool
  // names. Static mode → 'serve_workspace'; dev mode → 'run_in_workspace'.
  // `toolName` only feeds the header label; `iframeKind` is derived from
  // the result shape (effectiveKind / isDevMode), not from toolName.
  const iframeKind =
    isDevMode ? 'run_in_workspace' : 'serve_workspace'

  // Build the result shape expected by IframePreview — it uses path + url.
  // Pass path directly; IframePreview.extractPath falls back to url when
  // path is absent, so there is no need to duplicate the fallback logic here.
  const iframeResult = typedResult
    ? iframeKind === 'run_in_workspace'
      ? {
          path: typedResult.path,
          url: typedResult.url,
          expires_at: typedResult.expires_at,
          command: typedResult.command ?? command,
          port: typedResult.port ?? port ?? 0,
        }
      : {
          path: typedResult.path,
          url: typedResult.url,
          expires_at: typedResult.expires_at,
        }
    : null

  return (
    <div className="mt-2 text-xs">
      <PreviewToolHeader
        data-testid="webserve-tool-header"
        // Mode icon (Terminal/Globe) is a fixed, muted glyph — it identifies
        // WHICH kind of preview this is (dev server vs static), never the
        // call's status. Status lives only in PreviewToolHeader's own
        // leading dot/spinner; tinting this icon by isRunning/typedResult
        // (the old behavior) duplicated that signal and, in static mode,
        // rendered a SECOND running spinner alongside the header's own.
        icon={
          isDevMode ? (
            <Terminal size={13} className="text-[var(--color-muted)]" />
          ) : (
            <Globe size={13} weight="duotone" className="text-[var(--color-muted)]" />
          )
        }
        toolName={toolName}
        label={isDevMode ? (command || undefined) : (pathLabel || undefined)}
        // Static mode has no trailing chip — the header's own leading dot
        // already carries status (see comment above); a second trailing
        // status icon here was a duplicate/triplicate status render.
        trailing={isDevMode ? portChip : undefined}
        isRunning={isRunning}
        isError={!!isError}
        isCancelled={!!isCancelled}
      />

      {isDevMode ? (
        <IframePreview
          kind="run_in_workspace"
          result={iframeResult as RunInWorkspaceIframeResult | null}
        />
      ) : (
        <IframePreview
          kind="serve_workspace"
          result={iframeResult as ServeWorkspaceIframeResult | null}
        />
      )}
    </div>
  )
}

// ── Factory ───────────────────────────────────────────────────────────────────

/**
 * Creates a registered AssistantUI tool component for the given tool name.
 * The toolName is threaded into WebServeBlock so the header displays the
 * correct name for each alias.
 */
export function makeWebServeUI(toolName: string) {
  return makeAssistantToolUI<WebServeArgs, unknown>({
    toolName,
    render: ({ args, result, status, isError }) => (
      <WebServeBlock
        args={args ?? {}}
        result={result}
        isRunning={status.type === 'running'}
        isError={isError}
        isCancelled={isCancelledStatus(status)}
        toolName={toolName}
      />
    ),
  })
}

// ── Canonical registration ────────────────────────────────────────────────────

export const WebServeUI = makeWebServeUI('web_serve')
