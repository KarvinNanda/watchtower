<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NIcon, NSwitch, NTag } from 'naive-ui'
import { ArrowDownRight, ArrowUpRight } from '@lucide/vue'

import { formatUSD, formatIDR, formatPct, ruleSummary, ruleState } from '@/utils/assetRules'
import { ruleDistance, trailingStopLevel, NEAR_PCT } from '@/utils/ruleDistance'
import { ruleIcon } from '@/utils/ruleIcons'
import { formatAge, isOlderThanHours } from '@/utils/time'

// One symbol: price header and its rules (Spec 4a, section 3.3).
const props = defineProps({
  group: { type: Object, required: true }, // from groupRulesBySymbol
  highlightIds: { type: Set, default: () => new Set() },
  staleAfterHours: { type: Number, default: null },
  // Current time in ms, ticking (from the page), so the data age stays true
  // while the page is open.
  now: { type: Number, required: true },
})
const emit = defineEmits(['edit', 'delete', 'toggle', 'rearm'])

const { t, locale } = useI18n()

const market = computed(() => props.group.market)
const change = computed(() => market.value?.change_pct_24h ?? null)
const age = computed(() => (market.value ? formatAge(market.value.last_fetched, locale.value, props.now) : ''))
const stale = computed(
  () => !!market.value && isOlderThanHours(market.value.last_fetched, props.staleAfterHours, props.now),
)

function levelText(rule) {
  switch (rule.rule_type) {
    case 'price_level':
      return formatUSD(rule.level_usd)
    case 'trailing_stop': {
      const stop = trailingStopLevel(rule)
      if (stop == null) return t('asset.rules.peak_pending')
      return t('asset.rules.stop_at', { stop: formatUSD(stop), peak: formatUSD(rule.peak_price_usd) })
    }
    case 'pct_change_24h':
      return `±${rule.pct}%`
    default:
      return '—'
  }
}

function distanceText(d) {
  if (!d) return '—'
  if (d.kind === 'change24h') {
    return t('asset.rules.change_vs_threshold', { current: formatPct(d.current), threshold: d.threshold })
  }
  return d.passed ? t('asset.rules.passed') : formatPct(d.pct)
}

const rows = computed(() =>
  props.group.rules.map((rule) => {
    const d = ruleDistance(rule, market.value)
    return {
      rule,
      state: ruleState(rule),
      icon: ruleIcon(rule),
      summary: ruleSummary(rule, t),
      level: levelText(rule),
      distance: distanceText(d),
      near: d?.kind === 'level' && d.near,
      highlighted: props.highlightIds.has(rule.id),
    }
  }),
)

function firedText(rule) {
  return t('asset.rules.fired', {
    price: formatUSD(rule.triggered_price_usd),
    date: new Date(rule.triggered_at).toLocaleString(),
  })
}
</script>

