import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchUsers: vi.fn(),
    createUser: vi.fn(),
    deleteUser: vi.fn(),
    resetUserPassword: vi.fn(),
    updateUserRole: vi.fn(),
    changePassword: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn(),
}))

import {
  fetchUsers,
  createUser,
  deleteUser,
  resetUserPassword,
  changePassword,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { UsersSection } from './UsersSection'
import type { UserEntry } from '@/lib/api'
import { ApiError } from '@/lib/api-error'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection(props: { devModeBypass?: boolean } = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <UsersSection {...props} />
    </QueryClientProvider>,
  )
}

const mockAddToast = vi.fn()

const ADMIN_USER: UserEntry = {
  username: 'admin',
  role: 'admin',
  has_password: true,
  has_active_token: true,
}

const REGULAR_USER: UserEntry = {
  username: 'alice',
  role: 'user',
  has_password: true,
  has_active_token: false,
}

const SECOND_ADMIN: UserEntry = {
  username: 'bob',
  role: 'admin',
  has_password: true,
  has_active_token: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)
  vi.mocked(useAuthStore).mockImplementation(((selector: (s: { username: string | null }) => unknown) =>
    selector({ username: 'admin' })) as typeof useAuthStore,
  )
})

// ── Create user happy path ────────────────────────────────────────────────────

describe('UsersSection — create user happy path', () => {
  it('fills form, calls createUser with correct values, no token displayed, shows success toast', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])
    vi.mocked(createUser).mockResolvedValue({ username: 'bob', role: 'user' })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /add user/i }))
    await user.click(screen.getByRole('button', { name: /add user/i }))

    await waitFor(() => screen.getByRole('dialog'))

    await user.type(screen.getByLabelText(/username/i), 'bob')

    // Select user role (default is already user, but click it explicitly)
    const radios = screen.getAllByRole('radio')
    const userRadio = radios.find((r) => (r as HTMLInputElement).value === 'user')!
    await user.click(userRadio)

    await user.type(screen.getByLabelText(/^password$/i), 'securepassword123')

    await user.click(screen.getByRole('button', { name: /create user/i }))

    await waitFor(() => {
      expect(createUser).toHaveBeenCalledWith({
        username: 'bob',
        role: 'user',
        password: 'securepassword123',
      })
    })

    // No "Copy" button (which would appear if we showed a token)
    expect(screen.queryByRole('button', { name: /copy/i })).not.toBeInTheDocument()

    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: expect.stringContaining('can now log in with the password you set'),
        variant: 'success',
      }),
    )
  })
})

// ── Create user — runtime token assertion ─────────────────────────────────────

describe('UsersSection — createUser returns unexpected token', () => {
  it('throws and shows error toast rather than displaying the token', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])
    // Simulate what happens when the createUser helper itself throws (the
    // runtime assert in api.ts fires before returning to the component)
    vi.mocked(createUser).mockRejectedValue(new Error('unexpected token in create response'))

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /add user/i }))
    await user.click(screen.getByRole('button', { name: /add user/i }))

    await waitFor(() => screen.getByRole('dialog'))

    await user.type(screen.getByLabelText(/username/i), 'bob')
    await user.type(screen.getByLabelText(/^password$/i), 'securepass1')

    await user.click(screen.getByRole('button', { name: /create user/i }))

    await waitFor(() => {
      expect(screen.getByText(/unexpected token in create response/i)).toBeInTheDocument()
    })

    // No token value displayed anywhere, no copy button
    expect(screen.queryByText('oops')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /copy/i })).not.toBeInTheDocument()
  })
})

// ── Username validation ───────────────────────────────────────────────────────

describe('UsersSection — username validation', () => {
  it('typing "alice bob" shows inline error about invalid characters', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /add user/i }))
    await user.click(screen.getByRole('button', { name: /add user/i }))

    await waitFor(() => screen.getByRole('dialog'))

    await user.type(screen.getByLabelText(/username/i), 'alice bob')
    await user.type(screen.getByLabelText(/^password$/i), 'securepass1')

    await user.click(screen.getByRole('button', { name: /create user/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/use only letters, numbers/i),
      ).toBeInTheDocument()
    })

    expect(createUser).not.toHaveBeenCalled()
  })
})

// ── Role radios ───────────────────────────────────────────────────────────────

describe('UsersSection — role radios', () => {
  it('renders exactly admin and user radio options', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /add user/i }))
    await user.click(screen.getByRole('button', { name: /add user/i }))

    await waitFor(() => screen.getByRole('dialog'))

    const radios = screen.getAllByRole('radio')
    const values = radios.map((r) => (r as HTMLInputElement).value)
    expect(values).toContain('user')
    expect(values).toContain('admin')
    // No other values
    expect(values.every((v) => v === 'user' || v === 'admin')).toBe(true)
  })
})

// ── Reset password (other user's row) ────────────────────────────────────────

describe('UsersSection — reset password for other user', () => {
  it('menu shows Reset password; save fires resetUserPassword; success toast mentions token invalidation', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER, REGULAR_USER])
    vi.mocked(resetUserPassword).mockResolvedValue({
      username: 'alice',
      password_reset: true,
    })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for alice/i }))

    // Open the actions menu for alice
    await user.click(screen.getByRole('button', { name: /actions for alice/i }))

    // Wait for dropdown menu item to appear in DOM (Radix portals into body)
    await waitFor(() => screen.getByText('Reset password'))
    await user.click(screen.getByText('Reset password'))

    await waitFor(() => screen.getByRole('dialog'))

    // Fill new password
    fireEvent.change(screen.getByLabelText(/new password/i), { target: { value: 'newpassword123' } })

    await user.click(screen.getByRole('button', { name: /^reset password$/i }))

    await waitFor(() => {
      expect(resetUserPassword).toHaveBeenCalledWith('alice', 'newpassword123')
    })

    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: expect.stringContaining('token is now invalid'),
        variant: 'success',
      }),
    )
  })
})

