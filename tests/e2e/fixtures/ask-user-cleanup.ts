// Teardown helper for #1769: an open AskUserQuestion card locks the composer
// ("chat input is locked while questions are pending — Cancel to unlock") on
// the session it belongs to. A test that leaves one open on a shared session
// (Jim's main session) poisons every later test and retry that selects it.

export interface QuestionLocator {
  count(): Promise<number>;
  /** Like Playwright: click() throws a strict-mode error when more than one element matches. */
  click(options?: { timeout?: number }): Promise<void>;
  first(): QuestionLocator;
}

/** The slice of Playwright's Page this helper needs (lets unit tests fake it). */
export interface QuestionPage {
  locator(selector: string): QuestionLocator;
  waitForTimeout(ms: number): Promise<void>;
}

export interface CancelResult {
  /** Cards that were cancelled AND disappeared afterwards. */
  cancelled: number;
  /** Cards still open after the cancel click or whose Cancel could not be clicked (overlay, ...). */
  unresolved: number;
}

const CARD = '[data-testid="ask-user-question-card"]';
const CANCEL = '[data-testid="ask-user-cancel"]';
const SETTLE_POLLS = 10;
const SETTLE_MS = 100;

/**
 * Cancels every open AskUserQuestion card on the page and checks each one
 * actually closed. Bounded by `maxCancels` and never throws: teardown must not
 * fail a test, and the page may already be closing. A card that stays open is
 * counted in `unresolved` so the caller can say so instead of staying silent.
 */
export async function cancelOpenAskUserQuestions(page: QuestionPage, maxCancels = 8): Promise<CancelResult> {
  const result: CancelResult = { cancelled: 0, unresolved: 0 };
  for (let attempt = 0; attempt < maxCancels; attempt++) {
    let before: number;
    try {
      before = await page.locator(CARD).count();
    } catch {
      return result; // page already gone: nothing to clean up
    }
    if (before === 0) return result;
    try {
      await page.locator(CANCEL).first().click({ timeout: 2_000 });
    } catch {
      result.unresolved += 1; // Cancel not clickable (covered, detached)
      return result;
    }
    // The click only sends the cancel; wait for one card to actually close.
    let closed = false;
    for (let poll = 0; poll < SETTLE_POLLS && !closed; poll++) {
      try {
        await page.waitForTimeout(SETTLE_MS);
        closed = (await page.locator(CARD).count()) < before;
      } catch {
        return result;
      }
    }
    if (!closed) {
      result.unresolved += 1; // clicked, but the card is still pending
      return result;
    }
    result.cancelled += 1;
  }
  // Ran out of attempts with a card still showing.
  try {
    if ((await page.locator(CARD).count()) > 0) result.unresolved += 1;
  } catch {
    /* page gone */
  }
  return result;
}

/** The slice of Playwright's TestInfo the reporter needs. */
export interface AnnotationSink {
  annotations: Array<{ type: string; description?: string }>;
}

const TEARDOWN_DEADLINE_MS = 20_000;

/**
 * Teardown entry point: cancels open questions within an overall deadline and,
 * when any stays open, says so (console warning + `ask-user-left-open`
 * annotation) instead of leaving the next test to discover a locked composer.
 */
export async function cancelAndReportAskUserQuestions(
  page: QuestionPage,
  testInfo: AnnotationSink,
  warn: (message: string) => void = (m) => console.warn(m),
  deadlineMs = TEARDOWN_DEADLINE_MS,
): Promise<CancelResult> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timedOut = new Promise<CancelResult>((resolve) => {
    timer = setTimeout(() => resolve({ cancelled: 0, unresolved: 1 }), deadlineMs);
  });
  const result = await Promise.race([cancelOpenAskUserQuestions(page), timedOut]);
  if (timer !== undefined) clearTimeout(timer);
  if (result.unresolved > 0) {
    warn(`cancelOnTeardown: ${result.unresolved} AskUserQuestion card(s) could not be cancelled (#1769)`);
    testInfo.annotations.push({ type: 'ask-user-left-open', description: `${result.unresolved} unresolved` });
  }
  return result;
}
