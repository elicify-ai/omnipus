// security.ts: Credentials, audit log, god-mode, sandbox, and backups

import { maybeDevToast } from '../dev-toast'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  AuditLogResponse as AuditLogResponseSchema,
  AuditEntry as AuditEntrySchema,
  BackupEntry as BackupEntrySchema,
  ExecProxyStatus as ExecProxyStatusSchema,
  PendingRestartEntry as PendingRestartEntrySchema,
  PromptGuardResponse as PromptGuardResponseSchema,
  SandboxConfig as SandboxConfigSchema,
  SandboxStatus as SandboxStatusSchema,
  SkillTrustResponse as SkillTrustResponseSchema,
  // Newly promoted from inline openapi.yaml schemas:
  SkillTrustUpdateResponse as SkillTrustUpdateResponseSchema,
  PromptGuardUpdateResponse as PromptGuardUpdateResponseSchema,
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse as GatewayRestartResponseSchema,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus as GodModeStatusSchema,
  GodModeUpdateResponse as GodModeUpdateResponseSchema,
  // WP4: local backup — off in platform mode (contract-first #8):
  BackupCreateResponse as BackupCreateResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  SandboxConfig,
  SandboxConfigUpdate,
  SandboxStatus,
  AuditLogResponse,
  BackupEntry,
  ExecProxyStatus,
  SkillTrustResponse,
  PromptGuardResponse,
  PendingRestartEntry,
  // Newly promoted from inline openapi.yaml schemas:
  SkillTrustUpdateRequest,
  SkillTrustUpdateResponse,
  PromptGuardUpdateRequest,
  PromptGuardUpdateResponse,
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus,
  GodModeUpdateRequest,
  GodModeUpdateResponse,
  // WP4: local backup — off in platform mode (contract-first #8):
  BackupCreateResponse,
} from '@/lib/api/generated/openapi-types'
import { REAUTH_HEADER } from './auth'
import { ApiSchemaError, _recordApiSchemaError, request } from './http'

// ── Credentials ───────────────────────────────────────────────────────────────

export interface CredentialKey { // not-wire-format: SPA-internal credential display shape. The wire GET /credentials endpoint returns string[] (key names only); the component accesses .key on each entry. This interface reflects how the SPA displays credential entries but does NOT match the wire format. The wire format is defined inline in the openapi.yaml /credentials GET response schema as string[].
  key: string
  created_at?: string
  updated_at?: string
}

export async function fetchCredentials(): Promise<CredentialKey[]> {
  // Wire format: GET /credentials returns string[] (key names only).
  // The SPA uses CredentialKey[] (objects with .key) so SecuritySection.tsx can
  // render cred.key and use it as a React key. We validate the wire shape, then
  // transform string[] → {key:string}[].
  const wire = await request<string[]>('/credentials', undefined, z.array(z.string()) as ZodType<string[]>)
  return wire.map((key) => ({ key }))
}

// Credential add/delete are re-auth gated server-side (ADR-022 / Spec-6 FR-12.2):
// the server rejects with 403 unless a single-use consent token (from reAuth) is
// replayed in the X-Reauth-Token header. Pass reAuthToken via runGated().
export function addCredential(key: string, value: string, reAuthToken?: string): Promise<void> {
  // no-schema: void response; POST body is a write-only operation.
  return request<void>('/credentials', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify({ key, value }),
  })
}

