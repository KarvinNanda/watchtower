<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NGrid, NGi, NCard, NStatistic, NSpin, NEmpty, NModal, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'

import * as marketApi from '@/api/market'
import { useMarketStore } from '@/stores/market'
import { message } from '@/plugins/naive'
import MarketCard from '@/components/market/MarketCard.vue'
import NotifCard from '@/components/notifications/NotifCard.vue'

const { t } = useI18n()
const marketStore = useMarketStore()

const loading = ref(true)
const stats = ref({
  asset_count: 0,
  keyword_count: 0,
  last_asset_alert: null,
  last_sentinel_alert: null,
})
const recentNotifications = ref([])

const selectedNotification = ref(null)
const detailVisible = ref(false)

function formatTimestamp(value) {
  if (!value) return t('dashboard.never')
  return new Date(value).toLocaleString()
}

function openDetail(notification) {
  selectedNotification.value = notification
  detailVisible.value = true
}

async function loadDashboard() {
  loading.value = true
  try {
    const res = await marketApi.getDashboard()
    stats.value = res.data
    recentNotifications.value = res.data.recent_notifications || []
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadDashboard()
  marketStore.fetchSnapshot()
  marketStore.startAutoRefresh()
})

onUnmounted(() => {
  marketStore.stopAutoRefresh()
})
</script>

<template>
  <div class="dashboard">
    <n-spin :show="loading">
      <n-grid cols="1 768:3" x-gap="20" y-gap="20" responsive="screen">
        <!-- Column 1: Stats -->
        <n-gi>
          <n-card :title="t('dashboard.title')" size="small" class="dashboard__stats-card">
            <div class="dashboard__stat">
              <n-statistic :label="t('dashboard.total_assets')" :value="stats.asset_count" />
            </div>
            <div class="dashboard__stat">
              <n-statistic :label="t('dashboard.total_keywords')" :value="stats.keyword_count" />
            </div>
            <div class="dashboard__stat">
              <span class="dashboard__stat-label">{{ t('dashboard.last_asset_alert') }}</span>
              <span class="dashboard__stat-value">{{ formatTimestamp(stats.last_asset_alert) }}</span>
            </div>
            <div class="dashboard__stat">
              <span class="dashboard__stat-label">{{ t('dashboard.last_sentinel_alert') }}</span>
              <span class="dashboard__stat-value">{{ formatTimestamp(stats.last_sentinel_alert) }}</span>
            </div>
          </n-card>
        </n-gi>

        <!-- Column 2: Market snapshot -->
        <n-gi>
          <n-card :title="t('dashboard.market_snapshot')" size="small">
            <n-empty v-if="!marketStore.snapshot.length" :description="t('dashboard.no_market_data')" />
            <div v-else class="dashboard__market-list">
              <market-card v-for="item in marketStore.snapshot" :key="item.symbol" :data="item" />
            </div>
          </n-card>
        </n-gi>

        <!-- Column 3: Recent notifications -->
        <n-gi>
          <n-card :title="t('dashboard.recent_notifications')" size="small">
            <template #header-extra>
              <router-link to="/notifications">{{ t('dashboard.view_all') }}</router-link>
            </template>
            <n-empty v-if="!recentNotifications.length" :description="t('dashboard.no_notifications')" />
            <div v-else class="dashboard__notif-list">
              <notif-card
                v-for="item in recentNotifications"
                :key="item.id"
                :notification="item"
                @click="openDetail"
              />
            </div>
          </n-card>
        </n-gi>
      </n-grid>
    </n-spin>

    <n-modal v-model:show="detailVisible" preset="card" :title="t('notifications.detail_title')" style="max-width: 500px">
      <n-descriptions v-if="selectedNotification" :column="1" label-placement="left">
        <n-descriptions-item :label="t('asset.status')">
          <n-tag :type="selectedNotification.status === 'sent' ? 'success' : 'error'" size="small">
            {{ selectedNotification.status === 'sent' ? t('notifications.status_sent') : t('notifications.status_failed') }}
          </n-tag>
        </n-descriptions-item>
        <n-descriptions-item v-if="selectedNotification.asset_symbol" :label="t('asset.symbol')">
          {{ selectedNotification.asset_symbol }}
        </n-descriptions-item>
        <n-descriptions-item v-if="selectedNotification.keyword" :label="t('keyword.keyword')">
          {{ selectedNotification.keyword }}
        </n-descriptions-item>
        <n-descriptions-item label="Timestamp">
          {{ formatTimestamp(selectedNotification.sent_at) }}
        </n-descriptions-item>
      </n-descriptions>
      <p class="dashboard__modal-content">{{ selectedNotification?.content_summary }}</p>
    </n-modal>
  </div>
</template>

<style scoped>
.dashboard__stats-card {
  height: 100%;
}

.dashboard__stat {
  display: flex;
  flex-direction: column;
  padding: 10px 0;
  border-bottom: 1px solid var(--n-border-color, transparent);
}

.dashboard__stat:last-child {
  border-bottom: none;
}

.dashboard__stat-label {
  font-size: 13px;
  opacity: 0.65;
}

.dashboard__stat-value {
  font-size: 15px;
  font-weight: 600;
  margin-top: 2px;
}

.dashboard__market-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.dashboard__notif-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.dashboard__modal-content {
  margin-top: 16px;
  line-height: 1.6;
  white-space: pre-wrap;
}
</style>
