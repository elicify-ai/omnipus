// ToolPolicyEditor.globalSettingLink.test.tsx — Skills & Tools redesign
// (#1963, epic #1519, page F), order 62 (S-127's exact sentences and the
// per-row "Change the global setting" link).
//
// Oracle: skills-tools-redesign-spec.md S-127 — global `bash` at Ask and
// global `send_email` at Deny: focusing the `bash` Allow badge shows the
// disabled badge's tooltip "The global setting is Ask. Set it to Allow on
// Skills & Tools first."; the `send_email` Allow AND Ask badges are
// disabled with "The global setting is Deny. Change it on Skills & Tools
// first."; and each row's "Change the global setting" link opens Skills &
// Tools with that row in view and focused.
//
// Order 22 (ToolPolicyEditor.locks.test.tsx) pins FR-023's disabled-with-
// tooltip behaviour loosely (regex /the global setting is ask/i); this
// file pins S-127's exact sentences and the link, which do not exist yet.
//
// RED today: the row's lock text is the old sentence "Locked by a global
// \"Ask\" policy in Settings → Security. The effective policy is the most
// restrictive …" (ToolPolicyEditor.tsx::lockTitle), and the row's link is
// "Global: Ask" pointing at /#/settings — neither S-127 sentence nor the
// "Change the global setting" link exists.
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor } from './ToolPolicyEditor'

const tools: RegistryTool[] = [
  {
    name: 'bash',
    description: 'Run a shell command.',
    scope: 'core',
    category: 'shell',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
  {
    name: 'send_email',
    description: 'Send an email.',
    scope: 'core',
    category: 'messaging',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
]

function mountEditor(
  value: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
  globalPolicies: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
) {
  const view = render(
    <ToolPolicyEditor tools={tools} value={value} globalPolicies={globalPolicies} onChange={() => {}} />,
  )
  return view
}

// The category groups start collapsed (ToolPolicyEditor's per-category
// DisclosureRow); expand both fixtures' groups so the rows render.
async function expandCategories() {
  const user = userEvent.setup()
  for (const name of [/shell/i, /messaging/i]) {
    const trigger = screen
      .getAllByRole('button', { name })
      .find((b) => b.getAttribute('aria-expanded') !== undefined)
    if (trigger && trigger.getAttribute('aria-expanded') === 'false') {
      await user.click(trigger)
    }
  }
}

describe('ToolPolicyEditor — S-127 tooltips and the global-setting link (order 62)', () => {
  it('S-127: the bash Allow badge under a global Ask carries the Ask sentence', async () => {
    mountEditor({ policies: { bash: 'allow' } }, { policies: { bash: 'ask' } })
    await expandCategories()

    const row = screen.getByTestId('tool-row-bash')
    const allow = within(row).getByRole('button', { name: /^allow$/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(
      'The global setting is Ask. Set it to Allow on Skills & Tools first.',
    )
  })

  it('S-127: the send_email Allow badge under a global Deny carries the Deny sentence', async () => {
    mountEditor({ policies: { send_email: 'allow' } }, { policies: { send_email: 'deny' } })
    await expandCategories()

    const row = screen.getByTestId('tool-row-send_email')
    const allow = within(row).getByRole('button', { name: /^allow$/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(
      'The global setting is Deny. Change it on Skills & Tools first.',
    )
  })

  it('S-127: the send_email Ask badge is disabled too, with the Deny sentence', async () => {
    mountEditor({ policies: { send_email: 'ask' } }, { policies: { send_email: 'deny' } })
    await expandCategories()

    const row = screen.getByTestId('tool-row-send_email')
    const ask = within(row).getByRole('button', { name: /^ask$/i })
    expect(ask).toBeDisabled()
    expect(ask).toHaveAccessibleDescription(
      'The global setting is Deny. Change it on Skills & Tools first.',
    )
  })

  it('S-127: each row carries a "Change the global setting" link', async () => {
    mountEditor({ policies: { bash: 'allow' } }, { policies: { bash: 'ask', send_email: 'deny' } })
    await expandCategories()

    const bashRow = screen.getByTestId('tool-row-bash')
    const bashLink = within(bashRow).getByRole('link', { name: 'Change the global setting' })
    expect(bashLink).toBeInTheDocument()

    const emailRow = screen.getByTestId('tool-row-send_email')
    const emailLink = within(emailRow).getByRole('link', { name: 'Change the global setting' })
    expect(emailLink).toBeInTheDocument()
  })
})
