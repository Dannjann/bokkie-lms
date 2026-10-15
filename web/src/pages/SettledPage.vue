<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ReportShell from '@/components/ReportShell.vue'
import { ApiError } from '@/api/client'
import { getSettled, type Settled } from '@/api/reports'
import { balanceLabel, inCredit } from '@/api/settlements'
import { formatPesos, ratePercent, toISODate } from '@/format'

const report = ref<Settled | null>(null)
const loading = ref(true)
const error = ref('')

const now = new Date()
const from = ref(new Date(now.getFullYear(), 0, 1))
const to = ref(new Date())

async function run(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    report.value = await getSettled(toISODate(from.value), toISODate(to.value))
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    report.value = null
  } finally {
    loading.value = false
  }
}

onMounted(run)
</script>

<template>
  <ReportShell
    title="Settled"
    description="Every settlement in the period — open one to read it back or reprint it."
    mode="period"
    v-model:from="from"
    v-model:to="to"
    :loading="loading"
    :error="error"
    :rows="report?.rows.length ?? 0"
    @refresh="run"
  >
    <template #empty>Nobody was settled over this period.</template>

    <table v-if="report" class="report__table">
      <thead>
        <tr>
          <th class="report__code">No.</th>
          <th class="report__code">Code</th>
          <th>Farmer</th>
          <th class="report__code">Cut-off</th>
          <th class="report__amount">Rate</th>
          <th class="report__amount">Interest charged</th>
          <th class="report__amount">Paid to interest</th>
          <th class="report__amount">Paid to principal</th>
          <th class="report__amount">Balance after</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in report.rows" :key="row.settlement_id">
          <td class="report__code numeric">
            <RouterLink :to="{ name: 'settlement', params: { id: row.settlement_id } }">
              {{ row.settlement_no }}
            </RouterLink>
          </td>
          <td class="report__code numeric">{{ row.code }}</td>
          <td>
            <RouterLink :to="{ name: 'customer', params: { id: row.customer_id } }">
              {{ row.name }}
            </RouterLink>
          </td>
          <td class="report__code numeric">{{ row.cutoff }}</td>
          <td class="report__amount numeric">{{ ratePercent(row.rate_bps) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.interest_charged) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.paid_interest) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.paid_principal) }}</td>
          <td
            class="report__amount numeric"
            :class="{ settled__credit: inCredit(row.balance_after) }"
          >
            {{ balanceLabel(row.balance_after) }}
          </td>
        </tr>
      </tbody>
      <tfoot>
        <tr class="report__totals">
          <td colspan="9">
            {{ report.rows.length }}
            {{ report.rows.length === 1 ? 'settlement' : 'settlements' }}
          </td>
        </tr>
      </tfoot>
    </table>
  </ReportShell>
</template>

<style scoped>
@import '@/styles/report-table.css';

.settled__credit {
  color: var(--p-green-700);
  font-weight: 600;
}
</style>
