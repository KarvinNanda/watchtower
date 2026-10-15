const UNITS = [
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
]

// formatAge shows how long ago iso was, in the UI language ("4 minutes ago",
// "4 menit yang lalu"). A time in the future (server clock ahead) reads as
// "this minute".
export function formatAge(iso, locale, now = Date.now()) {
  const t = Date.parse(iso ?? '')
  if (Number.isNaN(t)) return '—'
  const seconds = Math.max(0, Math.round((now - t) / 1000))
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  for (const [unit, size] of UNITS) {
    if (seconds >= size) return rtf.format(-Math.floor(seconds / size), unit)
  }
  return rtf.format(0, 'minute')
}

// isOlderThanHours reports whether iso is more than hours old. Unknown input
// is not treated as stale.
export function isOlderThanHours(iso, hours, now = Date.now()) {
  const t = Date.parse(iso ?? '')
  if (Number.isNaN(t) || hours == null) return false
  return now - t > hours * 3600 * 1000
}
