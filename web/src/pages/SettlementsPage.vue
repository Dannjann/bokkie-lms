<script setup lang="ts">
/**
 * Every settlement, newest first.
 *
 * A settlement is one farmer settled up to a cut-off date — the legacy
 * "Compute Percentage" screen — and it is counter work, so this list is open
 * to encoders. The button that matters is "Settle a farmer".
 */
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  listSettlements,
  settlementStatusLabel,
  type SettlementStatus,
  type SettlementSummary,
} from '@/api/settlements'
import { formatPesos, ratePercent } from '@/format'

const router = useRouter()

const settlements = ref<SettlementSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const search = ref('')
const status = ref<SettlementStatus | 'all'>('all')
const first = ref(0)
const perPage = ref(25)

const statusOptions = [
  { label: 'All', value: 'all' },
  { label: 'Drafts', value: 'draft' },
  { label: 'Settled', value: 'posted' },
  { label: 'Voided', value: 'voided' },
]

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listSettlements({
      q: search.value.trim(),
      status: status.value === 'all' ? undefined : status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    settlements.value = page.settlements
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    settlements.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

let debounce: ReturnType<typeof setTimeout> | undefined

watch([search, status], () => {
  first.value = 0
  clearTimeout(debounce)
  debounce = setTimeout(load, 200)
})

function onPage(event: DataTablePageEvent): void {
  first.value = event.first
  perPage.value = event.rows
  load()
}

function open(row: SettlementSummary): void {
  router.push({ name: 'settlement', params: { id: row.id } })
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Settlements"
      description="A farmer is settled when he delivers: interest up to a cut-off date, then his recorded payments applied, interest first."
    >
      <template #actions>
        <Button
          label="Settle a farmer"
          icon="pi pi-calculator"
          @click="router.push({ name: 'settlement-new' })"
        />
      </template>
    </PageHeader>

    <Message v-if="error" severity="error" :closable="false" class="settlements__error">
      {{ error }}
    </Message>

    <div class="card">
      <div class="settlements__filters">
        <span class="settlements__search">
          <i class="pi pi-search settlements__search-icon" aria-hidden="true" />
          <InputText
            v-model="search"
            placeholder="Search by number or farmer"
            aria-label="Search settlements"
            autocapitalize="none"
            autocorrect="off"
            spellcheck="false"
            fluid
          />
        </span>
        <Select
          v-model="status"
          :options="statusOptions"
          option-label="label"
          option-value="value"
          aria-label="Filter by status"
          class="settlements__status"
        />
        <span class="settlements__count numeric">
          {{ total }} {{ total === 1 ? 'settlement' : 'settlements' }}
        </span>
      </div>

      <DataTable
        :value="settlements"
        :loading="loading"
        lazy
        paginator
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="id"
        selection-mode="single"
        class="settlements__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="settlements__empty">Nobody has been settled yet.</p>
        </template>

        <Column header="No." class="settlements__no">
          <template #body="{ data }">
            <span v-if="data.settlement_no">{{ data.settlement_no }}</span>
            <span v-else class="settlements__muted">—</span>
          </template>
        </Column>
        <Column header="Farmer">
          <template #body="{ data }">{{ data.customer_code }} — {{ data.customer_name }}</template>
        </Column>
        <Column header="Cut-off" class="settlements__date">
          <template #body="{ data }">{{ data.cutoff }}</template>
        </Column>
        <Column header="Rate" class="settlements__amount">
          <template #body="{ data }">{{ ratePercent(data.rate_bps) }}</template>
        </Column>
        <Column header="Interest" class="settlements__amount">
          <template #body="{ data }">{{ formatPesos(data.interest) }}</template>
        </Column>
        <Column header="Paid" class="settlements__amount">
          <template #body="{ data }">{{ formatPesos(data.paid) }}</template>
        </Column>
        <Column header="Status" class="settlements__status-cell">
          <template #body="{ data }">
            <Tag
              :severity="
                data.status === 'draft' ? 'warn' : data.status === 'voided' ? 'danger' : 'success'
              "
              :value="settlementStatusLabel(data.status)"
            />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.settlements__error {
  margin-bottom: 1rem;
}

.settlements__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.settlements__search {
  position: relative;
  flex: 1;
  min-width: 12rem;
  max-width: 26rem;
}

.settlements__search-icon {
  position: absolute;
  top: 50%;
  left: 0.65rem;
  transform: translateY(-50%);
  font-size: 0.8125rem;
  color: var(--p-surface-400);
  pointer-events: none;
}

.settlements__search :deep(input) {
  padding-left: 2rem;
}

.settlements__status {
  min-width: 9rem;
}

.settlements__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

.settlements__table :deep(.settlements__no) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
}

.settlements__muted {
  color: var(--shell-muted);
}

.settlements__table :deep(.settlements__date) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.settlements__table :deep(.settlements__amount) {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.settlements__table :deep(.settlements__status-cell) {
  width: 1%;
  white-space: nowrap;
}

.settlements__table :deep(tbody tr) {
  cursor: pointer;
}

.settlements__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.settlements__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.settlements__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .settlements__filters {
    flex-wrap: wrap;
  }

  .settlements__search {
    max-width: none;
    flex-basis: 100%;
  }

  .settlements__count {
    margin-left: 0;
  }
}
</style>
