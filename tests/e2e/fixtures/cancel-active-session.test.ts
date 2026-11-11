// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import type { Page } from '@playwright/test';
import { CANCEL_TEARDOWN_TIMEOUTS, cancelActiveSessionOnTeardown } from './cancel-active-session';

// Browser-driver boundary only; the actual auto-fixture helper remains real.
// A replacement card has its own DOM identity, even when its Cancel looks identical.
function browserEdge(options: {
  closed?: boolean; pending?: boolean; questions?: number; enabled?: boolean;
  clickError?: Error; hideError?: Error; composerError?: Error;
  streaming?: boolean; stopClickError?: Error; duplicateControls?: boolean;
} = {}) {
  let active = 0;
  const clickedCards = new Set<number>();
  const cards = Array.from({ length: options.questions ?? (options.pending === false ? 0 : 1) }, (_, index) => {
    const attributes = new Map<string, string>();
    const setAttribute = vi.fn((name: string, value: string) => { attributes.set(name, value); });
    return {
      attributes, setAttribute,
      evaluate: vi.fn(async (fn: (element: Element, marker: string) => void, marker: string) => {
        fn({ setAttribute } as unknown as Element, marker);
      }),
      waitFor: vi.fn(async () => {
        if (options.hideError) throw options.hideError;
        if (!clickedCards.has(index)) throw new Error('fixture transport: Cancel did not run');
        active++;
      }),
    };
  });
  const cancel = {
    isVisible: vi.fn(async () => active < cards.length),
    click: vi.fn(async () => {
      if (options.clickError) throw options.clickError;
      clickedCards.add(active);
    }),
    locator: vi.fn((selector: string) => {
      if (selector !== 'xpath=ancestor::*[@data-testid="ask-user-question-card"][1]') {
        throw new Error(`unexpected card locator: ${selector}`);
      }
      return cards[active];
    }),
  };
  // Keep the original driver's API available for RED: a generic Cancel wait
  // cannot distinguish a replacement question from the card just cancelled.
  const controls = {
    first: vi.fn(() => cancel),
    isVisible: vi.fn(async () => {
      if (options.duplicateControls) throw new Error('fixture transport: strict mode violation: 2 Cancel controls');
      return cancel.isVisible();
    }),
    click: cancel.click,
    waitFor: vi.fn(async () => {
      if (options.hideError) throw options.hideError;
      if (active + 1 < cards.length) throw new Error('fixture transport: replacement question still visible');
      active++;
    }),
  };
  const input = { isEnabled: vi.fn(async () => {
    if (options.composerError) throw options.composerError;
    return options.enabled ?? true;
  }) };
  const stop = {
    isVisible: vi.fn(async () => options.streaming ?? false),
    click: vi.fn(async () => { if (options.stopClickError) throw options.stopClickError; }),
  };
  const page = {
    isClosed: () => options.closed ?? false,
    getByTestId: vi.fn((id: string) => {
      if (id !== 'ask-user-cancel') throw new Error(`unexpected test id: ${id}`);
      return controls;
    }),
    locator: vi.fn((selector: string) => {
      if (selector === 'textarea[aria-label="Message input"]') return input;
      if (selector === '[data-testid="stop-btn"]') return stop;
      const marker = /^\[data-e2e-teardown-card="([^"]+)"\]$/.exec(selector)?.[1];
      const card = cards.find(item => item.attributes.get('data-e2e-teardown-card') === marker);
      if (!marker || !card) throw new Error(`unexpected browser locator: ${selector}`);
      return card;
    }),
    waitForTimeout: vi.fn(async () => undefined),
  };
  return { page: page as unknown as Page, cancel, controls, cards, input, stop,
    getByTestId: page.getByTestId, waitForTimeout: page.waitForTimeout };
}

const clickOptions = { timeout: CANCEL_TEARDOWN_TIMEOUTS.click };
const hiddenOptions = { state: 'hidden', timeout: CANCEL_TEARDOWN_TIMEOUTS.cardHidden };
const composerOptions = { timeout: CANCEL_TEARDOWN_TIMEOUTS.composer };

