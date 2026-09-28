import { type Page, expect } from '@playwright/test'
import { E2E_MODEL } from './e2e-model.js'

/**
 * select-model.ts — the ONE way a test selects a model in a ModelSelector
 * combobox: the central e2e model (tests/e2e/e2e-model.json, via
 * fixtures/e2e-model.ts). Never "first option": which model the suite runs
 * on is decided by the central setting, so a model change is a one-line
 * JSON edit, not a suite-wide picker drift.
 *
 * Works in every ModelSelector mode:
 *   - catalog mode (wizard / onboarding with a loaded catalog): types the
 *     slug into the "Search models..." input first — the list is virtualised
 *     past the threshold, so without filtering the target row may not be in
 *     the DOM at all.
 *   - plain/grouped mode: same search input, flat option list.
 *   - `itemTestIdPrefix` (onboarding: "onboarding-model-"): clicks the exact
 *     `${prefix}${model}` testid; otherwise matches `[role="option"]` by
 *     EXACT text (an anchored `^slug$` RegExp — a superset slug like
 *     `<slug>-preview` never matches, see selectExact below).
 *
 * Fails CLOSED when the central model is not offered: the thrown error names
 * the setting file, the slug, and the selector that found nothing — never a
 * silent fallback to another model.
 */

/** The ModelSelector search box renders only while the popover is open. */
const SEARCH_INPUT_PLACEHOLDER = 'Search models...'

/** Escape a string for literal use inside a RegExp. */
function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * The EXACT text matcher for the central model: an anchored RegExp so a
 * superset slug (e.g. `<slug>-preview`, `<slug>:floor`) can never stand in
 * for the central model. Anchored on the slug ELEMENT (`getByText(exact)`),
 * not the whole row: a row can carry sibling text — the "Recommended for
 * chat" chip (RECOMMENDED_CHIP_LABEL) — which a whole-row match would
 * wrongly exclude.
 */
function selectExact(slug: string): RegExp {
  return new RegExp(`^${escapeRegExp(slug)}$`)
}

/**
 * Select the central e2e model in the ModelSelector whose trigger carries
 * `triggerTestId`. Returns the model id it selected (always the central
 * setting). Throws a contextual error when the picker does not offer it.
 */
export async function selectCentralModel(
  page: Page,
  opts: {
    /** data-testid of the combobox trigger (e.g. "wizard-model"). */
    triggerTestId: string
    /** data-testid prefix of the pickable items (e.g. "onboarding-model-"). */
    itemTestIdPrefix?: string
    /** Visibility timeout for the trigger/readiness assertions. */
    timeout?: number
  },
): Promise<string> {
  const { triggerTestId, itemTestIdPrefix, timeout = 20_000 } = opts
  const modelTrigger = page.getByTestId(triggerTestId)

  // Wait for the trigger to be the READY combobox, not merely visible.
  //
  // This spec family spent weeks being misdiagnosed — first as OpenRouter
  // latency, then as a provider misconfiguration — because the model field
  // used to be swapped for a non-interactive placeholder carrying this same
  // test id while the provider catalog loaded (0.13s idle, measured 1.2-4.5s
  // under a full shard). toBeVisible() passed on that placeholder, click()
  // "succeeded" and did nothing, and the click was lost: when the catalog
  // landed the real combobox replaced it with the popover still closed. The
  // product bug is fixed (the trigger is now the real combobox in every
  // state, with aria-busy while loading), and this assertion pins the READY
  // state so a regression surfaces here as an honest timeout rather than a
  // mystery. (History from create-agent.spec.ts, where the incident played
  // out — kept here because the helper now owns the interaction.)
  await expect(modelTrigger).toBeVisible({ timeout })
  await expect(modelTrigger).toHaveAttribute('role', 'combobox', { timeout })
  await expect(modelTrigger).not.toHaveAttribute('aria-busy', 'true', { timeout })

  await modelTrigger.click()
  try {
    const searchInput = page.getByPlaceholder(SEARCH_INPUT_PLACEHOLDER)
    await expect(searchInput).toBeVisible({ timeout })

    // Real keystrokes: cmdk's CommandInput is a controlled input, and
    // pressSequentially fires the React synthetic onChange fill() has
    // historically missed in this suite.
    await searchInput.pressSequentially(E2E_MODEL)

    // Exact option match: the row must CONTAIN an element whose whole text
    // is the central slug (selectExact, anchored ^slug$) — a superset slug
    // (e.g. <slug>-preview) never matches, and sibling row text (the
    // "Recommended for chat" chip) cannot break the match either. The
    // testid path is exact by construction. `.first()` is gone on purpose:
    // exactly one candidate must match before the click — zero is "picker
    // does not offer it" (fail closed below), two or more is ambiguous and
    // refuses too.
    const modelItem = itemTestIdPrefix
      ? page.getByTestId(`${itemTestIdPrefix}${E2E_MODEL}`)
      : page
          .getByRole('option')
          .filter({ has: page.getByText(selectExact(E2E_MODEL)) })

    // Fail closed with context when the central model is not offered —
    // e.g. the provider catalog changed and no longer lists the slug the
    // whole suite is pinned to. Never silently pick a different model.
    try {
      await expect(modelItem).toHaveCount(1, { timeout: 15_000 })
      await modelItem.click()
    } catch (err) {
      throw new Error(
        `The model picker ("${triggerTestId}") does not offer the central e2e model ` +
          `"${E2E_MODEL}" (looked for ` +
          (itemTestIdPrefix
            ? `testid "${itemTestIdPrefix}${E2E_MODEL}"`
            : `the single option with exact text "${E2E_MODEL}"`) +
          `). tests/e2e/e2e-model.json pins the whole suite to it — fix the setting or ` +
          `the provider catalog, never fall back to another model. Underlying wait: ${(err as Error).message}`,
        { cause: err },
      )
    }
  } catch (err) {
    // Anything else (popover never opened, search input missing) gets the
    // same contextual prefix. Never swallow the cause.
    if (
      err instanceof Error &&
      err.message.includes('does not offer the central e2e model')
    ) {
      throw err
    }
    throw new Error(
      `Could not select the central e2e model "${E2E_MODEL}" in the "${triggerTestId}" picker: ${(err as Error).message}`,
      { cause: err },
    )
  }

  // The chosen model is E2E_MODEL — asserted EXACTLY: the trigger's value
  // span (model-selector.tsx::ModelSelector combobox branch) renders
  // displayValue = value || placeholder as its WHOLE text once a value is
  // set, so exact:true + count-1 pins it. A superset slug left on the
  // trigger (<slug>-preview) cannot pass, and the Unresolved chip and the
  // caret icon never confound the match.
  const triggerValue = modelTrigger.getByText(E2E_MODEL, { exact: true })
  await expect(triggerValue).toHaveCount(1, { timeout: 10_000 })
  await expect(triggerValue).toBeVisible({ timeout: 10_000 })
  return E2E_MODEL
}
