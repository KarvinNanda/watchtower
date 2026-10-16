import { failureReasonKey } from './runPoller'

// runResultText turns a Check now or status report result into text. The
// result holds data, not text, so the panel follows a language switch.
export function runResultText(result, t) {
  if (!result) return ''
  if (result.status === 'done') {
    const summary = result.summary ?? {}
    if (result.type === 'asset_report') {
      // No symbols with errors means rules exist but could not be loaded.
      if (!summary.symbols) return t(summary.errors > 0 ? 'run.asset_report_failed' : 'run.asset_report_empty')
      if (!summary.sent) return t('run.asset_report_failed')
    }
    let text = t(`run.${result.type}_summary`, summary)
    if (result.type === 'asset_report' && summary.errors > 0) {
      text += ' ' + t('run.asset_report_unavailable', { errors: summary.errors })
    }
    // Some prices could not be refreshed and the run used cached ones (#20).
    if (summary.stale > 0) text += ' ' + t('run.stale_prices', { stale: summary.stale })
    // IDR price levels skipped because no real USD/IDR rate was known (#61).
    if (summary.idr_waiting > 0) text += ' ' + t('run.idr_waiting', { idr_waiting: summary.idr_waiting })
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
  if (result.type !== 'asset_report') return true
  return summary.symbols > 0 ? summary.sent > 0 : !(summary.errors > 0)
}

export function runTitleKey(type) {
  return type === 'asset_report' ? 'run.send_report' : 'run.check_now'
}

export function runButtonKeys(type) {
  return type === 'asset_report'
    ? { idle: 'run.send_report', running: 'run.sending_report' }
    : { idle: 'run.check_now', running: 'run.running' }
}
