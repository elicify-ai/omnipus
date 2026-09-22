import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Label } from './label'
import { Input } from './input'

describe('Label — control association', () => {
  it('uses htmlFor to give a native control its accessible name', () => {
    render(<><Label htmlFor="email">Email</Label><Input id="email" /></>)
    expect(screen.getByRole('textbox', { name: 'Email' })).toHaveAttribute('id', 'email')
  })
})
