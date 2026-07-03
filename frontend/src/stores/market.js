import { defineStore } from 'pinia'
import { ref } from 'vue'

import * as marketApi from '@/api/market'

const AUTO_REFRESH_INTERVAL_MS = 5 * 60 * 1000

export const useMarketStore = defineStore('market', () => {
  const snapshot = ref([])
  const lastUpdated = ref(null)
  const loading = ref(false)
  let refreshTimer = null

  async function fetchSnapshot() {
    loading.value = true
    try {
      const res = await marketApi.getSnapshot()
      snapshot.value = res.data
      lastUpdated.value = new Date()
    } finally {
      loading.value = false
    }
  }

  function startAutoRefresh() {
    if (refreshTimer) return
    refreshTimer = setInterval(fetchSnapshot, AUTO_REFRESH_INTERVAL_MS)
  }

  function stopAutoRefresh() {
    if (refreshTimer) {
      clearInterval(refreshTimer)
      refreshTimer = null
    }
  }

  return { snapshot, lastUpdated, loading, fetchSnapshot, startAutoRefresh, stopAutoRefresh }
})
