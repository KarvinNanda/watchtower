// runChangedRuleIds returns the ids of rules that alerted or fired during a
// manual run, by comparing server timestamps (browser clock skew does not
// matter).
//
// last_alerted_at and triggered_at are MySQL TIMESTAMP columns: whole
// seconds, rounded. run.started_at has nanoseconds. So the run start is
// floored to the second, or an alert sent in the same second could be
// stored before it. A periodic run alerting in the same window is also
// matched; that is a known limitation (Spec 4a, section 8).
export function runChangedRuleIds(rules, run) {
  const ids = new Set()
  const start = Date.parse(run?.started_at ?? run?.created_at ?? '')
  if (Number.isNaN(start)) return ids
  const since = Math.floor(start / 1000) * 1000

  for (const rule of rules) {
    const times = [rule.last_alerted_at, rule.triggered_at].filter(Boolean).map(Date.parse)
    if (times.some((t) => t >= since)) ids.add(rule.id)
  }
  return ids
}

// highlightAfterRun returns the rules to highlight once the post-run reload
// finished, but only while that run's result is still the one shown: a
// panel closed or replaced during the reload would leave a highlight with
// nothing to clear it (#55).
export function highlightAfterRun(shownResult, result, rules) {
  if (shownResult !== result) return new Set()
  return runChangedRuleIds(rules, result.run)
}
