<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useNow } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NEmpty, NForm, NFormItem, NInputNumber, NModal, NRadioButton, NRadioGroup, NSelect, NSpace, NSpin, NText, useDialog,
} from 'naive-ui'

import { useSubscriptionsStore } from '@/stores/subscriptions'
import { useMarketStore } from '@/stores/market'
import RunNowButton from '@/components/runs/RunNowButton.vue'
import RunResultPanel from '@/components/runs/RunResultPanel.vue'
import SymbolRuleCard from '@/components/assets/SymbolRuleCard.vue'
import { message } from '@/plugins/naive'
import { ASSET_OPTIONS } from '@/constants/assets'
import {
  RULE_TYPES, INTENTS, emptyRuleForm, ruleToForm, validateRuleForm, buildRulePayload, ruleSummary,
  setCurrency, changeAssetType, formatLevelInput, parseLevelInput,
} from '@/utils/assetRules'
import { groupRulesBySymbol } from '@/utils/groupRules'
import { runChangedRuleIds } from '@/utils/runHighlight'

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

const marketStore = useMarketStore()
const rulesError = ref('')
// Last Check now result, shown until closed, and the rules it changed.
const runResult = ref(null)
const highlightIds = ref(new Set())

const groups = computed(() => groupRulesBySymbol(store.assets, marketStore.snapshot))
// One ticking clock for every card's "Updated … ago" label.
const now = useNow({ interval: 60_000 })

const ERROR_FIELD = {
  'asset.rules.error_level': 'level',
  'asset.rules.error_level_idr': 'level',
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
// The rate note only matters for a converted price (not gold's own rupiah).
const showRateNote = computed(() => isLevel.value && form.value.currency === 'IDR' && form.value.assetType !== 'gold')

function handleCurrencyChange(currency) {
  form.value = setCurrency(form.value, currency)
}
const showDirection = computed(
  () => (isLevel.value && form.value.intent === 'none') || form.value.ruleType === 'week52_break',
)
const showPct = computed(() => ['pct_change_24h', 'trailing_stop'].includes(form.value.ruleType))

const formRules = {
  assetType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  assetSymbol: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
  ruleType: { required: true, message: () => t('common.required_field'), trigger: ['change', 'blur'] },
}

const modalTitle = computed(() => (editingId.value ? t('asset.edit_title') : t('asset.add_title')))

// Not v-model: the handler needs the old type to tell a real change from
// re-picking the same one.
function handleAssetTypeChange(assetType) {
  form.value = changeAssetType(form.value, assetType)
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
      // A new symbol shows its price at once if one is already cached.
      marketStore.fetchSnapshot().catch(() => {})
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

// Rules and prices load in parallel. A failed snapshot only hides prices;
// the rules still render (Spec 4a, section 6).
async function loadAll() {
  loading.value = true
  rulesError.value = ''
  const [rules] = await Promise.allSettled([store.fetchAssets(), marketStore.fetchSnapshot()])
  if (rules.status === 'rejected') rulesError.value = apiErrorMessage(rules.reason)
  loading.value = false
}

function handleRunStarted() {
  runResult.value = null
  highlightIds.value = new Set()
}

async function handleRunFinished(result) {
  runResult.value = result
  // A report changes no rule state: nothing to reload or highlight (#7).
  if (result.status !== 'done' || result.type === 'asset_report') return
  await loadAll()
  if (!rulesError.value) highlightIds.value = runChangedRuleIds(store.assets, result.run)
}

function closeRunResult() {
  runResult.value = null
  highlightIds.value = new Set()
}

onMounted(() => {
  loadAll()
  marketStore.startAutoRefresh()
})
onUnmounted(() => marketStore.stopAutoRefresh())
</script>

<template>
  <div class="asset-view">
    <div class="asset-view__toolbar">
      <run-now-button type="asset_report" @started="handleRunStarted" @finished="handleRunFinished" />
      <run-now-button type="asset" @started="handleRunStarted" @finished="handleRunFinished" />
      <n-button type="primary" @click="openCreateModal">{{ t('asset.add') }}</n-button>
    </div>
    <n-text v-if="store.assetIntervalHours" depth="3" class="asset-view__hint">
      {{ t('asset.rules.help_interval', { hours: store.assetIntervalHours }) }}
    </n-text>

    <run-result-panel :result="runResult" @close="closeRunResult" />

    <n-alert v-if="rulesError" type="error" role="alert" class="asset-view__error">
      {{ t('asset.load_error') }} {{ rulesError }}
      <n-button size="small" @click="loadAll">{{ t('common.retry') }}</n-button>
    </n-alert>

    <n-spin :show="loading">
      <n-empty v-if="!loading && !rulesError && !groups.length" :description="t('asset.empty')" />
      <symbol-rule-card
        v-for="group in groups"
        :key="group.symbol"
        :group="group"
        :highlight-ids="highlightIds"
        :stale-after-hours="store.assetIntervalHours"
        :now="now.getTime()"
        @edit="openEditModal"
        @delete="confirmDelete"
        @toggle="handleToggleActive"
        @rearm="handleRearm"
      />
    </n-spin>

    <n-modal v-model:show="formVisible" preset="card" :title="modalTitle" style="max-width: 480px">
      <n-alert v-if="formError" type="error" role="alert" style="margin-bottom: 16px">{{ formError }}</n-alert>
      <n-form ref="formRef" :model="form" :rules="formRules" label-placement="top">
        <n-form-item :label="t('asset.type')" path="assetType">
          <n-select :value="form.assetType" :options="assetTypeOptions" @update:value="handleAssetTypeChange" />
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
          <div class="asset-view__level">
            <n-radio-group
              :value="form.currency"
              size="small"
              :aria-label="t('asset.rules.currency')"
              @update:value="handleCurrencyChange"
            >
              <n-radio-button value="IDR">IDR</n-radio-button>
              <n-radio-button value="USD">USD</n-radio-button>
            </n-radio-group>
            <n-input-number
              v-model:value="form.level"
              class="asset-view__level-input wt-num"
              :min="0"
              :step="form.currency === 'IDR' ? 1000 : 0.01"
              :format="form.currency === 'IDR' ? formatLevelInput : undefined"
              :parse="form.currency === 'IDR' ? parseLevelInput : undefined"
              :input-props="showRateNote ? { 'aria-describedby': 'asset-rate-note' } : undefined"
              clearable
            />
          </div>
        </n-form-item>
        <n-text v-if="showRateNote" id="asset-rate-note" depth="3" tag="p" class="asset-view__rate-note">
          {{ t('asset.rules.idr_rate_note') }}
        </n-text>

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
  flex-wrap: wrap;
  gap: 8px;
  justify-content: flex-end;
  margin-bottom: 8px;
}
.asset-view__hint {
  display: block;
  margin-bottom: 16px;
}
.asset-view__error {
  margin-bottom: var(--wt-space-lg);
}
.asset-view__level {
  display: flex;
  gap: var(--wt-space-sm);
  width: 100%;
  align-items: center;
}
.asset-view__level-input {
  flex: 1;
}
.asset-view__rate-note {
  margin: calc(-1 * var(--wt-space-sm)) 0 var(--wt-space-lg);
  font-size: 12px;
}
</style>
