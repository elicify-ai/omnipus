// CommandPreview — the primary executor-transparency UI for a subagent_3p
// (external-CLI) worker. Replaces the old, generic `AutoAppliedFlags`
// reference list (a static per-CLI description with placeholder text like
// "--model <configured model> (only when a model is configured)") as the
// thing rendered inline in the Runtime section of `AgentProfile.tsx` and in
// the create wizard's `ExecutorInputs` — the operator asked to see the
// ACTUAL parameters and the ACTUAL command line for their real current
// settings, not a generic description.
//
// Backed by `useCommandPreview`, which calls the new stateless
// `POST /agents/executor-preview` endpoint: the REAL argv Omnipus would
// spawn, computed by the same buildArgs() logic each driver
// (claude/codex/opencode) uses at real dispatch time — not a
// hand-maintained approximation. Live-updates (debounced 400ms) as the
// operator edits model / cli_path / cli_args, so the command line visibly
// tracks what they type instead of only refreshing on blur.
//
// `AutoAppliedFlags`/`useExecutorDefaults`/`fetchExecutorDefaults`/
// `GET /agents/executor-defaults` are left in place (still valid generic
// reference data) but, as of this change, are fully unreachable dead code in
// the SPA: a repo-wide grep confirms `<AutoAppliedFlags` has ZERO remaining
// JSX call sites anywhere in `src/`, and `useExecutorDefaults()` has zero
// callers outside its own component file (`AutoAppliedFlags.tsx`) — not just
// "not rendered here." No follow-up removal ticket exists yet; nothing in
// this change depends on keeping them. Flag for a future cleanup pass
// (delete `AutoAppliedFlags.tsx` + `useExecutorDefaults.ts`, and
// `fetchExecutorDefaults`/`GET /agents/executor-defaults` if nothing else
// claims a use for the endpoint) rather than carrying them indefinitely as
// phantom "still valid" code a future maintainer might waste time hunting
// for a usage of.
//
// Visual conventions deliberately reused rather than invented:
//   - Loading/error skeleton shape + retry affordance: `AutoAppliedFlags`.
//   - Warning/error inline line (icon + amber/error text): `CliPathValidationHint`.
//   - Real-command code block styling (`bg-[var(--color-primary)]` +
//     `text-[var(--color-accent)]` mono block): `GatewaySection`'s
//     Tailscale/SSH-tunnel command blocks.
//   - Copy-to-clipboard affordance + toast: `AboutSection`'s "Copy" button.
//
// Also renders the "Send a test message" smoke-test affordance (backed by
// `useExecutorSmokeTest` / `POST /agents/executor-smoke-test`) as a sibling
// block right below the command-line preview — same "does this actually
// work" story, but an explicit, imperative, real-cost action rather than the
// free live preview above it. Deliberately a plain sibling (not nested
// inside the preview's own status div) so it renders regardless of whether
// the preview itself is loading/ready/degraded — the smoke test only needs
// `req`'s cli/model/cli_path/cli_args, not a successful preview fetch.

import { useEffect, useRef } from 'react'
import { CheckCircle, Copy, PaperPlaneTilt, Spinner, WarningCircle, XCircle } from '@phosphor-icons/react'
import { useCommandPreview } from '@/hooks/useCommandPreview'
import { useExecutorSmokeTest } from '@/hooks/useExecutorSmokeTest'
import { useUiStore } from '@/store/ui'
import type { ExecutorCommandPreviewRequest, ExecutorCommandPreviewResponse, ExecutorSmokeTestResponse } from '@/lib/api'

export interface CommandPreviewProps {
  /** The full request to preview. `undefined` (or a `cli`-less request)
   *  renders nothing — no CLI has been chosen yet. Callers rebuild this from
   *  their own state shape every render (see `AgentProfile.tsx`'s
   *  `executor`/`model` or the wizard's flat `WizardSubmitPayload`); the
   *  hook itself only re-fetches when the field VALUES actually change. */
  req: ExecutorCommandPreviewRequest | undefined
  /** The real, saved agent id this preview/test belongs to — passed ONLY by
   *  `AgentProfile.tsx` (the edit form, where the agent already exists and
   *  has its own bound workspace on disk). `undefined` in the create wizard
   *  (`Step1Identity.tsx`), where the agent hasn't been saved yet and there
   *  is no workspace to run the smoke test in.
   *
   *  Irrelevant to the free, stateless command-line preview above (that
   *  endpoint is agent-agnostic — it only computes argv from cli/model/
   *  cli_path/cli_args) — used exclusively by `SmokeTestSection`'s real
   *  `POST /agents/executor-smoke-test` call, where it decides whether the
   *  test spawns a subprocess in the agent's real, persistent workspace
   *  (`used_agent_workspace: true`) or a disposable ephemeral scratch
   *  directory (`used_agent_workspace: false`). */
  agentId?: string
  testId?: string
}

