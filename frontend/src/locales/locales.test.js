import { describe, it, expect } from 'vitest'
import en from './en.json'
import id from './id.json'

describe('run summary messages', () => {
  // The asset run reports per-symbol fetch errors (e.g. no Twelve Data key).
  // Without {errors} the user just sees "0 rules checked" and no reason.
  it.each([
    ['en', en],
    ['id', id],
  ])('%s asset summary shows the error count', (_, messages) => {
    expect(messages.run.asset_summary).toContain('{errors}')
  })
})

function flatKeys(obj, prefix = '') {
  return Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === 'object' ? flatKeys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
}

describe('asset rules help text', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s interval help uses the configured hours', (_, messages) => {
    expect(messages.asset.rules.help_interval).toContain('{hours}')
    // Check now refreshes prices older than 15 minutes (#20); the help
    // must not say it only reuses the last fetched price.
    expect(messages.asset.rules.help_interval).toContain('15')
  })
})

describe('locale parity', () => {
  it('en and id have exactly the same keys', () => {
    expect(flatKeys(id).sort()).toEqual(flatKeys(en).sort())
  })
})

describe('stale prices message', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s shows the stale count', (_, messages) => {
    expect(messages.run.stale_prices).toContain('{stale}')
  })
})

describe('run result panel', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s finished-at text shows the time', (_, messages) => {
    expect(messages.run.finished_at).toContain('{time}')
  })
})
