import { defineStore } from 'pinia'
import { ref } from 'vue'

import * as subscriptionsApi from '@/api/subscriptions'

export const useSubscriptionsStore = defineStore('subscriptions', () => {
  const assets = ref([])
  const keywords = ref([])
  const loading = ref(false)

  async function fetchAssets() {
    loading.value = true
    try {
      const res = await subscriptionsApi.getAssetSubscriptions()
      assets.value = res.data
    } finally {
      loading.value = false
    }
  }

  async function createAsset(payload) {
    await subscriptionsApi.createAssetSubscription(payload)
    await fetchAssets()
  }

  async function updateAsset(id, payload) {
    await subscriptionsApi.updateAssetSubscription(id, payload)
    await fetchAssets()
  }

  async function deleteAsset(id) {
    await subscriptionsApi.deleteAssetSubscription(id)
    await fetchAssets()
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
    keywords,
    loading,
    fetchAssets,
    createAsset,
    updateAsset,
    deleteAsset,
    fetchKeywords,
    createKeyword,
    deleteKeyword,
  }
})
