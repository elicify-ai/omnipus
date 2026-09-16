/**
 * truncation-notice-live.spec.ts — ADR-087 D2, finding #10.
 *
 * The sibling `truncation-notice.spec.ts` seeds a synthetic transcript entry
 * directly on disk and asserts the "(cut off at the output limit)" notice
 * appears AFTER a reload — it never proves the notice appears live, while
 * the turn is still on screen, because the fixture never drives a real
 * WebSocket turn at all. Finding #10 (code review of ADR-087's
 * implementation) is specifically that gap: a turn that hits the provider's
 * output-token limit WHILE THE USER IS WATCHING rendered no notice until a
 * reload or reconnect replayed it back in. `DoneStats` gained
 * `truncated`/`truncation_reason` (mirrors `Message.truncation_reason`) and
 * `store/chat.ts`'s `case 'done'` reducer now stamps them on the finishing
 * bubble — this spec is the live-turn counterpart that actually proves it.
 *
 * MECHANISM: forces a real provider truncation by setting the target
 * agent's `model_params.max_tokens` very low via `PUT /api/v1/agents/{id}`
 * (`contracts/components/schemas/AgentUpdateRequest.yaml`'s `model_params`),
 * then sends a prompt engineered to produce far more than that many tokens.
 * A real OpenRouter-backed model (`OPENROUTER_API_KEY_CI`, enforced by
 * `global-setup.ts`'s preflight for the whole suite) will then genuinely hit
 * `finish_reason: "length"` — no gateway/provider mocking, matching every
 * other real-LLM spec in this directory (`cancel-cross-channel.spec.ts`,
 * `verifier-eval.spec.ts`).
 *
 * CAPABILITY GUARD: the per-agent `model_params.max_tokens` PERSISTENCE path
 * (`config.AgentConfig.ModelParams`, `pkg/config/config.go`) is newer than
 * the wire contract itself and may not be present in every build this spec
 * runs against — `pkg/gateway/rest.go`/`pkg/config/config.go` are out of
 * this agent's scope. Rather than assume it is always wired, this spec
 * verifies the PUT actually round-trips (not just a 200 — CLAUDE.md's own
 * false-green-patterns doc and the Q1 regression this endpoint just fixed
 * are both about a PUT that returns 200 and persists nothing) via an
 * independent GET, and FAILS with a BLOCKED error if it does not — it does
 * NOT silently pass, and it does NOT skip: a missing capability is red by
 * policy ("use … a BLOCKED message so CI shows red, not skipped" —
 * fixtures/skip-tracking.ts). Converted 2026-09-16 from a raw test.skip()
 * that bypassed the skip gate entirely.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, agentPicker, waitForConnected } from './fixtures/selectors'
import { apiFetch } from './fixtures/conformance-helpers'

interface AgentSummary {
  id: string
  name: string
  model_params?: { max_tokens?: number | null; temperature?: number | null } | null
}

/** A prompt engineered to overrun a tiny max_tokens budget with real prose,
 * no tool calls (mirrors cancel-cross-channel.spec.ts's LONG_PROSE_PROMPT —
 * forbidding tools keeps the model from shortcutting to a `write_file` call
 * that would end the turn instantly with nothing to truncate). */
const LONG_PROSE_PROMPT =
  'Do not use any tools. Reply only with inline prose, no files. Write a detailed ' +
  '500-word explanation of how photosynthesis works, beginning immediately and ' +
  'continuing without stopping.'

/** Deliberately tiny — small enough that even a terse reply overruns it,
 * large enough that the model produces a few real words before the cutoff
 * (D4b territory) rather than nothing at all (D4a) most of the time; either
 * outcome still stamps `truncated`/`truncation_reason` on the live `done`. */
const TINY_MAX_TOKENS = 40

async function findAgentIdByName(page: Page, nameRe: RegExp): Promise<AgentSummary | null> {
  const res = await apiFetch<AgentSummary[]>(page, 'GET', '/api/v1/agents')
  if (!res.ok || !Array.isArray(res.body)) return null
  return res.body.find((a) => nameRe.test(a.name)) ?? null
}

