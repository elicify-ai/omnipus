// packages.ts — SPA client for the package export/import calls (epic #1094).
//
// RED STUB (issue #1967 slice B1, test author scaffold): the module and its
// export names are the spec's contract (spec Reachability: "Client:
// src/lib/api/packages.ts (new)"; packageErrorText map). Every function throws
// until GREEN implements the calls over the real transport — the four package
// calls must ride request()/requestOnce() with the replay switches off
// (`csrfRetry: false`, `plantedCookieToast: false`, `retry: false`), the
// export download must return the blob and the Content-Disposition name, and
// the upload must send the raw bytes. Nothing here is implemented yet.
// The interfaces below are the spec's field tables as test scaffolding; GREEN
// re-binds them to the generated schemas (Hard Constraint #8).

// ---------------------------------------------------------------------------
// Types (wire shapes come from the generated schemas; GREEN regenerates and
// re-binds these to src/lib/api/generated — see the plan's known gaps).
// ---------------------------------------------------------------------------

export interface PackageExportPlanRequest {
  scope: 'agents'
  agent_ids: string[]
}

export interface PackageExportPlanFileEntry {
  path: string
  size: number
}

export interface PackageExportPlanElement {
  type: 'agent'
  source_id: string
  name: string
  kind: 'main' | 'worker' | 'external_worker'
  files: PackageExportPlanFileEntry[]
  bytes: number
}

export interface PackageExportPlanLeftOutItem {
  type: string
  reason: string
  message: string
  items?: string[]
}

export interface PackageExportPlanNeedsSecret {
  name: string
  used_by: string
  kind: string
}

export interface PackageExportPlanDisclosure {
  code: string
  message: string
}

export interface PackageExportPlan {
  plan_id: string
  created_at: string
  expires_at: string
  scope: string
  elements: PackageExportPlanElement[]
  left_out: PackageExportPlanLeftOutItem[]
  needs_secrets: PackageExportPlanNeedsSecret[]
  disclosures: PackageExportPlanDisclosure[]
  size_bytes: number
  file_name: string
}

export interface PackageImportPreview {
  staging_id: string
  expires_at?: string
  will_add?: Array<{ source_id?: string; source_name?: string; final_name?: string }>
  skipped?: Array<{ source_id?: string; source_name?: string; reason?: string }>
  warnings?: Array<{ code?: string; message?: string; items?: string[] }>
  needs_secrets?: PackageExportPlanNeedsSecret[]
}

export interface PackageImportApplyRequest {
  mode: 'new' | 'replace' | 'merge'
  workspace_id?: string
}

export interface PackageImportResult {
  mode?: string
  created?: Array<{
    type?: string
    source_id?: string
    name?: string
    new_id?: string
    workspace_id?: string
    activation?: string
  }>
  warnings?: Array<{ code?: string; message?: string; items?: string[] }>
  needs_secrets?: PackageExportPlanNeedsSecret[]
}

/** The download outcome: the bundle bytes and the server's file name. */
export interface PackageDownload {
  blob: Blob
  fileName: string
}

// ---------------------------------------------------------------------------
// packageErrorText — the exact UI text per code (spec "UI text for codes");
// an unknown code falls back to the server's `error` text.
//
// RED STUB: the map ships EMPTY. The texts are feature content — filling them
// here would hand test 18 a green that never showed red. GREEN fills this map
// from the spec's "UI text for codes" table.
// ---------------------------------------------------------------------------

export const packageErrorText: Record<string, string> = {}

/**
 * Text for a package error code. `element_not_creatable` differs by wizard
 * side (import vs export — ctx.side); `element_not_creatable` and
 * `agent_file_unreadable` append the field / file sentence (ctx.field,
 * ctx.file, ctx.check). An unknown code passes the server's own `error` text
 * through. RED STUB: throws.
 */
export interface PackageErrorTextContext {
  side?: 'import' | 'export'
  field?: string
  check?: string
  file?: string
}

export function packageErrorTextFor(code: string, serverText: string, ctx?: PackageErrorTextContext): string {
  void ctx
  throw new Error(
    `packages: packageErrorTextFor not implemented (RED stub, issue #1967 slice B1; code=${code} server=${serverText})`,
  )
}

/**
 * The apply answer classes (spec "Apply outcome classes"): every apply answer
 * sorts into exactly one.
 *   - 'added':            200 parsed against PackageImportResult (the call resolves;
 *                         the classifier is only consulted on a throw)
 *   - 'known_refusal':    client csrf_missing; 400; 403 CSRF failure; 403 planted_cookie_cleared;
 *                         404 staging_not_found | workspace_not_found; 409 import_in_progress |
 *                         name_suffix_exhausted | default_workspace_missing; 413; 422 (any package code);
 *                         500 import_rolled_back
 *   - 'cleanup_pending':  500 import_rollback_incomplete
 *   - 'unknown':          network failure or abort (status 0); timeout; any 5xx without one of the
 *                         codes above (including uncoded proxy 502/503/504 and the uncoded
 *                         developer-bypass 503); any other status
 *
 * RED STUB: throws. GREEN implements the table from the spec (never from a
 * server answer observed in a test).
 */
export type ApplyOutcomeClass = 'known_refusal' | 'cleanup_pending' | 'unknown'

export function classifyApplyOutcome(err: unknown): ApplyOutcomeClass {
  void err
  throw new Error('packages: classifyApplyOutcome not implemented (RED stub, issue #1967 slice B1)')
}

/** The same sort for an upload answer that never confirmed (spec "Unknown" row). */
export function classifyUploadOutcome(err: unknown): 'known_refusal' | 'unknown' {
  void err
  throw new Error('packages: classifyUploadOutcome not implemented (RED stub, issue #1967 slice B1)')
}

// ---------------------------------------------------------------------------
// The calls. RED STUB: every function throws not-implemented.
// ---------------------------------------------------------------------------

function notImplemented(what: string): never {
  throw new Error(`packages: ${what} not implemented (RED stub, issue #1967 slice B1)`)
}

export async function createPackageExportPlan(body: PackageExportPlanRequest): Promise<PackageExportPlan> {
  void body
  return notImplemented('createPackageExportPlan')
}

export async function getPackageExportPlan(planId: string): Promise<PackageExportPlan> {
  void planId
  return notImplemented('getPackageExportPlan')
}

export async function cancelPackageImport(stagingId: string): Promise<void> {
  void stagingId
  return notImplemented('cancelPackageImport')
}

export async function exportPackage(agentIds: string[]): Promise<PackageDownload> {
  void agentIds
  return notImplemented('exportPackage')
}

export async function uploadPackageImport(file: File): Promise<PackageImportPreview> {
  void file
  return notImplemented('uploadPackageImport')
}

export async function applyPackageImport(
  stagingId: string,
  body: PackageImportApplyRequest,
): Promise<PackageImportResult> {
  void stagingId
  void body
  return notImplemented('applyPackageImport')
}
