import { describe, it, expect, vi } from 'vitest'
import { useRunNow } from './useRunNow'

const noSleep = async () => {}
const doneRun = { status: 'done', summary: { sent: 1 }, started_at: '2026-10-15T03:00:00Z' }

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

function setup({ createRun, getRun, sleep = noSleep } = {}) {
  return useRunNow({
    type: 'asset',
    createRun: createRun ?? vi.fn(async () => ({ data: { run_id: 'r1' } })),
    getRun: getRun ?? vi.fn(async () => ({ data: doneRun })),
    pollOptions: { sleep },
  })
}

describe('useRunNow', () => {
  it('ignores a second click while a run is in progress', async () => {
    const created = deferred()
    const createRun = vi.fn(() => created.promise)
    const { start, running } = setup({ createRun })

    const first = start()
    const second = start() // fast double-click
    expect(running.value).toBe(true)
    await second
    expect(running.value).toBe(true) // the second click must not reset the button

    created.resolve({ data: { run_id: 'r1' } })
    await first
    expect(createRun).toHaveBeenCalledTimes(1)
    expect(running.value).toBe(false)
  })

  it('a finished run gives a done result with the run record', async () => {
    const { start, result } = setup()
    await start()
    expect(result.value).toEqual({ status: 'done', type: 'asset', summary: { sent: 1 }, run: doneRun })
  })

  it('a missing summary becomes {}', async () => {
    const { start, result } = setup({ getRun: async () => ({ data: { status: 'done' } }) })
    await start()
    expect(result.value.summary).toEqual({})
  })

  it('a failed run keeps the raw reason', async () => {
    const { start, result } = setup({ getRun: async () => ({ data: { status: 'failed', error: 'timed out' } }) })
    await start()
    expect(result.value).toEqual({ status: 'failed', type: 'asset', reason: 'timed out' })
  })

  it('a refused start gives an error result', async () => {
    const createRun = async () => {
      throw { response: { status: 429, data: { retry_after: 120 } } }
    }
    const { start, result } = setup({ createRun })
    await start()
    expect(result.value).toEqual({ status: 'error', type: 'asset', key: 'run.error_too_many', params: { seconds: 120 } })
  })

  it('starting a new run clears the previous result', async () => {
    const created = deferred()
    let calls = 0
    const createRun = vi.fn(() => (++calls === 1 ? Promise.resolve({ data: { run_id: 'r1' } }) : created.promise))
    const { start, result } = setup({ createRun })
    await start()
    expect(result.value).not.toBeNull()

    const second = start()
    expect(result.value).toBeNull()
    created.resolve({ data: { run_id: 'r2' } })
    await second
  })

  it('sets no result when the page is left during polling', async () => {
    let runNow
    const sleep = async () => runNow.stop()
    runNow = setup({ getRun: async () => ({ data: { status: 'running' } }), sleep })
    await runNow.start()
    expect(runNow.result.value).toBeNull()
    expect(runNow.running.value).toBe(false)
  })
})
