import type { Page } from '@playwright/test';
import type { EnvKind } from './env';

/** One entry from budgets/key-rows.json, narrowed to what the in-page script needs. */
export interface KeyRowBudget {
  id: string;
  selector: string;
  budgetPx: Record<EnvKind, number>;
}

export type Violation =
  | { type: 'ancestor-overflow-clip'; selector: string; detail: string }
  | { type: 'text-clipped'; selector: string; detail: string }
  | { type: 'hit-region-overlap'; selector: string; otherSelector: string; detail: string }
  | { type: 'key-row-budget'; rowId: string; detail: string };

export interface KeyRowMeasurement {
  id: string;
  found: boolean;
  heightPx: number | null;
  budgetPx: number;
  withinBudget: boolean;
}

export interface PageMeasurement {
  controlsChecked: number;
  violations: Violation[];
  keyRows: KeyRowMeasurement[];
}

/**
 * The single definition of "visible interactive control" this whole suite
 * uses — both for what measurePage() actually checks AND for what
 * specs/in-context-touch.spec.ts's settle() waits to exist before measuring.
 * Exported (not inlined per-callsite) on purpose: a first version had a
 * SECOND, broader ad hoc selector (`[role]`, matching any ARIA role at all —
 * including purely decorative ones like `role="presentation"`/`role="status"`
 * on chrome that paints before real page content does) in settle()'s
 * readiness check. That mismatch made "ready" fire on the sidebar/header
 * alone, before route-specific content (e.g. `_app/agents`'s fetched agent
 * cards) ever rendered — 83 of 140 route checks in this suite's own baseline
 * run measured a page that plainly has controls as having zero. One
 * constant, used both places, makes "ready" and "counted" the same claim.
 */
