// panel-shell.stories.tsx — the wave-0 demo stories (SP-15/SP-16: the
// stories ARE the demo). One interactive workspace — a mock chat, the six
// panel toggles, and a two-workspace switcher (SP-13's per-workspace width
// memory made clickable) — exercised at the MIN-201 viewport set:
// 1280x800 docked, 680x900 (the takeover boundary, where the docked floors
// still fit exactly), and 679x900 (takeover). Each per-panel story
// pre-opens its panel through the REAL trigger so a founder click opens
// each panel; the §9 click-test rows drive these stories.
//
// Demo-only harness (never app code): installFixtureApi() swaps window.fetch
// for the fixture interceptor so the real LibraryExplorer reads fixture
// data with no gateway (SP-31), and FixtureHarness supplies the QueryClient
// LibraryExplorer's useQuery hooks require.

import { useState } from 'react'
import type { ReactNode } from 'react'
import type { Meta, StoryObj } from '@storybook/react-vite'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { expect, userEvent, within } from 'storybook/test'
import { DemoWorkspace } from './DemoWorkspace'
import { installFixtureApi } from './fixtureApi'
import type { PanelId } from '../types'

installFixtureApi()

/** QueryClient for the real LibraryExplorer (it mounts useQuery hooks). */
function FixtureHarness({ children }: { children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: false, refetchOnWindowFocus: false, staleTime: Infinity },
        },
      }),
  )
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

const meta = {
  title: 'Side Panel Shell/Demo Workspace',
  component: DemoWorkspace,
  parameters: { layout: 'fullscreen' },
  decorators: [
    (Story) => (
      <FixtureHarness>
        {/* Definite height, as the real app shell gives the row (the demo
            stage must mirror the app, not Storybook's content-height chain).
            Without it the takeover row — whose chat column is display:none —
            collapses to zero height and the panel renders 0px tall. */}
        <div className="h-screen w-screen overflow-hidden">
          <Story />
        </div>
      </FixtureHarness>
    ),
  ],
} satisfies Meta<typeof DemoWorkspace>
export default meta
type Story = StoryObj<typeof meta>

/** Open a panel through its real tab-strip trigger (never by store poking). */
function openViaTrigger(id: PanelId) {
  return async ({ canvasElement }: { canvasElement: HTMLElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(canvas.getByTestId(`panel-trigger-${id}`))
    await expect(canvas.getByTestId('side-panel')).toBeInTheDocument()
  }
}

export const Docked: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
}

/** Row 11: at exactly 680px the docked floors fit — chat 360, panel 320. */
export const DockedBoundary: Story = {
  parameters: { viewport: { defaultViewport: 'panel680x900' } },
}

/** Rows 10/12/14/15: below 680px the panel takes over the full row. */
export const PhoneTakeover: Story = {
  parameters: { viewport: { defaultViewport: 'panel679x900' } },
}

/** Row 13: the Back affordance — one pushed history step per open panel. */
export const PhoneBack: Story = {
  parameters: { viewport: { defaultViewport: 'panel679x900' } },
}

export const PanelLibrary: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
  play: openViaTrigger('library'),
}

export const PanelBrowser: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
}

export const PanelMail: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
  play: openViaTrigger('mail'),
}

export const PanelTasks: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
  play: openViaTrigger('tasks'),
}

export const PanelTeam: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
  play: openViaTrigger('team'),
}

export const PanelCalendar: Story = {
  parameters: { viewport: { defaultViewport: 'panel1280x800' } },
  play: openViaTrigger('calendar'),
}
