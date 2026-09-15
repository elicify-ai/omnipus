// skills.ts: Installed skills, the marketplace and slash commands

import { logError } from '../telemetry'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  Skill as SkillSchema,
  SkillSearchResult as SkillSearchResultSchema,
  SkillMarketplaceStatus as SkillMarketplaceStatusSchema,
  // Slash-command harmonization (contract-first #8):
  SlashCommand as SlashCommandSchema,
} from '@/lib/api/generated/schemas'
import type {
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

export async function installSkillFromFile(content: string, filename: string): Promise<void> {
  await request<void>('/skills/install', {
    method: 'POST',
    body: JSON.stringify({ content, filename }),
  })
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
 * ({ slug, version? }). Returns the freshly installed Skill on success.
 * The backend returns 409 when the skill is already installed and 502 when
 * the registry is unreachable.
 */
export async function installSkillBySlug(slug: string, version?: string): Promise<Skill> {
  const body: SkillInstallRequest = version ? { slug, version } : { slug }
  return request<Skill>(
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
  if (dropped > 0 && import.meta.env?.DEV) {

    console.warn(`fetchSkills: dropped ${dropped} skill(s) that failed schema validation`)
  }
  return out
}

export function deleteSkill(name: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
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
