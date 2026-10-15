import { idID, dateIdID, enUS, dateEnUS } from 'naive-ui'

// naiveLocaleFor returns Naive UI's own strings (select/input placeholders,
// date pickers, empty states) for the app language, so the Indonesian UI no
// longer shows "Please Select".
export function naiveLocaleFor(lang) {
  return lang === 'id' ? { locale: idID, dateLocale: dateIdID } : { locale: enUS, dateLocale: dateEnUS }
}