function promptDeliveryLabel(delivery: ExecutorCommandPreviewResponse['prompt_delivery']): string {
  switch (delivery) {
    case 'stdin':
      return 'Your task prompt is sent via stdin.'
    case 'positional argument after --':
      return 'Your task prompt is sent as the final command-line argument, after a literal -- separator.'
    default:
      return ''
  }
}

export function CommandPreview({ req, agentId, testId }: CommandPreviewProps) {
  const { status, retry } = useCommandPreview(req)
  const { addToast } = useUiStore()

  if (!req?.cli) return null

  async function copyCommand(commandLine: string) {
    try {
      await navigator.clipboard.writeText(commandLine)
      addToast({ message: 'Command copied to clipboard', variant: 'success' })
    } catch {
      addToast({ message: 'Could not copy to clipboard', variant: 'error' })
    }
  }

  let previewBlock: React.ReactNode

  if (status.kind === 'idle' || status.kind === 'pending') {
    previewBlock = (
      <div
        data-testid={testId}
        data-preview-status="loading"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
        aria-busy="true"
      >
        <p className="flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]">
          <Spinner size={11} className="animate-spin" />
          Computing the real command line…
        </p>
      </div>
    )
  } else if (status.kind === 'error') {
    // Degraded state: a preview fetch failure never blocks Create/Save (it's
    // purely informational), but it also must not disappear silently — same
    // shape + retry affordance as AutoAppliedFlags' error state. The copy
    // itself branches on `isValidationError`: a genuine 4xx rejection of the
    // request body (e.g. a field tripping the schema's maxLength) means the
    // value the operator typed is itself the problem, so claiming "the
    // settings below still apply when this agent runs" would be actively
    // misleading — a value this endpoint rejects as schema-invalid is likely
    // to misbehave at real save/dispatch time too (same shared schema
    // family). Transport/network/5xx/429 failures keep the original
    // reassuring copy — those say nothing about whether the settings
    // themselves are valid.
    previewBlock = (
      <div
        data-testid={testId}
        data-preview-status="error"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
      >
        <p
          className={`text-[11px] leading-snug ${
            status.isValidationError ? 'text-[var(--color-error)]' : 'text-[var(--color-muted)]'
          }`}
        >
          {status.isValidationError ? (
            <>This value couldn't be validated ({status.message}) — check it before saving.</>
          ) : (
            <>Couldn't compute a live command preview — the settings below still apply when this agent runs.</>
          )}{' '}
          <button tabIndex={0}
            type="button"
            onClick={retry}
            data-testid={testId ? `${testId}-retry` : undefined}
            className="font-medium text-[var(--color-accent)] underline underline-offset-2 hover:no-underline"
          >
            Retry
          </button>
        </p>
      </div>
    )
  } else {
    const { result } = status
    previewBlock = (
      <div
        data-testid={testId}
        data-preview-status="ready"
        role="group"
        aria-label="The real command Omnipus will run"
        className="space-y-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2.5"
      >
        <div className="flex items-center justify-between gap-2">
          <p className="text-[11px] font-medium text-[var(--color-secondary)]">Command Omnipus will run</p>
          <button tabIndex={0}
            type="button"
            onClick={() => copyCommand(result.command_line)}
            data-testid={testId ? `${testId}-copy` : undefined}
            className="flex shrink-0 items-center gap-1 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-accent)]"
          >
            <Copy size={11} />
            Copy
          </button>
        </div>

        <code
          data-testid={testId ? `${testId}-command-line` : undefined}
          className="block whitespace-pre-wrap break-all rounded border border-[var(--color-border)] bg-[var(--color-primary)] px-3 py-2 font-mono text-[11px] text-[var(--color-accent)]"
        >
          {result.command_line}
        </code>

        <p
          data-testid={testId ? `${testId}-prompt-delivery` : undefined}
          className="text-[11px] text-[var(--color-muted)] leading-snug"
        >
          {promptDeliveryLabel(result.prompt_delivery)}
        </p>

        {result.model_dropped_reason && (
          <p
            data-testid={testId ? `${testId}-model-dropped-reason` : undefined}
            className="flex items-start gap-1 text-[11px] text-amber-400 leading-snug"
          >
            <WarningCircle size={11} weight="fill" className="mt-0.5 shrink-0" />
            <span>
              <span className="font-medium">--model omitted:</span> {result.model_dropped_reason}
            </span>
          </p>
        )}

        {result.dropped_args.length > 0 && (
          <div data-testid={testId ? `${testId}-dropped-args` : undefined} className="space-y-1">
            {result.dropped_args.map((dropped, i) => (
              <p
                key={`${dropped.flag}-${i}`}
                className="flex items-start gap-1 text-[11px] text-amber-400 leading-snug"
              >
                <WarningCircle size={11} weight="fill" className="mt-0.5 shrink-0" />
                <span>
                  <code className="font-mono">{dropped.flag}</code> was dropped from the CLI arguments —{' '}
                  {dropped.reason}
                </span>
              </p>
            ))}
          </div>
        )}

        <p className="text-[10px] text-[var(--color-muted)]/70">
          Computed live from your current settings — nothing here is persisted until you save.
        </p>
      </div>
    )
  }

  return (
    <>
      {previewBlock}
      <SmokeTestSection
        cli={req.cli}
        model={req.model}
        cliPath={req.cli_path}
        cliArgs={req.cli_args}
        agentId={agentId}
        testId={testId}
      />
    </>
  )
}

