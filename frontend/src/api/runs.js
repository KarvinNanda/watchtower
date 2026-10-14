import api from './axios'

export function createRun(type) {
  return api.post('/runs', { type })
}

export function getRun(id) {
  return api.get(`/runs/${id}`)
}
