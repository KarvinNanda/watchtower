import { describe, it, expect } from 'vitest'
import { validatePasswordChange, passwordChangeErrorKey } from './passwordChange'

const ok = { current: 'Old1Pass!', next: 'New2Pass@', confirm: 'New2Pass@' }

describe('validatePasswordChange', () => {
  it('accepts a strong new password that differs and matches its confirmation', () => {
    expect(validatePasswordChange(ok)).toBeNull()
  })

  it.each([
    [{ ...ok, current: '' }, { field: 'current', key: 'common.required_field' }],
    [{ ...ok, next: 'weak', confirm: 'weak' }, { field: 'next', key: 'auth.password_hint' }],
    [{ ...ok, next: 'Old1Pass!', confirm: 'Old1Pass!' }, { field: 'next', key: 'profile.password_same' }],
    [{ ...ok, confirm: 'New2Pass#' }, { field: 'confirm', key: 'auth.password_mismatch' }],
  ])('%o → %o', (form, want) => {
    expect(validatePasswordChange(form)).toEqual(want)
  })
})

describe('passwordChangeErrorKey', () => {
  const err = (status, code) => ({ response: { status, data: { error: code } } })

  it('maps the API answers to messages', () => {
    expect(passwordChangeErrorKey(err(400, 'wrong_password'))).toEqual({ field: 'current', key: 'profile.password_wrong' })
    expect(passwordChangeErrorKey(err(400, 'validation_error'))).toEqual({ field: 'next', key: 'auth.password_hint' })
    expect(passwordChangeErrorKey(err(429, 'too_many_requests'))).toEqual({ field: null, key: 'profile.password_too_many' })
    expect(passwordChangeErrorKey(err(500, 'internal_error'))).toEqual({ field: null, key: 'profile.password_failed' })
    expect(passwordChangeErrorKey(new Error('offline'))).toEqual({ field: null, key: 'profile.password_failed' })
  })
})
