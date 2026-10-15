import { defineStore } from 'pinia'
import { ref } from 'vue'

import * as subscriptionsApi from '@/api/subscriptions'

export const useSubscriptionsStore = defineStore('subscriptions', () => {
  const assets = ref([])
  // ASSET_SCHEDULER_INTERVAL_HOURS, sent with the rules list.
  const assetIntervalHours = ref(null)
  const keywords = ref([])
  const loading = ref(false)

  async function fetchAssets() {
    loading.value = true
    try {
      const res = await subscriptionsApi.getAssetRules()
      assets.value = res.data
      assetIntervalHours.value = res.meta?.asset_interval_hours ?? null
    } finally {
      loading.value = false
    }
  }

  async function createAsset(payload) {
    await subscriptionsApi.createAssetRule(payload)
    await fetchAssets()
  }

  async function updateAsset(id, payload) {
    await subscriptionsApi.updateAssetRule(id, payload)
    await fetchAssets()
  }

  async function deleteAsset(id) {
    await subscriptionsApi.deleteAssetRule(id)
    await fetchAssets()
  }

  async function rearmAsset(id) {
    await subscriptionsApi.rearmAssetRule(id)
    await fetchAssets()
  }

  // reset forgets the current user's rules and keywords (logout, expired session).
  function reset() {
    assets.value = []
    assetIntervalHours.value = null
    keywords.value = []
  }

  async function fetchKeywords() {
    loading.value = true
    try {
      const res = await subscriptionsApi.getKeywordSubscriptions()
      keywords.value = res.data
    } finally {
      loading.value = false
    }
  }

  async function createKeyword(payload) {
    await subscriptionsApi.createKeywordSubscription(payload)
    await fetchKeywords()
  }

  async function deleteKeyword(id) {
    await subscriptionsApi.deleteKeywordSubscription(id)
    await fetchKeywords()
  }

  return {
    assets,
    assetIntervalHours,
    keywords,
    loading,
    fetchAssets,
    createAsset,
    updateAsset,
    deleteAsset,
    rearmAsset,
    fetchKeywords,
    createKeyword,
    deleteKeyword,
    reset,
  }
})
