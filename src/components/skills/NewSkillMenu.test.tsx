// NewSkillMenu.test.tsx — order 29 (S-52, S-54; US-7). Driven through the
// imported component (CHECK round 1 finding 2: the earlier file held only a
// file-existence guard):
//   S-52: the New skill menu offers exactly "Write with Omnipus",
//         "Upload a skill", "Browse the catalog" — and no item carries the
//         retired "Ava"/"Admin" names;
//   S-54: with the connection down, choosing "Write with Omnipus" shows the
//         warning "Not connected — "Write a new skill" was not sent. Press
//         the button again once you are reconnected." and nothing is queued;
//   S-53 (control): with the connection up the same choice opens the
//         Omnipus chat with the person's message "Write a new skill" (the
//         openOmnipusChat edge — C-8/W5).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentType } from 'react'

// The pack's BLOCKED seam (support.tsx::requireModule convention).
const menuModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/NewSkillMenu.tsx'])
async function requireNewSkillMenu(): Promise<ComponentType<{ connectionUp?: boolean }>> {
  expect(
    Object.keys(menuModules),
    'BLOCKED: NewSkillMenu is not implemented — required by skills-tools-redesign-spec S-52, S-54 and C-8',
  ).toContain('/src/components/skills/NewSkillMenu.tsx')
  const mod = (await menuModules['/src/components/skills/NewSkillMenu.tsx']()) as Record<string, unknown>
  expect(typeof mod.NewSkillMenu, 'BLOCKED: NewSkillMenu public export').toBe('function')
  return mod.NewSkillMenu as ComponentType<{ connectionUp?: boolean }>
}

let NewSkillMenu: ComponentType<{ connectionUp?: boolean }>

const openOmnipusChat = vi.fn()
vi.mock('@/store/omnipusChat', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/omnipusChat')>()
  return { ...actual, openOmnipusChat }
})

beforeEach(async () => {
  NewSkillMenu = await requireNewSkillMenu()
})

describe('NewSkillMenu — write with Omnipus (order 29)', () => {
  beforeEach(() => {
    openOmnipusChat.mockReset()
  })

  it('S-52: the menu offers exactly the three items and no retired name', async () => {
    const user = userEvent.setup()
    render(<NewSkillMenu />)
    await user.click(screen.getByRole('button', { name: /new skill/i }))

    expect(screen.getByRole('menuitem', { name: 'Write with Omnipus' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Upload a skill' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Browse the catalog' })).toBeInTheDocument()
    for (const retired of ['Ava', 'Admin']) {
      expect(screen.queryByText(retired), `no retired name ${retired} (S-52)`).toBeNull()
    }
  })

  it('S-54: offline, the choice sends nothing and shows the reconnect warning', async () => {
    const user = userEvent.setup()
    render(<NewSkillMenu connectionUp={false} />)
    await user.click(screen.getByRole('button', { name: /new skill/i }))

    await user.click(screen.getByRole('menuitem', { name: 'Write with Omnipus' }))

    expect(
      screen.getByText(
        'Not connected — "Write a new skill" was not sent. Press the button again once you are reconnected.',
      ),
    ).toBeInTheDocument()
    expect(openOmnipusChat).not.toHaveBeenCalled() // no message queued either
  })

  it('S-53 control: online, the choice opens the Omnipus chat with "Write a new skill"', async () => {
    const user = userEvent.setup()
    render(<NewSkillMenu connectionUp />)
    await user.click(screen.getByRole('button', { name: /new skill/i }))

    await user.click(screen.getByRole('menuitem', { name: 'Write with Omnipus' }))

    expect(openOmnipusChat).toHaveBeenCalledTimes(1)
    const call = openOmnipusChat.mock.calls[0]
    expect(JSON.stringify(call)).toContain('Write a new skill')
  })
})
