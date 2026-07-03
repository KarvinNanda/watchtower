<script setup>
import { NButton, NIcon } from 'naive-ui'
import { isDark, toggleTheme } from '@/plugins/naive'
</script>

<template>
  <n-button quaternary circle @click="toggleTheme" :aria-label="isDark ? 'Switch to light mode' : 'Switch to dark mode'">
    <template #icon>
      <n-icon size="18">
        <span v-if="isDark">🌙</span>
        <span v-else>☀️</span>
      </n-icon>
    </template>
  </n-button>
</template>