/**
 * Set the agent's max_tokens and verify it actually persisted via an
 * INDEPENDENT GET (not just the PUT response echo) — the exact round-trip
 * the Q1 regression this endpoint fixed was missing. Returns the ORIGINAL
 * model_params (for restoration) on success, or `'unsupported'` if the
 * override did not take — the caller must FAIL with a BLOCKED error in
 * that case rather than proceed against an agent that will never truncate.
 */
async function tryForceMaxTokens(
  page: Page,
  agentId: string,
  maxTokens: number,
): Promise<AgentSummary['model_params'] | null | 'unsupported'> {
  const before = await apiFetch<AgentSummary>(page, 'GET', `/api/v1/agents/${agentId}`)
  if (!before.ok) return 'unsupported'
  const original = before.body.model_params ?? null

  const put = await apiFetch<AgentSummary>(page, 'PUT', `/api/v1/agents/${agentId}`, {
    model_params: { max_tokens: maxTokens },
  })
  if (!put.ok) return 'unsupported'

  // Independent GET — the PUT response echoing the right value is not
  // sufficient proof (that is precisely the shape of the Q1 regression:
  // 200 + a correct-looking response body, nothing persisted).
  const after = await apiFetch<AgentSummary>(page, 'GET', `/api/v1/agents/${agentId}`)
  if (!after.ok || after.body.model_params?.max_tokens !== maxTokens) {
    return 'unsupported'
  }
  return original
}

async function restoreModelParams(
  page: Page,
  agentId: string,
  original: AgentSummary['model_params'] | null,
): Promise<void> {
  await apiFetch(page, 'PUT', `/api/v1/agents/${agentId}`, {
    model_params: { max_tokens: original?.max_tokens ?? null },
  })
}

test(
  'a real turn that hits max_tokens shows "(cut off at the output limit)" live, with no reload',
  async ({ page }) => {
    test.setTimeout(180_000)

    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 20_000 })

    const jim = await findAgentIdByName(page, /Jim/i)
    if (!jim) {
      // Jim is part of the locked core roster seeded on every fresh install
      // (coreagent.SeedConfig), so an unresolvable Jim means the roster or the
      // endpoint is broken — RED, not a skip. Converted 2026-09-16 from a raw
      // test.skip() that bypassed the skip gate entirely.
      throw new Error(
        'BLOCKED: could not resolve an agent named "Jim" via GET /api/v1/agents — cannot target ' +
        'the max_tokens override. Jim is a locked core agent seeded on every fresh install, so ' +
        'his absence is a defect or a broken environment, not a precondition to skip on ' +
        '(tests/e2e/README.md §Skip policy).',
      )
    }

    const forceResult = await tryForceMaxTokens(page, jim.id, TINY_MAX_TOKENS)
    if (forceResult === 'unsupported') {
      throw new Error(
        'BLOCKED: PUT /api/v1/agents/{id} model_params.max_tokens did not round-trip through an ' +
          'independent GET on this build (config.AgentConfig.ModelParams persistence — see ' +
          'pkg/gateway/rest.go, pkg/config/config.go). Without it the turn will never truncate, ' +
          'so every downstream assertion would be meaningless. A missing capability is exactly ' +
          'the case the skip policy says must fail RED, not skip green ' +
          '(tests/e2e/fixtures/skip-tracking.ts, "What does NOT belong in the allow-list").',
      )
    }
    const originalModelParams = forceResult

    try {
      const input = chatInput(page)
      await expect(input).toBeEnabled({ timeout: 20_000 })
      await waitForConnected(page, { timeout: 20_000 })

      const picker = agentPicker(page)
      await expect(picker).toBeVisible({ timeout: 15_000 })
      await picker.click()
      await page.getByRole('menuitem', { name: /Jim/i }).click()
      await expect(picker).toContainText(/Jim/i, { timeout: 5_000 })

      await input.fill(LONG_PROSE_PROMPT)
      await input.press('Enter')

      // No page.reload() anywhere in this test — the assertion below must
      // be satisfied purely by the live `done` frame the WebSocket manager
      // hands to the store's `case 'done'` reducer.
      const cutOffLabel = page.locator('text=(cut off at the output limit)')
      await expect(cutOffLabel.first()).toBeVisible({ timeout: 90_000 })

      // D1 precedence: never both, and this turn was never cancelled.
      await expect(page.locator('text=(interrupted)')).toHaveCount(0)
    } finally {
      await restoreModelParams(page, jim.id, originalModelParams)
    }
  },
)
