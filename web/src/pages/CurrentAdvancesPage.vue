<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ReportShell from '@/components/ReportShell.vue'
import { ApiError } from '@/api/client'
import { getCurrentAdvances, type CurrentAdvances } from '@/api/reports'
import { formatPesos, toISODate } from '@/format'

const report = ref<CurrentAdvances | null>(null)
const loading = ref(true)
const error = ref('')
const asOf = ref(new Date())

async function run(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    report.value = await getCurrentAdvances(toISODate(asOf.value))
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
    title="Current Advances"
    description="Who owes money, and how much, on a given day."
    mode="as-of"
    v-model:as-of="asOf"
    :loading="loading"
    :error="error"
    :rows="report?.rows.length ?? 0"
    @refresh="run"
    @update:as-of="run"
  >
    <template #empty>Nobody owed anything on this day.</template>

    <table v-if="report" class="report__table">
      <thead>
        <tr>
          <th class="report__code">Code</th>
          <th>Farmer</th>
          <th class="report__amount">Principal</th>
          <th class="report__amount">Interest</th>
          <th class="report__amount">Balance</th>
          <th class="report__code">Last activity</th>
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
          <td class="report__amount numeric">{{ formatPesos(row.principal) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.interest) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.total) }}</td>
          <td class="report__code numeric">{{ row.last_activity }}</td>
        </tr>
      </tbody>
      <tfoot>
        <tr class="report__totals">
          <td colspan="2">{{ report.rows.length }} farmers</td>
          <td class="report__amount numeric">{{ formatPesos(report.principal) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.interest) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.total) }}</td>
          <td />
        </tr>
      </tfoot>
    </table>
  </ReportShell>
</template>

<style scoped>
@import '@/styles/report-table.css';
</style>