export function deleteCredential(key: string, reAuthToken?: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/credentials/${encodeURIComponent(key)}`, {
    method: 'DELETE',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
  })
}

// rotateCredentials re-encrypts the whole vault under a new passphrase (G5). Like
// add/delete it is re-auth gated server-side (ADR-022): pass reAuthToken via runGated().
export function rotateCredentials(newPassphrase: string, reAuthToken?: string): Promise<void> {
  // no-schema: void/{status} response; POST body triggers re-encryption.
  return request<void>('/credentials/rotate', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify({ new_passphrase: newPassphrase }),
  })
}

// ── Backup / Restore (WP4: local backup — off in platform mode) ────────────────
//
// createBackup/fetchBackups/restoreBackup only ever succeed against a local-mode
// engine (config.EditionAuthMode() == config.AuthModeLocal); the server answers
// 404 in platform mode and the routes are not even registered there. The
// caller in DataSection.tsx is responsible for not rendering this section
// outside local mode — these functions do not themselves check the mode.

// BackupEntry — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/BackupEntry.yaml.

export function createBackup(): Promise<BackupCreateResponse> {
  return request<BackupCreateResponse>('/backup', { method: 'POST' }, BackupCreateResponseSchema as ZodType<BackupCreateResponse>)
}

export function fetchBackups(): Promise<BackupEntry[]> {
  return request<BackupEntry[]>('/backups', undefined, z.array(BackupEntrySchema))
}

// restoreBackup overwrites the whole vault, so it takes the step-up gate like
// the credential writes above: the consent token in local mode, none in
// platform mode (where the route does not exist anyway).
export function restoreBackup(filename: string, reAuthToken?: string): Promise<void> {
  // no-schema: void response; 204 No Content on success.
  return request<void>('/restore', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify({ filename }),
  })
}

// ── Audit Log ─────────────────────────────────────────────────────────────────

export type AuditEventType = 'tool_call' | 'exec' | 'file_op' | 'llm_call' | 'policy_eval' | 'rate_limit' | 'ssrf' | 'startup' | 'shutdown'

export type AuditDecision = 'allow' | 'deny' | 'error'

// AuditEntry — re-exported from generated openapi-types (no local body needed).
// AuditEventType and AuditDecision remain as local type aliases for UI use.

// Top-level shape assertion for GET /audit-log. `entries` is deliberately
// typed as an array of UNKNOWN here so the array itself is not rejected
// wholesale — each element is validated separately by fetchAuditLog below.
// Everything else on the envelope (chain_status, chain_broken_index) is
// re-validated against the generated AuditLogResponse schema afterwards, so
// the generated schema stays the single source of truth (Constraint #8) and
// genuine envelope drift still fails loudly.
const AuditLogEnvelopeShapeSchema = z.object({ entries: z.array(z.unknown()) }).passthrough()

/**
 * GET /api/v1/audit-log — with PER-ENTRY validation.
 *
 * Issue #667, second half. Validating the whole AuditLogResponse in one
 * `safeParse` (as `request()` does for every other endpoint) means `entries:
 * z.array(AuditEntry)` fails as a UNIT: a single record the schema rejects
 * throws ApiSchemaError for the entire response and AuditLogViewer renders
 * "Failed to load audit log" — the operator sees NOTHING, not "one row
 * missing". Widening the `event` pattern to allow dots fixed the names that
 * exist today; it did not remove the fragility, and an audit file is
 * append-only history nobody can rewrite, so the next unrecognised name has
 * exactly the same blast radius.
 *
 * Per CLAUDE.md hard-constraint #8 the SPA edge drops the bad item, bumps the
 * counter and shows a dev-mode toast instead of crashing. This mirrors
 * `parseWireMessageList` above: invalid entries are counted through the
 * SHARED `_recordApiSchemaError` path (dev toast + production telemetry +
 * the window.__omnipus_test_hooks counter), never a parallel counter.
 *
 * Unlike the message list this does NOT substitute a placeholder row. An
 * audit record is evidence: a synthesised row would need an invented
 * timestamp to satisfy the schema, and a fabricated timestamp interleaved
 * into an append-only chronological log is worse than an absent row. The
 * drop is surfaced through the counter/telemetry channel instead.
 */
export async function fetchAuditLog(): Promise<AuditLogResponse> {
  const endpoint = 'GET /api/v1/audit-log'
  // A non-object body, or `entries` that is not an array at all, is genuine
  // contract drift and still throws ApiSchemaError from request().
  const raw = await request<{ entries: unknown[] }>(
    '/audit-log',
    undefined,
    AuditLogEnvelopeShapeSchema as unknown as ZodType<{ entries: unknown[] }>,
  )

  const kept: unknown[] = []
  let dropped = 0
  let firstIssue: string | undefined
  for (const item of raw.entries) {
    const result = AuditEntrySchema.safeParse(item)
    if (result.success) {
      kept.push(result.data)
      continue
    }
    dropped++
    firstIssue ??= result.error.issues[0]?.message
    _recordApiSchemaError(endpoint, result.error.issues.length)
  }
  if (dropped > 0) {
    void maybeDevToast(
      `[api] Dropped ${dropped} unreadable audit record${dropped === 1 ? '' : 's'} from ${endpoint}: ${firstIssue ?? 'unknown'}`,
      `${endpoint}:audit-entry-schema`,
    )
  }

  // Envelope re-validation against the GENERATED schema. Every surviving
  // entry already passed AuditEntry, so a failure here can only come from the
  // envelope's own fields (e.g. a chain_status outside the enum) — real
  // contract drift that must stay loud.
  const parsed = AuditLogResponseSchema.safeParse({ ...raw, entries: kept })
  if (!parsed.success) {
    _recordApiSchemaError(endpoint, parsed.error.issues.length)
    const schemaErr = new ApiSchemaError(
      endpoint,
      parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message })),
      raw,
    )
    void maybeDevToast(
      `[api] Schema mismatch: /audit-log — ${schemaErr.zodIssues[0]?.message ?? 'unknown'}`,
      'GET:/audit-log:schema',
    )
    throw schemaErr
  }
  return parsed.data
}

// ── Security Admin Endpoints ──────────────────────────────────────────────────
//
// Enums and typed helpers for the security and admin endpoints.
// These are separate from the pre-existing /security/* helpers above — they use
// the canonical request/response shapes and are wired to the admin UI panels.

export type SkillTrustLevel = 'block_unverified' | 'warn_unverified' | 'allow_all'

export type PromptInjectionLevel = 'low' | 'medium' | 'high'

export type DMScope = 'main' | 'per-peer' | 'per-channel-peer' | 'per-account-channel-peer'

// PendingRestartEntry — re-exported from generated openapi-types (no local body needed).

export function fetchPendingRestart(): Promise<PendingRestartEntry[]> {
  return request<PendingRestartEntry[]>('/config/pending-restart', undefined, z.array(PendingRestartEntrySchema) as ZodType<PendingRestartEntry[]>)
}

// GatewayRestartResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/GatewayRestartResponse.yaml.
//
// POST /api/v1/gateway/restart triggers a graceful self-restart. The server replies
// with 202 Accepted (status:"restarting") immediately, before re-execing. It is
// secured by RequireNotBypass (returns 503 when dev_mode_bypass is active).
export function gatewayRestart(): Promise<GatewayRestartResponse> {
  return request<GatewayRestartResponse>('/gateway/restart', {
    method: 'POST',
  }, GatewayRestartResponseSchema)
}

// ── God-mode (O14) ─────────────────────────────────────────────────────────────
//
// God-mode is the single global "bypass-permissions" switch: flipping it ON
// floors every agent's tool policy at "allow" (no prompts), turns the kernel
// sandbox off, opens network egress, and disables the shell guard — regardless
// of per-agent profiles. Audit logging, the prompt-injection guard, and rate
// limiting STAY ON. The per-agent overrides are non-destructive: switching god
// mode off restores prior behaviour exactly.
//
// GodModeStatus / GodModeUpdateRequest / GodModeUpdateResponse are the
// generated contract types (#8); see contracts/components/schemas/GodMode*.yaml.
// fetchGodMode reads the live runtime state ({ enabled, available, supported });
// setGodMode flips it and returns { enabled, restart_required } — a distinct
// shape because enabling from an unauthorized boot persists config but does
// NOT take live effect until the gateway restarts (see restart_required).
//
// `supported` (build support, nogodmode tag) and `available` (this boot was
// authorized, either via --allow-god-mode or a prior UI enable + restart) are
// DIFFERENT gates. Enabling is permitted whenever `supported` is true, even
// if `available` is currently false — that is exactly the UI-driven
// enablement flow: flip switch -> persist authorization -> restart to
// activate. Enabling when `supported` is false always returns 403.
//
// The SPA confirms the flip with the operator before calling (ADR-0008 ruling
// 6); on the wire the guard is the authenticated session.

export function fetchGodMode(): Promise<GodModeStatus> {
  return request<GodModeStatus>('/gateway/god-mode', undefined, GodModeStatusSchema)
}

export function setGodMode(enabled: boolean, reAuthToken?: string): Promise<GodModeUpdateResponse> {
  const body: GodModeUpdateRequest = { enabled }
  return request<GodModeUpdateResponse>('/gateway/god-mode', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, GodModeUpdateResponseSchema)
}

// Skill trust — controls how unverified community skills are handled.
// SkillTrustResponse is re-exported from generated openapi-types above.
// SkillTrustUpdateRequest — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SkillTrustUpdateRequest.yaml.
// SkillTrustUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SkillTrustUpdateResponse.yaml.

export function fetchSkillTrust(): Promise<SkillTrustResponse> {
  return request<SkillTrustResponse>('/security/skill-trust', undefined, SkillTrustResponseSchema)
}

export function updateSkillTrust(level: SkillTrustLevel): Promise<SkillTrustUpdateResponse> {
  return request<SkillTrustUpdateResponse>('/security/skill-trust', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies SkillTrustUpdateRequest),
  }, SkillTrustUpdateResponseSchema)
}

// Prompt guard — uses `level` field, aligns with PromptInjectionLevel.
// PromptGuardResponse is re-exported from generated openapi-types above.
// PromptGuardUpdateRequest — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/PromptGuardUpdateRequest.yaml.
// PromptGuardUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/PromptGuardUpdateResponse.yaml.

export function fetchPromptGuardLevel(): Promise<PromptGuardResponse> {
  return request<PromptGuardResponse>('/security/prompt-guard', undefined, PromptGuardResponseSchema)
}

export function updatePromptGuardLevel(level: PromptInjectionLevel): Promise<PromptGuardUpdateResponse> {
  return request<PromptGuardUpdateResponse>('/security/prompt-guard', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies PromptGuardUpdateRequest),
  }, PromptGuardUpdateResponseSchema)
}

// Sandbox config — mode, filesystem model, allowed paths, SSRF controls,
// God Mode, the workspace path guard, and the global Auto-approve default.
// SandboxConfig — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SandboxConfig.yaml.
// SandboxConfigUpdate — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SandboxConfigUpdate.yaml.

// SandboxConfigResponse is a backward-compat alias for the generated SandboxConfig.
// SandboxConfig already contains requires_restart, applied_mode, saved, and all
// sandbox fields — no extra SPA-specific shape is needed.
export type SandboxConfigResponse = SandboxConfig

export function fetchSandboxConfig(): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', undefined, SandboxConfigSchema)
}

// updateSandboxConfig persists a sandbox-config mutation. It is re-auth gated
// (Spec-6 FR-12.2): the server rejects the PUT with 403 unless a single-use
// consent token (from reAuth) is replayed in the X-Reauth-Token header.
export function updateSandboxConfig(
  body: SandboxConfigUpdate,
  reAuthToken?: string,
): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, SandboxConfigSchema)
}

// ── Exec Proxy ────────────────────────────────────────────────────────────────
// ExecProxyStatus — re-exported from generated openapi-types (no local body needed).

export function fetchExecProxyStatus(): Promise<ExecProxyStatus> {
  return request<ExecProxyStatus>('/security/exec-proxy-status', undefined, ExecProxyStatusSchema)
}

// ── Sandbox Status ────────────────────────────────────────────────────────────
// SandboxStatus — re-exported from generated openapi-types (no local body needed).
// The generated schema is a superset of the previous hand-written shape.

export function fetchSandboxStatus(): Promise<SandboxStatus> {
  return request<SandboxStatus>('/security/sandbox-status', undefined, SandboxStatusSchema)
}

export const auditLogQueryKeys = {
  list: () => ['audit-log'] as const,
}
