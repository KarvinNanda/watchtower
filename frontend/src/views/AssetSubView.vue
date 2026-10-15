<script setup>
import { ref, computed, onMounted, h } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NForm, NFormItem, NInputNumber, NModal, NSelect, NSpace, NSwitch, NTag, NText, useDialog,
} from 'naive-ui'

import { useSubscriptionsStore } from '@/stores/subscriptions'
import RunNowButton from '@/components/runs/RunNowButton.vue'
import { message } from '@/plugins/naive'
import { ASSET_OPTIONS } from '@/constants/assets'
import {
  RULE_TYPES, INTENTS, emptyRuleForm, ruleToForm, validateRuleForm, buildRulePayload,
  ruleSummary, ruleState, sortRulesBySymbol, formatUSD,
} from '@/utils/assetRules'

const { t, te } = useI18n()
const store = useSubscriptionsStore()
const dialog = useDialog()

const loading = ref(false)
const formVisible = ref(false)
const submitting = ref(false)
const editingId = ref(null)
const formRef = ref(null)
const formError = ref('')
const form = ref(emptyRuleForm())
// Field-level validation error, shown under the field it belongs to.
const fieldError = ref({ field: '', key: '' })

const ERROR_FIELD = {
  'asset.rules.error_level': 'level',
  'asset.rules.error_pct': 'pct',
  'asset.rules.error_pct_trailing': 'pct',
  'asset.rules.error_direction': 'direction',
}

function feedbackFor(field) {
  return fieldError.value.field === field ? t(fieldError.value.key) : undefined
}

function statusFor(field) {
  return fieldError.value.field === field ? 'error' : undefined
}

const assetTypeOptions = computed(() => [
  { label: t('asset.type_stock'), value: 'stock' },
  { label: t('asset.type_crypto'), value: 'crypto' },
  { label: t('asset.type_gold'), value: 'gold' },
])
const symbolOptions = computed(() => ASSET_OPTIONS[form.value.assetType] || [])
const ruleTypeOptions = computed(() =>
  RULE_TYPES.filter((rt) => !(rt === 'week52_break' && form.value.assetType === 'gold'))
    .map((rt) => ({ label: t(`asset.rules.type_${rt}`), value: rt })),
)
const intentOptions = computed(() => INTENTS.map((v) => ({ label: t(`asset.rules.intent_${v}`), value: v })))
const directionOptions = computed(() =>
  form.value.ruleType === 'week52_break'
    ? [{ label: t('asset.rules.week52_above'), value: 'above' }, { label: t('asset.rules.week52_below'), value: 'below' }]
    : [{ label: t('asset.rules.dir_above'), value: 'above' }, { label: t('asset.rules.dir_below'), value: 'below' }],
)

const isLevel = computed(() => form.value.ruleType === 'price_level')
const showDirection = computed(
  () => (isLevel.value && form.value.intent === 'none') || form.value.ruleType === 'week52_break',
)
const showPct = computed(() => ['pct_change_24h', 'trailing_stop'].includes(form.value.ruleType))
const rows = computed(() => sortRulesBySymbol(store.assets))

const formRules = {
  assetType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  assetSymbol: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  ruleType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
}

const modalTitle = computed(() => (editingId.value ? t('asset.edit_title') : t('asset.add_title')))

function handleAssetTypeChange() {
  form.value.assetSymbol = null
  if (form.value.assetType === 'gold' && form.value.ruleType === 'week52_break') form.value.ruleType = null
}

function handleRuleTypeChange() {
  form.value.direction = null
  form.value.intent = 'none'
}

function openCreateModal() {
  editingId.value = null
  form.value = emptyRuleForm()
  formError.value = ''
  fieldError.value = { field: '', key: '' }
  formVisible.value = true
}

function openEditModal(row) {
  editingId.value = row.id
  form.value = ruleToForm(row)
  formError.value = ''
  fieldError.value = { field: '', key: '' }
  formVisible.value = true
}

function apiErrorMessage(err) {
  const code = err.response?.data?.error
  const key = `asset.rules.error_${code}`
  if (code && te(key)) return t(key)
  return err.response?.data?.message || t('common.error_generic')
}

async function handleSubmit() {
  formError.value = ''
  fieldError.value = { field: '', key: '' }
  try {
    await formRef.value?.validate()
  } catch {
    return
  }
  const errorKey = validateRuleForm(form.value)
  if (errorKey) {
    if (ERROR_FIELD[errorKey]) fieldError.value = { field: ERROR_FIELD[errorKey], key: errorKey }
    else formError.value = t(errorKey)
    return
  }

  submitting.value = true
  try {
    const payload = buildRulePayload(form.value)
    if (editingId.value) {
      await store.updateAsset(editingId.value, payload)
      message.success(t('asset.updated_success'))
    } else {
      await store.createAsset(payload)
      message.success(t('asset.created_success'))
    }
    formVisible.value = false
  } catch (err) {
    formError.value = apiErrorMessage(err)
  } finally {
    submitting.value = false
  }
}

function confirmDelete(row) {
  dialog.warning({
    title: t('asset.delete_confirm_title'),
    content: `${row.asset_symbol} — ${ruleSummary(row, t)}`,
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await store.deleteAsset(row.id)
        message.success(t('asset.deleted_success'))
      } catch (err) {
        message.error(apiErrorMessage(err))
      }
    },
  })
}

async function handleToggleActive(row, value) {
  try {
    await store.updateAsset(row.id, { is_active: value })
    message.success(t('asset.toggle_success'))
  } catch (err) {
    message.error(apiErrorMessage(err))
  }
}

