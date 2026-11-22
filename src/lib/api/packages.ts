// packages.ts — SPA client for the package export/import calls (epic #1094,
// slice B1, issue #1967).
//
// The four package calls that change something or release bytes — upload,
// apply, cancel and the export download — ride the real transport with BOTH
// transport replay layers switched off (`csrfRetry: false`,
// `plantedCookieToast: false`); the wizard's mutations add `retry: false`
// (spec "No automatic resend — all three replay layers"). The wizard owns the
// recovery from a session refusal (see isSessionRefusal).
//
// Wire types are the GENERATED contracts (Hard Constraint #8): the import
// preview/result/apply re-export the existing generated schemas unchanged.
// PackageExportPlan is the B1 review contract, generated the same way.
import type {
  PackageImportPreview,
  PackageImportApplyRequest,
  PackageImportResult,
  PackageManifest,
  components,
} from './generated/openapi-types'
import {
  PackageImportPreview as PackageImportPreviewSchema,
  PackageImportResult as PackageImportResultSchema,
  PackageExportPlan as PackageExportPlanSchema,
} from './generated/schemas'
import type { ZodType } from 'zod'
import { request, requestResponse, type RequestOptions } from './http'
import { ApiError, isApiError, PLANTED_COOKIE_CLEARED_CODE } from '../api-error'

export type {
  PackageImportPreview,
  PackageImportApplyRequest,
  PackageImportResult,
  PackageManifest,
}

// The schemas, exported for tests that validate fixtures against the real
// generated contract (CHECK F02: every successful fixture must parse).
export { PackageImportPreviewSchema, PackageImportResultSchema }

// The B1 review contract (spec "PackageExportPlan fields"), generated from
// contracts/components/schemas/PackageExportPlan*.yaml.
export type PackageExportPlanRequest = components['schemas']['PackageExportPlanRequest']
export type PackageExportPlan = components['schemas']['PackageExportPlan']
export type PackageExportPlanElement = PackageExportPlan['elements'][number]
export type PackageExportPlanLeftOutItem = PackageExportPlan['left_out'][number]
export type PackageExportPlanNeedsSecret = PackageExportPlan['needs_secrets'][number]
export type PackageExportPlanDisclosure = PackageExportPlan['disclosures'][number]

/** The download outcome: the bundle bytes and the server's file name. */
export interface PackageDownload { // not-wire-format: the in-browser result of the export download (a Blob and its file name)
  blob: Blob
  fileName: string
}

// ---------------------------------------------------------------------------
// packageErrorText — the exact UI text per code (spec "UI text for codes");
// an unknown code falls back to the server's `error` text.
// ---------------------------------------------------------------------------

const DAMAGED = 'This bundle is damaged: a file does not match its record. Export it again.'
const UNSAFE = 'This bundle contains something unsafe and was refused.'
const NOTHING_IMPORTABLE = 'This bundle contains nothing this version can import.'

export const packageErrorText: Record<string, string> = {
  not_a_package: 'This file is not an Omnipus bundle.',
  package_too_new: 'This bundle was made by a newer Omnipus. Update this install to import it.',
  element_schema_too_new: 'Part of this bundle was made by a newer Omnipus. Update this install to import it.',
  package_too_large: 'This bundle is too large for this version (most is 64 MB, 256 MB unpacked).',
  checksum_mismatch: DAMAGED,
  manifest_mismatch: DAMAGED,
  unsafe_path: UNSAFE,
  unsafe_entry_type: UNSAFE,
  no_supported_element: NOTHING_IMPORTABLE,
  element_type_not_supported: NOTHING_IMPORTABLE,
  too_many_agents: 'This bundle holds more than one agent. This version imports one agent per bundle.',
  invalid_element: 'The agent in this bundle is not valid.',
  external_worker_not_supported_yet: 'Third-party workers cannot be exported or imported yet.',
  too_many_staged_imports:
    'Too many imports are waiting. Each one expires 30 minutes after upload; finish or cancel one, then try again.',
  staging_not_found: 'This upload expired or was already used. Choose the file again.',
  staging_cleanup_failed: 'The upload was cancelled. Omnipus will try to remove its temporary files at a later upload.',
  import_in_progress: 'Another import is running. Try again in a moment.',
  workspace_not_found: 'That workspace no longer exists. Choose another.',
  default_workspace_missing: 'Choose a workspace to import into.',
  name_suffix_exhausted: 'Every "(imported N)" name is taken. Rename one of the existing agents, then try again.',
  import_rolled_back: 'The import failed and was undone. Nothing was added.',
  import_rollback_incomplete: 'The import failed and could not be fully undone. Restart Omnipus to finish the clean-up.',
  core_agent_not_exportable: 'Built-in agents are on every install and cannot be exported.',
  agent_prompt_missing: 'This agent has no prompt, so it cannot be exported.',
  agent_not_found: 'This agent no longer exists.',
  export_selection_invalid: 'Choose exactly one agent.',
  plan_not_found: 'The review expired. Check your choice and review again.',
  audit_failed: 'The export was stopped because the audit log could not be written. Nothing was downloaded.',
}

