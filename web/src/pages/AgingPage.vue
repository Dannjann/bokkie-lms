<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ReportShell from '@/components/ReportShell.vue'
import { ApiError } from '@/api/client'
import { getAging, type Aging } from '@/api/reports'
import { formatPesos, toISODate } from '@/format'

const report = ref<Aging | null>(null)
const loading = ref(true)
const error = ref('')
const asOf = ref(new Date())

async function run(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    report.value = await getAging(toISODate(asOf.value))
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
    title="Aging"
    description="How long the money still out has been out. Payments count against the oldest advance first."
    mode="as-of"
    v-model:as-of="asOf"
    :loading="loading"
    :error="error"
    :rows="report?.rows.length ?? 0"
    @refresh="run"
    @update:as-of="run"
  >
    <template #empty>Nothing was outstanding on this day.</template>

    <table v-if="report" class="report__table">
      <thead>
        <tr>
          <th class="report__code">Code</th>
          <th>Farmer</th>
          <!--
            Six columns of figures and a name did not fit the 1024px window the
            station runs, and the last one was cut off the right edge — which on
            a report is worse than not showing it at all, because nothing says
            it is missing. The per-row principal was the column to drop: it is
            the four buckets beside it added up, and the eye cannot use both.
          -->
          <th class="report__amount">0–30 days</th>
          <th class="report__amount">31–60</th>
          <th class="report__amount">61–90</th>
          <th class="report__amount">Over 90</th>
          <!--
            Interest sits outside the buckets on purpose: it is charged against
            a balance rather than lent on a day, so it has no age the ledger can
            support.
          -->
          <th class="report__amount">Interest</th>
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
          <td class="report__amount numeric">{{ formatPesos(row.current) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.days_60) }}</td>
          <td class="report__amount numeric">{{ formatPesos(row.days_90) }}</td>
          <td class="report__amount numeric" :class="{ 'aging__old': row.over_90 !== '0.00' }">
            {{ formatPesos(row.over_90) }}
          </td>
          <td class="report__amount numeric report__muted">{{ formatPesos(row.interest) }}</td>
        </tr>
      </tbody>
      <tfoot>
        <tr class="report__totals">
          <!--
            The outstanding principal is said here rather than given a column of
            its own: it is the four bucket totals beside it added up, and it is
            the one figure worth stating in words.
          -->
          <td colspan="2">
            {{ report.rows.length }} farmers ·
            <span class="numeric">{{ formatPesos(report.principal) }}</span> outstanding
          </td>
          <td class="report__amount numeric">{{ formatPesos(report.current) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.days_60) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.days_90) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.over_90) }}</td>
          <td class="report__amount numeric">{{ formatPesos(report.interest) }}</td>
        </tr>
      </tfoot>
    </table>
  </ReportShell>
</template>

<style scoped>
@import '@/styles/report-table.css';

/* The one column worth the eye going to first. */
.aging__old {
  color: var(--p-red-600);
  font-weight: 600;
}

@media print {
  .aging__old {
    color: #000;
    font-weight: 700;
  }
}
</style>
