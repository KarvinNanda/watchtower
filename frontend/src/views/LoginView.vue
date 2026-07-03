<script setup>
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NCard, NForm, NFormItem, NInput, NButton, NAlert } from 'naive-ui'

import { useAuthStore } from '@/stores/auth'
import { message } from '@/plugins/naive'
import ThemeToggle from '@/components/common/ThemeToggle.vue'
import LanguageSwitcher from '@/components/common/LanguageSwitcher.vue'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const formRef = ref(null)
const submitting = ref(false)
const errorMessage = ref('')

const form = ref({
  email: '',
  password: '',
})

const rules = {
  email: {
    required: true,
    message: () => t('common.required_field'),
    trigger: ['input', 'blur'],
  },
  password: {
    required: true,
    message: () => t('common.required_field'),
    trigger: ['input', 'blur'],
  },
}

onMounted(() => {
  if (route.query.sessionExpired) {
    message.warning(t('common.session_expired'))
  }
})

async function handleSubmit() {
  errorMessage.value = ''
  try {
    await formRef.value?.validate()
  } catch {
    return
  }

  submitting.value = true
  try {
    await authStore.login(form.value.email, form.value.password)
    router.push({ name: 'dashboard' })
  } catch (err) {
    errorMessage.value = err.response?.data?.message || t('auth.login_failed')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="auth-page">
    <div class="auth-page__topbar">
      <language-switcher />
      <theme-toggle />
    </div>

    <div class="auth-page__center">
      <div class="auth-page__logo">
        <span class="auth-page__logo-icon">🗼</span>
        <span class="auth-page__logo-text">{{ t('app.name') }}</span>
      </div>

      <n-card class="auth-page__card" :title="t('auth.login_title')">
        <p class="auth-page__subtitle">{{ t('auth.login_subtitle') }}</p>

        <n-alert v-if="errorMessage" type="error" style="margin-bottom: 16px" closable @close="errorMessage = ''">
          {{ errorMessage }}
        </n-alert>

        <n-form ref="formRef" :model="form" :rules="rules" @keyup.enter="handleSubmit">
          <n-form-item :label="t('auth.email')" path="email">
            <n-input
              v-model:value="form.email"
              :placeholder="t('auth.email_placeholder')"
              autofocus
            />
          </n-form-item>
          <n-form-item :label="t('auth.password')" path="password">
            <n-input
              v-model:value="form.password"
              type="password"
              show-password-on="click"
            />
          </n-form-item>

          <n-button
            type="primary"
            block
            :loading="submitting"
            @click="handleSubmit"
          >
            {{ t('auth.login') }}
          </n-button>
        </n-form>

        <div class="auth-page__footer">
          {{ t('auth.no_account') }}
          <router-link to="/register">{{ t('auth.register') }}</router-link>
        </div>
      </n-card>
    </div>
  </div>
</template>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.auth-page__topbar {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 12px;
  padding: 16px 20px;
}

.auth-page__center {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.auth-page__logo {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 24px;
}

.auth-page__logo-icon {
  font-size: 32px;
}

.auth-page__logo-text {
  font-size: 26px;
  font-weight: 700;
}

.auth-page__card {
  width: 100%;
  max-width: 400px;
}

.auth-page__subtitle {
  margin: -8px 0 16px;
  opacity: 0.7;
  font-size: 14px;
}

.auth-page__footer {
  margin-top: 16px;
  text-align: center;
  font-size: 14px;
}
</style>
