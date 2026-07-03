import api from './axios'

export function getAssetSubscriptions() {
  return api.get('/subscriptions/assets')
}

export function createAssetSubscription(payload) {
  return api.post('/subscriptions/assets', payload)
}

export function updateAssetSubscription(id, payload) {
  return api.put(`/subscriptions/assets/${id}`, payload)
}

export function deleteAssetSubscription(id) {
  return api.delete(`/subscriptions/assets/${id}`)
}

export function getKeywordSubscriptions() {
  return api.get('/subscriptions/keywords')
}

export function createKeywordSubscription(payload) {
  return api.post('/subscriptions/keywords', payload)
}

export function deleteKeywordSubscription(id) {
  return api.delete(`/subscriptions/keywords/${id}`)
}
