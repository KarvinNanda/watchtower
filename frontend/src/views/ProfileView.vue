<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NCard,
  NForm,
  NFormItem,
  NInputNumber,
  NSelect,
  NRadioGroup,
  NRadio,
  NSpace,
  NButton,
  NSpin,
} from 'naive-ui'

import { useAuthStore } from '@/stores/auth'
import * as authApi from '@/api/auth'
import { message } from '@/plugins/naive'

const { t } = useI18n()
const authStore = useAuthStore()

const loading = ref(true)
const submitting = ref(false)

const DEVICE_OPTIONS = [
  { label: 'Android Phone', value: 'android_phone' },
  { label: 'Android Tablet', value: 'android_tablet' },
  { label: 'iPhone', value: 'iphone' },
  { label: 'iPad', value: 'ipad' },
  { label: 'Windows PC', value: 'windows_pc' },
  { label: 'Windows Laptop', value: 'windows_laptop' },
  { label: 'MacBook', value: 'macbook' },
  { label: 'iMac', value: 'imac' },
  { label: 'Linux Desktop', value: 'linux_desktop' },
  { label: 'Linux Server', value: 'linux_server' },
  { label: 'Raspberry Pi', value: 'raspberry_pi' },
  { label: 'NAS Device', value: 'nas_device' },
  { label: 'Smart TV', value: 'smart_tv' },
  { label: 'Router / Firewall', value: 'router_firewall' },
  { label: 'IoT Device', value: 'iot_device' },
]

const OS_OPTIONS = [
  { label: 'Android (General)', value: 'android' },
  { label: 'Samsung One UI', value: 'one_ui' },
  { label: 'Xiaomi HyperOS / MIUI', value: 'hyperos' },
  { label: 'Oppo ColorOS', value: 'coloros' },
  { label: 'Vivo OriginOS', value: 'originos' },
  { label: 'iOS', value: 'ios' },
  { label: 'iPadOS', value: 'ipados' },
  { label: 'Windows 10', value: 'windows_10' },
  { label: 'Windows 11', value: 'windows_11' },
  { label: 'Windows Server', value: 'windows_server' },
  { label: 'macOS', value: 'macos' },
  { label: 'Ubuntu', value: 'ubuntu' },
  { label: 'Debian', value: 'debian' },
  { label: 'Kali Linux', value: 'kali_linux' },
  { label: 'CentOS / RHEL', value: 'centos_rhel' },
  { label: 'Arch Linux', value: 'arch_linux' },
  { label: 'Fedora', value: 'fedora' },
  { label: 'Proxmox VE', value: 'proxmox' },
  { label: 'TrueNAS', value: 'truenas' },
  { label: 'pfSense / OPNsense', value: 'pfsense' },
]

const languageOptions = computed(() => [
  { label: 'Bahasa Indonesia', value: 'id' },
  { label: 'English', value: 'en' },
])

const form = ref({
  telegramAssetChatId: null,
  telegramSentinelChatId: null,
  cooldownHours: 4,
  preferredLanguage: 'id',
  devices: [],
  osList: [],
  expertiseLevel: 'beginner',
})

function populateForm(profile) {
  form.value = {
    telegramAssetChatId: profile.telegram_asset_chat_id ?? null,
    telegramSentinelChatId: profile.telegram_sentinel_chat_id ?? null,
    cooldownHours: profile.alert_cooldown_hours ?? 4,
    preferredLanguage: profile.preferred_language ?? 'id',
    devices: profile.devices ?? [],
    osList: profile.os_list ?? [],
    expertiseLevel: profile.expertise_level ?? 'beginner',
  }
}

async function handleSubmit() {
  submitting.value = true
  try {
    await authApi.updateProfile({
      telegram_asset_chat_id: form.value.telegramAssetChatId,
      telegram_sentinel_chat_id: form.value.telegramSentinelChatId,
      alert_cooldown_hours: form.value.cooldownHours,
      preferred_language: form.value.preferredLanguage,
      devices: form.value.devices,
      os_list: form.value.osList,
      expertise_level: form.value.expertiseLevel,
    })
    await authStore.fetchProfile()
    message.success(t('profile.save_success'))
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  loading.value = true
  try {
    if (!authStore.user) {
      await authStore.fetchProfile()
    }
    if (authStore.user) {
      populateForm(authStore.user)
    }
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <n-spin :show="loading">
    <div class="profile-view">
      <n-card :title="t('profile.telegram_section')" size="small" class="profile-view__card">
        <n-form label-placement="top">
          <n-form-item :label="t('profile.asset_chat_id')">
            <n-input-number v-model:value="form.telegramAssetChatId" style="width: 100%" :min="1" clearable />
          </n-form-item>
          <n-form-item :label="t('profile.sentinel_chat_id')">
            <n-input-number v-model:value="form.telegramSentinelChatId" style="width: 100%" :min="1" clearable />
          </n-form-item>
        </n-form>
        <router-link to="/telegram-setup" class="profile-view__link">
          {{ t('profile.telegram_setup_link') }}
        </router-link>
      </n-card>

      <n-card :title="t('profile.preferences_section')" size="small" class="profile-view__card">
        <n-form label-placement="top">
          <n-form-item :label="t('profile.cooldown_hours')">
            <n-input-number v-model:value="form.cooldownHours" style="width: 100%" :min="1" />
          </n-form-item>
          <n-form-item :label="t('profile.language')">
            <n-select v-model:value="form.preferredLanguage" :options="languageOptions" />
          </n-form-item>
        </n-form>
      </n-card>

      <n-card :title="t('profile.device_section')" size="small" class="profile-view__card">
        <n-form label-placement="top">
          <n-form-item :label="t('profile.devices')">
            <n-select
              v-model:value="form.devices"
              :options="DEVICE_OPTIONS"
              multiple
              filterable
              :max-tag-count="3"
              :placeholder="t('profile.devices_placeholder')"
            />
          </n-form-item>
          <n-form-item :label="t('profile.os_list')">
            <n-select
              v-model:value="form.osList"
              :options="OS_OPTIONS"
              multiple
              filterable
              :max-tag-count="3"
              :placeholder="t('profile.os_list_placeholder')"
            />
          </n-form-item>
          <n-form-item :label="t('profile.expertise')">
            <n-radio-group v-model:value="form.expertiseLevel">
              <n-space>
                <n-radio value="beginner" :label="t('profile.expertise_beginner')" />
                <n-radio value="intermediate" :label="t('profile.expertise_intermediate')" />
                <n-radio value="advanced" :label="t('profile.expertise_advanced')" />
              </n-space>
            </n-radio-group>
          </n-form-item>
        </n-form>
      </n-card>

      <div class="profile-view__actions">
        <n-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.save') }}</n-button>
      </div>
    </div>
  </n-spin>
</template>

<style scoped>
.profile-view {
  max-width: 640px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.profile-view__link {
  display: inline-block;
  margin-top: 8px;
  font-size: 13px;
  color: var(--n-primary-color, #7c6af7);
}

.profile-view__actions {
  display: flex;
  justify-content: flex-end;
}
</style>
