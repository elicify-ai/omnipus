// @vitest-environment node
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  cancelAndReportAskUserQuestions, cancelOpenAskUserQuestions, type QuestionLocator, type QuestionPage,
} from './ask-user-cleanup';

const CARD = '[data-testid="ask-user-question-card"]';
const CANCEL = '[data-testid="ask-user-cancel"]';

interface Opts {
  /** Click succeeds but the card stays pending (cancel frame lost). */
  stuck?: boolean;
  /** Click throws (Cancel covered by an overlay / detached). */
  clickFails?: boolean;
  /** Page closed: isVisible throws. */
  visibleThrows?: boolean;
}

/**
 * A fake page holding `open` pending cards, each with its own Cancel button.
 * Strictness-faithful: click() on a locator matching more than one element
 * throws (as Playwright does) unless reached through first().
 */
function fakePage(open: number, opts: Opts = {}) {
  const state = { open, clicks: 0 };
  const make = (selector: string, single: boolean): QuestionLocator => ({
    count: async () => {
      if (opts.visibleThrows) throw new Error('page closed');
      return selector === CARD ? state.open : 0;
    },
    first: () => make(selector, true),
    click: async () => {
      expect(selector).toBe(CANCEL);
      if (!single && state.open > 1) throw new Error('strict mode violation: multiple elements');
      state.clicks += 1;
      if (opts.clickFails) throw new Error('covered by overlay');
      if (!opts.stuck) state.open -= 1;
    },
  });
  const page: QuestionPage = { locator: (selector: string) => make(selector, false), waitForTimeout: async () => undefined };
  return { page, state };
}

describe('cancelOpenAskUserQuestions', () => {
  it('cancels the open question so the next test does not inherit a locked composer', async () => {
    const { page, state } = fakePage(1);
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 1, unresolved: 0 });
    expect(state.open).toBe(0);
  });

  it('cancels every queued question, not just the first', async () => {
    const { page, state } = fakePage(3);
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 3, unresolved: 0 });
    expect(state.open).toBe(0);
  });

  it('positive control: with no question open it clicks nothing', async () => {
    const { page, state } = fakePage(0);
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 0, unresolved: 0 });
    expect(state.clicks).toBe(0);
  });

  it('reports a card that stays open after a successful click instead of counting it cancelled', async () => {
    const { page, state } = fakePage(1, { stuck: true });
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 0, unresolved: 1 });
    expect(state.clicks).toBe(1);
  });

  it('reports a Cancel that cannot be clicked (overlay) and does not throw', async () => {
    const { page } = fakePage(1, { clickFails: true });
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 0, unresolved: 1 });
  });

  it('stops at maxCancels and reports the card left over (the loop bound is reached)', async () => {
    const { page, state } = fakePage(5);
    expect(await cancelOpenAskUserQuestions(page, 3)).toEqual({ cancelled: 3, unresolved: 1 });
    expect(state.clicks).toBe(3);
    expect(state.open).toBe(2);
  });

  it('never throws when the page is already gone', async () => {
    const { page } = fakePage(1, { visibleThrows: true });
    await expect(cancelOpenAskUserQuestions(page)).resolves.toEqual({ cancelled: 0, unresolved: 0 });
  });

  it('cancels two simultaneous cards despite strict locators', async () => {
    const { page, state } = fakePage(2);
    expect(await cancelOpenAskUserQuestions(page)).toEqual({ cancelled: 2, unresolved: 0 });
    expect(state.open).toBe(0);
  });
});

describe('cancelAndReportAskUserQuestions (teardown reporting)', () => {
  const sink = () => ({ annotations: [] as Array<{ type: string; description?: string }> });

  it('warns and annotates when a card cannot be cancelled', async () => {
    const info = sink();
    const warnings: string[] = [];
    await cancelAndReportAskUserQuestions(fakePage(1, { stuck: true }).page, info, (m) => warnings.push(m));
    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toContain('could not be cancelled');
    expect(info.annotations).toEqual([{ type: 'ask-user-left-open', description: '1 unresolved' }]);
  });

  it('positive control: stays quiet when the cancel worked', async () => {
    const info = sink();
    const warnings: string[] = [];
    await cancelAndReportAskUserQuestions(fakePage(1).page, info, (m) => warnings.push(m));
    expect(warnings).toEqual([]);
    expect(info.annotations).toEqual([]);
  });

  it('gives up at the overall deadline when the page hangs, and reports it', async () => {
    const hung: QuestionPage = {
      locator: () => ({ count: () => new Promise<number>(() => undefined), first() { return this; }, click: async () => undefined }),
      waitForTimeout: async () => undefined,
    };
    const info = sink();
    const result = await cancelAndReportAskUserQuestions(hung, info, () => undefined, 50);
    expect(result.unresolved).toBe(1);
    expect(info.annotations).toHaveLength(1);
  });
});

describe('cancelOnTeardown wiring (console-errors.ts)', () => {
  // Source-level guard, deliberately modest: importing the fixture needs the
  // Playwright runner, so this only pins that teardown goes through the
  // reporting entry point with testInfo, not the bare helper (round-3 CHECK).
  const source = readFileSync(new URL('./console-errors.ts', import.meta.url), 'utf8');

  it('calls the reporting helper with testInfo after the test body', () => {
    expect(source).toMatch(/await use\(\);[\s\S]*await cancelAndReportAskUserQuestions\(page, testInfo\);/);
  });

  it('does not use the non-reporting helper directly', () => {
    expect(source).not.toMatch(/\bcancelOpenAskUserQuestions\b/);
  });
});
