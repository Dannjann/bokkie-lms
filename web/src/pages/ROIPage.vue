<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ReportShell from '@/components/ReportShell.vue'
import { ApiError } from '@/api/client'
import { getROI, type ROI } from '@/api/reports'
import { formatPesos, toISODate } from '@/format'

const report = ref<ROI | null>(null)
const loading = ref(true)
const error = ref('')

// The year so far, which is what somebody opening this without touching a
// picker almost always means.
const now = new Date()
const from = ref(new Date(now.getFullYear(), 0, 1))
const to = ref(new Date())

async function run(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    report.value = await getROI(toISODate(from.value), toISODate(to.value))
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
    title="Return of Investment Status"
    description="What the station put out over a period, what came back at each liquidation, and what is still out."
    mode="period"
    v-model:from="from"
    v-model:to="to"
    :loading="loading"
    :error="error"
    :rows="report?.rows.length ?? 0"
    @refresh="run"
  >
    <template #empty>Nobody was liquidated over this period.</template>

    <template v-if="report">
      <!--
        The portfolio block. Every figure comes off the server; the percentage
        is computed there too, in integers, because a recovery rate arrived at
        by float division is a number nobody can reproduce by hand.
      -->
      <dl class="roi__summary">
        <div class="roi__figure">
          <dt>Capital deployed</dt>
          <dd class="numeric">{{ formatPesos(report.deployed) }}</dd>
          <dd class="roi__note">
            {{ report.vouchers }} {{ report.vouchers === 1 ? 'advance slip' : 'advance slips' }}
          </dd>
        </div>
        <div class="roi__figure">
          <dt>Principal returned</dt>
          <dd class="numeric">{{ formatPesos(report.repaid) }}</dd>
          <dd v-if="report.repaid_share" class="roi__note">
            {{ report.repaid_share }} of what was deployed
          </dd>
        </div>
        <div class="roi__figure">
          <dt>Interest charged</dt>
          <dd class="numeric">{{ formatPesos(report.interest_charged) }}</dd>
          <dd class="roi__note">{{ formatPesos(report.interest_collected) }} paid</dd>
        </div>
        <div class="roi__figure">
          <dt>Still outstanding</dt>
          <dd class="numeric">{{ formatPesos(report.outstanding) }}</dd>
          <dd class="roi__note">the whole book, at the end of the period</dd>
        </div>
      </dl>

      <table class="report__table">
        <thead>
          <tr>
            <th class="report__code">Code</th>
            <th class="report__code">Date</th>
            <th>Customer name</th>
            <th class="report__amount">Principal</th>
            <th class="report__amount">Interest</th>
            <th class="report__amount">Amount</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in report.rows" :key="row.settlement_id">
            <td class="report__code numeric">{{ row.code }}</td>
            <td class="report__code numeric">{{ row.cutoff }}</td>
            <td>
              <RouterLink
                :to="{ name: 'settlement', params: { id: row.settlement_id } }"
                :title="`Liquidation No. ${row.settlement_no}`"
              >
                {{ row.name }}
              </RouterLink>
            </td>
            <td class="report__amount numeric">{{ formatPesos(row.principal) }}</td>
            <td class="report__amount numeric">{{ formatPesos(row.interest) }}</td>
            <td class="report__amount numeric">{{ formatPesos(row.amount) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr class="report__totals">
            <td colspan="3">
              {{ report.farmers }} {{ report.farmers === 1 ? 'farmer' : 'farmers' }} liquidated ·
              Principal returned / Interest paid / Total
            </td>
            <td class="report__amount numeric">{{ formatPesos(report.repaid) }}</td>
            <td class="report__amount numeric">{{ formatPesos(report.interest_collected) }}</td>
            <td class="report__amount numeric">{{ formatPesos(report.returned) }}</td>
          </tr>
        </tfoot>
      </table>
    </template>
  </ReportShell>
</template>

<style scoped>
@import '@/styles/report-table.css';

.roi__summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
  gap: 1rem;
  margin: 0.75rem 0.25rem 1.25rem;
}

.roi__figure dt {
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.roi__figure dd {
  margin: 0.15rem 0 0;
  font-size: 1.25rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.roi__note {
  font-size: 0.8125rem !important;
  font-weight: 400 !important;
  color: var(--shell-muted);
}

@media print {
  .roi__summary {
    grid-template-columns: repeat(4, 1fr);
    margin: 0 0 0.5cm;
  }

  .roi__figure dd {
    font-size: 11pt;
  }

  .roi__note {
    font-size: 8pt !important;
  }
}
</style>