/** Texts the wizard shows for answers that carry no package code (spec "UI text for codes"). */
export const PACKAGE_TEXT = {
  uncertain:
    'Omnipus could not confirm what happened to this upload. It may have been imported, for example from another tab. Check the workspace Team before importing again.',
  unknownUpload:
    'Omnipus did not confirm the upload. Choose the file again. If the upload did arrive, it can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload.',
  unknownApply:
    'The connection dropped before Omnipus answered. The import may have finished: check the workspace Team before importing again.',
  applyTooLarge: 'Omnipus refused the request as too large. Nothing was imported. Select Import again.',
  sessionApply: 'Your session needed refreshing. Nothing was imported. Select Import again.',
  sessionExport: 'Your session needed refreshing. Nothing was downloaded. Select Download again.',
  sessionUpload: 'Your session needed refreshing. Choose the file again.',
  sessionCancel: 'Your session needed refreshing. Select Cancel again.',
  readFailed: 'Omnipus could not be reached. Check the connection and try again.',
} as const

/**
 * Text for a package error code. `element_not_creatable` differs by wizard
 * side (import vs export — ctx.side); `element_not_creatable` and
 * `agent_file_unreadable` append the field / file sentence (ctx.field,
 * ctx.file, ctx.check). An unknown code passes the server's own `error` text
 * through.
 */
export interface PackageErrorTextContext { // not-wire-format: in-browser text-map options, never sent
  side?: 'import' | 'export'
  field?: string
  check?: string
  file?: string
}

export function packageErrorTextFor(code: string, serverText: string, ctx?: PackageErrorTextContext): string {
  if (code === 'element_not_creatable') {
    const base =
      ctx?.side === 'export'
        ? 'This agent breaks a rule for agents and cannot be exported'
        : 'The agent in this bundle breaks a rule for new agents'
    return ctx?.field ? `${base}: ${ctx.field}` : base
  }
  if (code === 'agent_file_unreadable') {
    const file = ctx?.file ? ` (${ctx.file})` : ''
    const tooLarge = ctx?.check === 'too_large' ? ' It is over 1 MB.' : ''
    return `A file of this agent could not be read safely${file}.${tooLarge}`
  }
  return Object.prototype.hasOwnProperty.call(packageErrorText, code) ? packageErrorText[code] : serverText
}

// The error body's optional `field` and `details` (ErrorResponse: field,
// details.file, details.check), read from the raw body ApiError keeps.
function errorBodyParts(err: ApiError): { field?: string; file?: string; check?: string; error?: string } {
  const out: { field?: string; file?: string; check?: string; error?: string } = {}
  if (err.field) out.field = err.field
  if (!err.body) return out
  try {
    const parsed = JSON.parse(err.body) as {
      field?: unknown
      error?: unknown
      details?: { file?: unknown; check?: unknown }
    }
    if (typeof parsed.field === 'string' && !out.field) out.field = parsed.field
    if (typeof parsed.error === 'string') out.error = parsed.error
    if (typeof parsed.details?.file === 'string') out.file = parsed.details.file
    if (typeof parsed.details?.check === 'string') out.check = parsed.details.check
  } catch {
    // Not JSON — no field or details to read.
  }
  return out
}

/** The UI text for a failed package or list call (spec "UI text for codes"). */
export function packageErrorMessage(err: unknown, side: 'import' | 'export'): string {
  if (!isApiError(err)) return PACKAGE_TEXT.readFailed
  if (err.status === 0) return PACKAGE_TEXT.readFailed
  const parts = errorBodyParts(err)
  return packageErrorTextFor(err.code ?? '', parts.error ?? err.userMessage, {
    side,
    field: parts.field,
    file: parts.file,
    check: parts.check,
  })
}

/**
 * True for the three known pre-write session refusals (spec "Wizard-owned
 * recovery"): the client's own csrf_missing gate, a server CSRF 403 (its
 * fixed "csrf …" body) and a 403 planted_cookie_cleared.
 */
export function isSessionRefusal(err: unknown): boolean {
  if (!isApiError(err) || err.status !== 403) return false
  if (err.code === 'csrf_missing' || err.code === PLANTED_COOKIE_CLEARED_CODE) return true
  return err.code === undefined && typeof err.body === 'string' && /csrf/i.test(err.body)
}

/**
 * The wizard's one safe recovery read after a session refusal: GET
 * /api/v1/state lets the gateway re-mint the CSRF cookie. Best effort — its
 * outcome changes nothing on screen; the person acts again deliberately.
 */
export async function refreshSession(): Promise<void> {
  try {
    await request<unknown>('/state')
  } catch {
    // The read is only a re-mint trigger; the next deliberate action reports
    // its own outcome.
  }
}

/**
 * The apply answer classes (spec "Apply outcome classes"): every apply answer
 * that is not a parsed 200 sorts into exactly one.
 */
export type ApplyOutcomeClass = 'known_refusal' | 'cleanup_pending' | 'unknown'

