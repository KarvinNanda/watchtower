import { describe, it, expect } from 'vitest'
import { runResultText } from './runResult'

const t = (key, params) => (params ? `${key}|${JSON.stringify(params)}` : key)

describe('runResultText', () => {
  it('summary of a finished run', () => {
    expect(runResultText({ status: 'done', type: 'asset', summary: { sent: 1 } }, t)).toBe('run.asset_summary|{"sent":1}')
  })

  it('adds the stale sentence when some prices came from cache', () => {
    expect(runResultText({ status: 'done', type: 'asset', summary: { sent: 1, stale: 2 } }, t)).toBe(
      'run.asset_summary|{"sent":1,"stale":2} run.stale_prices|{"stale":2}',
    )
  })

  it('omits the stale sentence when every price was fresh', () => {
    expect(runResultText({ status: 'done', type: 'asset', summary: { sent: 1, stale: 0 } }, t)).toBe(
      'run.asset_summary|{"sent":1,"stale":0}',
    )
  })

  it('translates a known failure reason', () => {
    expect(runResultText({ status: 'failed', type: 'asset', reason: 'timed out' }, t)).toBe(
      'run.failed|{"reason":"run.reason_timed_out"}',
    )
  })

  it('shows an unknown failure reason as sent', () => {
    expect(runResultText({ status: 'failed', type: 'asset', reason: 'disk full' }, t)).toBe('run.failed|{"reason":"disk full"}')
  })

  it('request errors use their key and params', () => {
    expect(runResultText({ status: 'error', type: 'asset', key: 'run.error_too_many', params: { seconds: 120 } }, t)).toBe(
      'run.error_too_many|{"seconds":120}',
    )
  })

  it('follows the current language: same result, different t', () => {
    const result = { status: 'done', type: 'sentinel', summary: { new_items: 1 } }
    const tEn = (key) => `en:${key}`
    const tId = (key) => `id:${key}`
    expect(runResultText(result, tEn)).toBe('en:run.sentinel_summary')
    expect(runResultText(result, tId)).toBe('id:run.sentinel_summary')
  })

  it('null result is empty text', () => {
    expect(runResultText(null, t)).toBe('')
  })
})