// ── SmokeTestSection ─────────────────────────────────────────────────────
//
// "Send a test message" — actually RUNS a trivial, real prompt through the
// executor's real dispatch path via `useExecutorSmokeTest` /
// `POST /agents/executor-smoke-test`. Distinct from everything above this
// point in the file: this spends real model usage and spawns a real
// subprocess (bounded to ~30s server-side), so it is a deliberate,
// imperative operator click — it NEVER fires on mount or on a field change,
// unlike the free, reactive command-line preview above it.

interface SmokeTestSectionProps {
  cli: ExecutorCommandPreviewRequest['cli']
  model: ExecutorCommandPreviewRequest['model']
  cliPath: ExecutorCommandPreviewRequest['cli_path']
  cliArgs: ExecutorCommandPreviewRequest['cli_args']
  /** The real, saved agent whose own bound workspace the test should run
   *  in — see `CommandPreviewProps.agentId`'s doc comment. `undefined` from
   *  the create wizard, which falls back to the ephemeral-scratch-dir path
   *  server-side exactly as before this field existed. */
  agentId?: string
  testId?: string
}

function SmokeTestSection({ cli, model, cliPath, cliArgs, agentId, testId }: SmokeTestSectionProps) {
  const { status, run, cancel } = useExecutorSmokeTest()
  const smokeTestId = testId ? `${testId}-smoke-test` : undefined
  const isPending = status.kind === 'pending'

  // Guards against a stale smoke-test result being mistaken for a fresh one:
  // this section holds its own status independent of the cli/model/cliPath/
  // cliArgs props it receives, so if the operator runs a test, gets a
  // result, then changes the CLI/model dropdown (or edits cli_path/cli_args)
  // WITHOUT re-clicking the button, the previous result would otherwise
  // stay rendered as if it still described the current settings — a real
  // risk given the entire point of this feature is "prove this
  // configuration actually works before saving." `cancel()` (aborting any
  // mid-flight request) resets back to idle whenever any of these field
  // VALUES actually change, clearing the stale result and re-enabling the
  // button. Deliberately does NOT re-fire the test automatically — this
  // endpoint spends real model usage, so it must only ever run on a
  // deliberate operator click, never as a side effect of editing a field.
  // Skips the first render (mount) — there is nothing stale to clear yet,
  // and `status` already starts at 'idle'.
  const isFirstRender = useRef(true)
  useEffect(() => {
    if (isFirstRender.current) {
      isFirstRender.current = false
      return
    }
    cancel()
  }, [cli, model, cliPath, cliArgs, agentId, cancel])

  const handleClick = () => {
    run({ cli, model, cli_path: cliPath, cli_args: cliArgs, agent_id: agentId })
  }

  return (
    <div
      data-testid={smokeTestId}
      className="space-y-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2.5"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <p className="text-[11px] font-medium text-[var(--color-secondary)]">Send a test message</p>
          <p className="text-[10px] text-[var(--color-muted)] leading-snug">
            Runs a real request through this CLI (spends a small amount of usage).
          </p>
        </div>
        <button tabIndex={0}
          type="button"
          onClick={handleClick}
          disabled={isPending}
          data-testid={smokeTestId ? `${smokeTestId}-button` : undefined}
          className="flex shrink-0 items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--color-secondary)] hover:border-[var(--color-accent)] hover:text-[var(--color-accent)] disabled:cursor-not-allowed disabled:opacity-60"
        >
          {isPending ? <Spinner size={12} className="animate-spin" /> : <PaperPlaneTilt size={12} />}
          {isPending ? 'Running…' : 'Send a test message'}
        </button>
      </div>

      {isPending && (
        <p
          data-testid={smokeTestId ? `${smokeTestId}-pending` : undefined}
          className="text-[11px] text-[var(--color-muted)] leading-snug"
        >
          Running a real test — this can take up to 30 seconds…
        </p>
      )}

      {status.kind === 'result' && <SmokeTestResult result={status.result} testId={smokeTestId} />}

      {status.kind === 'error' && (
        <p
          data-testid={smokeTestId ? `${smokeTestId}-error` : undefined}
          className="flex items-start gap-1 text-[11px] text-[var(--color-error)] leading-snug"
        >
          <XCircle size={11} weight="fill" className="mt-0.5 shrink-0" />
          <span>{status.message}</span>
        </p>
      )}
    </div>
  )
}

