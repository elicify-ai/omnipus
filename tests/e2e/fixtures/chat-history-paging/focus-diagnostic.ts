// #1801 — what held focus? The reconnect-retention-retry spec has failed three
// times in CI with the history region reporting "not focused" for the whole 15 s
// poll, and no artifact says which element had focus or whether the region node
// was replaced. This records, in the page, every focus change and every removal
// of the tagged region from the moment it is armed, and returns the log so the
// spec can attach it when the focus assertion fails. Diagnostic only: it never
// changes focus, never throws into the page, and the spec asserts exactly what
// it asserted before.
import type { Page } from '@playwright/test';

const REGION = '[aria-label="Conversation history"]';

export interface FocusDiagnostic {
  /** Time-ordered focusin/focusout/removal events since the diagnostic was armed. */
  log: string[];
  /** The element that has focus right now. */
  active: string;
  /** The region now: same node as when armed, a replacement, or absent. */
  region: 'same-node' | 'replaced' | 'absent';
  regionIsActive: boolean;
}

/** Tag the region and start recording. Call after the region has the focus under test. */
export async function armFocusDiagnostic(page: Page): Promise<void> {
  await page.evaluate((selector) => {
    const w = window as unknown as { __focusDiag?: string[]; __focusDiagArmed?: boolean };
    const region = document.querySelector<HTMLElement>(selector);
    if (region) region.dataset.focusDiagId = 'armed';
    if (w.__focusDiagArmed) return;
    w.__focusDiagArmed = true;
    w.__focusDiag = [];
    const note = (text: string) => w.__focusDiag!.push(`${Math.round(performance.now())}ms ${text}`);
    const describe = (node: EventTarget | Element | null) => {
      if (!(node instanceof Element)) return 'null';
      const label = node.getAttribute('aria-label') ?? node.getAttribute('data-testid') ?? '';
      const tag = (node as HTMLElement).dataset?.focusDiagId ? ' [armed region]' : '';
      return `${node.tagName.toLowerCase()}${label ? `(${label})` : ''}${tag}`;
    };
    document.addEventListener('focusout', (e) => note(`focusout ${describe(e.target)} -> ${describe((e as FocusEvent).relatedTarget)}`), true);
    document.addEventListener('focusin', (e) => note(`focusin ${describe(e.target)}`), true);
    new MutationObserver((records) => {
      for (const record of records) {
        for (const removed of Array.from(record.removedNodes)) {
          if (!(removed instanceof Element)) continue;
          if (removed.matches('[data-focus-diag-id="armed"]') || removed.querySelector('[data-focus-diag-id="armed"]')) {
            note(`armed region removed from ${describe(record.target)}`);
          }
        }
      }
    }).observe(document.body, { childList: true, subtree: true });
    note(`armed; active=${describe(document.activeElement)}`);
  }, REGION);
}

/** Read the log and the current focus state. Safe to call when the page is closing. */
export async function readFocusDiagnostic(page: Page): Promise<FocusDiagnostic | { error: string }> {
  try {
    return await page.evaluate((selector) => {
      const w = window as unknown as { __focusDiag?: string[] };
      const region = document.querySelector<HTMLElement>(selector);
      const active = document.activeElement;
      const label = active ? (active.getAttribute('aria-label') ?? active.getAttribute('data-testid') ?? '') : '';
      return {
        log: w.__focusDiag ?? ['(not armed)'],
        active: active ? `${active.tagName.toLowerCase()}${label ? `(${label})` : ''} text="${(active.textContent ?? '').slice(0, 60)}"` : 'null',
        region: !region ? 'absent' : region.dataset.focusDiagId === 'armed' ? 'same-node' : 'replaced',
        regionIsActive: region === active,
      } as FocusDiagnostic;
    }, REGION);
  } catch (error) {
    return { error: String(error) };
  }
}
