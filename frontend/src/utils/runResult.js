import { failureReasonKey } from './runPoller'

// runResultText turns a Check now or status report result into text. The
// result holds data, not text, so the panel follows a language switch.
export function runResultText(result, t) {
  if (!result) return ''
  if (result.status === 'done') {
    const summary = result.summary ?? {}
    if (result.type === 'asset_report') {
      if (!summary.symbols) return t('run.asset_report_empty')
      if (!summary.sent) return t('run.asset_report_failed')
    }
    let text = t(`run.${result.type}_summary`, summary)
    if (result.type === 'asset_report' && summary.errors > 0) {
      text += ' ' + t('run.asset_report_unavailable', { errors: summary.errors })
    }
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

// runOk: the run finished and did what the user asked. A report with
// symbols that could not be sent is a failure even though the run is done.
export function runOk(result) {
  if (result?.status !== 'done') return false
  const summary = result.summary ?? {}
  return !(result.type === 'asset_report' && summary.symbols > 0 && !summary.sent)
}

export function runTitleKey(type) {
  return type === 'asset_report' ? 'run.send_report' : 'run.check_now'
}

export function runButtonKeys(type) {
  return type === 'asset_report'
    ? { idle: 'run.send_report', running: 'run.sending_report' }
    : { idle: 'run.check_now', running: 'run.running' }
}
