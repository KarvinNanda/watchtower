import { createI18n } from 'vue-i18n'

import id from '@/locales/id.json'
import en from '@/locales/en.json'

const STORAGE_KEY = 'watchtower-locale'
const savedLocale = localStorage.getItem(STORAGE_KEY)
const defaultLocale = savedLocale === 'en' || savedLocale === 'id' ? savedLocale : 'id'

const i18n = createI18n({
  legacy: false,
  locale: defaultLocale,
  fallbackLocale: 'en',
  messages: { id, en },
})

export function setLocale(locale) {
  i18n.global.locale.value = locale
  localStorage.setItem(STORAGE_KEY, locale)
}

export default i18n
