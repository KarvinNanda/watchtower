<script setup>
import { ref, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import {
  NLayout,
  NLayoutSider,
  NLayoutHeader,
  NLayoutContent,
  NDrawer,
  NDrawerContent,
} from 'naive-ui'

import { useAuthStore } from '@/stores/auth'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const isMobile = useMediaQuery('(max-width: 768px)')
const mobileDrawerOpen = ref(false)
const desktopCollapsed = ref(false)

const route = useRoute()
const authStore = useAuthStore()

watch(
  () => route.fullPath,
  () => {
    mobileDrawerOpen.value = false
  },
)

function handleToggleMenu() {
  mobileDrawerOpen.value = !mobileDrawerOpen.value
}

// On a hard page reload, the token survives in localStorage but the
// in-memory user profile does not — refetch it so the header/profile/
// telegram-setup views always have data to render.
onMounted(() => {
  if (!authStore.user) {
    authStore.fetchProfile()
  }
})
</script>

<template>
  <n-layout has-sider style="height: 100vh">
    <n-layout-sider
      v-if="!isMobile"
      bordered
      collapse-mode="width"
      :collapsed-width="0"
      :width="240"
      :collapsed="desktopCollapsed"
      show-trigger="bar"
      @collapse="desktopCollapsed = true"
      @expand="desktopCollapsed = false"
    >
      <app-sidebar />
    </n-layout-sider>

    <n-drawer v-if="isMobile" v-model:show="mobileDrawerOpen" :width="260" placement="left">
      <n-drawer-content :native-scrollbar="false" closable>
        <app-sidebar @navigate="mobileDrawerOpen = false" />
      </n-drawer-content>
    </n-drawer>

    <n-layout>
      <n-layout-header bordered style="height: 60px">
        <app-header :show-menu-button="isMobile" @toggle-menu="handleToggleMenu" />
      </n-layout-header>
      <n-layout-content content-style="padding: 24px;">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>
