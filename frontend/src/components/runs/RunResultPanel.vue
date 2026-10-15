<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NIcon } from 'naive-ui'
import { X } from '@lucide/vue'

import { runResultText } from '@/utils/runResult'

// Shows the last Check now result until closed (replaces the toast, Spec 4a).
const props = defineProps({
  result: { type: Object, default: null },
})
const emit = defineEmits(['close'])

const { t } = useI18n()
const ok = computed(() => props.result?.status === 'done')
const text = computed(() => runResultText(props.result, t))
const finishedAt = computed(() => {
  const at = props.result?.run?.finished_at
  return at ? t('run.finished_at', { time: new Date(at).toLocaleTimeString() }) : ''
})
</script>

<template>
  <!-- The status region stays in the DOM so screen readers announce what
       appears in it; a failure is announced at once through role="alert". -->
  <div role="status" aria-atomic="true">
    <div v-if="result" class="run-result" :role="ok ? undefined : 'alert'">
      <n-alert :type="ok ? 'success' : 'error'" :show-icon="true">
        <template #header>
          <div class="run-result__header">
            <span>{{ t('run.check_now') }}</span>
            <n-button quaternary circle size="small" :aria-label="t('common.close')" @click="emit('close')">
              <template #icon><n-icon :component="X" aria-hidden="true" /></template>
            </n-button>
          </div>
        </template>
        <p class="run-result__text">{{ text }}</p>
        <p v-if="finishedAt" class="run-result__time">{{ finishedAt }}</p>
      </n-alert>
    </div>
  </div>
</template>

<style scoped>
.run-result {
  margin-bottom: var(--wt-space-lg);
}

.run-result__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--wt-space-sm);
}

.run-result__text {
  margin: 0;
}

.run-result__time {
  margin: var(--wt-space-xs) 0 0;
  color: var(--wt-text-subtle);
  font-size: 12px;
}
</style>
