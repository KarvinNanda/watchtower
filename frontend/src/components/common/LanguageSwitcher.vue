<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NButtonGroup } from 'naive-ui'
import { setLocale } from '@/plugins/i18n'

const { locale } = useI18n()

const current = computed(() => locale.value)

function select(lang) {
  setLocale(lang)
}
</script>

<template>
  <n-button-group>
    <n-button
      size="small"
      :type="current === 'id' ? 'primary' : 'default'"
      @click="select('id')"
    >
      ID
    </n-button>
    <n-button
      size="small"
      :type="current === 'en' ? 'primary' : 'default'"
      @click="select('en')"
    >
      EN
    </n-button>
  </n-button-group>
</template>
