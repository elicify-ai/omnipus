/**
 * -wu11-session-scope-client.test.ts — WU-11 RED: the session-scope SPA
 * client is GONE (team-lead decision: a WU-11 preparation commit — removing
 * the client first avoids a compile break when the backend regenerates;
 * the spec itself traces FR-148 to US-16 / landing 2 via K22).
 *
 * Oracles (omnipus-system-agent-spec.md):
 *   - FR-148: "The global `session.dm_scope` setting MUST be retired … and
 *     its `/api/v1/security/session-scope` route removed with its handler,
 *     OpenAPI path, `SessionScopeUpdateResponse` schema and SPA client (K22);
 *     `session.identity_links` stays global (RQ-4 A)."
 *   - K22 (row of the K-table): "… and the SPA client in
 *     `src/lib/api/config.ts`".
 *
 * SCOPE — SPA-authored sources only. `src/lib/api/generated/**` is OUT OF
 * SCOPE: it is regenerated from `contracts/` (the backend lane's K22/WU-0b
 * edit) and no SPA-lane change may demand its diff. Today the client's
 * functions are gone (removed as zero-consumer exports) but the re-exported
 * types and their "future Settings surface" comment REMAIN:
 *   - src/lib/api.ts imports SessionScopeRequest/Response/UpdateResponse
 *   - src/lib/api/config.ts re-exports them with the keep-around comment
 *   - src/lib/api/security.ts hand-writes `type DMScope = 'main' | …`
 * All three are RED today and green after the SPA client is deleted.
 */
import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = join(import.meta.dirname, '..', '..', '..')
const read = (p: string) => readFileSync(join(ROOT, p), 'utf8')

describe('WU-11 RED — the session-scope SPA client is removed (FR-148, K22)', () => {
  it('src/lib/api.ts no longer imports or re-exports the SessionScope wire types', () => {
    const src = read('src/lib/api.ts')
    const lines = src.split('\n')
    const hits = lines
      .map((line, i) => (/SessionScope/.test(line) ? `src/lib/api.ts:${i + 1}: ${line.trim()}` : ''))
      .filter(Boolean)
    expect(hits, 'FR-148/K22: the SPA client of /api/v1/security/session-scope must go').toEqual([])
  })

  it('src/lib/api/config.ts carries no session-scope client remnants or "future Settings surface" reservation', () => {
    const src = read('src/lib/api/config.ts')
    const lines = src.split('\n')
    const hits = lines
      .map((line, i) =>
        (/SessionScope|session-scope|dm_scope|DMScope|dmScope/i.test(line)
          ? `src/lib/api/config.ts:${i + 1}: ${line.trim()}`
          : ''),
      )
      .filter(Boolean)
    expect(hits, 'K22: the session-scope client lives in src/lib/api/config.ts — nothing of it may remain').toEqual([])
  })

  it('src/lib/api/security.ts no longer hand-writes the DMScope type', () => {
    const src = read('src/lib/api/security.ts')
    const lines = src.split('\n')
    const hits = lines
      .map((line, i) =>
        (/DMScope|dm_scope|dmScope|session-scope/i.test(line)
          ? `src/lib/api/security.ts:${i + 1}: ${line.trim()}`
          : ''),
      )
      .filter(Boolean)
    expect(hits, 'FR-148: dm_scope is retired — no hand-written DMScope type may remain in the SPA client').toEqual([])
  })

  it('no SPA-authored source references the session-scope route or wire types', () => {
    // Same walk rules as -wu11-no-manual-create.test.ts: SPA-authored sources
    // only — generated artifacts (contract-owned) and tests are out of scope.
    const skipDirs = new Set(['node_modules', 'generated', 'fixtures'])
    const violations: string[] = []
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir)) {
        const full = join(dir, entry)
        const stat = statSync(full)
        if (stat.isDirectory()) {
          if (!skipDirs.has(entry)) walk(full)
          continue
        }
        if (!/\.(ts|tsx)$/.test(entry) || /\.(test|spec)\.(ts|tsx)$/.test(entry)) continue
        const relPath = full.slice(ROOT.length + 1)
        const src = readFileSync(full, 'utf8')
        if (/SessionScope|security\/session-scope|\bdm_scope\b|\bdmScope\b|\bDMScope\b/.test(src)) {
          violations.push(relPath)
        }
      }
    }
    walk(join(ROOT, 'src'))
    expect(violations, 'FR-148: the SPA no longer touches /api/v1/security/session-scope or dm_scope anywhere').toEqual([])
  })
})
