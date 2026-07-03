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
  NInputNumber,
  NSelect,
  NSwitch,
  NSpace,
  NAlert,
  useDialog,
} from 'naive-ui'

import { useSubscriptionsStore } from '@/stores/subscriptions'
import { message } from '@/plugins/naive'
import { ASSET_OPTIONS } from '@/constants/assets'

const { t } = useI18n()
const store = useSubscriptionsStore()
const dialog = useDialog()

const loading = ref(false)
const formVisible = ref(false)
const submitting = ref(false)
const editingId = ref(null)
const formRef = ref(null)
const formError = ref('')

const assetTypeOptions = computed(() => [
  { label: t('asset.type_stock'), value: 'stock' },
  { label: t('asset.type_crypto'), value: 'crypto' },
  { label: t('asset.type_gold'), value: 'gold' },
])

const symbolOptions = computed(() => ASSET_OPTIONS[form.value.assetType] || [])

function handleAssetTypeChange() {
  form.value.assetSymbol = null
}

const alertTypeOptions = computed(() => [
  { label: t('asset.alert_price_threshold'), value: 'price_threshold' },
  { label: t('asset.alert_pct_change'), value: 'pct_change' },
  { label: t('asset.alert_both'), value: 'both' },
])

const defaultForm = () => ({
  assetType: null,
  assetSymbol: null,
  alertType: null,
  priceLowerUsd: null,
  priceUpperUsd: null,
  pctChangeThreshold: null,
})

const form = ref(defaultForm())

const rules = {
  assetType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  assetSymbol: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  alertType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
}

const modalTitle = computed(() => (editingId.value ? t('asset.edit_title') : t('asset.add_title')))

function openCreateModal() {
  editingId.value = null
  form.value = defaultForm()
  formError.value = ''
  formVisible.value = true
}

function openEditModal(row) {
  editingId.value = row.id
  form.value = {
    assetType: row.asset_type,
    assetSymbol: row.asset_symbol,
    alertType: row.alert_type,
    priceLowerUsd: row.price_lower_usd ?? null,
    priceUpperUsd: row.price_upper_usd ?? null,
    pctChangeThreshold: row.pct_change_threshold ?? null,
  }
  formError.value = ''
  formVisible.value = true
}

function validateThresholds() {
  const { alertType, priceLowerUsd, priceUpperUsd, pctChangeThreshold } = form.value
  const needsPrice = alertType === 'price_threshold' || alertType === 'both'
  const needsPct = alertType === 'pct_change' || alertType === 'both'

  if (needsPrice && priceLowerUsd == null && priceUpperUsd == null) {
    return t('asset.threshold_hint')
  }
  if (needsPct && pctChangeThreshold == null) {
    return t('asset.pct_hint')
  }
  return ''
}

async function handleSubmit() {
  formError.value = ''
  try {
    await formRef.value?.validate()
  } catch {
    return
  }

  const thresholdError = validateThresholds()
  if (thresholdError) {
    formError.value = thresholdError
    return
  }

  const payload = {
    asset_type: form.value.assetType,
    asset_symbol: form.value.assetSymbol,
    alert_type: form.value.alertType,
    price_lower_usd: form.value.priceLowerUsd,
    price_upper_usd: form.value.priceUpperUsd,
    pct_change_threshold: form.value.pctChangeThreshold,
  }

  submitting.value = true
  try {
    if (editingId.value) {
      await store.updateAsset(editingId.value, payload)
      message.success(t('asset.updated_success'))
    } else {
      await store.createAsset(payload)
      message.success(t('asset.created_success'))
    }
    formVisible.value = false
  } catch (err) {
    formError.value = err.response?.data?.message || t('common.error_generic')
  } finally {
    submitting.value = false
  }
}

function confirmDelete(row) {
  dialog.warning({
    title: t('asset.delete_confirm_title'),
    content: `${row.asset_symbol} — ${row.alert_type}`,
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await store.deleteAsset(row.id)
        message.success(t('asset.deleted_success'))
      } catch (err) {
        message.error(err.response?.data?.message || t('common.error_generic'))
      }
    },
  })
}