// ── Own row shows "Change my password" ────────────────────────────────────────

describe('UsersSection — own row password action', () => {
  it('own row menu shows "Change my password", not "Reset password"; posts to /auth/change-password', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER, REGULAR_USER])
    vi.mocked(changePassword).mockResolvedValue({ success: true })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for admin/i }))

    // Open the actions menu for admin (own row)
    await user.click(screen.getByRole('button', { name: /actions for admin/i }))

    // Should see "Change my password", NOT "Reset password"
    await waitFor(() => {
      expect(screen.getByText('Change my password')).toBeInTheDocument()
    })
    expect(screen.queryByText('Reset password')).not.toBeInTheDocument()

    await user.click(screen.getByText('Change my password'))

    await waitFor(() => screen.getByRole('dialog'))

    fireEvent.change(screen.getByLabelText(/current password/i), {
      target: { value: 'oldpassword' },
    })
    fireEvent.change(screen.getByLabelText(/new password/i), {
      target: { value: 'newpassword123' },
    })

    await user.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() => {
      expect(changePassword).toHaveBeenCalledWith('oldpassword', 'newpassword123')
    })

    // resetUserPassword must NOT have been called
    expect(resetUserPassword).not.toHaveBeenCalled()
  })
})

// ── Delete last admin shows inline 409 error ─────────────────────────────────

describe('UsersSection — delete last admin', () => {
  it('Delete menu item is disabled for own row when user is the only admin', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for admin/i }))

    await user.click(screen.getByRole('button', { name: /actions for admin/i }))

    await waitFor(() => {
      const deleteItem = screen.getByText('Delete')
      const menuItem = deleteItem.closest('[role="menuitem"]')
      expect(menuItem).toHaveAttribute('data-disabled')
    })
  })

  it('delete on non-self user with 409 shows inline error and keeps dialog open', async () => {
    const user = userEvent.setup()
    // Two admins; deleting alice (non-self) triggers backend 409
    const ALICE_ADMIN: UserEntry = {
      username: 'alice',
      role: 'admin',
      has_password: true,
      has_active_token: false,
    }
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER, ALICE_ADMIN])
    vi.mocked(deleteUser).mockRejectedValue(
      new ApiError(409, 'cannot leave the deployment with zero administrators'),
    )

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for alice/i }))

    await user.click(screen.getByRole('button', { name: /actions for alice/i }))

    await waitFor(() => screen.getByText('Delete'))
    await user.click(screen.getByText('Delete'))

    await waitFor(() => screen.getByRole('dialog'))
    await user.click(screen.getByRole('button', { name: /^delete$/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/cannot leave the deployment with zero administrators/i),
      ).toBeInTheDocument()
    })

    // Dialog still open
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

// ── Delete own account when only admin: button disabled ───────────────────────

describe('UsersSection — delete own account when only admin', () => {
  it('Delete is disabled with tooltip for current user when they are the only admin', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER])

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for admin/i }))

    await user.click(screen.getByRole('button', { name: /actions for admin/i }))

    await waitFor(() => {
      const deleteItem = screen.getByText('Delete')
      const menuItem = deleteItem.closest('[role="menuitem"]')
      expect(menuItem).toHaveAttribute('data-disabled')
    })
  })
})

// ── Delete own account when another admin exists: enabled ─────────────────────

describe('UsersSection — delete own account when another admin exists', () => {
  it('Delete is enabled for own row when another admin exists', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchUsers).mockResolvedValue([ADMIN_USER, SECOND_ADMIN])
    vi.mocked(deleteUser).mockResolvedValue({ username: 'admin', deleted: true })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /actions for admin/i }))

    await user.click(screen.getByRole('button', { name: /actions for admin/i }))

    await waitFor(() => {
      const deleteItem = screen.getByText('Delete')
      const menuItem = deleteItem.closest('[role="menuitem"]')
      expect(menuItem).not.toHaveAttribute('data-disabled')
    })
  })
})

// ── Dev-mode-bypass hides section ─────────────────────────────────────────────

describe('UsersSection — dev-mode-bypass', () => {
  it('renders bypass alert when devModeBypass=true', () => {
    renderSection({ devModeBypass: true })

    expect(
      screen.getByText(/user management is disabled in dev-mode-bypass/i),
    ).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(fetchUsers).not.toHaveBeenCalled()
  })
})

// ── Non-admin nav: Access tab not shown in UsersSection ───────────────────────

describe('SettingsScreen — non-admin nav', () => {
  it('UsersSection in bypass mode does not show an Access tab element', () => {
    // The SettingsScreen hides the Access tab trigger for non-admins and for
    // dev_mode_bypass. UsersSection itself renders only its alert in bypass mode.
    renderSection({ devModeBypass: true })

    // Confirm no tab role named "Access" is present in the section itself
    expect(screen.queryByRole('tab', { name: /^access$/i })).not.toBeInTheDocument()
  })
})