<template>
  <section class="symbol-card" :aria-labelledby="`symbol-${group.symbol}`">
    <header class="symbol-card__head">
      <div class="symbol-card__title">
        <h3 :id="`symbol-${group.symbol}`" class="symbol-card__symbol">{{ group.symbol }}</h3>
        <span class="symbol-card__type">{{ t(`asset.type_${group.assetType}`) }}</span>
      </div>

      <div v-if="market" class="symbol-card__price">
        <span class="symbol-card__usd wt-num">{{ formatUSD(market.price_usd) }}</span>
        <span
          v-if="change != null"
          class="symbol-card__change wt-num"
          :class="change >= 0 ? 'is-up' : 'is-down'"
        >
          <n-icon :component="change >= 0 ? ArrowUpRight : ArrowDownRight" size="14" aria-hidden="true" />
          {{ formatPct(change) }}
        </span>
        <span class="symbol-card__idr wt-num">{{ formatIDR(market.price_idr) }}</span>
        <span
          class="symbol-card__age"
          :class="{ 'is-stale': stale }"
          :title="stale ? t('asset.card.stale_hint', { hours: staleAfterHours }) : undefined"
        >
          {{ t('asset.card.updated', { age }) }}
        </span>
      </div>
      <div v-else class="symbol-card__price symbol-card__price--missing">
        {{ t('asset.card.price_unavailable') }}
      </div>
    </header>

    <div role="table" class="symbol-card__rules" :aria-label="t('asset.card.rules_of', { symbol: group.symbol })">
      <div role="row" class="rule-row rule-row--head">
        <span role="columnheader">{{ t('asset.rules.rule') }}</span>
        <span role="columnheader">{{ t('asset.rules.level') }}</span>
        <span role="columnheader">{{ t('asset.rules.distance') }}</span>
        <span role="columnheader">{{ t('asset.status') }}</span>
        <span role="columnheader">{{ t('asset.actions') }}</span>
      </div>

      <div
        v-for="row in rows"
        :key="row.rule.id"
        role="row"
        class="rule-row"
        :class="{ 'is-highlighted': row.highlighted }"
      >
        <span role="cell" class="rule-row__rule">
          <n-icon :component="row.icon" size="16" aria-hidden="true" />
          <span>{{ row.summary }}</span>
          <n-tag v-if="row.highlighted" size="small" type="warning" round>{{ t('asset.rules.just_now') }}</n-tag>
        </span>
        <span role="cell" class="rule-row__level wt-num">{{ row.level }}</span>
        <span
          role="cell"
          class="rule-row__distance wt-num"
          :class="{ 'is-near': row.near }"
          :title="row.near ? t('asset.rules.near_hint', { pct: NEAR_PCT }) : undefined"
        >
          {{ row.distance }}
        </span>
        <span role="cell" class="rule-row__status">
          <n-tag
            v-if="row.state === 'unsupported'"
            type="error"
            size="small"
            round
            :title="t('asset.rules.unsupported_hint')"
          >
            {{ t('asset.rules.unsupported') }}
          </n-tag>
          <template v-if="row.state === 'fired'">
            <n-tag type="warning" size="small" round>{{ firedText(row.rule) }}</n-tag>
            <n-button size="small" type="warning" @click="emit('rearm', row.rule)">{{ t('asset.rules.rearm') }}</n-button>
          </template>
          <template v-else>
            <n-switch
              :value="row.rule.is_active"
              :aria-label="t('asset.status')"
              @update:value="(value) => emit('toggle', row.rule, value)"
            />
            <span class="rule-row__state">{{ row.rule.is_active ? t('common.active') : t('common.inactive') }}</span>
          </template>
        </span>
        <span role="cell" class="rule-row__actions">
          <n-button size="small" @click="emit('edit', row.rule)">{{ t('common.edit') }}</n-button>
          <n-button size="small" type="error" @click="emit('delete', row.rule)">{{ t('common.delete') }}</n-button>
        </span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.symbol-card {
  background: var(--wt-surface);
  border: 1px solid var(--wt-border);
  border-radius: var(--wt-radius-md);
  margin-bottom: var(--wt-space-lg);
  overflow: hidden;
}

.symbol-card__head {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--wt-space-sm);
  padding: var(--wt-space-md) var(--wt-space-lg);
  background: var(--wt-surface-raised);
}

.symbol-card__title {
  display: flex;
  align-items: baseline;
  gap: var(--wt-space-sm);
}

.symbol-card__symbol {
  margin: 0;
  font-size: 16px;
}

.symbol-card__type,
.symbol-card__idr,
.symbol-card__age,
.symbol-card__price--missing {
  color: var(--wt-text-subtle);
  font-size: 12px;
}

.symbol-card__price {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--wt-space-sm);
}

.symbol-card__usd {
  font-size: 16px;
  font-weight: 600;
}

.symbol-card__change {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.is-up {
  color: var(--wt-up);
}

.is-down {
  color: var(--wt-down);
}

.symbol-card__age.is-stale {
  color: var(--wt-stale);
}

.rule-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1.4fr) minmax(0, 1fr) minmax(0, 1.8fr) auto;
  gap: var(--wt-space-sm);
  align-items: center;
  padding: var(--wt-space-sm) var(--wt-space-lg);
  border-top: 1px solid var(--wt-border);
}

.rule-row--head {
  color: var(--wt-text-subtle);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.rule-row.is-highlighted {
  background: var(--wt-fired-tint);
}

.rule-row__rule,
.rule-row__status,
.rule-row__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--wt-space-sm);
}

.rule-row__distance.is-near {
  color: var(--wt-fired);
  font-weight: 600;
}

.rule-row__state {
  color: var(--wt-text-subtle);
  font-size: 12px;
}

@media (max-width: 767px) {
  .rule-row--head {
    display: none;
  }

  .rule-row {
    grid-template-columns: 1fr auto;
    grid-template-areas:
      'rule status'
      'level distance'
      'actions actions';
    padding: var(--wt-space-md);
  }

  .rule-row__rule {
    grid-area: rule;
  }

  .rule-row__status {
    grid-area: status;
    justify-content: flex-end;
  }

  .rule-row__level {
    grid-area: level;
  }

  .rule-row__distance {
    grid-area: distance;
    text-align: right;
  }

  .rule-row__actions {
    grid-area: actions;
  }

  .rule-row__actions :deep(.n-button),
  .rule-row__status :deep(.n-button) {
    min-height: 44px;
  }
}
</style>
