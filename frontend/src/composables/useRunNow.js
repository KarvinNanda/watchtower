import { ref } from 'vue'

import { pollRun, runErrorKey, failureReasonKey } from '@/utils/runPoller'

// useRunNow holds the "Check now" flow: queue a run, poll it, report the
// result. It lives outside RunNowButton.vue so it can be unit-tested.
//
// notify is { success(text), error(text) }. Call stop() when the page that
// started the run goes away: polling ends and nothing is reported.
export function useRunNow({ type, createRun, getRun, notify, t, pollOptions = {} }) {
  const running = ref(false)
  const controller = new AbortController()

  function reasonText(raw) {
    const key = failureReasonKey(raw)
    return key ? t(key) : raw || ''
  }

  async function start() {
    // A fast double-click must not queue a second run or reset the button
    // while the first run is still being polled.
    if (running.value) return
    running.value = true
    try {
      const res = await createRun(type)
      const run = await pollRun(getRun, res.data.run_id, { ...pollOptions, signal: controller.signal })
      if (!run) return
      if (run.status === 'done') {
        const summary = run.summary || {}
        let text = t(`run.${type}_summary`, summary)
        // Some prices could not be refreshed (daily quota or a failing
        // source) and the run used cached ones; say so (#20).
        if (summary.stale > 0) text += ' ' + t('run.stale_prices', { stale: summary.stale })
        notify.success(text)
      } else {
        notify.error(t('run.failed', { reason: reasonText(run.error) }))
      }
    } catch (err) {
      if (controller.signal.aborted) return
      const { key, params } = runErrorKey(err)
      notify.error(t(key, params))
    } finally {
      running.value = false
    }
  }

  function stop() {
    controller.abort()
  }

  return { running, start, stop }
}