async function handleRearm(row) {
  try {
    await store.rearmAsset(row.id)
    message.success(t('asset.rules.rearm_success'))
  } catch (err) {
    message.error(apiErrorMessage(err))
  }
}

function renderDetail(row) {
  if (ruleState(row) === 'unsupported') {
    // Text, not color alone, and the reason on hover/focus.
    return h(
      NTag,
      { type: 'error', size: 'small', round: true, title: t('asset.rules.unsupported_hint') },
      { default: () => t('asset.rules.unsupported') },
    )
  }
  if (row.rule_type !== 'trailing_stop') return '—'
  if (ruleState(row) === 'fired') {
    const date = new Date(row.triggered_at).toLocaleString()
    return h(NTag, { type: 'warning', size: 'small', round: true }, {
      default: () => t('asset.rules.fired', { price: formatUSD(row.triggered_price_usd), date }),
    })
  }
  return row.peak_price_usd == null
    ? t('asset.rules.peak_pending')
    : t('asset.rules.peak', { price: formatUSD(row.peak_price_usd) })
}

function renderStatus(row) {
  if (ruleState(row) === 'fired') {
    return h(NButton, { size: 'small', type: 'warning', onClick: () => handleRearm(row) }, { default: () => t('asset.rules.rearm') })
  }
  return h(NSwitch, {
    value: row.is_active,
    'aria-label': t('asset.status'),
    onUpdateValue: (value) => handleToggleActive(row, value),
  })
}

const columns = computed(() => [
  { title: t('asset.symbol'), key: 'asset_symbol' },
  {
    title: t('asset.type'),
    key: 'asset_type',
    render: (row) => assetTypeOptions.value.find((o) => o.value === row.asset_type)?.label || row.asset_type,
  },
  { title: t('asset.rules.rule'), key: 'rule', render: (row) => ruleSummary(row, t) },
  { title: t('asset.rules.detail'), key: 'detail', render: renderDetail },
  { title: t('asset.status'), key: 'is_active', render: renderStatus },
  {
    title: t('asset.actions'),
    key: 'actions',
    render: (row) =>
      h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', onClick: () => openEditModal(row) }, { default: () => t('common.edit') }),
          h(NButton, { size: 'small', type: 'error', onClick: () => confirmDelete(row) }, { default: () => t('common.delete') }),
        ],
      }),
  },
])

onMounted(async () => {
  loading.value = true
  try {
    await store.fetchAssets()
  } catch (err) {
    message.error(apiErrorMessage(err))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="asset-view">
    <div class="asset-view__toolbar">
      <run-now-button type="asset" />
      <n-button type="primary" @click="openCreateModal">{{ t('asset.add') }}</n-button>
    </div>
    <n-text v-if="store.assetIntervalHours" depth="3" class="asset-view__hint">
      {{ t('asset.rules.help_interval', { hours: store.assetIntervalHours }) }}
    </n-text>

    <n-data-table :columns="columns" :data="rows" :loading="loading" :bordered="false" :pagination="{ pageSize: 10 }" />

    <n-modal v-model:show="formVisible" preset="card" :title="modalTitle" style="max-width: 480px">
      <n-alert v-if="formError" type="error" role="alert" style="margin-bottom: 16px">{{ formError }}</n-alert>
      <n-form ref="formRef" :model="form" :rules="formRules" label-placement="top">
        <n-form-item :label="t('asset.type')" path="assetType">
          <n-select v-model:value="form.assetType" :options="assetTypeOptions" @update:value="handleAssetTypeChange" />
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
        <n-form-item :label="t('asset.rules.rule_type')" path="ruleType">
          <n-select v-model:value="form.ruleType" :options="ruleTypeOptions" @update:value="handleRuleTypeChange" />
        </n-form-item>

        <n-form-item v-if="isLevel" :label="t('asset.rules.intent')">
          <n-select v-model:value="form.intent" :options="intentOptions" />
        </n-form-item>
        <n-alert v-if="isLevel" type="info" :show-icon="false" style="margin-bottom: 16px">
          {{ t('asset.rules.help_intent') }}
        </n-alert>

        <n-form-item
          v-if="showDirection"
          :label="t('asset.rules.direction')"
          :feedback="feedbackFor('direction')"
          :validation-status="statusFor('direction')"
        >
          <n-select v-model:value="form.direction" :options="directionOptions" />
        </n-form-item>

        <n-form-item
          v-if="isLevel"
          :label="t('asset.rules.level')"
          :feedback="feedbackFor('level')"
          :validation-status="statusFor('level')"
        >
          <n-input-number v-model:value="form.levelUsd" style="width: 100%" :min="0" :step="0.01" clearable />
        </n-form-item>

        <n-form-item
          v-if="showPct"
          :label="t('asset.rules.pct')"
          :feedback="feedbackFor('pct')"
          :validation-status="statusFor('pct')"
        >
          <n-input-number
            v-model:value="form.pct"
            style="width: 100%"
            :min="0"
            :max="form.ruleType === 'trailing_stop' ? 99.99 : 100"
            :step="0.1"
            :precision="2"
            clearable
          />
        </n-form-item>
        <n-text v-if="form.ruleType === 'trailing_stop'" depth="3" tag="p">{{ t('asset.rules.help_trailing') }}</n-text>

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
  gap: 8px;
  justify-content: flex-end;
  margin-bottom: 8px;
}
.asset-view__hint {
  display: block;
  margin-bottom: 16px;
}
</style>
