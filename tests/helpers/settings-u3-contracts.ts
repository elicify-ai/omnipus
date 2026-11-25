// U3-1/2/3: direct fix-round-u3 ruling, US-9 AS5 / FR-070 / SC-009.
// Token/class contracts only. jsdom cannot establish 14px-root layout arithmetic,
// measured 44x44 hit regions or 390px reflow; the browser re-test owns those.
// Accepted: pointer-coarse (or the equivalent media variant) of size-, h-, w-,
// min-h- or min-w- with the touch-minimum token, in bracket or v4 shorthand form.
const TOUCH_TOKEN = '(?:\\[var\\(--target-touch-minimum\\)\\]|\\(--target-touch-minimum\\))'

export function tokenAxisU3(classes: Iterable<string>, axis: 'h' | 'w') {
  const pattern = new RegExp(`^(?:pointer-coarse:|\\[@media\\(pointer:coarse\\)\\]:)(?:(?:min-)?${axis}|size)-${TOUCH_TOKEN}$`)
  return [...classes].some((candidate) => pattern.test(candidate))
}

export function tokenTouchU3(target: HTMLElement) {
  const height = tokenAxisU3(target.classList, 'h')
  const width = tokenAxisU3(target.classList, 'w') || target.classList.contains('w-full')
    || (target.classList.contains('flex-1') && target.parentElement?.classList.contains('flex') === true)
  return { height, width }
}

// U3-5 label containment, from the design system's own precedents (FilterMenu trigger
// `min-w-0 truncate`, option `min-w-0 wrap-anywhere`) and CSS Text 3: overflow-wrap
// `anywhere` lowers the min-content size, so a long token such as "(minimax.io)" can
// break inside the word; `break-word` does not, so it only helps a separate label that
// can itself shrink. A class contract, not layout: the 390px browser re-test measures.
const WRAPS = ['whitespace-normal', 'whitespace-pre-line', 'whitespace-pre-wrap', 'whitespace-break-spaces']
const WHITE_SPACE = [...WRAPS, 'whitespace-nowrap', 'whitespace-pre']
const BREAK_ANYWHERE = ['wrap-anywhere', 'break-all', '[overflow-wrap:anywhere]']
const BREAK_WORD = ['wrap-break-word', 'break-words', '[overflow-wrap:break-word]']
const SHRINKS = ['min-w-0', 'w-full', 'max-w-full']

export function tileLabelU3(tile: HTMLElement, label: HTMLElement) {
  const own = [...label.classList]
  const outer = [...tile.classList]
  const both = [...own, ...outer]
  // grid-cols-N is repeat(N, minmax(0, 1fr)): its items have no content-based minimum width.
  const inGridTrack = [...(tile.parentElement?.classList ?? [])].some((name) => name.startsWith('grid-cols-'))
  const canShrink = outer.includes('min-w-0') || inGridTrack
  // white-space and overflow-wrap inherit from the tile unless the label sets its own.
  const whiteSpace = own.find((name) => WHITE_SPACE.includes(name)) ?? outer.find((name) => WHITE_SPACE.includes(name)) ?? ''
  const wraps = WRAPS.includes(whiteSpace)
  const breaksAnywhere = both.some((name) => BREAK_ANYWHERE.includes(name))
  const breaksWord = both.some((name) => BREAK_WORD.includes(name))
  const labelShrinks = label !== tile && own.some((name) => SHRINKS.includes(name))
  const truncated = both.includes('truncate') || own.some((name) => /^line-clamp-[1-9]\d*$/.test(name))
  return { canShrink, contained: truncated || (wraps && (breaksAnywhere || (breaksWord && labelShrinks))) }
}
