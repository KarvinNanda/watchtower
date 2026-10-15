<script setup>
import { onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'

import { createRun, getRun } from '@/api/runs'
import { useRunNow } from '@/composables/useRunNow'
import { message } from '@/plugins/naive'

const props = defineProps({
  type: {
    type: String,
    required: true,
    validator: (v) => ['asset', 'sentinel'].includes(v),
  },
})

const { t } = useI18n()
const { running, start, stop } = useRunNow({
  type: props.type,
  createRun,
  getRun,
  t,
  notify: {
    success: (text) => message.success(text, { duration: 6000 }),
    error: (text) => message.error(text),
  },
})

// Leaving the page stops polling, so no result pops up on another page.
onUnmounted(stop)
</script>

<template>
  <n-button :loading="running" :disabled="running" :aria-busy="running" @click="start">
    {{ running ? t('run.running') : t('run.check_now') }}
  </n-button>
</template>
