<script setup>
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NSteps, NStep, NButton, NSpace, NTag, NDivider, NAlert } from 'naive-ui'

import * as authApi from '@/api/auth'
import { message } from '@/plugins/naive'

const { t } = useI18n()

const assetBotUsername = import.meta.env.VITE_ASSET_BOT_USERNAME || 'WatchTowerAssetBot'
const sentinelBotUsername = import.meta.env.VITE_SENTINEL_BOT_USERNAME || 'WatchTowerSentinelBot'

const currentStep = ref(1)
const verifying = ref(false)
const verifiedAsset = ref(null)
const verifiedSentinel = ref(null)

const assetBotLink = computed(() => `https://t.me/${assetBotUsername}`)
const sentinelBotLink = computed(() => `https://t.me/${sentinelBotUsername}`)

async function handleVerify() {
  verifying.value = true
  try {
    const res = await authApi.getProfile()
    verifiedAsset.value = Boolean(res.data.telegram_asset_chat_id)
    verifiedSentinel.value = Boolean(res.data.telegram_sentinel_chat_id)
    currentStep.value = 5
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    verifying.value = false
  }
}
</script>

<template>
  <div class="telegram-setup">
    <n-card :title="t('telegram.title')" size="small">
      <p class="telegram-setup__intro">{{ t('telegram.intro') }}</p>

      <n-steps vertical :current="currentStep" class="telegram-setup__steps">
        <n-step :title="t('telegram.step1_title')">
          <p>{{ t('telegram.step1_desc', { bot: `@${assetBotUsername}` }) }}</p>
          <n-button tag="a" :href="assetBotLink" target="_blank" rel="noopener" size="small" type="primary">
            {{ t('telegram.open_bot') }}
          </n-button>
          <p class="telegram-setup__button-hint">{{ t('telegram.open_bot_hint') }}</p>
        </n-step>
        <n-step :title="t('telegram.step2_title')">
          <p>{{ t('telegram.step2_desc') }}</p>
        </n-step>
        <n-step :title="t('telegram.step3_title')">
          <p>{{ t('telegram.step3_desc') }}</p>
        </n-step>
        <n-step :title="t('telegram.step4_title')">
          <p>{{ t('telegram.step4_desc') }}</p>
          <router-link to="/profile">
            <n-button size="small">{{ t('telegram.go_to_profile') }}</n-button>
          </router-link>
        </n-step>
        <n-step :title="t('telegram.step5_title')">
          <p>{{ t('telegram.step5_desc', { bot: `@${sentinelBotUsername}` }) }}</p>
          <n-button tag="a" :href="sentinelBotLink" target="_blank" rel="noopener" size="small" type="primary">
            {{ t('telegram.open_bot') }}
          </n-button>
          <p class="telegram-setup__button-hint">{{ t('telegram.open_bot_hint') }}</p>
        </n-step>
      </n-steps>

      <n-alert type="info" :show-icon="false" class="telegram-setup__note">
        {{ t('telegram.auto_open_note') }}
      </n-alert>

      <n-divider />

      <n-space vertical>
        <n-button type="primary" :loading="verifying" @click="handleVerify">
          {{ t('telegram.verify_button') }}
        </n-button>

        <n-space v-if="verifiedAsset !== null">
          <n-tag :type="verifiedAsset ? 'success' : 'warning'" round>
            {{ verifiedAsset ? t('telegram.verified_asset') : t('telegram.not_verified_asset') }}
          </n-tag>
          <n-tag :type="verifiedSentinel ? 'success' : 'warning'" round>
            {{ verifiedSentinel ? t('telegram.verified_sentinel') : t('telegram.not_verified_sentinel') }}
          </n-tag>
        </n-space>
      </n-space>
    </n-card>
  </div>
</template>

<style scoped>
.telegram-setup {
  max-width: 640px;
}

.telegram-setup__intro {
  opacity: 0.75;
  margin-top: 0;
}

.telegram-setup__steps {
  margin: 20px 0;
}

.telegram-setup__steps :deep(p) {
  margin: 4px 0 10px;
}

.telegram-setup__button-hint {
  font-size: 12px;
  opacity: 0.6;
  margin: 6px 0 0 !important;
}

.telegram-setup__note {
  margin-bottom: 16px;
}
</style>
