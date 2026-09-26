/**
 * preview-isolation-wiring.test.ts — RED test, ADR-094 TDD Plan order 29
 * (TestPreviewIsolationWiring, round-2 MAJ-007).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md, order 29
 * and the regression table's browser-matrix row: the new spec file
 * tests/e2e/preview-isolation-webserve.spec.ts is a member of BOTH
 * playwright.config.ts::ISOLATION_SPEC_FILES and the preview-isolation
 * shard's specs in tests/e2e/shards.json; fails CI when dropped from either
 * list.
 *
 * RED (current failure mode): the file is in NEITHER list — both membership
 * rows fail today.
 *
 * Runs under plain node (fs) — no browser, no gateway. Vitest picks it up
 * via the tests-glob include in vite.config.ts (see vite.config.ts).
 */

import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const ROOT = resolve(__dirname, '..', '..')
const SPEC = 'tests/e2e/preview-isolation-webserve.spec.ts'

const configText = readFileSync(resolve(ROOT, 'playwright.config.ts'), 'utf-8')
const shardsText = readFileSync(resolve(ROOT, 'tests/e2e/shards.json'), 'utf-8')

describe('preview-isolation-webserve.spec.ts E2E wiring (order 29, MAJ-007)', () => {
  it('is a member of playwright.config.ts ISOLATION_SPEC_FILES', () => {
    // Parse the ISOLATION_SPEC_FILES literal out of the config source rather
    // than importing playwright.config.ts (importing would execute
    // defineConfig and drag the whole config surface into a unit test).
    const match = configText.match(
      /const ISOLATION_SPEC_FILES[^=]*=\s*\[([^\]]*)\]/,
    )
    expect(match, 'ISOLATION_SPEC_FILES literal must exist in playwright.config.ts').not.toBeNull()
    const listed = (match![1] ?? '')
      .split(',')
      .map((s) => s.trim().replace(/^['"]|['"]$/g, ''))
      .filter(Boolean)
    expect(listed).toContain(SPEC)
  })

  it('is a member of the preview-isolation shard specs in tests/e2e/shards.json', () => {
    const shards = JSON.parse(shardsText) as {
      shards: Array<{ group: string; specs: string[] }>
    }
    const shard = shards.shards.find((s) => s.group === 'preview-isolation')
    expect(shard, 'preview-isolation shard must exist').toBeDefined()
    expect(shard!.specs).toContain(SPEC)
  })

  it('guard: the spec file EXISTS on disk at the wired path', () => {
    // If someone deletes the spec while leaving the lists intact, the
    // shard checker's dead-reference error is the only tripwire — this row
    // makes the breakage loud in unit CI too.
    expect(() => readFileSync(resolve(ROOT, SPEC))).not.toThrow()
  })
})
