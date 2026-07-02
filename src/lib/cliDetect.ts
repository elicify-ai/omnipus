// Shared helpers for the external-executor-cli-path-detection feature
// (ADR-030). `CliDetect` keys the wire object by binary name ("claude"),
// while `ExecutorConfig.cli` / `CliValidateRequest.cli` spell the Claude
// executor "claude-code". Both the create wizard (Step1Identity) and the
// edit form (AgentProfile) need the same detect→prefill mapping, so it
// lives here rather than being duplicated per component.

import type { CliDetect, CliDetectEntry, CliValidateRequest } from './api'
import { CliValidateRequest as CliValidateRequestSchema } from './api/generated/schemas'

/** The three supported external-executor CLI identifiers (wire enum). */
export type SupportedCli = CliValidateRequest['cli']

/** Derived from the generated `CliValidateRequest` Zod schema's `cli` enum
 *  (single source of truth — see Constraint #8) so a future 4th CLI added to
 *  the wire contract can't silently drift out of sync with this hand-listed
 *  array ever again. */
export const SUPPORTED_CLIS: readonly SupportedCli[] = CliValidateRequestSchema.shape.cli.options

/** Maps the executor CLI identifier to its `CliDetect` object key. */
export const CLI_TO_DETECT_KEY: Record<SupportedCli, keyof CliDetect> = {
  'claude-code': 'claude',
  codex: 'codex',
  opencode: 'opencode',
}

/** Looks up the detection entry for a CLI, or undefined when detect hasn't
 *  resolved yet or no CLI is selected. */
export function detectEntryFor(detect: CliDetect | null | undefined, cli: SupportedCli | undefined): CliDetectEntry | undefined {
  if (!detect || !cli) return undefined
  return detect[CLI_TO_DETECT_KEY[cli]]
}

// ── Detection hint (static, pre-validation) ─────────────────────────────────
//
// Shown while no blur-triggered validate() result exists yet — tells the
// operator where a prefilled value came from, or that nothing was found
// (US-1 AC-1/AC-3). Once a validate() result lands, the validation status
// takes over (see CliPathValidationHint.tsx).

export type CliDetectHint =
  | { kind: 'unknown' }
  | { kind: 'detected'; source: 'path' | 'well-known' }
  | { kind: 'not-found' }

export function resolveCliDetectHint(detect: CliDetect | null | undefined, cli: SupportedCli | undefined): CliDetectHint {
  const entry = detectEntryFor(detect, cli)
  if (!entry) return { kind: 'unknown' }
  if (entry.installed && entry.path) {
    // F-17 stale-bundle safety: tolerate an unrecognized `source` value by
    // falling back to "path" rather than crashing the render.
    return { kind: 'detected', source: entry.source === 'well-known' ? 'well-known' : 'path' }
  }
  return { kind: 'not-found' }
}
