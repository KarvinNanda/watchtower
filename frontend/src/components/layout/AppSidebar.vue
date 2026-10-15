<script setup>
import { computed, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NMenu, NButton, NIcon } from 'naive-ui'
import { LayoutDashboard, ChartCandlestick, ShieldAlert, Bell, User, Send, LogOut, TowerControl } from '@lucide/vue'

import { useAuthStore } from '@/stores/auth'
import ThemeToggle from '@/components/common/ThemeToggle.vue'
import LanguageSwitcher from '@/components/common/LanguageSwitcher.vue'

const emit = defineEmits(['navigate'])

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const authStore = useAuthStore()

function renderIcon(icon) {
  return () => h(NIcon, { component: icon, 'aria-hidden': 'true' })
}

const menuOptions = computed(() => [
  { label: t('nav.dashboard'), key: 'dashboard', icon: renderIcon(LayoutDashboard) },
  { label: t('nav.assets'), key: 'assets', icon: renderIcon(ChartCandlestick) },
  { label: t('nav.sentinel'), key: 'keywords', icon: renderIcon(ShieldAlert) },
  { label: t('nav.notifications'), key: 'notifications', icon: renderIcon(Bell) },
  { label: t('nav.profile'), key: 'profile', icon: renderIcon(User) },
  { label: t('nav.telegram'), key: 'telegram-setup', icon: renderIcon(Send) },
])

const activeKey = computed(() => route.name)

function handleUpdateValue(key) {
  router.push({ name: key })
  emit('navigate')
}

function handleLogout() {
  authStore.logout()
}
</script>

<template>
  <div class="app-sidebar">
    <div class="app-sidebar__logo">
      <n-icon class="app-sidebar__logo-icon" :component="TowerControl" size="22" aria-hidden="true" />
      <span class="app-sidebar__logo-text">{{ t('app.name') }}</span>
    </div>

    <n-menu
      class="app-sidebar__menu"
      :options="menuOptions"
      :value="activeKey"
      @update:value="handleUpdateValue"
    />

    <div class="app-sidebar__footer">
      <div class="app-sidebar__footer-row">
        <theme-toggle />
        <language-switcher />
      </div>
      <n-button block quaternary @click="handleLogout">
        <template #icon>
          <n-icon :component="LogOut" aria-hidden="true" />
        </template>
        {{ t('nav.logout') }}
      </n-button>
    </div>
  </div>
</template>

<style scoped>
.app-sidebar {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.app-sidebar__logo {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 20px 16px;
}

.app-sidebar__logo-icon {
  font-size: 24px;
}

.app-sidebar__logo-text {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.app-sidebar__menu {
  flex: 1;
  overflow-y: auto;
}

.app-sidebar__footer {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.app-sidebar__footer-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
