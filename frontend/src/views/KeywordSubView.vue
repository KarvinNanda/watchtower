<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton,
  NDataTable,
  NModal,
  NForm,
  NFormItem,
  NInput,
  NTag,
  NSpace,
  NAlert,
  useDialog,
} from 'naive-ui'

import { useSubscriptionsStore } from '@/stores/subscriptions'
import { message } from '@/plugins/naive'

const { t } = useI18n()
const store = useSubscriptionsStore()
const dialog = useDialog()

const loading = ref(false)
const formVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const formError = ref('')

const form = ref({ keyword: '', contextNote: '' })

const rules = {
  keyword: { required: true, message: () => t('common.required_field'), trigger: ['input', 'blur'] },
}

function openCreateModal() {
  form.value = { keyword: '', contextNote: '' }
  formError.value = ''
  formVisible.value = true
}

async function handleSubmit() {
  formError.value = ''
  try {
    await formRef.value?.validate()
  } catch {
    return
  }

  submitting.value = true
  try {
    await store.createKeyword({
      keyword: form.value.keyword.trim(),
      context_note: form.value.contextNote.trim() || null,
    })
    message.success(t('keyword.created_success'))
    formVisible.value = false
  } catch (err) {
    formError.value = err.response?.data?.message || t('common.error_generic')
  } finally {
    submitting.value = false
  }
}

function confirmDelete(row) {
  dialog.warning({
    title: t('keyword.delete_confirm_title'),
    content: row.keyword,
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await store.deleteKeyword(row.id)
        message.success(t('keyword.deleted_success'))
      } catch (err) {
        message.error(err.response?.data?.message || t('common.error_generic'))
      }
    },
  })
}

const columns = computed(() => [
  { title: t('keyword.keyword'), key: 'keyword' },
  {
    title: t('keyword.context_note'),
    key: 'context_note',
    render: (row) => row.context_note || '—',
  },
  {
    title: t('keyword.status'),
    key: 'is_active',
    render: (row) =>
      h(
        NTag,
        { type: row.is_active ? 'success' : 'default', size: 'small', round: true },
        { default: () => (row.is_active ? t('common.active') : t('common.inactive')) },
      ),
  },
  {
    title: t('keyword.actions'),
    key: 'actions',
    render: (row) =>
      h(
        NSpace,
        null,
        {
          default: () => [
            h(
              NButton,
              { size: 'small', type: 'error', disabled: !row.is_active, onClick: () => confirmDelete(row) },
              { default: () => t('common.delete') },
            ),
          ],
        },
      ),
  },
])

onMounted(async () => {
  loading.value = true
  try {
    await store.fetchKeywords()
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="keyword-view">
    <div class="keyword-view__toolbar">
      <n-button type="primary" @click="openCreateModal">{{ t('keyword.add') }}</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="store.keywords"
      :loading="loading"
      :bordered="false"
      :pagination="{ pageSize: 10 }"
    />

    <n-modal v-model:show="formVisible" preset="card" :title="t('keyword.add_title')" style="max-width: 460px">
      <n-alert v-if="formError" type="error" style="margin-bottom: 16px">{{ formError }}</n-alert>
      <n-form ref="formRef" :model="form" :rules="rules" label-placement="top">
        <n-form-item :label="t('keyword.keyword')" path="keyword">
          <n-input v-model:value="form.keyword" :placeholder="t('keyword.keyword_placeholder')" />
        </n-form-item>
        <n-form-item :label="t('keyword.context_note')">
          <n-input
            v-model:value="form.contextNote"
            type="textarea"
            :placeholder="t('keyword.context_note_placeholder')"
            :autosize="{ minRows: 2, maxRows: 4 }"
          />
        </n-form-item>

        <n-space justify="end">
          <n-button @click="formVisible = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.save') }}</n-button>
        </n-space>
      </n-form>
    </n-modal>
  </div>
</template>

<style scoped>
.keyword-view__toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 16px;
}
</style>
