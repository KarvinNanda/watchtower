<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NIcon } from 'naive-ui'
import { CircleAlert, CircleCheck, X } from '@lucide/vue'

import { runOk, runResultText, runTitleKey } from '@/utils/runResult'
import { formatClock } from '@/utils/time'

// Shows the last Check now or status report result until closed (replaces
// the toast, Spec 4a).
const props = defineProps({
  result: { type: Object, default: null },
})
const emit = defineEmits(['close'])

const { t, locale } = useI18n()
const ok = computed(() => runOk(props.result))
const text = computed(() => runResultText(props.result, t))
const finishedAt = computed(() => {
  const at = props.result?.run?.finished_at
  return at ? t('run.finished_at', { time: formatClock(at, locale.value) }) : ''
})
</script>

<template>
  <!-- Both live regions are always in the DOM (empty while idle), so screen
       readers announce the text that appears in them: success politely
       (status), failure at once (alert). Naive's NAlert is not used because
       it always renders role="alert". The close button sits outside both
       regions so it is not read as part of the message. -->
  <div class="run-result" :class="{ 'is-open': result, 'is-ok': ok, 'is-error': result && !ok }">
    <n-icon
      v-if="result"
      class="run-result__icon"
      :component="ok ? CircleCheck : CircleAlert"
      size="20"
      aria-hidden="true"
    />
    <div class="run-result__body">
      <p v-if="result" class="run-result__title">{{ t(runTitleKey(result.type)) }}</p>
      <div role="status" aria-atomic="true">
        <template v-if="result && ok">
          <p class="run-result__text">{{ text }}</p>
          <p v-if="finishedAt" class="run-result__time">{{ finishedAt }}</p>
        </template>
      </div>
      <div role="alert" aria-atomic="true">
        <p v-if="result && !ok" class="run-result__text">{{ text }}</p>
      </div>
    </div>
    <n-button
      v-if="result"
      quaternary
      circle
      size="small"
      class="run-result__close"
      :aria-label="t('common.close')"
      @click="emit('close')"
    >
      <template #icon><n-icon :component="X" aria-hidden="true" /></template>
    </n-button>
  </div>
</template>

<style scoped>
.run-result.is-open {
  display: flex;
  align-items: flex-start;
  gap: var(--wt-space-md);
  margin-bottom: var(--wt-space-lg);
  padding: var(--wt-space-md) var(--wt-space-lg);
  background: var(--wt-surface-raised);
  border: 1px solid var(--wt-border);
  border-left-width: 4px;
  border-radius: var(--wt-radius-md);
}

.run-result.is-ok {
  border-left-color: var(--wt-up);
}

.run-result.is-error {
  border-left-color: var(--wt-danger);
}

.is-ok .run-result__icon {
  color: var(--wt-up);
}

.is-error .run-result__icon {
  color: var(--wt-danger);
}

.run-result__body {
  flex: 1;
  min-width: 0;
}

.run-result__title {
  margin: 0 0 var(--wt-space-xs);
  font-weight: 600;
}

.run-result__text {
  margin: 0;
}

.run-result__time {
  margin: var(--wt-space-xs) 0 0;
  color: var(--wt-text-subtle);
  font-size: 12px;
}

.run-result__close {
  flex-shrink: 0;
}
</style>