const APPLY_KNOWN_CODES: Record<number, readonly string[]> = {
  404: ['staging_not_found', 'workspace_not_found'],
  409: ['import_in_progress', 'name_suffix_exhausted', 'default_workspace_missing'],
  500: ['import_rolled_back'],
}

export function classifyApplyOutcome(err: unknown): ApplyOutcomeClass {
  if (!isApiError(err)) return 'unknown' // schema mismatch, unreadable body, abort
  if (err.status === 500 && err.code === 'import_rollback_incomplete') return 'cleanup_pending'
  if (isSessionRefusal(err)) return 'known_refusal'
  if (err.status === 400 || err.status === 413) return 'known_refusal'
  if (err.status === 422 && err.code !== undefined) return 'known_refusal'
  const codes = APPLY_KNOWN_CODES[err.status]
  if (codes && err.code !== undefined && codes.includes(err.code)) return 'known_refusal'
  return 'unknown'
}

/** The same sort for an upload answer that never confirmed (spec "Unknown" row). */
export function classifyUploadOutcome(err: unknown): 'known_refusal' | 'unknown' {
  if (!isApiError(err)) return 'unknown'
  if (isSessionRefusal(err)) return 'known_refusal'
  if (err.status === 400 || err.status === 413) return 'known_refusal'
  if (err.status >= 400 && err.status < 500 && err.status !== 401 && err.code !== undefined) return 'known_refusal'
  return 'unknown'
}

// ---------------------------------------------------------------------------
// The calls.
// ---------------------------------------------------------------------------

// Both transport replay layers off: no CSRF re-send, no planted-cookie toast
// with a stored Retry (spec "No automatic resend").
const NO_REPLAY: RequestOptions = { csrfRetry: false, plantedCookieToast: false }

export async function createPackageExportPlan(body: PackageExportPlanRequest): Promise<PackageExportPlan> {
  return request<PackageExportPlan>(
    '/packages/export/plan',
    { method: 'POST', body: JSON.stringify(body) },
    PackageExportPlanSchema as ZodType<PackageExportPlan>,
  )
}

export async function getPackageExportPlan(planId: string): Promise<PackageExportPlan> {
  return request<PackageExportPlan>(
    `/packages/export/plan/${encodeURIComponent(planId)}`,
    undefined,
    PackageExportPlanSchema as ZodType<PackageExportPlan>,
  )
}

// Only a 204 proves the cancel ran (spec "What a cancel answer proves"). Any
// other successful answer (a proxy's 200 page, a 202, a 205) proves nothing:
// it is rejected with its raw status so the wizard shows Uncertain. Error
// answers keep the transport's ApiError (session, coded 500, network).
export async function cancelPackageImport(stagingId: string): Promise<void> {
  const res = await requestResponse(
    `/packages/import/${encodeURIComponent(stagingId)}`,
    { method: 'DELETE' },
    NO_REPLAY,
  )
  if (res.status !== 204) {
    throw new ApiError(res.status, PACKAGE_TEXT.uncertain, { code: 'cancel_unconfirmed' })
  }
}

export async function exportPackage(agentIds: string[]): Promise<PackageDownload> {
  const res = await requestResponse(
    '/packages/export',
    { method: 'POST', body: JSON.stringify({ agent_ids: agentIds }) },
    NO_REPLAY,
  )
  const blob = await res.blob()
  return { blob, fileName: fileNameFromDisposition(res.headers.get('Content-Disposition')) }
}

export async function uploadPackageImport(file: File): Promise<PackageImportPreview> {
  return request<PackageImportPreview>(
    '/packages/import',
    { method: 'POST', body: file, headers: { 'Content-Type': 'application/octet-stream' } },
    PackageImportPreviewSchema as ZodType<PackageImportPreview>,
    undefined,
    NO_REPLAY,
  )
}

export async function applyPackageImport(
  stagingId: string,
  body: PackageImportApplyRequest,
): Promise<PackageImportResult> {
  return request<PackageImportResult>(
    `/packages/import/${encodeURIComponent(stagingId)}/apply`,
    { method: 'POST', body: JSON.stringify(body) },
    PackageImportResultSchema as ZodType<PackageImportResult>,
    undefined,
    NO_REPLAY,
  )
}

/**
 * The download name from a Content-Disposition header: the RFC 5987
 * `filename*` form when present, else the quoted `filename`, else the
 * server's default name for an unnamed agent.
 */
export function fileNameFromDisposition(header: string | null): string {
  if (header) {
    const extended = /filename\*\s*=\s*UTF-8''([^;]+)/i.exec(header)
    if (extended) {
      try {
        return decodeURIComponent(extended[1].trim())
      } catch {
        // Malformed escape — fall back to the plain form.
      }
    }
    const plain = /filename\s*=\s*"([^"]*)"/i.exec(header) ?? /filename\s*=\s*([^;]+)/i.exec(header)
    if (plain && plain[1].trim() !== '') return plain[1].trim()
  }
  return 'agent.omnpkg'
}
