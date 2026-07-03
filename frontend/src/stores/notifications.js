import { defineStore } from 'pinia'
import { ref } from 'vue'

import * as notificationsApi from '@/api/notifications'

export const useNotificationsStore = defineStore('notifications', () => {
  const logs = ref([])
  const total = ref(0)
  const loading = ref(false)

  async function fetchNotifications(params = {}) {
    loading.value = true
    try {
      const res = await notificationsApi.getNotifications(params)
      logs.value = res.data
      total.value = res.meta?.total ?? 0
    } finally {
      loading.value = false
    }
  }

  return { logs, total, loading, fetchNotifications }
})
