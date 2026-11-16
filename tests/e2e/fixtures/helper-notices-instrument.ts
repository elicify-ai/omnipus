import { expect, type Page, type TestInfo } from '@playwright/test';
import { visibleNamed } from './helper-notices-harness';
import { expectPainted, visibleNamedRows } from './helper-notices-visible';
import { checkFirstPaintInstrument } from './helper-notices-paint';

// Instrument controls, inside an existing product case: no added case, mocked
// app, synthetic frame, paid model, or product PASS from this fixture page.
export async function checkNoticeVisibilityInstrument(page: Page, info: TestInfo) {
  const probe = await page.context().newPage();
  const layouts = [
    'aria-live="polite"', 'role="status"', 'role="alert"', 'role="log"', 'aria-hidden="true"', 'hidden',
    'class="sr-only"', 'style="display:none"', 'style="visibility:hidden"', 'style="opacity:0"',
    'style="content-visibility:hidden"', 'style="clip-path:inset(50%)"',
    'style="position:absolute;clip:rect(0px,0px,0px,0px)"', 'style="width:0;height:0;overflow:hidden"',
  ];
  try {
    for (const attributes of layouts) {
      await probe.setContent(`<main><div id="notice"><p><span>Researcher: </span><span>visible countdown 0:13</span></p>
        <section ${attributes}><div><p><span>Researcher: frozen countdown 0:14</span></p></div></section></div>
        <p>Writer: The model hasn't responded for 35s — still waiting.</p></main>`.replace(/\n\s*/g, ''));
      // Demonstrate that the OLD wrapper-based instrument really gives a false
      // positive on this adversarial layout, before trusting its replacement.
      const old = await probe.locator('span, p, div').filter({ hasText: /^Researcher: .*0:14/ }).evaluateAll(nodes => {
        const visible = nodes.filter(n => !(n as HTMLElement).closest('[aria-live], .sr-only, [aria-hidden="true"]') &&
          (n as HTMLElement).checkVisibility());
        return visible.filter(n => !visible.some(other => other !== n && n.contains(other))).map(n => n.textContent?.trim());
      });
      expect(old.length, `old selector can falsely accept hidden descendant: ${attributes}`).toBeGreaterThan(0);
      expect(await visibleNamed(probe, 'Researcher', '0:14'), `hidden descendant never supplies a named row: ${attributes}`).toEqual([]);
      expect(await visibleNamed(probe, 'Researcher', 'responded'), 'a named wrapper cannot borrow a sibling helper clause').toEqual([]);
      expect(await visibleNamed(probe, 'Researcher', 'retry|responded'), 'alternation cannot escape the Researcher prefix').toEqual([]);
      expect(await visibleNamed(probe, 'Researcher', '0:13')).toEqual(['Researcher: visible countdown 0:13']);
      await expectPainted(probe.locator('#notice'), 'Researcher: visible countdown 0:13');
      expect((await visibleNamedRows(probe, 'Researcher', '0:13')).map(row => row.lines), 'hidden text never supplies wrap geometry').toEqual([1]);
    }
    await probe.setContent('<div><p>R(1)+. [x]: current notice</p><p>R1xx: wrong agent</p></div>');
    expect(await visibleNamed(probe, 'R(1)+. [x]', 'current notice')).toEqual(['R(1)+. [x]: current notice']);
    await probe.setContent('<div><span hidden>not a prefix</span><p>Researcher: visible countdown 0:13</p></div>');
    expect(await visibleNamed(probe, 'Researcher', '0:13'), 'a hidden leading node cannot hide a real painted prefix').toEqual(['Researcher: visible countdown 0:13']);
    await checkFirstPaintInstrument(probe, info);
    await info.attach('painted-notice-instrument', { body: JSON.stringify({ hiddenLayouts: layouts, positivePaintedText: '0:13',
      rejectedFrozenText: '0:14', groupedAlternation: true, literalName: true, oldInstrumentFalsePositive: true,
      firstPaintCounterControls: ['immutable first value', 'off by3s', 'not restored', '2000/2001ms', '1/1.001s'], pageClockOnly: true }), contentType: 'application/json' });
    console.log(`NOTICE VISIBILITY INSTRUMENT: ${layouts.length} hidden/announcement layouts rejected; painted text, literal name, prefix isolation and reflow controls passed`);
  } finally { await probe.close(); }
}
