import { ref } from 'vue'

import { pollRun, runErrorKey } from '@/utils/runPoller'

// useRunNow holds the "Check now" flow: queue a run, poll it, expose the
// result. It lives outside RunNowButton.vue so it can be unit-tested.
//
// result is null while idle or running, then one of:
//   { status: 'done', type, summary, run }   run = the run record (started_at, ...)
//   { status: 'failed', type, reason }       reason as sent by the backend
//   { status: 'error', type, key, params }   POST /api/runs refused (runErrorKey)
// Call stop() when the page that started the run goes away: polling ends and
// no result is set.
export function useRunNow({ type, createRun, getRun, pollOptions = {} }) {
  const running = ref(false)
  const result = ref(null)
  const controller = new AbortController()

  async function start() {
    // A fast double-click must not queue a second run or reset the button
    // while the first run is still being polled.
    if (running.value) return
    running.value = true
    result.value = null
    try {
      const res = await createRun(type)
      const run = await pollRun(getRun, res.data.run_id, { ...pollOptions, signal: controller.signal })
      if (!run) return
      result.value =
        run.status === 'done'
          ? { status: 'done', type, summary: run.summary || {}, run }
          : { status: 'failed', type, reason: run.error ?? '' }
    } catch (err) {
      if (controller.signal.aborted) return
      result.value = { status: 'error', type, ...runErrorKey(err) }
    } finally {
      running.value = false
    }
  }

  function stop() {
    controller.abort()
  }

  return { running, result, start, stop }
}
