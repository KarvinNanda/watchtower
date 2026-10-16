import { describe, it, expect } from 'vitest'

// Every .vue file under src, as raw text.
const files = import.meta.glob('../**/*.vue', { query: '?raw', import: 'default', eager: true })

const HEX = /(^|[^\w&])#[0-9a-fA-F]{3,8}\b/m
const RGBA = /rgba?\(/
const EMOJI = /[\p{Extended_Pictographic}☀-➿]/u

describe('.vue files', () => {
  it('found some files', () => {
    expect(Object.keys(files).length).toBeGreaterThan(10)
  })

  it.each(Object.entries(files))('%s has no color literal (use var(--wt-*))', (_, src) => {
    expect(src).not.toMatch(HEX)
    expect(src).not.toMatch(RGBA)
  })

  it.each(Object.entries(files))('%s uses no emoji as an icon (use @lucide/vue)', (_, src) => {
    expect(src).not.toMatch(EMOJI)
  })
})

// #55: emoji also crept into UI text. Telegram messages are built in Go;
// the web UI uses @lucide/vue icons, so locale strings carry no emoji.
const locales = import.meta.glob('../locales/*.json', { query: '?raw', import: 'default', eager: true })

describe('locale files', () => {
  it('found both languages', () => {
    expect(Object.keys(locales).length).toBe(2)
  })

  it.each(Object.entries(locales))('%s has no emoji', (_, src) => {
    expect(src).not.toMatch(EMOJI)
  })
})