export const INTERACTIVE_SELECTOR = [
  'a[href]',
  'button',
  'input:not([type="hidden"])',
  'textarea',
  'select',
  '[role="button"]',
  '[role="link"]',
  '[role="menuitem"]',
  '[role="option"]',
  '[role="tab"]',
  '[role="checkbox"]',
  '[role="radio"]',
  '[role="switch"]',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

/**
 * Runs the four D17 in-context assertions against the CURRENT page state and
 * returns a structured, JSON-safe result — never throws on a finding.
 * Callers decide pass/fail policy; this function only measures and reports.
 *
 * All four checks (see design-system-definition.md D17 "Enforcement") run in
 * a single page.evaluate() round trip against the live rendered DOM:
 *   1. no ancestor with overflow hidden/clip clips a control;
 *   2. no label/entered text is clipped (scrollWidth vs clientWidth);
 *   3. hit regions (bounding boxes) of non-nested interactive controls do
 *      not overlap;
 *   4. declared key rows (budgets/key-rows.json) stay within their
 *      environment's height budget.
 */
export async function measurePage(
  page: Page,
  envKind: EnvKind,
  keyRowBudgets: KeyRowBudget[],
): Promise<PageMeasurement> {
  return page.evaluate(
    ({ envKind, keyRowBudgets, interactiveSelector }) => {
      const INTERACTIVE_SELECTOR = interactiveSelector;
      const OVERLAP_EPS = 1; // px — subpixel rounding tolerance
      const CLIP_EPS = 0.5; // px

      function describeElement(el: Element): string {
        const tag = el.tagName.toLowerCase();
        const id = (el as HTMLElement).id ? `#${(el as HTMLElement).id}` : '';
        const testId = el.getAttribute('data-testid');
        const testIdPart = testId ? `[data-testid="${testId}"]` : '';
        const label =
          el.getAttribute('aria-label') ||
          (el.textContent ?? '').trim().slice(0, 40) ||
          '';
        return `${tag}${id}${testIdPart}${label ? ` "${label}"` : ''}`;
      }

      function isVisible(el: Element): boolean {
        const rect = el.getBoundingClientRect();
        if (rect.width <= 0 || rect.height <= 0) return false;
        const style = getComputedStyle(el);
        if (style.visibility === 'hidden' || style.display === 'none') return false;
        if (parseFloat(style.opacity || '1') === 0) return false;
        if ((el as HTMLButtonElement).disabled) return false;
        if (el.getAttribute('aria-hidden') === 'true') return false;
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        if (rect.right <= 0 || rect.bottom <= 0 || rect.left >= vw || rect.top >= vh) return false;
        return true;
      }

      function checkAncestorClip(el: Element): string | null {
        const rect = el.getBoundingClientRect();
        let node = el.parentElement;
        while (node && node !== document.documentElement) {
          const style = getComputedStyle(node);
          const clips =
            style.overflowX === 'hidden' ||
            style.overflowX === 'clip' ||
            style.overflowY === 'hidden' ||
            style.overflowY === 'clip';
          if (clips) {
            const ar = node.getBoundingClientRect();
            const outside =
              rect.left < ar.left - CLIP_EPS ||
              rect.right > ar.right + CLIP_EPS ||
              rect.top < ar.top - CLIP_EPS ||
              rect.bottom > ar.bottom + CLIP_EPS;
            if (outside) {
              return `clipped by ${describeElement(node)} (overflow: ${style.overflowX}/${style.overflowY})`;
            }
          }
          node = node.parentElement;
        }
        return null;
      }

      function checkTextClipped(el: Element): string | null {
        if (el.scrollWidth > el.clientWidth + 1) {
          return `scrollWidth ${el.scrollWidth}px > clientWidth ${el.clientWidth}px`;
        }
        return null;
      }

      const controls = Array.from(document.querySelectorAll(INTERACTIVE_SELECTOR)).filter(isVisible);

      const violations: Array<
        | { type: 'ancestor-overflow-clip'; selector: string; detail: string }
        | { type: 'text-clipped'; selector: string; detail: string }
        | { type: 'hit-region-overlap'; selector: string; otherSelector: string; detail: string }
        | { type: 'key-row-budget'; rowId: string; detail: string }
      > = [];

      for (const control of controls) {
        const clip = checkAncestorClip(control);
        if (clip) violations.push({ type: 'ancestor-overflow-clip', selector: describeElement(control), detail: clip });
        const textClip = checkTextClipped(control);
        if (textClip) violations.push({ type: 'text-clipped', selector: describeElement(control), detail: textClip });
      }

      // Hit-region overlap: pairwise over non-nested controls only — a
      // control nested inside another interactive control (e.g. a button
      // inside a clickable row) is a separate, already-known a11y concern,
      // not a "two hit regions fight for the same tap" bug.
      for (let i = 0; i < controls.length; i++) {
        for (let j = i + 1; j < controls.length; j++) {
          const a = controls[i];
          const b = controls[j];
          if (a.contains(b) || b.contains(a)) continue;
          const ra = a.getBoundingClientRect();
          const rb = b.getBoundingClientRect();
          const left = Math.max(ra.left, rb.left);
          const right = Math.min(ra.right, rb.right);
          const top = Math.max(ra.top, rb.top);
          const bottom = Math.min(ra.bottom, rb.bottom);
          const w = right - left;
          const h = bottom - top;
          if (w > OVERLAP_EPS && h > OVERLAP_EPS) {
            violations.push({
              type: 'hit-region-overlap',
              selector: describeElement(a),
              otherSelector: describeElement(b),
              detail: `overlap ${w.toFixed(1)}x${h.toFixed(1)}px`,
            });
          }
        }
      }

      const keyRows = keyRowBudgets.map((row) => {
        const el = document.querySelector(row.selector);
        const budget = row.budgetPx[envKind as keyof typeof row.budgetPx];
        if (!el) {
          return { id: row.id, found: false, heightPx: null, budgetPx: budget, withinBudget: true };
        }
        const height = el.getBoundingClientRect().height;
        const withinBudget = height <= budget + 0.5;
        if (!withinBudget) {
          violations.push({
            type: 'key-row-budget',
            rowId: row.id,
            detail: `height ${height.toFixed(1)}px exceeds ${envKind} budget ${budget}px`,
          });
        }
        return { id: row.id, found: true, heightPx: Math.round(height * 10) / 10, budgetPx: budget, withinBudget };
      });

      return { controlsChecked: controls.length, violations, keyRows };
    },
    { envKind, keyRowBudgets, interactiveSelector: INTERACTIVE_SELECTOR },
  );
}
