import { describe, it, expect } from 'vitest'
import { idID, dateIdID, enUS, dateEnUS } from 'naive-ui'
import { naiveLocaleFor } from './naiveLocale'

describe('naiveLocaleFor', () => {
  it('gives Naive UI the Indonesian strings for "id"', () => {
    expect(naiveLocaleFor('id')).toEqual({ locale: idID, dateLocale: dateIdID })
  })
  it('falls back to English', () => {
    expect(naiveLocaleFor('en')).toEqual({ locale: enUS, dateLocale: dateEnUS })
    expect(naiveLocaleFor('fr')).toEqual({ locale: enUS, dateLocale: dateEnUS })
  })
})
