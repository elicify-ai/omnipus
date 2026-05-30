import '@testing-library/jest-dom'
import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'

// Unmount rendered components after each test to prevent DOM bleed between tests
// when running the full vitest suite (N5 fix: 5 tests failed due to leaked DOM state).
afterEach(() => {
  cleanup()
})
