// FR-014 / U4-C1,C2: same-document browser edge, NOT a router/blocker double.
// Each traversal applies to the index left by the preceding traversal. jsdom's
// native queued traversals can coalesce, hiding the extra-Back fault (CHECK PLAT1).
// Navigation subset: MDN Navigation / NavigationHistoryEntry, read 2026-11-26.
// All entries here are same-origin/same-document. No native reload/UAT claim.
import { vi } from 'vitest'

// not-wire-format: test-only browser ledger, never sent over the gateway.
interface EntryU42 { key: string; url: string; state: unknown }
interface RequestU42 { kind: 'back' | 'forward' | 'go'; delta: number; delay: number }

export function serialBrowserU42(withNavigation: boolean) {
  const history = window.history
  const names = ['pushState', 'replaceState', 'go', 'back', 'forward', 'length'] as const
  const descriptors = new Map(names.map((name) => [name, Object.getOwnPropertyDescriptor(history, name)]))
  const nativeReplace = history.replaceState.bind(history)
  const original = { url: window.location.href, state: structuredClone(history.state) }
  let serial = 0
  let index = 0
  let delayGo = 0
  let busy = false
  const entries: EntryU42[] = [{ key: `r42-${serial++}`, ...original }]
  const queue: RequestU42[] = []
  const waiters: (() => void)[] = []
  const requests: { kind: string; delta: number; requestedAt: number }[] = []
  const applied: { kind: string; delta: number; from: number; to: number }[] = []
  const reloads: number[] = []
  const entryView = (entry: EntryU42) => ({
    key: entry.key,
    get index() { return entries.indexOf(entry) },
    url: entry.url,
    sameDocument: true,
  })
  const navigation = {
    get canGoForward() { return index < entries.length - 1 },
    get currentEntry() { return entryView(entries[index]) },
    entries() { return entries.map(entryView) },
  }
  vi.stubGlobal('navigation', withNavigation ? navigation : undefined)
  const applyAddress = () => nativeReplace(structuredClone(entries[index].state), '', entries[index].url)
  const notify = (oldURL: string) => {
    window.dispatchEvent(new PopStateEvent('popstate', { state: structuredClone(entries[index].state) }))
    if (new URL(oldURL).hash !== new URL(entries[index].url).hash) {
      window.dispatchEvent(new HashChangeEvent('hashchange', { oldURL, newURL: entries[index].url }))
    }
  }
  const pump = () => {
    if (busy) return
    const request = queue.shift()
    if (!request) { waiters.splice(0).forEach((resolve) => resolve()); return }
    busy = true
    setTimeout(() => {
      const from = index
      const to = index + request.delta
      if (request.delta === 0) {
        // Record the actual reload request; executing a document reload is not
        // available in jsdom. Product assertions must reject this request.
        reloads.push(request.delta)
      } else if (to >= 0 && to < entries.length) {
        const oldURL = entries[index].url
        index = to
        applyAddress()
        notify(oldURL)
      }
      applied.push({ kind: request.kind, delta: request.delta, from, to: index })
      busy = false
      pump()
    }, request.delay)
  }
  const traverse = (kind: RequestU42['kind'], delta: number) => {
    requests.push({ kind, delta, requestedAt: index })
    queue.push({ kind, delta, delay: kind === 'go' ? delayGo : 0 })
    pump()
  }
  const changeEntry = (push: boolean, state: unknown, url?: string | URL | null) => {
    const address = url == null ? entries[index].url : new URL(String(url), window.location.href).href
    if (new URL(address).origin !== window.location.origin) throw new DOMException('History URL must be same-origin', 'SecurityError')
    const entry = { key: push ? `r42-${serial++}` : entries[index].key, url: address, state: structuredClone(state) }
    if (push) { entries.splice(index + 1); entries.push(entry); index++ }
    else entries[index] = entry
    applyAddress()
  }
  const install = (name: typeof names[number], value: unknown) => Object.defineProperty(history, name, {
    configurable: true, writable: true, value,
  })
  install('pushState', (state: unknown, _title: string, url?: string | URL | null) => changeEntry(true, state, url))
  install('replaceState', (state: unknown, _title: string, url?: string | URL | null) => changeEntry(false, state, url))
  install('go', (delta = 0) => traverse('go', delta))
  install('back', () => traverse('back', -1))
  install('forward', () => traverse('forward', 1))
  Object.defineProperty(history, 'length', { configurable: true, get: () => entries.length })
  return {
    navigation, requests, applied, reloads,
    get index() { return index },
    delayGo(ms: number) { delayGo = ms },
    idle() { return busy || queue.length ? new Promise<void>((resolve) => waiters.push(resolve)) : Promise.resolve() },
    typeHash(hash: string) {
      const oldURL = entries[index].url
      const url = new URL(oldURL)
      url.hash = hash
      // Browser address-bar hash entry: null state, no router pushState call.
      changeEntry(true, null, url)
      notify(oldURL)
    },
    restore() {
      if (busy || queue.length) throw new Error('Cannot restore a browser edge with pending traversals')
      for (const name of names) {
        const descriptor = descriptors.get(name)
        if (descriptor) Object.defineProperty(history, name, descriptor)
        else Reflect.deleteProperty(history, name)
      }
      nativeReplace(original.state, '', original.url)
    },
  }
}
