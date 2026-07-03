import api from './axios'

export function getSnapshot() {
  return api.get('/market/snapshot')
}

export function getQuote(symbol) {
  return api.get(`/market/${symbol}`)
}

export function getDashboard() {
  return api.get('/dashboard')
}
