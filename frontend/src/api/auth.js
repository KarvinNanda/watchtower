import api from './axios'

export function register(email, password) {
  return api.post('/auth/register', { email, password })
}

export function login(email, password) {
  return api.post('/auth/login', { email, password })
}

export function me() {
  return api.get('/auth/me')
}

export function getProfile() {
  return api.get('/user/profile')
}

export function updateProfile(payload) {
  return api.put('/user/profile', payload)
}
