<script setup>
import { NConfigProvider, NGlobalStyle, NLoadingBarProvider, NDialogProvider, NNotificationProvider, NMessageProvider } from 'naive-ui'
import { currentTheme, currentThemeOverrides } from '@/plugins/naive'
</script>

<template>
  <n-config-provider :theme="currentTheme" :theme-overrides="currentThemeOverrides">
    <n-global-style />
    <n-loading-bar-provider>
      <n-dialog-provider>
        <n-notification-provider>
          <n-message-provider>
            <router-view />
          </n-message-provider>
        </n-notification-provider>
      </n-dialog-provider>
    </n-loading-bar-provider>
  </n-config-provider>
</template>
