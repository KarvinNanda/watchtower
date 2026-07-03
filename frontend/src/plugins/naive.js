import { computed } from 'vue'
import { useStorage } from '@vueuse/core'
import { darkTheme, lightTheme, createDiscreteApi } from 'naive-ui'

// Dark mode is the default; the choice persists across visits.
export const isDark = useStorage('watchtower-dark-mode', true)

export function toggleTheme() {
  isDark.value = !isDark.value
}

export const currentTheme = computed(() => (isDark.value ? darkTheme : lightTheme))

const darkOverrides = {
  common: {
    bodyColor: '#0f1117',
    baseColor: '#0f1117',
    cardColor: '#1a1d27',
    modalColor: '#1a1d27',
    popoverColor: '#1a1d27',
    borderColor: '#2a2d3e',
    dividerColor: '#2a2d3e',
    primaryColor: '#7c6af7',
    primaryColorHover: '#8f80f8',
    primaryColorPressed: '#6a58e0',
    primaryColorSuppl: '#8f80f8',
    successColor: '#36d399',
    successColorHover: '#4fdcac',
    successColorPressed: '#2bb583',
    warningColor: '#fbbd23',
    warningColorHover: '#fccb4f',
    warningColorPressed: '#e0a710',
    errorColor: '#f87272',
    errorColorHover: '#fa8f8f',
    errorColorPressed: '#e05a5a',
    textColorBase: '#e2e8f0',
    textColor1: '#e2e8f0',
    textColor2: '#c3cad6',
    textColor3: '#8b94a7',
  },
}

const lightOverrides = {
  common: {
    bodyColor: '#f8fafc',
    baseColor: '#ffffff',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    borderColor: '#e2e8f0',
    dividerColor: '#e2e8f0',
    primaryColor: '#6d58f0',
    primaryColorHover: '#7d6af2',
    primaryColorPressed: '#5b46d1',
    primaryColorSuppl: '#7d6af2',
    successColor: '#2ecc71',
    successColorHover: '#4bd889',
    successColorPressed: '#25a75a',
    warningColor: '#f39c12',
    warningColorHover: '#f5ad3d',
    warningColorPressed: '#cc830f',
    errorColor: '#e74c3c',
    errorColorHover: '#eb6a5c',
    errorColorPressed: '#c93f31',
    textColorBase: '#1e293b',
    textColor1: '#1e293b',
    textColor2: '#42536b',
    textColor3: '#6b7891',
  },
}

export const currentThemeOverrides = computed(() => (isDark.value ? darkOverrides : lightOverrides))

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
