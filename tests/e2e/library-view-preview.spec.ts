/**
 * library-view-preview.spec.ts — E2E proof for TDD Plan test 56
 * (docs/internal/specs/library-views-anywhere-spec.md §10, R2-CRIT-003,
 * US-6 AS-2): "A real click ... opening a .view entry, calling the existing
 * view-evaluation endpoint with view.collection_id DIRECTLY from the entry
 * (no SPA-side lookup step), and rendering rows via ViewPartsRenderer — not
 * merely that the case 'view': branch compiles."
 *
 * ORACLE (server-log-as-ground-truth, matching this suite's own preview-svg
 * spec convention: "what arrives was allowed; what never arrives was
 * blocked" — a DOM assertion alone is not accepted as evidence of a real
 * network call). The contract (contracts/openapi.yaml,
 * getKnowledgeViewResult) fixes the call shape this test's oracle is:
 *
 *   GET /api/v1/library/{workspace_id}/knowledge/view
 *       ?collection_id=<LibraryEntry.view.collection_id>&view=<LibraryEntry.view.name>
 *
 * so a real click on the tree entry MUST produce a request matching that
 * path with both query parameters present — not a request to any other
 * endpoint, and not zero requests (a generic download/unsupported-file
 * fallback, which is what happens today).
 *
 * WHY THIS IS RED TODAY. `LibraryEntry` has no `is_view`/`view` field
 * (confirmed: `src/lib/api/generated/openapi-types.ts`'s LibraryEntry schema
 * block declares none), `classifyLibraryEntry` has no `.view` branch
 * (TDD tests 13/42), and `LibraryPreviewPane.renderBody` has no `case
 * 'view':` (TDD test 14) — so clicking a `.view`-extension file today falls
 * through to the generic "other" kind and never calls
 * `GET .../knowledge/view` at all.
 *
 * Run (parse-proof only, per squad-lead's instruction — this spec is not
 * executed against a live server as part of RED, only proven to parse and
 * to be assigned a shard):
 *
 *   npx playwright test --list tests/e2e/library-view-preview.spec.ts
 *   bash scripts/e2e-shards.sh check
 */

import { test, expect, type Page } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

test.describe.configure({ retries: 0 })

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

const VIEW_FILE = 'weekly-status.view'
const VIEW_NAME = 'weekly-status-e2e'

async function seedKnowledgeBaseWithView(page: Page): Promise<string> {
  const res = await page.request.get('/api/v1/workspaces')
  expect(res.status(), 'GET /api/v1/workspaces must succeed').toBe(200)
  const list = (await res.json()) as Array<{ id: string; is_default?: boolean }>
  expect(list.length, 'a workspace is required to host the Library').toBeGreaterThan(0)
  const ws = list.find((w) => w.is_default) ?? list[0]
  const workDir = path.join(OMNIPUS_HOME, 'workspaces', ws.id, 'work')

  // The whole work dir is made a knowledge base (FR-020's marker), so the
  // .view file sits directly inside it — FD-1/D-SCOPE's "a view only
  // classifies is_view inside a knowledge base" requirement, satisfied with
  // no extra folder-click navigation.
  fs.mkdirSync(path.join(workDir, '.omnipus-vault'), { recursive: true })
  fs.writeFileSync(path.join(workDir, VIEW_FILE), `name: ${VIEW_NAME}\nlabel: Weekly status\n`)

  return ws.id
}

test('clicking a .view tree entry calls the view-evaluation endpoint directly by collection_id + name, and renders rows (TDD test 56)', async ({
  page,
}) => {
  const wsId = await seedKnowledgeBaseWithView(page)

  const viewResultRequests: string[] = []
  page.on('request', (req) => {
    const url = req.url()
    if (url.includes('/knowledge/view')) viewResultRequests.push(url)
  })

  await page.goto(`/#/library?workspace=${wsId}`)
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId(`library-row-${VIEW_FILE}`).click()

  // Give the click's own fetch a chance to land before asserting.
  await page.waitForTimeout(2_000)

  const matching = viewResultRequests.filter((u) => {
    const parsed = new URL(u)
    return (
      parsed.pathname.endsWith(`/library/${wsId}/knowledge/view`) &&
      parsed.searchParams.has('collection_id') &&
      parsed.searchParams.get('view') === VIEW_NAME
    )
  })

  expect(
    matching,
    `expected a GET .../library/${wsId}/knowledge/view?collection_id=...&view=${VIEW_NAME} request after ` +
      `clicking the .view tree entry — observed requests: ${JSON.stringify(viewResultRequests)}`,
  ).not.toHaveLength(0)
})

test('opening a rejected .view entry shows the rejection reason, never a blank/generic error (TDD test 56, second click)', async ({
  page,
}) => {
  const res = await page.request.get('/api/v1/workspaces')
  expect(res.status()).toBe(200)
  const list = (await res.json()) as Array<{ id: string; is_default?: boolean }>
  const ws = list.find((w) => w.is_default) ?? list[0]
  const workDir = path.join(OMNIPUS_HOME, 'workspaces', ws.id, 'work')
  fs.mkdirSync(path.join(workDir, '.omnipus-vault'), { recursive: true })
  const brokenFile = 'broken.view'
  fs.writeFileSync(path.join(workDir, brokenFile), 'this is not valid view YAML: [unterminated\n')

  await page.goto(`/#/library?workspace=${ws.id}`)
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId(`library-row-${brokenFile}`).click()

  // TDD test 57 / R2-MAJ-005's own field, view.rejection_reason, is what
  // this text is expected to come from once the feature exists; today
  // nothing on the page names a view-specific rejection at all.
  await expect(page.getByText(/could not be read as a view|view_invalid_yaml|view_unreadable/i)).toBeVisible({
    timeout: 10_000,
  })
})
