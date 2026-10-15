import { describe, it, expect } from 'vitest'
import { formatAge, isOlderThanHours } from './time'

const now = Date.parse('2026-10-15T10:00:00Z')

describe('formatAge', () => {
  it.each([
    ['2026-10-15T09:56:00Z', 'en', '4 minutes ago'],
    ['2026-10-15T07:00:00Z', 'en', '3 hours ago'],
    ['2026-10-13T10:00:00Z', 'en', '2 days ago'],
    ['2026-10-15T09:59:30Z', 'en', 'this minute'],
    ['2026-10-15T09:56:00Z', 'id', '4 menit yang lalu'],
    ['2026-10-15T10:05:00Z', 'en', 'this minute'], // server clock slightly ahead
  ])('%s in %s → %s', (iso, locale, expected) => {
    expect(formatAge(iso, locale, now)).toBe(expected)
  })

  it('invalid input is a dash', () => {
    expect(formatAge('garbage', 'en', now)).toBe('—')
    expect(formatAge(null, 'en', now)).toBe('—')
  })
})

describe('isOlderThanHours', () => {
  it('compares the age with the hours', () => {
    expect(isOlderThanHours('2026-10-15T05:00:00Z', 4, now)).toBe(true)
    expect(isOlderThanHours('2026-10-15T07:00:00Z', 4, now)).toBe(false)
  })

  it('unknown time or hours is not stale', () => {
    expect(isOlderThanHours(null, 4, now)).toBe(false)
    expect(isOlderThanHours('2026-10-15T05:00:00Z', null, now)).toBe(false)
  })
})
