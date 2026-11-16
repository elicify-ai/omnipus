import { expect, type Locator, type Page } from '@playwright/test';

// Test instrument, not a product selector contract. Read each painted text node,
// never an ancestor's aggregate textContent/innerText (which can include a frozen
// announcement). Filter candidate rows by prefix BEFORE testing the clause: a
// Researcher wrapper must not borrow Writer's "responded" from a sibling row.
export async function paintedRows(locator: Locator, prefix = '^', clause = '.*', minimal = false) {
  return locator.evaluateAll((nodes, options) => {
    const excluded = '[aria-live], [role="status"], [role="alert"], [role="log"], [aria-hidden="true"], [hidden], [inert], .sr-only, script, style, template';
    const allowed = new Map<Element, boolean>();
    function canPaint(element: Element): boolean {
      const cached = allowed.get(element);
      if (cached !== undefined) return cached;
      let ok = element instanceof HTMLElement && !element.closest(excluded) &&
        element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
      for (let parent: Element | null = element; ok && parent; parent = parent.parentElement) {
        const css = getComputedStyle(parent);
        // CSS-only screen-reader clipping is hidden too, even without a class
        // or an ARIA marker. No clipped announcement supplies notice evidence.
        if (css.clip !== 'auto' || css.clipPath !== 'none' || css.opacity === '0') ok = false;
      }
      allowed.set(element, ok);
      return ok;
    }
    const pieces = new Map<Node, Array<{ text: string; rects: DOMRect[] }>>();
    function painted(node: Node): Array<{ text: string; rects: DOMRect[] }> {
      const cached = pieces.get(node);
      if (cached) return cached;
      let result: Array<{ text: string; rects: DOMRect[] }> = [];
      if (node.nodeType === Node.TEXT_NODE && node.textContent && node.parentElement && canPaint(node.parentElement)) {
        const range = document.createRange();
        range.selectNodeContents(node);
        const rects = [...range.getClientRects()].filter(rect => {
          let left = Math.max(0, rect.left), right = Math.min(innerWidth, rect.right);
          let top = Math.max(0, rect.top), bottom = Math.min(innerHeight, rect.bottom);
          for (let p = node.parentElement; p; p = p.parentElement) {
            const css = getComputedStyle(p), box = p.getBoundingClientRect();
            if (/hidden|clip|auto|scroll/.test(css.overflowX)) {
              left = Math.max(left, box.left); right = Math.min(right, box.right);
            }
            if (/hidden|clip|auto|scroll/.test(css.overflowY)) {
              top = Math.max(top, box.top); bottom = Math.min(bottom, box.bottom);
            }
          }
          return right - left > 1 && bottom - top > 1;
        });
        if (rects.length) result = [{ text: node.textContent, rects }];
      } else if (node instanceof Element && canPaint(node)) {
        result = [...node.childNodes].flatMap(painted);
      }
      pieces.set(node, result);
      return result;
    }
    const candidates = nodes.map(node => {
      const content = painted(node);
      return { node, text: content.map(p => p.text).join('').replace(/\s+/g, ' ').trim(),
        lines: new Set(content.flatMap(p => p.rects.map(r => Math.round(r.top)))).size };
    }).filter(row => row.text && new RegExp(options.prefix).test(row.text));
    return candidates.filter(row => (!options.minimal || !candidates.some(other =>
      other.node !== row.node && row.node.contains(other.node))) && new RegExp(`(?:${options.clause})`).test(row.text))
      .map(({ text, lines }) => ({ text, lines }));
  }, { prefix, clause, minimal });
}

export async function paintedText(locator: Locator) {
  return (await paintedRows(locator)).map(row => row.text);
}

export async function visibleNamedRows(page: Page, name: string, clause: string) {
  const literal = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return paintedRows(page.locator('span, p, div'), `^${literal}: `, clause, true);
}

export async function expectPainted(locator: Locator, text: string | RegExp, timeout = 2_000) {
  await expect.poll(() => paintedText(locator), { message: 'painted notice text, excluding every hidden/announcement descendant', timeout })
    .toEqual([typeof text === 'string' ? text : expect.stringMatching(text)]);
}
