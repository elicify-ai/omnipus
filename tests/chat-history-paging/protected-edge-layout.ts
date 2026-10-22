// Browser edge only: actual virtualizer, estimated prefixes and protection
// stay real. An80px viewport keeps an offscreen focused source within the
// production5-row overscan, separately from the visible/two-screen guard.
import { installHistoryLayout } from './measurement-layout'

export function installProtectedEdgeLayout() {
  const layout = installHistoryLayout({ virtual: true })
  layout.state.height = 80
  layout.state.rowHeight = 80
  return layout
}
