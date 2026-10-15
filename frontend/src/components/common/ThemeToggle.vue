<script setup>
import { NButton, NIcon } from 'naive-ui'
import { Moon, Sun } from '@lucide/vue'
import { isDark, toggleTheme } from '@/plugins/naive'
</script>

<template>
  <n-button quaternary circle @click="toggleTheme" :aria-label="isDark ? 'Switch to light mode' : 'Switch to dark mode'">
    <template #icon>
      <n-icon size="18" :component="isDark ? Moon : Sun" aria-hidden="true" />
    </template>
  </n-button>
</template>
