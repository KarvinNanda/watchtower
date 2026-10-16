import { validatePassword } from './security'

// validatePasswordChange checks the change-password form before it is sent.
// It returns { field, key } for the first problem (an i18n key shown next to
// that field) or null. The server applies the same rules again.
export function validatePasswordChange({ current, next, confirm }) {
  if (!current) return { field: 'current', key: 'common.required_field' }
  if (validatePassword(next ?? '').length > 0) return { field: 'next', key: 'auth.password_hint' }
  if (next === current) return { field: 'next', key: 'profile.password_same' }
  if (confirm !== next) return { field: 'confirm', key: 'auth.password_mismatch' }
  return null
}

// passwordChangeErrorKey turns a failed PUT /auth/password into { field, key }.
export function passwordChangeErrorKey(error) {
  const status = error?.response?.status
  const code = error?.response?.data?.error
  if (status === 400 && code === 'wrong_password') return { field: 'current', key: 'profile.password_wrong' }
  if (status === 400 && code === 'validation_error') return { field: 'next', key: 'auth.password_hint' }
  if (status === 429) return { field: null, key: 'profile.password_too_many' }
  return { field: null, key: 'profile.password_failed' }
}
