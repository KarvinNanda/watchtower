import { defineStore } from 'pinia'
import { ref } from 'vue'

import * as marketApi from '@/api/market'

const AUTO_REFRESH_INTERVAL_MS = 5 * 60 * 1000

export const useMarketStore = defineStore('market', () => {
  const snapshot = ref([])
  const lastUpdated = ref(null)
  const loading = ref(false)
  const autoRefreshing = ref(false)
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
    autoRefreshing.value = true
  }

  function stopAutoRefresh() {
    if (refreshTimer) {
      clearInterval(refreshTimer)
      refreshTimer = null
    }
    autoRefreshing.value = false
  }

  // reset forgets the current user's symbols (logout, expired session).
  function reset() {
    stopAutoRefresh()
    snapshot.value = []
    lastUpdated.value = null
  }

  return { snapshot, lastUpdated, loading, autoRefreshing, fetchSnapshot, startAutoRefresh, stopAutoRefresh, reset }
})
