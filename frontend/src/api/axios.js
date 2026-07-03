import axios from 'axios'
import router from '@/router'
import { useAuthStore } from '@/stores/auth'

// The backend does not version its routes (no "/api/v1") — every endpoint
// lives directly under /api, e.g. /api/auth/login.
const baseURL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api'

// Strips obviously malicious content (script tags, javascript: URIs, inline
// event handler attributes) out of a single string value. This is
// defense-in-depth on top of Vue's default template auto-escaping — it
// guards against the value ever reaching a v-html/unescaped sink anywhere
// in the app, now or in a future change, not because the API response is
// otherwise untrusted.
function sanitizeString(str) {
  if (typeof str !== 'string') return str
  return str
    .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
    .replace(/javascript:/gi, '')
    .replace(/on\w+\s*=/gi, '')
}

// Recursively applies sanitizeString to every string value in obj, walking
// arrays and plain objects.
function sanitizeDeep(obj) {
  if (typeof obj === 'string') return sanitizeString(obj)
  if (Array.isArray(obj)) return obj.map(sanitizeDeep)
  if (obj && typeof obj === 'object') {
    return Object.fromEntries(Object.entries(obj).map(([k, v]) => [k, sanitizeDeep(v)]))
  }
  return obj
}

const api = axios.create({
  baseURL,
  timeout: 30000,
  headers: {
    'X-Requested-With': 'XMLHttpRequest',
  },
})

api.interceptors.request.use((config) => {
  const authStore = useAuthStore()
  if (authStore.token) {
    config.headers.Authorization = `Bearer ${authStore.token}`
  }
  return config
})

// The backend's response envelope is not uniform across endpoints:
//   - /auth/register, /auth/login, /auth/me return their payload at the
//     top level (e.g. {token, user}), with no "data" wrapper.
//   - /user/profile, /subscriptions/*, /market/*, /dashboard return
//     {success, data, message}.
//   - /notifications additionally returns {success, data, meta} where
//     meta carries pagination info that a blind ".data.data" extraction
//     would silently drop.
// Because of that, this interceptor deliberately does NOT unwrap a
// generic "data" field — each api/*.js module extracts exactly what its
// own endpoint returns. It only normalizes to the parsed JSON body.
api.interceptors.response.use(
  (response) => sanitizeDeep(response.data),
  (error) => {
    if (error.response?.status === 401) {
      const authStore = useAuthStore()
      const wasLoggedIn = Boolean(authStore.token)
      authStore.clearSession()
      if (wasLoggedIn && router.currentRoute.value.name !== 'login') {
        router.push({ name: 'login', query: { sessionExpired: '1' } })
      }
    }
    return Promise.reject(error)
  },
)

export default api
