<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTabs, NTabPane, NEmpty, NSpin, NPagination, NDrawer, NDrawerContent, NDescriptions, NDescriptionsItem, NTag } from 'naive-ui'

import { useNotificationsStore } from '@/stores/notifications'
import { message } from '@/plugins/naive'
import NotifCard from '@/components/notifications/NotifCard.vue'

const { t } = useI18n()
const store = useNotificationsStore()

const PAGE_SIZE = 20

const activeTab = ref('all')
const page = ref(1)

const selectedNotification = ref(null)
const drawerVisible = ref(false)

const notifType = computed(() => (activeTab.value === 'all' ? '' : activeTab.value))
const pageCount = computed(() => Math.max(1, Math.ceil(store.total / PAGE_SIZE)))

async function load() {
  try {
    await store.fetchNotifications({
      limit: PAGE_SIZE,
      offset: (page.value - 1) * PAGE_SIZE,
      type: notifType.value,
    })
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  }
}

function handleTabChange(tab) {
  activeTab.value = tab
  page.value = 1
}

function openDetail(notification) {
  selectedNotification.value = notification
  drawerVisible.value = true
}

watch([activeTab, page], load)
onMounted(load)
</script>

<template>
  <div class="notifications-view">
    <n-tabs :value="activeTab" type="line" animated @update:value="handleTabChange">
      <n-tab-pane name="all" :tab="t('notifications.tab_all')" />
      <n-tab-pane name="asset" :tab="t('notifications.tab_asset')" />
      <n-tab-pane name="sentinel" :tab="t('notifications.tab_sentinel')" />
    </n-tabs>

    <n-spin :show="store.loading">
      <n-empty v-if="!store.loading && !store.logs.length" :description="t('notifications.empty')" />
      <div v-else class="notifications-view__list">
        <notif-card
          v-for="item in store.logs"
          :key="item.id"
          :notification="item"
          @click="openDetail"
        />
      </div>
    </n-spin>

    <div class="notifications-view__pagination" v-if="store.total > PAGE_SIZE">
      <n-pagination v-model:page="page" :page-count="pageCount" />
    </div>

    <n-drawer v-model:show="drawerVisible" :width="380" placement="right">
      <n-drawer-content :title="t('notifications.detail_title')" closable>
        <n-descriptions v-if="selectedNotification" :column="1" label-placement="left">
          <n-descriptions-item :label="t('asset.status')">
            <n-tag :type="selectedNotification.status === 'sent' ? 'success' : 'error'" size="small">
              {{
                selectedNotification.status === 'sent'
                  ? t('notifications.status_sent')
                  : t('notifications.status_failed')
              }}
            </n-tag>
          </n-descriptions-item>
          <n-descriptions-item v-if="selectedNotification.asset_symbol" :label="t('asset.symbol')">
            {{ selectedNotification.asset_symbol }}
          </n-descriptions-item>
          <n-descriptions-item v-if="selectedNotification.keyword" :label="t('keyword.keyword')">
            {{ selectedNotification.keyword }}
          </n-descriptions-item>
          <n-descriptions-item label="Timestamp">
            {{ new Date(selectedNotification.sent_at).toLocaleString() }}
          </n-descriptions-item>
        </n-descriptions>
        <p class="notifications-view__content">{{ selectedNotification?.content_summary }}</p>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<style scoped>
.notifications-view__list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 16px;
  min-height: 120px;
}

.notifications-view__pagination {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

.notifications-view__content {
  margin-top: 16px;
  line-height: 1.6;
  white-space: pre-wrap;
}
</style>
