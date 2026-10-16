<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NIcon, NEllipsis } from 'naive-ui'
import { Menu } from '@lucide/vue'

import { useAuthStore } from '@/stores/auth'

const props = defineProps({
  showMenuButton: {
    type: Boolean,
    default: false,
  },
})

defineEmits(['toggle-menu'])

const route = useRoute()
const { t } = useI18n()
const authStore = useAuthStore()

const titleKeyByRoute = {
  dashboard: 'nav.dashboard',
  assets: 'nav.assets',
  keywords: 'nav.sentinel',
  notifications: 'nav.notifications',
  profile: 'nav.profile',
  'telegram-setup': 'nav.telegram',
}

const title = computed(() => {
  const key = titleKeyByRoute[route.name]
  return key ? t(key) : t('app.name')
})
</script>

<template>
  <header class="app-header">
    <div class="app-header__left">
      <n-button
        v-if="showMenuButton"
        quaternary
        circle
        :aria-label="t('common.toggle_navigation')"
        @click="$emit('toggle-menu')"
      >
        <template #icon>
          <n-icon size="20" :component="Menu" aria-hidden="true" />
        </template>
      </n-button>
      <h1 class="app-header__title">{{ title }}</h1>
    </div>
    <div class="app-header__right">
      <n-ellipsis v-if="authStore.user" style="max-width: min(220px, 40vw)" class="app-header__email">
        {{ authStore.user.email }}
      </n-ellipsis>
    </div>
  </header>
</template>

<style scoped>
.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 20px;
  height: 100%;
}

.app-header__left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

/* One line with an ellipsis, so it never runs into the email at 375 px (#55). */
.app-header__title {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.app-header__email {
  font-size: 13px;
  opacity: 0.7;
}
</style>