describe('#1769 automatic question teardown helper', () => {
  it('clicks the real question Cancel, waits for disappearance and verifies input unlock', async () => {
    const edge = browserEdge();
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.getByTestId).toHaveBeenCalledExactlyOnceWith('ask-user-cancel');
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
    expect(edge.cards[0].waitFor).toHaveBeenCalledExactlyOnceWith(hiddenOptions);
    expect(edge.controls.waitFor, 'a replacement Cancel must never be the disappearance oracle').not.toHaveBeenCalled();
    expect(edge.cards[0].setAttribute).toHaveBeenCalledExactlyOnceWith('data-e2e-teardown-card', expect.any(String));
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
    expect(edge.stop.isVisible).toHaveBeenCalledExactlyOnceWith();
  });
  it('clears one re-ask after Cancel using each card identity, then verifies composer unlock', async () => {
    const edge = browserEdge({ questions: 2 }); // F1: original question + one re-ask.
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.cancel.click).toHaveBeenCalledTimes(2);
    for (const [index, card] of edge.cards.entries()) {
      expect(edge.cancel.click).toHaveBeenNthCalledWith(index + 1, clickOptions);
      expect(card.waitFor).toHaveBeenCalledExactlyOnceWith(hiddenOptions);
      expect(card.setAttribute).toHaveBeenCalledExactlyOnceWith('data-e2e-teardown-card', expect.any(String));
      expect(card.evaluate).toHaveBeenCalledExactlyOnceWith(expect.any(Function),
        card.attributes.get('data-e2e-teardown-card'), clickOptions);
    }
    const markers = edge.cards.map(card => card.attributes.get('data-e2e-teardown-card'));
    expect(new Set(markers).size, 'the old-card wait must not retarget the successor').toBe(2);
    expect(edge.controls.waitFor).not.toHaveBeenCalled();
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
    expect(await edge.cancel.isVisible(), 'both question cards are gone').toBe(false);
  });
  it('clears exactly three question cards at the chosen cancellation bound', async () => {
    const edge = browserEdge({ questions: 3 }); // Plan's maximum, not an implementation-derived count.
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.cancel.click).toHaveBeenCalledTimes(3);
    for (const card of edge.cards) expect(card.waitFor).toHaveBeenCalledExactlyOnceWith(hiddenOptions);
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
    expect(await edge.cancel.isVisible()).toBe(false);
  });
  it('fails with the named model-re-ask cause when a fourth question exceeds the three-round bound', async () => {
    const edge = browserEdge({ questions: 4 }); // F1: max + 1 must remain loud, not hang or stop early.
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow(
      new Error('cancelOnTeardown: the model asked again 3 times after Cancel (maximum 3 cancellation rounds)'),
    );
    expect(edge.cancel.click, 'never click more than the three-round bound').toHaveBeenCalledTimes(3);
    expect(edge.cards[3].waitFor).not.toHaveBeenCalled();
    expect(await edge.cancel.isVisible(), 'the unhandled fourth card explains the failure').toBe(true);
    expect(edge.input.isEnabled).not.toHaveBeenCalled();
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('selects the first Cancel when two matching controls would otherwise violate strict mode', async () => {
    const edge = browserEdge({ duplicateControls: true });
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.controls.first).toHaveBeenCalledExactlyOnceWith();
    expect(edge.controls.isVisible).not.toHaveBeenCalled();
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
    expect(edge.cards[0].waitFor).toHaveBeenCalledExactlyOnceWith(hiddenOptions);
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
  });
  it('does not attempt question cancellation when no question is open', async () => {
    const edge = browserEdge({ pending: false });
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.cancel.click).not.toHaveBeenCalled();
    expect(edge.controls.waitFor).not.toHaveBeenCalled();
    expect(edge.input.isEnabled).not.toHaveBeenCalled();
    expect(edge.stop.isVisible).toHaveBeenCalledExactlyOnceWith();
  });
  it('does not inspect an already closed browser page', async () => {
    const edge = browserEdge({ closed: true });
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.getByTestId).not.toHaveBeenCalled();
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails loudly when an attempted question Cancel click fails', async () => {
    const error = new Error('fixture transport: Cancel click failed');
    const edge = browserEdge({ clickError: error });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow(error);
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
    expect(edge.cards[0].waitFor).not.toHaveBeenCalled();
    expect(edge.controls.waitFor).not.toHaveBeenCalled();
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails loudly when Cancel does not remove the pending question', async () => {
    const error = new Error('fixture transport: question still pending');
    const edge = browserEdge({ hideError: error });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow(error);
    expect(edge.cards[0].waitFor).toHaveBeenCalledExactlyOnceWith(hiddenOptions);
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails loudly when the card disappears but the composer stays locked', async () => {
    const edge = browserEdge({ enabled: false });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow(
      new Error('cancelOnTeardown: question Cancel did not unlock the active session composer'),
    );
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails clearly with a short explicit driver timeout when the composer is missing', async () => {
    const error = new Error('fixture transport: Message input was not found');
    const edge = browserEdge({ composerError: error });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow(
      new Error('cancelOnTeardown: active session composer is missing after question Cancel', { cause: error }),
    );
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith(composerOptions);
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('clicks Stop exactly once while streaming without a pending question', async () => {
    const edge = browserEdge({ pending: false, streaming: true });
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.stop.isVisible).toHaveBeenCalledExactlyOnceWith();
    expect(edge.stop.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
    expect(edge.waitForTimeout).toHaveBeenCalledExactlyOnceWith(CANCEL_TEARDOWN_TIMEOUTS.settle);
    expect(edge.cancel.click).not.toHaveBeenCalled();
  });
  it('warns without throwing when the streaming Stop click fails', async () => {
    const edge = browserEdge({ pending: false, streaming: true, stopClickError: new Error('fixture transport: Stop click failed') });
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    try {
      await expect(cancelActiveSessionOnTeardown(edge.page)).resolves.toBeUndefined();
      expect(edge.stop.click).toHaveBeenCalledExactlyOnceWith(clickOptions);
      expect(warning).toHaveBeenCalledExactlyOnceWith('[cancelOnTeardown] Stop click failed: fixture transport: Stop click failed');
      expect(edge.waitForTimeout).toHaveBeenCalledExactlyOnceWith(CANCEL_TEARDOWN_TIMEOUTS.settle);
    } finally { warning.mockRestore(); }
  });
});
