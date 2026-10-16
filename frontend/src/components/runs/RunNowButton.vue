<script setup>
import { computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'

import { createRun, getRun } from '@/api/runs'
import { useRunNow } from '@/composables/useRunNow'
import { runButtonKeys } from '@/utils/runResult'

const props = defineProps({
  type: {
    type: String,
    required: true,
    validator: (v) => ['asset', 'asset_report', 'sentinel'].includes(v),
  },
})
// started: a new run begins (clear the old result). finished: the result.
const emit = defineEmits(['started', 'finished'])

const { t } = useI18n()
const { running, result, start, stop } = useRunNow({ type: props.type, createRun, getRun })
const labels = computed(() => runButtonKeys(props.type))

watch(result, (r) => {
  if (r) emit('finished', r)
})

function handleClick() {
  if (running.value) return
  emit('started')
  start()
}

// Leaving the page stops polling, so no result arrives on another page.
onUnmounted(stop)
</script>

<template>
  <n-button :loading="running" :disabled="running" :aria-busy="running" @click="handleClick">
    {{ running ? t(labels.running) : t(labels.idle) }}
  </n-button>
</template>
