<script setup>
import { NConfigProvider, NGlobalStyle, NLoadingBarProvider, NDialogProvider, NNotificationProvider, NMessageProvider } from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { currentTheme, currentThemeOverrides } from '@/plugins/naive'
import { naiveLocaleFor } from '@/plugins/naiveLocale'

const { locale } = useI18n()
const naiveLocale = computed(() => naiveLocaleFor(locale.value))
</script>

<template>
  <n-config-provider
    :theme="currentTheme"
    :theme-overrides="currentThemeOverrides"
    :locale="naiveLocale.locale"
    :date-locale="naiveLocale.dateLocale"
  >
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
