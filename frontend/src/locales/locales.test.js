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

describe('locale parity', () => {
  it('en and id have exactly the same keys', () => {
    expect(flatKeys(id).sort()).toEqual(flatKeys(en).sort())
  })
})
