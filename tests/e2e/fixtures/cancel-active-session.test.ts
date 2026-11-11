// @vitest-environment node
import { describe, expect, it, vi } from 'vitest';
import type { Page } from '@playwright/test';
import { cancelActiveSessionOnTeardown } from './cancel-active-session';

// Browser-driver boundary only; call the actual helper used by the auto fixture.
// No product state/registry or helper internals are replaced.
function browserEdge(options: { closed?: boolean; pending?: boolean; enabled?: boolean; clickError?: Error; hideError?: Error } = {}) {
  const cancel = {
    isVisible: vi.fn(async () => options.pending ?? true),
    click: vi.fn(async () => { if (options.clickError) throw options.clickError; }),
    waitFor: vi.fn(async () => { if (options.hideError) throw options.hideError; }),
  };
  const input = { isEnabled: vi.fn(async () => options.enabled ?? true) };
  const stop = { isVisible: vi.fn(async () => false) };
  const page = {
    isClosed: () => options.closed ?? false,
    getByTestId: vi.fn((id: string) => {
      if (id !== 'ask-user-cancel') throw new Error(`unexpected test id: ${id}`);
      return cancel;
    }),
    locator: vi.fn((selector: string) => {
      if (selector === 'textarea[aria-label="Message input"]') return input;
      if (selector === '[data-testid="stop-btn"]') return stop;
      throw new Error(`unexpected browser locator: ${selector}`);
    }),
  };
  return { page: page as unknown as Page, cancel, input, stop, getByTestId: page.getByTestId };
}

describe('#1769 automatic question teardown helper', () => {
  it('clicks the real question Cancel, waits for disappearance and verifies input unlock', async () => {
    const edge = browserEdge();
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.getByTestId).toHaveBeenCalledExactlyOnceWith('ask-user-cancel');
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith({ timeout: 2_000 });
    expect(edge.cancel.waitFor).toHaveBeenCalledExactlyOnceWith({ state: 'hidden', timeout: 5_000 });
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith();
    expect(edge.stop.isVisible).toHaveBeenCalledExactlyOnceWith();
  });
  it('does not attempt question cancellation when no question is open', async () => {
    const edge = browserEdge({ pending: false });
    await cancelActiveSessionOnTeardown(edge.page);
    expect(edge.cancel.click).not.toHaveBeenCalled();
    expect(edge.cancel.waitFor).not.toHaveBeenCalled();
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
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow('fixture transport: Cancel click failed');
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith({ timeout: 2_000 });
    expect(edge.cancel.waitFor).not.toHaveBeenCalled();
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails loudly when Cancel does not remove the pending question', async () => {
    const edge = browserEdge({ hideError: new Error('fixture transport: question still pending') });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow('fixture transport: question still pending');
    expect(edge.cancel.waitFor).toHaveBeenCalledExactlyOnceWith({ state: 'hidden', timeout: 5_000 });
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
  it('fails loudly when the card disappears but the composer stays locked', async () => {
    const edge = browserEdge({ enabled: false });
    await expect(cancelActiveSessionOnTeardown(edge.page)).rejects.toThrow('cancelOnTeardown: question Cancel did not unlock the active session composer');
    expect(edge.cancel.click).toHaveBeenCalledExactlyOnceWith({ timeout: 2_000 });
    expect(edge.input.isEnabled).toHaveBeenCalledExactlyOnceWith();
    expect(edge.stop.isVisible).not.toHaveBeenCalled();
  });
});
