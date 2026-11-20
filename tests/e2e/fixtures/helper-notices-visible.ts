import { expect, type Locator, type Page } from '@playwright/test';

// Standalone so the page-side first-paint recorder uses EXACTLY this reader.
// Cache inherited visibility/clipping once per element, not once per text node
// and ancestor on every candidate. Never read aggregate text as visible text.
export function readPaintedRows(nodes: Element[], options: { prefix: string; clause: string; minimal: boolean }) {
  const excluded = '[aria-live], [role="status"], [role="alert"], [role="log"], [aria-hidden="true"], [hidden], [inert], .sr-only, script, style, template';
  type Clip = { ok: boolean; left: number; right: number; top: number; bottom: number };
  const geometry = new Map<Element, Clip>();
  function clip(element: Element): Clip {
    const cached = geometry.get(element);
    if (cached) return cached;
    const parent = element.parentElement ? clip(element.parentElement) : { ok: true, left: 0, right: innerWidth, top: 0, bottom: innerHeight };
    const value = { ...parent };
    if (value.ok) {
      value.ok = element instanceof HTMLElement && !element.matches(excluded) &&
        element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
      if (value.ok) {
        const css = getComputedStyle(element);
        value.ok = css.clip === 'auto' && css.clipPath === 'none' && css.opacity !== '0';
        if (/hidden|clip|auto|scroll/.test(css.overflowX + css.overflowY)) {
          const box = element.getBoundingClientRect();
          if (/hidden|clip|auto|scroll/.test(css.overflowX)) {
            value.left = Math.max(value.left, box.left); value.right = Math.min(value.right, box.right);
          }
          if (/hidden|clip|auto|scroll/.test(css.overflowY)) {
            value.top = Math.max(value.top, box.top); value.bottom = Math.min(value.bottom, box.bottom);
          }
        }
      }
    }
    geometry.set(element, value);
    return value;
  }
  const pieces = new Map<Node, Array<{ text: string; rects: DOMRect[] }>>();
  function painted(node: Node): Array<{ text: string; rects: DOMRect[] }> {
    const cached = pieces.get(node);
    if (cached) return cached;
    let result: Array<{ text: string; rects: DOMRect[] }> = [];
    if (node.nodeType === Node.TEXT_NODE && node.textContent && node.parentElement) {
      const bounds = clip(node.parentElement);
      if (bounds.ok) {
        const range = document.createRange();
        range.selectNodeContents(node);
        const rects = [...range.getClientRects()].filter(rect =>
          Math.min(bounds.right, rect.right) - Math.max(bounds.left, rect.left) > 1 &&
          Math.min(bounds.bottom, rect.bottom) - Math.max(bounds.top, rect.top) > 1);
        if (rects.length) result = [{ text: node.textContent, rects }];
      }
    } else if (node instanceof Element && clip(node).ok) result = [...node.childNodes].flatMap(painted);
    pieces.set(node, result);
    return result;
  }
  const prefix = new RegExp(options.prefix), clause = new RegExp(`(?:${options.clause})`);
  // Do not prefix-filter raw textContent: a hidden leading child could then
  // hide an otherwise valid painted prefix. Prefix-check only painted text.
  const candidates = nodes.map(node => {
    const content = painted(node);
    return { node, text: content.map(p => p.text).join('').replace(/\s+/g, ' ').trim(),
      lines: new Set(content.flatMap(p => p.rects.map(r => Math.round(r.top)))).size };
  }).filter(row => row.text && prefix.test(row.text));
  return candidates.filter(row => (!options.minimal || !candidates.some(other =>
    other.node !== row.node && row.node.contains(other.node))) && clause.test(row.text))
    .map(({ text, lines }) => ({ text, lines }));
}

export async function paintedRows(locator: Locator, prefix = '^', clause = '.*', minimal = false) {
  return locator.evaluateAll(readPaintedRows, { prefix, clause, minimal });
}

export async function paintedText(locator: Locator) {
  return (await paintedRows(locator)).map(row => row.text);
}

export async function visibleNamedRows(page: Page, name: string, clause: string) {
  const literal = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return paintedRows(page.locator('span, p, div'), `^${literal}: `, clause, true);
}

export async function expectPainted(locator: Locator, text: string | RegExp, timeout = 2_000) {
  // Short-lived countdown rows: sample densely, as expectNamed does.
  await expect.poll(() => paintedText(locator), { message: 'painted notice text, excluding every hidden/announcement descendant', timeout, intervals: [10, 20, 50] })
    .toEqual([typeof text === 'string' ? text : expect.stringMatching(text)]);
}
