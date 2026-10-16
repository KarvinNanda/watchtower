import { describe, it, expect } from 'vitest'
import { ASSET_OPTIONS } from './assets'

describe('ASSET_OPTIONS', () => {
  it('offers SPY as a stock', () => {
    expect(ASSET_OPTIONS.stock.map((o) => o.value)).toContain('SPY')
  })

  it('lists every symbol once', () => {
    const values = Object.values(ASSET_OPTIONS).flat().map((o) => o.value)
    expect(new Set(values).size).toBe(values.length)
  })
})
