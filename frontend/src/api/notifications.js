import api from './axios'

export function getNotifications({ limit = 20, offset = 0, type = '' } = {}) {
  return api.get('/notifications', { params: { limit, offset, type: type || undefined } })
}
