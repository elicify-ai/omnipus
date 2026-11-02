// skills.ts: Installed skills, the marketplace and slash commands

import { logError } from '../telemetry'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  Skill as SkillSchema,
  SkillSearchResult as SkillSearchResultSchema,
  SkillMarketplaceStatus as SkillMarketplaceStatusSchema,
  ConfigurationMutationState as ConfigurationMutationStateSchema,
  // Slash-command harmonization (contract-first #8):
  SlashCommand as SlashCommandSchema,
} from '@/lib/api/generated/schemas'
import type {
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  SkillCopyRequest,
  ConfigurationMutationState,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'
import { requestConfiguration } from './configuration'
import { uploadFiles } from './uploads'

/**
 * The 409 code POST /skills/install returns when the replaced skill has local
 * changes and the request did not confirm replacing them (skills spec r15 §5.6).
 */
export const LOCAL_CHANGES_CONFIRMATION_REQUIRED = 'LOCAL_CHANGES_CONFIRMATION_REQUIRED'

/**
 * The 409 code an install or copy returns when the target name is a built-in
 * skill's (skills spec r15 §5.10). Its text names the name and the remedy.
 */
export const BUILTIN_NAME_RESERVED = 'BUILTIN_NAME_RESERVED'

/**
 * installSkillFromFile installs an uploaded SKILL.md / zip package. Pass
 * `replaceLocalChanges` only after the person confirmed replacing a skill
 * whose `has_local_changes` is true; the replaced state stays in its history.
 */
export async function installSkillFromFile(
  file: File,
  uploadContextId: string,
  revision?: string,
  signal?: AbortSignal,
  replaceLocalChanges = false,
): Promise<Skill> {
  const uploaded = await uploadFiles(uploadContextId, [file], undefined, signal)
  const uploadRef = uploaded.files[0]?.ref
  if (!uploadRef) {
    throw new Error('Upload completed without an installable media reference.')
  }
  const body: SkillInstallRequest = {
    upload_id: uploadRef,
    ...(revision ? { revision } : {}),
    ...(replaceLocalChanges ? { replace_local_changes: true } : {}),
  }
  return requestConfiguration<Skill>(
    '/skills/install',
    { method: 'POST', body: JSON.stringify(body), signal },
    SkillSchema as ZodType<Skill>,
  )
}

/**
 * searchSkills queries the ClawHub marketplace registry via
 * GET /api/v1/skills/search?q=<query>&limit=<n>. Returns an array of
 * SkillSearchResult (marketplace hits, NOT installed skills). The backend
 * returns 400 for an empty/blank query and 502 when the registry is
 * unreachable — both surface as a typed ApiError to the caller.
 */
export async function searchSkills(q: string, limit = 20): Promise<SkillSearchResult[]> {
  const params = new URLSearchParams({ q, limit: String(limit) })
  return request<SkillSearchResult[]>(
    `/skills/search?${params.toString()}`,
    undefined,
    z.array(SkillSearchResultSchema) as ZodType<SkillSearchResult[]>,
  )
}

/**
 * installSkillBySlug installs a marketplace skill by its slug via
 * POST /api/v1/skills/install with a SkillInstallRequest body
 * ({ slug, version?, revision?, replace_local_changes? }). Returns the freshly
 * installed Skill on success. The backend returns 409 when the skill is
 * already installed, when the slug is a built-in name, and — with code
 * LOCAL_CHANGES_CONFIRMATION_REQUIRED — when a replacement would discard
 * local changes that `replaceLocalChanges` did not confirm; 502 when the
 * registry is unreachable.
 */
export async function installSkillBySlug(
  slug: string,
  version?: string,
  revision?: string,
  replaceLocalChanges = false,
): Promise<Skill> {
  const body: SkillInstallRequest = {
    slug,
    ...(version ? { version } : {}),
    ...(revision ? { revision } : {}),
    ...(replaceLocalChanges ? { replace_local_changes: true } : {}),
  }
  return requestConfiguration<Skill>(
    '/skills/install',
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
    SkillSchema as ZodType<Skill>,
  )
}

/**
 * fetchSkillMarketplaceStatus reports whether a skill marketplace is enabled
 * via GET /api/v1/skills/marketplace. When `enabled` is false the SPA hides
 * the search/browse UI and offers only file-based install — the backend
 * returns 409 for /skills/search and /skills/install in that state.
 */
export async function fetchSkillMarketplaceStatus(): Promise<SkillMarketplaceStatus> {
  return request<SkillMarketplaceStatus>(
    '/skills/marketplace',
    undefined,
    SkillMarketplaceStatusSchema as ZodType<SkillMarketplaceStatus>,
  )
}

// ── Skills ────────────────────────────────────────────────────────────────────

// Skill — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Skill.yaml.

// McpServer — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/McpServer.yaml.
// McpServerCreate — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/McpServerCreate.yaml.

/**
 * Read a skill's last-invocation timestamp (ISO 8601), or `null` when the
 * skill has never been invoked or the backend has no audit history for it.
 * `last_invoked` is a real `Skill` wire field (ADR-072 D3.1,
 * contracts/components/schemas/Skill.yaml) populated by
 * `pkg/gateway/rest.go::listSkills` from `pkg/audit.Logger
 * ::LastInvokedForSkill` — validated by `SkillSchema` like every other
 * `Skill` field, no raw-body access involved.
 */
export function skillLastInvoked(skill: Skill): string | null {
  return skill.last_invoked ?? null
}

export async function fetchSkills(): Promise<Skill[]> {
  // Tolerant per-item validation: a single skill whose payload fails the Skill
  // schema must NOT hide the entire installed-skills list. (A community/ClawHub
  // skill with an unexpected field value previously made the whole list silently
  // vanish.) Validate each item, keep the valid ones, drop + warn on the rest.
  const raw = await request<unknown[]>('/skills')
  if (!Array.isArray(raw)) return []
  const out: Skill[] = []
  let dropped = 0
  for (const item of raw) {
    const parsed = SkillSchema.safeParse(item)
    if (parsed.success) {
      out.push(parsed.data as Skill)
    } else dropped++
  }
  if (dropped > 0) {
    if (import.meta.env?.DEV) {
      console.warn(`fetchSkills: dropped ${dropped} skill(s) that failed schema validation`)
    } else if (import.meta.env?.MODE !== 'test') {
      logError({
        event: 'skillSchemaDrop',
        droppedCount: dropped,
        totalCount: raw.length,
      })
    }
  }
  return out
}

/**
 * copySkill copies an installed skill (built-in or user-owned) to a new
 * user-owned skill via POST /api/v1/skills/{name}/copy. 201 returns the new
 * Skill; 400 invalid name, 404 missing source, 409 name taken (any letter
 * case) or reserved; 500 carries ConfigurationMutationFailureState and is
 * raised as ConfigurationSaveError (skills spec r15 C-4, §5.8).
 */
export function copySkill(id: string, newId: string): Promise<Skill> {
  const body: SkillCopyRequest = { new_id: newId }
  return requestConfiguration<Skill>(
    `/skills/${encodeURIComponent(id)}/copy`,
    { method: 'POST', body: JSON.stringify(body) },
    SkillSchema as ZodType<Skill>,
  )
}

export function deleteSkill(id: string, revision: string): Promise<ConfigurationMutationState> {
  return requestConfiguration<ConfigurationMutationState>(
    `/skills/${encodeURIComponent(id)}?${new URLSearchParams({ revision })}`,
    { method: 'DELETE' },
    ConfigurationMutationStateSchema as ZodType<ConfigurationMutationState>,
  )
}

// ── Slash commands ─────────────────────────────────────────────────────────────

// SlashCommand is re-exported from the `export type {}` block above (contract-first #8).
// See contracts/components/schemas/SlashCommand.yaml.

// fetchCommands — mirrors fetchSkills; fetches the surface-applicable slash commands
// from GET /api/v1/commands?surface=<surface>.  Per US-4 / FR-008 / SC-005, the
// web palette must render from this endpoint and never from a hardcoded list.
// Tolerant per-item validation: a single malformed item must NOT hide the entire list.
export async function fetchCommands(surface: 'web' | 'cli' | 'channel' = 'web'): Promise<SlashCommand[]> {
  const raw = await request<unknown[]>(`/commands?surface=${encodeURIComponent(surface)}`)
  if (!Array.isArray(raw)) return []
  const out: SlashCommand[] = []
  let dropped = 0
  for (const item of raw) {
    const parsed = SlashCommandSchema.safeParse(item)
    if (parsed.success) out.push(parsed.data)
    else dropped++
  }
  if (dropped > 0) {
    if (import.meta.env?.DEV) {
       
      console.warn(`fetchCommands: dropped ${dropped} command(s) that failed schema validation`)
    } else if (import.meta.env?.MODE !== 'test') {
      // Bugfix (slash-palette silent-empty): this warning used to be DEV-only,
      // so a production build that dropped a command for failing
      // SlashCommandSchema had ZERO observable trace — the palette just
      // looked short with no signal anywhere. Mirrors recordCoercion /
      // _recordApiSchemaError's established DEV-console.warn-vs-production-
      // logError split (this file, above).
      logError({
        event: 'commandSchemaDrop',
        surface,
        droppedCount: dropped,
        totalCount: raw.length,
      })
    }
  }
  return out
}
