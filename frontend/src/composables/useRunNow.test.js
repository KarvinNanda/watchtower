import { describe, it, expect, vi } from 'vitest'
import { useRunNow } from './useRunNow'

const t = (key, params) => (params ? `${key}|${JSON.stringify(params)}` : key)
const noSleep = async () => {}

function deferred() {
  let resolve
  const promise = new Promise((r) => {
    resolve = r
  })
  return { promise, resolve }
}

function setup({ createRun, getRun, sleep = noSleep } = {}) {
  const notify = { success: vi.fn(), error: vi.fn() }
  const runNow = useRunNow({
    type: 'asset',
    createRun: createRun ?? vi.fn(async () => ({ data: { run_id: 'r1' } })),
    getRun: getRun ?? vi.fn(async () => ({ data: { status: 'done', summary: { sent: 1 } } })),
    notify,
    t,
    pollOptions: { sleep },
  })
  return { ...runNow, notify }
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

  it('reports the summary of a finished run', async () => {
    const { start, notify } = setup()
    await start()
    expect(notify.success).toHaveBeenCalledWith('run.asset_summary|{"sent":1}')
  })

  it('translates a known failure reason', async () => {
    const { start, notify } = setup({ getRun: async () => ({ data: { status: 'failed', error: 'timed out' } }) })
    await start()
    expect(notify.error).toHaveBeenCalledWith('run.failed|{"reason":"run.reason_timed_out"}')
  })

  it('shows an unknown failure reason as sent', async () => {
    const { start, notify } = setup({ getRun: async () => ({ data: { status: 'failed', error: 'disk full' } }) })
    await start()
    expect(notify.error).toHaveBeenCalledWith('run.failed|{"reason":"disk full"}')
  })

  it('stays silent when the page is left during polling', async () => {
    let runNow
    const sleep = async () => runNow.stop()
    runNow = setup({ getRun: async () => ({ data: { status: 'running' } }), sleep })
    await runNow.start()
    expect(runNow.notify.success).not.toHaveBeenCalled()
    expect(runNow.notify.error).not.toHaveBeenCalled()
    expect(runNow.running.value).toBe(false)
  })

  it('maps a refused start to its message', async () => {
    const createRun = async () => {
      throw { response: { status: 429, data: { retry_after: 120 } } }
    }
    const { start, notify } = setup({ createRun })
    await start()
    expect(notify.error).toHaveBeenCalledWith('run.error_too_many|{"seconds":120}')
  })
})
