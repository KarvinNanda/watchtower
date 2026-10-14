// Polls GET /api/runs/:id until the run finishes. The backend reports runs
// stuck for 15 minutes as failed; timeoutMs (16 min) is only a safety net
// in case the API itself stops answering.
const FINAL_STATUSES = new Set(['done', 'failed'])

const defaultSleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

export async function pollRun(
  getRun,
  runId,
  { intervalMs = 2000, timeoutMs = 16 * 60 * 1000, sleep = defaultSleep, now = Date.now } = {},
) {
  const deadline = now() + timeoutMs
  for (;;) {
    const res = await getRun(runId)
    const run = res.data
    if (FINAL_STATUSES.has(run.status)) return run
    if (now() >= deadline) return { ...run, status: 'failed', error: 'timed out' }
    await sleep(intervalMs)
  }
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
