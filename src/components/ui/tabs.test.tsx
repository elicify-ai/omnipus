import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Tabs, TabsContent, TabsList, TabsTrigger } from './tabs'

describe('Tabs keyboard contract', () => {
  it('marks every trigger for the shared CSS hit region', () => {
    render(<Tabs defaultValue="one"><TabsList><TabsTrigger value="one">One</TabsTrigger></TabsList></Tabs>)
    const trigger = screen.getByRole('tab', { name: 'One' })
    expect(trigger).toHaveAttribute('data-ds-action')
    expect(trigger.className).not.toContain('min-h-[44px]')
  })

  it('moves between tabs with ArrowRight and exposes the selected panel', async () => {
    const user = userEvent.setup()
    render(<Tabs defaultValue="one"><TabsList aria-label="Sections"><TabsTrigger value="one">One</TabsTrigger><TabsTrigger value="two">Two</TabsTrigger></TabsList><TabsContent value="one">First</TabsContent><TabsContent value="two">Second</TabsContent></Tabs>)
    const first = screen.getByRole('tab', { name: 'One' })
    first.focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Two' })).toHaveFocus()
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Second')
  })

  it('keeps panels out of the tab order when their contents provide focus targets', () => {
    render(<Tabs defaultValue="one"><TabsList><TabsTrigger value="one">One</TabsTrigger></TabsList><TabsContent value="one"><button type="button">Action</button></TabsContent></Tabs>)
    expect(screen.getByRole('tabpanel')).toHaveAttribute('tabindex', '-1')
  })
})
