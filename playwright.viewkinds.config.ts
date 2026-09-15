import { defineConfig } from '@playwright/test'

/**
 * The view-kinds UAT browser suite — a SEPARATE config, deliberately.
 *
 * ## Why it is not in tests/e2e/
 *
 * Every spec under `tests/e2e/` is governed by `tests/e2e/shards.json`, and
 * `scripts/e2e-shards.sh check` fails CI if one is unassigned. Assigning this
 * suite to a shard would make CI run it — and CI cannot run it yet, for a
 * reason that is a real product limitation rather than a harness gap:
 *
 *   A knowledge base is indexed for the collections that exist AT GATEWAY
 *   BOOT. A vault dropped into a workspace afterwards is detected by the
 *   library API (`is_knowledge_base: true`, a collection_id and all) but its
 *   properties index never opens, so every view against it answers
 *   `index_unavailable` — observed to persist for 120s with no recovery, and
 *   cleared only by restarting the gateway.
 *
 * So a spec cannot seed its own vault the way `preview-pdf.spec.ts` seeds its
 * PDFs: by the time a test runs, the gateway has booted. The fixture has to be
 * in place BEFORE the gateway starts, which is `scripts/uat/`'s job and not
 * something a Playwright fixture can do for itself.
 *
 * Putting the suite here keeps both facts honest: the shard guard still says
 * every CI spec is assigned, and this suite is visibly a separately-driven one
 * rather than a spec that silently never runs.
 *
 * ## Running it
 *
 *   node scripts/uat/gen-viewkinds-e2e-fixture.mjs --out /tmp/vk --force
 *   OMNIPUS_BIN=<binary> UAT_USER=admin UAT_PASS=admin123 \
 *     scripts/uat/setup-viewkinds-instance.sh /tmp/vk-home 6161 /tmp/vk/vault E2E
 *   OMNIPUS_URL=http://127.0.0.1:6161 \
 *   OMNIPUS_HOME=/tmp/vk-home \
 *   VIEWKINDS_E2E_FACTS=/tmp/vk/e2e-facts.json \
 *   OPENROUTER_API_KEY_CI=$OPENROUTER_API_KEY \
 *     npx playwright test --config=playwright.viewkinds.config.ts
 *
 * It runs against the EMBEDDED binary (the gateway serving `pkg/gateway/spa`),
 * never the Vite dev server — a dev-server pass says nothing about what a
 * released binary ships.
 *
 * ## retries: 0
 *
 * Nothing here involves a model, so the main config's retry allowance (which
 * exists for real-LLM latency variance) buys nothing and costs the ability to
 * tell a real renderer regression from a flake. A view either drew the number
 * or it did not.
 */
export default defineConfig({
  testDir: './tests/e2e-viewkinds',
  globalSetup: './tests/e2e/global-setup.ts',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['list']],
  use: {
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6161',
    storageState: process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [{ name: 'view-kinds', use: { browserName: 'chromium' } }],
})