function formatSmokeTestDuration(ms: number): string {
  return `${(ms / 1000).toFixed(1)}s`
}

// Surfaces `used_agent_workspace` distinctly from the pass/fail outcome
// itself — the whole point of threading `agent_id` through is transparency
// about WHERE the test ran, independent of whether it succeeded. Shown on
// both the success and domain-level-failure branches below, since the
// field is present (and meaningful) on both.
//
// Copy deliberately says "own folder/directory," not "own workspace" —
// Omnipus has a separate, multi-agent "Workspace" feature (shared team,
// delegation, project instructions) that this per-agent directory has
// nothing to do with; using "workspace" here would wrongly imply a
// connection to that feature.
function WorkspaceProvenanceNote({ usedAgentWorkspace, testId }: { usedAgentWorkspace: boolean; testId?: string }) {
  return (
    <p data-testid={testId} className="text-[10px] text-[var(--color-muted)] leading-snug">
      {usedAgentWorkspace
        ? "Ran in this agent's own folder — the same place a real delegation to it would run."
        : "Ran in a temporary folder, not this agent's own — save the agent first to test with its real project context."}
    </p>
  )
}

function SmokeTestResult({ result, testId }: { result: ExecutorSmokeTestResponse; testId?: string }) {
  if (!result.ok) {
    return (
      <div data-testid={testId ? `${testId}-failure` : undefined} className="space-y-1.5">
        <p
          data-testid={testId ? `${testId}-error` : undefined}
          className="flex items-start gap-1 text-[11px] text-[var(--color-error)] leading-snug"
        >
          <XCircle size={11} weight="fill" className="mt-0.5 shrink-0" />
          <span>{result.error || 'The test run did not succeed.'}</span>
        </p>
        <WorkspaceProvenanceNote
          usedAgentWorkspace={result.used_agent_workspace}
          testId={testId ? `${testId}-failure-workspace-note` : undefined}
        />
      </div>
    )
  }

  return (
    <div data-testid={testId ? `${testId}-success` : undefined} className="space-y-1.5">
      <p className="flex items-center gap-1 text-[11px] text-emerald-400">
        <CheckCircle size={11} weight="fill" />
        Responded in {formatSmokeTestDuration(result.duration_ms)}
      </p>
      <p
        data-testid={testId ? `${testId}-response-text` : undefined}
        className="rounded border border-[var(--color-border)] bg-[var(--color-primary)] px-3 py-2 font-mono text-[11px] text-[var(--color-secondary)] whitespace-pre-wrap break-words"
      >
        {result.response_text}
      </p>
      <WorkspaceProvenanceNote
        usedAgentWorkspace={result.used_agent_workspace}
        testId={testId ? `${testId}-success-workspace-note` : undefined}
      />
    </div>
  )
}

export default CommandPreview
