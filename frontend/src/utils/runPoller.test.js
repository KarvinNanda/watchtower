import { describe, it, expect, vi } from 'vitest'
import { pollRun, runErrorKey } from './runPoller'

const noSleep = async () => {}

describe('pollRun', () => {
  it('polls until the run is done', async () => {
    const statuses = ['queued', 'running', 'done']
    const getRun = vi.fn(async () => ({ data: { run_id: 'x', status: statuses.shift(), summary: { sent: 1 } } }))
    const sleep = vi.fn(noSleep)

    const run = await pollRun(getRun, 'x', { sleep })

    expect(run.status).toBe('done')
    expect(run.summary).toEqual({ sent: 1 })
    expect(getRun).toHaveBeenCalledTimes(3)
    expect(sleep).toHaveBeenCalledTimes(2)
  })

  it('returns a failed run as-is', async () => {
    const getRun = async () => ({ data: { status: 'failed', error: 'timed out' } })
    const run = await pollRun(getRun, 'x', { sleep: noSleep })
    expect(run).toEqual({ status: 'failed', error: 'timed out' })
  })

  it('gives up after the timeout', async () => {
    let clock = 0
    const getRun = async () => ({ data: { status: 'running' } })
    const run = await pollRun(getRun, 'x', {
      intervalMs: 2000,
      timeoutMs: 5000,
      now: () => clock,
      sleep: async (ms) => {
        clock += ms
      },
    })
    expect(run.status).toBe('failed')
    expect(run.error).toBe('timed out')
  })
})

describe('runErrorKey', () => {
  const httpError = (status, data = {}) => ({ response: { status, data } })

  it('maps scheduler offline', () => {
    expect(runErrorKey(httpError(503)).key).toBe('run.error_scheduler_offline')
  })
  it('maps telegram not linked', () => {
    expect(runErrorKey(httpError(409)).key).toBe('run.error_telegram_not_linked')
  })
  it('maps rate limit with seconds', () => {
    expect(runErrorKey(httpError(429, { retry_after: 120 }))).toEqual({
      key: 'run.error_too_many',
      params: { seconds: 120 },
    })
  })
  it('maps network errors', () => {
    expect(runErrorKey(new Error('Network Error')).key).toBe('common.network_error')
  })
  it('maps other HTTP errors to the generic message', () => {
    expect(runErrorKey(httpError(500)).key).toBe('common.error_generic')
  })
})
