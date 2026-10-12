<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ReportShell from '@/components/ReportShell.vue'
import { ApiError } from '@/api/client'
import { getLiquidated, type Liquidated } from '@/api/reports'
import { formatPesos, toISODate } from '@/format'

const report = ref<Liquidated | null>(null)
const loading = ref(true)
const error = ref('')

const now = new Date()
const from = ref(new Date(now.getFullYear(), 0, 1))
const to = ref(new Date())

async function run(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    report.value = await getLiquidated(toISODate(from.value), toISODate(to.value))
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
    title="Liquidated"
    description="Farmers who finished paying, and the day they did. Reading only — nothing here changes a balance."
    mode="period"
    v-model:from="from"
    v-model:to="to"
    :loading="loading"
    :error="error"
    :rows="report?.rows.length ?? 0"
    @refresh="run"
  >
    <template #empty>Nobody finished paying over this period.</template>

    <table v-if="report" class="report__table">
      <thead>
        <tr>
          <th class="report__code">Code</th>
          <th>Farmer</th>
          <th class="report__code">Settled</th>
          <th class="report__amount">Advanced in total</th>
          <th class="report__code" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in report.rows" :key="row.customer_id">
          <td class="report__code numeric">{{ row.code }}</td>
          <td>
            <RouterLink :to="{ name: 'customer', params: { id: row.customer_id } }">
              {{ row.name }}
            </RouterLink>
          </td>
          <td class="report__code numeric">{{ row.settled_on }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.advanced) }}</td>
          <td class="report__code liquidated__link">
            <RouterLink :to="{ name: 'statement', params: { id: row.customer_id } }">
              Statement
            </RouterLink>
          </td>
        </tr>
      </tbody>
      <tfoot>
        <tr class="report__totals">
          <td colspan="3">
            {{ report.rows.length }} {{ report.rows.length === 1 ? 'farmer' : 'farmers' }}
          </td>
          <td />
          <td />
        </tr>
      </tfoot>
    </table>
  </ReportShell>
</template>

<style scoped>
@import '@/styles/report-table.css';

/* A link to a screen is no use on paper. */
@media print {
  .liquidated__link {
    display: none;
  }
}
</style>
