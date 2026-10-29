// T-39 / S-50 — AvatarStack draws up to max avatars and a +N marker for the rest.
// Oracle: docs/internal/specs/1449-group-chat-spec.md S-50 (max 3; +N for the rest)
// and design-system/manifests/avatar-stack.json check avatar-stack-unit.
// The component is the shared mark row. A missing module fails loudly, never skips.
import { createElement, type ComponentType, type ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'

interface StackItem {
  key: string
  node: ReactNode
}

interface AvatarStackProps {
  items: readonly StackItem[]
  max?: number
  total?: number
}

const MODULE_PATH = '/src/components/ui/avatar-stack.tsx'
const modules = import.meta.glob<Record<string, unknown>>([MODULE_PATH])

async function loadStack(): Promise<ComponentType<AvatarStackProps>> {
  const load = modules[MODULE_PATH]
  if (!load) {
    throw new Error(`BLOCKED: ${MODULE_PATH}::AvatarStack not implemented — required by T-39 / S-50`)
  }
  const mod = await load()
  const Stack = mod.AvatarStack
  if (typeof Stack !== 'function') {
    throw new Error(`BLOCKED: ${MODULE_PATH} exports no AvatarStack — required by T-39 / S-50`)
  }
  return Stack as ComponentType<AvatarStackProps>
}

function items(count: number): StackItem[] {
  return Array.from({ length: count }, (_, index) => ({
    key: `m${index}`,
    node: createElement('span', null, `mark-${index}`),
  }))
}

describe('AvatarStack', () => {
  it('AvatarStack draws up to max avatars and a +N marker for the rest', async () => {
    const AvatarStack = await loadStack()
    // S-50 examples, max 3: 2 → 2 none; 3 → 3 none; 4 → 3 +1; 10 → 3 +7.
    const cases = [
      { count: 2, shown: 2, overflow: null },
      { count: 3, shown: 3, overflow: null },
      { count: 4, shown: 3, overflow: '+1' },
      { count: 10, shown: 3, overflow: '+7' },
    ]
    for (const row of cases) {
      const view = render(createElement(AvatarStack, { items: items(row.count), max: 3 }))
      for (let index = 0; index < row.shown; index += 1) {
        expect(screen.getByText(`mark-${index}`), `S-50: avatar ${index} of ${row.count} is drawn`).toBeInTheDocument()
      }
      if (row.shown < row.count) {
        expect(screen.queryByText(`mark-${row.shown}`), 'S-50: avatars past max are not drawn').toBeNull()
      }
      if (row.overflow) expect(screen.getByText(row.overflow, { exact: true })).toBeInTheDocument()
      else expect(view.container.textContent ?? '').not.toMatch(/\+\d+/)
      view.unmount()
    }
  })
})