async function handleToggleActive(row, value) {
  try {
    await store.updateAsset(row.id, { is_active: value })
    message.success(t('asset.toggle_success'))
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  }
}

function formatBound(value) {
  return value == null ? '—' : `$${value.toLocaleString()}`
}

const columns = computed(() => [
  { title: t('asset.symbol'), key: 'asset_symbol' },
  {
    title: t('asset.type'),
    key: 'asset_type',
    render: (row) => assetTypeOptions.value.find((o) => o.value === row.asset_type)?.label || row.asset_type,
  },
  {
    title: t('asset.alert_type'),
    key: 'alert_type',
    render: (row) => alertTypeOptions.value.find((o) => o.value === row.alert_type)?.label || row.alert_type,
  },
  { title: t('asset.lower_bound'), key: 'price_lower_usd', render: (row) => formatBound(row.price_lower_usd) },
  { title: t('asset.upper_bound'), key: 'price_upper_usd', render: (row) => formatBound(row.price_upper_usd) },
  {
    title: t('asset.pct_change'),
    key: 'pct_change_threshold',
    render: (row) => (row.pct_change_threshold == null ? '—' : `${row.pct_change_threshold}%`),
  },
  {
    title: t('asset.status'),
    key: 'is_active',
    render: (row) =>
      h(NSwitch, {
        value: row.is_active,
        onUpdateValue: (value) => handleToggleActive(row, value),
      }),
  },
  {
    title: t('asset.actions'),
    key: 'actions',
    render: (row) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEditModal(row) }, { default: () => t('common.edit') }),
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => confirmDelete(row) },
            { default: () => t('common.delete') },
          ),
        ],
      }),
  },
])

onMounted(async () => {
  loading.value = true
  try {
    await store.fetchAssets()
  } catch (err) {
    message.error(err.response?.data?.message || t('common.error_generic'))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="asset-view">
    <div class="asset-view__toolbar">
      <n-button type="primary" @click="openCreateModal">{{ t('asset.add') }}</n-button>
    </div>

    <n-data-table
      :columns="columns"
      :data="store.assets"
      :loading="loading"
      :bordered="false"
      :pagination="{ pageSize: 10 }"
    />

    <n-modal
      v-model:show="formVisible"
      preset="card"
      :title="modalTitle"
      style="max-width: 480px"
    >
      <n-alert v-if="formError" type="error" style="margin-bottom: 16px">{{ formError }}</n-alert>
      <n-form ref="formRef" :model="form" :rules="rules" label-placement="top">
        <n-form-item :label="t('asset.type')" path="assetType">
          <n-select
            v-model:value="form.assetType"
            :options="assetTypeOptions"
            @update:value="handleAssetTypeChange"
          />
        </n-form-item>
        <n-form-item :label="t('asset.symbol')" path="assetSymbol">
          <n-select
            v-model:value="form.assetSymbol"
            :options="symbolOptions"
            :disabled="!form.assetType"
            filterable
            :placeholder="form.assetType ? t('asset.symbol_select_placeholder') : t('asset.symbol_placeholder_disabled')"
          />
        </n-form-item>
        <n-form-item :label="t('asset.alert_type')" path="alertType">
          <n-select v-model:value="form.alertType" :options="alertTypeOptions" />
        </n-form-item>
        <n-form-item :label="t('asset.lower_bound')">
          <n-input-number v-model:value="form.priceLowerUsd" style="width: 100%" :min="0" clearable />
        </n-form-item>
        <n-form-item :label="t('asset.upper_bound')">
          <n-input-number v-model:value="form.priceUpperUsd" style="width: 100%" :min="0" clearable />
        </n-form-item>
        <n-form-item :label="t('asset.pct_change')">
          <n-input-number v-model:value="form.pctChangeThreshold" style="width: 100%" :min="0" clearable />
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
.asset-view__toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 16px;
}
</style>
