<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'

import { createRun, getRun } from '@/api/runs'
import { pollRun, runErrorKey } from '@/utils/runPoller'
import { message } from '@/plugins/naive'

const props = defineProps({
  type: {
    type: String,
    required: true,
    validator: (v) => ['asset', 'sentinel'].includes(v),
  },
})

const { t } = useI18n()
const running = ref(false)

async function handleClick() {
  running.value = true
  try {
    const res = await createRun(props.type)
    const run = await pollRun(getRun, res.data.run_id)
    if (run.status === 'done') {
      message.success(t(`run.${props.type}_summary`, run.summary || {}), { duration: 6000 })
    } else {
      message.error(t('run.failed', { reason: run.error || '' }))
    }
  } catch (err) {
    const { key, params } = runErrorKey(err)
    message.error(t(key, params))
  } finally {
    running.value = false
  }
}
</script>

<template>
  <n-button :loading="running" :disabled="running" :aria-busy="running" @click="handleClick">
    {{ running ? t('run.running') : t('run.check_now') }}
  </n-button>
</template>
