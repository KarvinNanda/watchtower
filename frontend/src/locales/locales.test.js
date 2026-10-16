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

describe('asset card texts', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s placeholders', (_, messages) => {
    expect(messages.asset.card.updated).toContain('{age}')
    expect(messages.asset.card.stale_hint).toContain('{hours}')
    expect(messages.asset.card.rules_of).toContain('{symbol}')
    expect(messages.asset.rules.stop_at).toContain('{stop}')
    expect(messages.asset.rules.stop_at).toContain('{peak}')
    expect(messages.asset.rules.change_vs_threshold).toContain('{current}')
    expect(messages.asset.rules.change_vs_threshold).toContain('{threshold}')
    expect(messages.asset.rules.near_hint).toContain('{pct}')
  })
})

describe('IDR level texts (#40)', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s has the currency label, rate note and IDR range error', (_, messages) => {
    expect(messages.asset.rules.currency).toBeTruthy()
    expect(messages.asset.rules.idr_rate_note).toBeTruthy()
    expect(messages.asset.rules.error_level_idr).toContain('999')
    expect(messages.asset.rules.level).not.toContain('USD')
  })
})

describe('status report texts (#7)', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s has the report keys', (_, messages) => {
    for (const key of ['send_report', 'sending_report', 'asset_report_summary', 'asset_report_unavailable', 'asset_report_empty', 'asset_report_failed']) {
      expect(messages.run[key], key).toBeTruthy()
    }
    expect(messages.run.asset_report_summary).toContain('{symbols}')
    expect(messages.run.asset_report_summary).toContain('{rules}')
    expect(messages.run.asset_report_unavailable).toContain('{errors}')
  })
})

describe('rate-limit text fits both run buttons (#7)', () => {
  it.each([
    ['en', en, /check/i],
    ['id', id, /mengecek/i],
  ])('%s error_too_many does not say "check"', (_, messages, word) => {
    expect(messages.run.error_too_many).toContain('{seconds}')
    expect(messages.run.error_too_many).not.toMatch(word)
  })
})

describe('IDR waiting text (#61)', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s explains waiting IDR rules', (_, messages) => {
    expect(messages.run.idr_waiting).toContain('{idr_waiting}')
  })
})

describe('header navigation label (#55)', () => {
  it.each([
    ['en', en],
    ['id', id],
  ])('%s labels the menu button', (_, messages) => {
    expect(messages.common.toggle_navigation).toBeTruthy()
  })
})
