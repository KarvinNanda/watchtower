import { describe, it, expect } from 'vitest'
import { runChangedRuleIds, highlightAfterRun } from './runHighlight'

const run = { created_at: '2026-10-15T03:00:00.100Z', started_at: '2026-10-15T03:00:05.700Z' }

describe('runChangedRuleIds', () => {
  it('includes rules alerted or triggered at or after the run start', () => {
    const rules = [
      { id: 1, last_alerted_at: '2026-10-15T03:00:09Z', triggered_at: null },
      { id: 2, last_alerted_at: null, triggered_at: '2026-10-15T03:00:10Z' },
      { id: 3, last_alerted_at: '2026-10-15T02:00:00Z', triggered_at: null },
      { id: 4, last_alerted_at: null, triggered_at: null },
    ]
    expect([...runChangedRuleIds(rules, run)].sort()).toEqual([1, 2])
  })

  it('MySQL TIMESTAMP is rounded to the second: same second as start still counts', () => {
    // started 03:00:05.700, alert stored as 03:00:05 (or 03:00:06)
    const rules = [{ id: 1, last_alerted_at: '2026-10-15T03:00:05Z', triggered_at: null }]
    expect(runChangedRuleIds(rules, run).has(1)).toBe(true)
  })

  it('falls back to created_at when started_at is missing', () => {
    const rules = [{ id: 1, last_alerted_at: '2026-10-15T03:00:01Z', triggered_at: null }]
    expect(runChangedRuleIds(rules, { created_at: run.created_at, started_at: null }).has(1)).toBe(true)
  })

  it('parses Go timestamps with offsets and nanoseconds', () => {
    const rules = [{ id: 1, last_alerted_at: '2026-10-15T10:00:06+07:00', triggered_at: null }]
    expect(runChangedRuleIds(rules, { started_at: '2026-10-15T10:00:05.123456789+07:00' }).has(1)).toBe(true)
  })

  it('no usable run time → empty set', () => {
    expect(runChangedRuleIds([{ id: 1, last_alerted_at: '2026-10-15T03:00:09Z' }], {}).size).toBe(0)
    expect(runChangedRuleIds([{ id: 1, last_alerted_at: '2026-10-15T03:00:09Z' }], null).size).toBe(0)
  })
})

// #55: the panel can be closed (or replaced by a newer run) while the rules
// reload; a highlight set after that has no panel to clear it.
describe('highlightAfterRun', () => {
  const result = { status: 'done', type: 'asset', run }
  const rules = [{ id: 1, last_alerted_at: '2026-10-15T03:00:09Z', triggered_at: null }]

  it('highlights while the same result is still shown', () => {
    expect([...highlightAfterRun(result, result, rules)]).toEqual([1])
  })

  it('highlights nothing once the panel was closed or replaced', () => {
    expect(highlightAfterRun(null, result, rules).size).toBe(0)
    expect(highlightAfterRun({ ...result }, result, rules).size).toBe(0)
  })
})
