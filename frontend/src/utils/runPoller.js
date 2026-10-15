// Polls GET /api/runs/:id until the run finishes. The backend reports runs
// stuck for 15 minutes as failed; timeoutMs (16 min) is only a safety net
// in case the API itself stops answering.
const FINAL_STATUSES = new Set(['done', 'failed'])

// A single failed poll (Wi-Fi blip, API restart) must not report the run as
// failed while it keeps running, so only this many failures in a row give up.
const MAX_CONSECUTIVE_POLL_ERRORS = 3

const defaultSleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// Returns the finished run, or null when signal is aborted (the page that
// started the run was left), so the caller shows nothing.
export async function pollRun(
  getRun,
  runId,
  { intervalMs = 2000, timeoutMs = 16 * 60 * 1000, sleep = defaultSleep, now = Date.now, signal } = {},
) {
  const deadline = now() + timeoutMs
  let failures = 0
  for (;;) {
    if (signal?.aborted) return null
    let run
    try {
      run = (await getRun(runId)).data
      failures = 0
    } catch (err) {
      failures += 1
      if (failures >= MAX_CONSECUTIVE_POLL_ERRORS) throw err
    }
    if (run && FINAL_STATUSES.has(run.status)) return run
    if (now() >= deadline) return { ...run, status: 'failed', error: 'timed out' }
    await sleep(intervalMs)
  }
}

// The failure reasons the backend sends (runs.Store / runs.Consumer) and the
// poller's own timeout, mapped to i18n keys. Unknown reasons return null and
// are shown as sent.
const FAILURE_REASON_KEYS = {
  'timed out': 'run.reason_timed_out',
  'run failed; check the scheduler logs': 'run.reason_failed',
}

export function failureReasonKey(reason) {
  return FAILURE_REASON_KEYS[reason] ?? null
}

// Maps an axios error from POST /api/runs to an i18n key.
export function runErrorKey(error) {
  const status = error?.response?.status
  switch (status) {
    case 503:
      return { key: 'run.error_scheduler_offline', params: {} }
    case 409:
      return { key: 'run.error_telegram_not_linked', params: {} }
    case 429:
      return { key: 'run.error_too_many', params: { seconds: error.response.data?.retry_after ?? 300 } }
    default:
      return { key: error?.response ? 'common.error_generic' : 'common.network_error', params: {} }
  }
}
