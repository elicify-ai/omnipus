// Small eager Navigation API subset for P2's real native-jsdom history.
// Browser input only: no router/blocker/Stay function is replaced.
import { vi } from 'vitest'

export function navigationU44() {
  const history = window.history
  let serial = 0, index = 0
  const entries = [{ key: `p2-${serial++}`, url: window.location.href }]
  const originalPush = history.pushState.bind(history)
  const originalReplace = history.replaceState.bind(history)
  const push = vi.spyOn(history, 'pushState').mockImplementation((...args) => {
    originalPush(...args)
    entries.splice(index + 1)
    entries.push({ key: `p2-${serial++}`, url: window.location.href })
    index++
  })
  const replace = vi.spyOn(history, 'replaceState').mockImplementation((...args) => {
    originalReplace(...args)
    entries[index] = { ...entries[index], url: window.location.href }
  })
  const observe = () => {
    // jsdom 29 exposes undefined for a new typed hash's absent state. Supply
    // the browser's documented null-state input, without adding an entry.
    // https://developer.mozilla.org/en-US/docs/Web/API/History/state
    if (history.state === undefined) originalReplace(null, '')
    if (entries[index].url === window.location.href) return
    const found = entries.findIndex((entry) => entry.url === window.location.href)
    if (found >= 0) index = found
    else {
      // Address-bar same-document navigation: null state, a new entry/key.
      entries.splice(index + 1)
      entries.push({ key: `p2-${serial++}`, url: window.location.href })
      index++
    }
  }
  // Installed BEFORE the router, so metadata follows the address eagerly.
  window.addEventListener('popstate', observe)
  window.addEventListener('hashchange', observe)
  const entryView = (entry: typeof entries[number]) => ({ ...entry,
    get index() { return entries.indexOf(entry) }, sameDocument: true })
  const navigation = {
    get canGoBack() { return index > 0 },
    get canGoForward() { return index < entries.length - 1 },
    get currentEntry() { return entryView(entries[index]) },
    entries() { return entries.map(entryView) },
  }
  vi.stubGlobal('navigation', navigation)
  return { navigation, restore() {
    window.removeEventListener('popstate', observe)
    window.removeEventListener('hashchange', observe)
    push.mockRestore()
    replace.mockRestore()
  } }
}
