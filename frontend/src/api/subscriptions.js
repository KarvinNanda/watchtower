import api from './axios'

export function getAssetRules() {
  return api.get('/rules/assets')
}

export function createAssetRule(payload) {
  return api.post('/rules/assets', payload)
}

export function updateAssetRule(id, payload) {
  return api.put(`/rules/assets/${id}`, payload)
}

export function deleteAssetRule(id) {
  return api.delete(`/rules/assets/${id}`)
}

export function rearmAssetRule(id) {
  return api.post(`/rules/assets/${id}/rearm`)
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
