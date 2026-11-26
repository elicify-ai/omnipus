// U6-1 / FR-070: menu items and list options are at least the 44 px touch
// minimum high on a coarse pointer, in the shared primitives (design-system
// rule 12). The virtualised model list's slot height is covered in
// settings/DefaultModelCard.picker.test.tsx. jsdom has no layout: this checks
// the coarse sizing rule each item carries; physical sizes stay a browser check.
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { touchContractU2 } from '../../../tests/helpers/settings-u2-css'
import { Command, CommandItem, CommandList } from './command'
import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSub, DropdownMenuSubTrigger, DropdownMenuTrigger,
} from './dropdown-menu'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './select'

beforeAll(() => {
  if (!window.ResizeObserver) {
    window.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} }
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
  if (!Element.prototype.hasPointerCapture) Element.prototype.hasPointerCapture = () => false
  if (!Element.prototype.releasePointerCapture) Element.prototype.releasePointerCapture = () => {}
})

afterEach(() => { cleanup() })

describe('U6-1 shared popup items carry the coarse 44 px height rule', () => {
  it('every DropdownMenu item kind', async () => {
    render(
      <DropdownMenu open>
        <DropdownMenuTrigger>More</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Plain</DropdownMenuItem>
          <DropdownMenuCheckboxItem checked>Checkbox</DropdownMenuCheckboxItem>
          <DropdownMenuRadioGroup value="a"><DropdownMenuRadioItem value="a">Radio</DropdownMenuRadioItem></DropdownMenuRadioGroup>
          <DropdownMenuSub><DropdownMenuSubTrigger>Sub</DropdownMenuSubTrigger></DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>,
    )
    const items = [...screen.getAllByRole('menuitem'), screen.getByRole('menuitemcheckbox'), screen.getByRole('menuitemradio')]
    expect(items).toHaveLength(4)
    for (const item of items) expect((await touchContractU2(item)).height, item.textContent ?? '').toBe(true)
  })

  it('CommandItem (model and web-search lists)', async () => {
    render(<Command><CommandList><CommandItem value="x">Option</CommandItem></CommandList></Command>)
    expect((await touchContractU2(screen.getByRole('option'))).height).toBe(true)
  })

  it('SelectItem', async () => {
    render(
      <Select open value="a">
        <SelectTrigger><SelectValue /></SelectTrigger>
        <SelectContent><SelectItem value="a">Option A</SelectItem></SelectContent>
      </Select>,
    )
    expect((await touchContractU2(screen.getByRole('option'))).height).toBe(true)
  })
})
