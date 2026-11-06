// Test-browser cache only. Sources are live DOM mutations and independent
// physical fixture inputs, never store/viewport-hook output or expected drift.
// MutationObserver.takeRecords is essential: synchronous reads must see DOM
// changes before the observer's microtask callback has been delivered.
export type PhysicalLayoutInputs = {
  height: number; width: number; rowHeight: number; controlHeight: number
  hidden: boolean; paragraphHeight: number
  rowHeights: Map<string, number>; toolHeights: Map<string, number>; partHeights: Map<string, number>
}

export function createPhysicalLayoutCache(state: PhysicalLayoutInputs) {
  let entries = new WeakMap<Element, Map<string, unknown>>()
  let scalars: unknown[] = []
  let maps: Map<string, number>[] = []
  let dirty = true
  const observed = new WeakSet<Element>()
  const observer = new MutationObserver(() => { dirty = true })
  const sameMap = (current: Map<string, number>, prior: Map<string, number> | undefined) => {
    if (!prior || prior.size !== current.size) return false
    for (const [key, value] of current) if (!prior.has(key) || !Object.is(prior.get(key), value)) return false
    return true
  }

  return {
    prepare(viewport: Element) {
      if (!observed.has(viewport)) {
        observer.observe(viewport, { subtree: true, attributes: true, childList: true, characterData: true })
        observed.add(viewport)
        dirty = true
      }
      const nextScalars = [state.height, state.width, state.rowHeight, state.controlHeight, state.hidden, state.paragraphHeight]
      const nextMaps = [state.rowHeights, state.toolHeights, state.partHeights]
      // Observe the viewport itself even when detached: DOM-only controls do
      // not need a mounted application, and must not get a stale cache either.
      const domChanged = observer.takeRecords().length > 0
      const inputsChanged = nextScalars.some((value, index) => !Object.is(value, scalars[index]))
        || nextMaps.some((map, index) => !sameMap(map, maps[index]))
      if (dirty || domChanged || inputsChanged) {
        entries = new WeakMap()
        scalars = nextScalars
        maps = nextMaps.map(map => new Map(map))
        dirty = false
      }
    },
    read<T>(element: Element, key: string, measure: () => T): T {
      let values = entries.get(element)
      if (!values) { values = new Map(); entries.set(element, values) }
      if (!values.has(key)) values.set(key, measure())
      return values.get(key) as T
    },
    disconnect() {
      observer.disconnect()
      entries = new WeakMap()
    },
  }
}
