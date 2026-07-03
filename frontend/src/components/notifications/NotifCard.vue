<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NThing, NTag, NTime } from 'naive-ui'

const props = defineProps({
  notification: {
    type: Object,
    required: true,
  },
})

defineEmits(['click'])

const { t } = useI18n()

const icon = computed(() => (props.notification.notif_type === 'sentinel' ? '🛡️' : '🔔'))

const label = computed(() => props.notification.asset_symbol || props.notification.keyword || '')

const preview = computed(() => {
  const text = props.notification.content_summary || ''
  return text.length > 100 ? `${text.slice(0, 100)}…` : text
})

const statusType = computed(() => (props.notification.status === 'sent' ? 'success' : 'error'))

const statusLabel = computed(() =>
  props.notification.status === 'sent' ? t('notifications.status_sent') : t('notifications.status_failed'),
)
</script>

<template>
  <n-thing class="notif-card" @click="$emit('click', notification)">
    <template #avatar>
      <span class="notif-card__icon">{{ icon }}</span>
    </template>
    <template #header>
      <span>{{ label }}</span>
    </template>
    <template #header-extra>
      <n-tag :type="statusType" size="small" round>{{ statusLabel }}</n-tag>
    </template>
    <template #description>
      <n-time :time="new Date(notification.sent_at)" type="relative" />
    </template>
    {{ preview }}
  </n-thing>
</template>

<style scoped>
.notif-card {
  cursor: pointer;
  padding: 8px;
  border-radius: 8px;
  transition: background-color 0.15s ease;
}

.notif-card:hover {
  background-color: rgba(124, 106, 247, 0.08);
}

.notif-card__icon {
  font-size: 20px;
}
</style>
