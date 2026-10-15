import api from './axios'

export function getSnapshot() {
  return api.get('/market/snapshot')
}

export function getDashboard() {
  return api.get('/dashboard')
}
