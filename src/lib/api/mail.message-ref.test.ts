import { describe, expect, it } from 'vitest'
import { mailUidRef } from './mail'

describe('mailUidRef', () => {
  it('builds the server-required folder-scoped UID reference', () => {
    expect(mailUidRef(777, 42)).toBe('uid:777:42')
  })
})
