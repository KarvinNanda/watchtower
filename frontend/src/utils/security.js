// Sanitize input sebelum dikirim ke API
export function sanitizeInput(value) {
  if (typeof value !== 'string') return value
  return value
    .trim()
    .replace(/[<>]/g, '') // strip angle brackets
    .substring(0, 1000) // max 1000 chars
}

// Validate email format
export function isValidEmail(email) {
  const re = /^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$/
  return re.test(email)
}

// Validate password strength. Mirrors the backend's own Register()
// requirements exactly (internal/auth/auth.go) — including the lowercase
// check, which the backend enforces but wasn't in the original literal
// spec for this function. Without it, a password like "VALID123!" would
// pass this client-side check yet still be rejected by the API as
// password_too_weak, a confusing mismatch between form validation and the
// actual server behavior.
export function validatePassword(password) {
  const errors = []
  if (password.length < 8) errors.push('Minimal 8 karakter')
  if (!/[A-Z]/.test(password)) errors.push('Minimal 1 huruf besar')
  if (!/[a-z]/.test(password)) errors.push('Minimal 1 huruf kecil')
  if (!/[0-9]/.test(password)) errors.push('Minimal 1 angka')
  if (!/[!@#$%^&*]/.test(password)) errors.push('Minimal 1 karakter spesial')
  return errors
}

// Safe redirect — prevent open redirect
export function safeRedirect(router, path) {
  const allowedPaths = [
    '/',
    '/dashboard',
    '/assets',
    '/keywords',
    '/notifications',
    '/profile',
    '/telegram-setup',
    '/login',
    '/register',
  ]
  if (allowedPaths.includes(path)) {
    router.push(path)
  } else {
    router.push('/')
  }
}
