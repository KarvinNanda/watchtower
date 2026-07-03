import { describe, it, expect, vi } from 'vitest'
import { sanitizeInput, isValidEmail, validatePassword, safeRedirect } from './security'

describe('sanitizeInput', () => {
  it('trim whitespace', () => {
    expect(sanitizeInput('  hello  ')).toBe('hello')
  })
  it('strip angle brackets', () => {
    expect(sanitizeInput('<script>alert(1)</script>')).not.toContain('<')
  })
  it('limit to 1000 chars', () => {
    const long = 'a'.repeat(2000)
    expect(sanitizeInput(long).length).toBe(1000)
  })
  it('return non-string as-is', () => {
    expect(sanitizeInput(123)).toBe(123)
  })
})

describe('isValidEmail', () => {
  it('valid email', () => {
    expect(isValidEmail('test@example.com')).toBe(true)
  })
  it('invalid — no domain', () => {
    expect(isValidEmail('test@')).toBe(false)
  })
  it('invalid — no @', () => {
    expect(isValidEmail('testexample.com')).toBe(false)
  })
  it('invalid — empty', () => {
    expect(isValidEmail('')).toBe(false)
  })
})

describe('validatePassword', () => {
  it('valid password returns empty array', () => {
    expect(validatePassword('Valid1Pass!')).toHaveLength(0)
  })
  it('too short', () => {
    expect(validatePassword('Ab1!')).not.toHaveLength(0)
  })
  it('no uppercase', () => {
    expect(validatePassword('valid1pass!')).not.toHaveLength(0)
  })
  it('no lowercase', () => {
    expect(validatePassword('VALID1PASS!')).not.toHaveLength(0)
  })
  it('no number', () => {
    expect(validatePassword('ValidPass!')).not.toHaveLength(0)
  })
  it('no special char', () => {
    expect(validatePassword('Valid1Pass')).not.toHaveLength(0)
  })
})

describe('safeRedirect', () => {
  it('allow valid path', () => {
    const mockRouter = { push: vi.fn() }
    safeRedirect(mockRouter, '/dashboard')
    expect(mockRouter.push).toHaveBeenCalledWith('/dashboard')
  })
  it('redirect to / for unknown path', () => {
    const mockRouter = { push: vi.fn() }
    safeRedirect(mockRouter, '/evil-path')
    expect(mockRouter.push).toHaveBeenCalledWith('/')
  })
})
