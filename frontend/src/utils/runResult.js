import { failureReasonKey } from './runPoller'

// runResultText turns a Check now result into text. The result holds data,
// not text, so the panel follows a language switch.
export function runResultText(result, t) {
  if (!result) return ''
  if (result.status === 'done') {
    const summary = result.summary ?? {}
    let text = t(`run.${result.type}_summary`, summary)
    // Some prices could not be refreshed and the run used cached ones (#20).
    if (summary.stale > 0) text += ' ' + t('run.stale_prices', { stale: summary.stale })
    return text
  }
  if (result.status === 'failed') {
    const key = failureReasonKey(result.reason)
    return t('run.failed', { reason: key ? t(key) : result.reason || '' })
  }
  return t(result.key, result.params)
}
