import { computed } from 'vue'
import { useStorage } from '@vueuse/core'
import { darkTheme, lightTheme, createDiscreteApi } from 'naive-ui'

import { tokens } from '@/theme/tokens'
import { naiveOverrides } from '@/theme/naiveOverrides'

// Dark mode is the default; the choice persists across visits.
export const isDark = useStorage('watchtower-dark-mode', true)

export function toggleTheme() {
  isDark.value = !isDark.value
}

export const currentTheme = computed(() => (isDark.value ? darkTheme : lightTheme))

const darkOverrides = naiveOverrides(tokens.dark)
const lightOverrides = naiveOverrides(tokens.light)

export const currentThemeOverrides = computed(() => (isDark.value ? darkOverrides : lightOverrides))
export const currentTokens = computed(() => (isDark.value ? tokens.dark : tokens.light))

// Discrete API: message/notification/dialog/loading-bar usable outside
// component setup (e.g. the axios interceptor), themed consistently with
// whatever the user currently has selected.
const { message, notification, dialog, loadingBar } = createDiscreteApi(
  ['message', 'notification', 'dialog', 'loadingBar'],
  {
    configProviderProps: computed(() => ({
      theme: currentTheme.value,
      themeOverrides: currentThemeOverrides.value,
    })),
  },
)

export { message, notification, dialog, loadingBar }
