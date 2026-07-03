<script setup>
import { computed } from 'vue'
import { NCard, NTag } from 'naive-ui'

const props = defineProps({
  data: {
    type: Object,
    required: true,
  },
})

const isPositive = computed(() => props.data.change_pct_24h >= 0)

const formattedUSD = computed(() =>
  new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(props.data.price_usd),
)

const formattedIDR = computed(() =>
  new Intl.NumberFormat('id-ID', { style: 'currency', currency: 'IDR', maximumFractionDigits: 0 }).format(
    props.data.price_idr,
  ),
)

const formattedPct = computed(() => {
  const sign = isPositive.value ? '+' : ''
  return `${sign}${props.data.change_pct_24h.toFixed(2)}%`
})
</script>

<template>
  <n-card size="small" :bordered="true" class="market-card">
    <div class="market-card__top">
      <span class="market-card__symbol">{{ data.symbol }}</span>
      <n-tag :type="isPositive ? 'success' : 'error'" size="small" round>
        {{ formattedPct }}
      </n-tag>
    </div>
    <div class="market-card__price-usd">{{ formattedUSD }}</div>
    <div class="market-card__price-idr">{{ formattedIDR }}</div>
  </n-card>
</template>

<style scoped>
.market-card {
  min-width: 160px;
}

.market-card__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.market-card__symbol {
  font-weight: 600;
  font-size: 15px;
  letter-spacing: 0.02em;
}

.market-card__price-usd {
  font-size: 18px;
  font-weight: 700;
}

.market-card__price-idr {
  font-size: 12px;
  opacity: 0.65;
  margin-top: 2px;
}
</style>
