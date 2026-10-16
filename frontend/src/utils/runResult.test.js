import { describe, it, expect } from 'vitest'
import { runResultText, runOk, runTitleKey, runButtonKeys } from './runResult'

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

describe('asset_report results', () => {
  const done = (summary) => ({ status: 'done', type: 'asset_report', summary })

  it('summary of a sent report', () => {
    expect(runResultText(done({ symbols: 2, rules: 4, sent: 1, errors: 0, stale: 0 }), t)).toBe(
      'run.asset_report_summary|{"symbols":2,"rules":4,"sent":1,"errors":0,"stale":0}',
    )
  })

  it('adds unavailable and stale sentences', () => {
    expect(runResultText(done({ symbols: 2, rules: 4, sent: 1, errors: 1, stale: 1 }), t)).toBe(
      'run.asset_report_summary|{"symbols":2,"rules":4,"sent":1,"errors":1,"stale":1} ' +
        'run.asset_report_unavailable|{"errors":1} run.stale_prices|{"stale":1}',
    )
  })

  it('no active rules', () => {
    expect(runResultText(done({ symbols: 0, rules: 0, sent: 0, errors: 0, stale: 0 }), t)).toBe('run.asset_report_empty')
    expect(runOk(done({ symbols: 0, sent: 0 }))).toBe(true)
  })

  it('no symbols because rules could not be loaded is a failure, not "no active rules"', () => {
    expect(runResultText(done({ symbols: 0, rules: 0, sent: 0, errors: 1, stale: 0 }), t)).toBe('run.asset_report_failed')
    expect(runOk(done({ symbols: 0, sent: 0, errors: 1 }))).toBe(false)
  })

  it('a report that could not be sent is not ok', () => {
    expect(runResultText(done({ symbols: 1, rules: 1, sent: 0, errors: 0, stale: 0 }), t)).toBe('run.asset_report_failed')
    expect(runOk(done({ symbols: 1, sent: 0 }))).toBe(false)
  })

  it('titles and button labels by type', () => {
    expect(runTitleKey('asset_report')).toBe('run.send_report')
    expect(runTitleKey('asset')).toBe('run.check_now')
    expect(runButtonKeys('asset_report')).toEqual({ idle: 'run.send_report', running: 'run.sending_report' })
    expect(runButtonKeys('sentinel')).toEqual({ idle: 'run.check_now', running: 'run.running' })
  })

  it('Check now results stay ok as before', () => {
    expect(runOk({ status: 'done', type: 'asset', summary: { sent: 0 } })).toBe(true)
    expect(runOk({ status: 'failed', type: 'asset', reason: 'x' })).toBe(false)
  })
})

describe('IDR rules waiting for a rate (#61)', () => {
  it('adds the waiting sentence to a Check now summary', () => {
    expect(runResultText({ status: 'done', type: 'asset', summary: { sent: 0, stale: 0, idr_waiting: 2 } }, t)).toBe(
      'run.asset_summary|{"sent":0,"stale":0,"idr_waiting":2} run.idr_waiting|{"idr_waiting":2}',
    )
  })

  it('says nothing when no IDR rule waits', () => {
    expect(runResultText({ status: 'done', type: 'asset', summary: { sent: 0, idr_waiting: 0 } }, t)).toBe(
      'run.asset_summary|{"sent":0,"idr_waiting":0}',
    )
  })
})
