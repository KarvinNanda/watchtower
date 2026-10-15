<script setup>
import { NConfigProvider, NGlobalStyle, NLoadingBarProvider, NDialogProvider, NNotificationProvider, NMessageProvider } from 'naive-ui'
import { computed, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { currentTheme, currentThemeOverrides, currentTokens } from '@/plugins/naive'
import { naiveLocaleFor } from '@/plugins/naiveLocale'
import { toCssVars, applyCssVars } from '@/theme/cssVars'

const { locale } = useI18n()
const naiveLocale = computed(() => naiveLocaleFor(locale.value))

// Our own CSS reads --wt-* variables; keep them in step with the theme.
watchEffect(() => applyCssVars(document.documentElement.style, toCssVars(currentTokens.value)))
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
