<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  listPayments,
  methodLabel,
  paymentStatusLabel,
  type PaymentStatus,
  type PaymentSummary,
} from '@/api/payments'
import { formatPesos } from '@/format'

const router = useRouter()

const payments = ref<PaymentSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const search = ref('')
const status = ref<PaymentStatus | 'all'>('all')
const first = ref(0)
const perPage = ref(25)

// All, and not posted: an unfinished draft is something somebody needs to see
// on opening this screen, not something to go looking for.
const statusOptions = [
  { label: 'All', value: 'all' },
  { label: 'Drafts', value: 'draft' },
  { label: 'Recorded or liquidated', value: 'posted' },
  { label: 'Voided', value: 'voided' },
]

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listPayments({
      q: search.value.trim(),
      status: status.value === 'all' ? undefined : status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    payments.value = page.payments
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    payments.value = []
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

function open(payment: PaymentSummary): void {
  router.push({ name: 'payment', params: { id: payment.id } })
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Payments"
      description="A payment is numbered when it is recorded and applied when the farmer is liquidated. It is undone by voiding, never by editing."
    >
      <template #actions>
        <Button label="New payment" icon="pi pi-plus" @click="router.push({ name: 'payment-new' })" />
      </template>
    </PageHeader>

    <Message v-if="error" severity="error" :closable="false" class="payments__error">
      {{ error }}
    </Message>

    <div class="card">
      <div class="payments__filters">
        <span class="payments__search">
          <i class="pi pi-search payments__search-icon" aria-hidden="true" />
          <InputText
            v-model="search"
            placeholder="Search by OR No. or farmer"
            aria-label="Search payments"
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
          class="payments__status"
        />
        <span class="payments__count numeric">
          {{ total }} {{ total === 1 ? 'payment' : 'payments' }}
        </span>
      </div>

      <DataTable
        :value="payments"
        :loading="loading"
        lazy
        :paginator="total > 25"
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="id"
        selection-mode="single"
        class="payments__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="payments__empty">
            {{ search ? `Nothing matches “${search}”.` : 'No payments yet.' }}
          </p>
        </template>

        <Column header="OR No." class="payments__or">
          <template #body="{ data }">
            <span v-if="data.or_no">{{ data.or_no }}</span>
            <span v-else class="payments__unnumbered">Draft</span>
          </template>
        </Column>
        <Column field="date" header="Date" class="payments__date" />
        <Column header="Customer">
          <template #body="{ data }">{{ data.customer_code }} — {{ data.customer_name }}</template>
        </Column>
        <Column header="Method" class="payments__method">
          <template #body="{ data }">{{ methodLabel(data.method) }}</template>
        </Column>
        <Column header="Amount" class="payments__amount">
          <template #body="{ data }">{{ formatPesos(data.amount) }}</template>
        </Column>
        <Column header="Status" class="payments__status-cell">
          <template #body="{ data }">
            <Tag v-if="data.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="data.status === 'voided'" severity="danger" value="Voided" />
            <RouterLink
              v-else-if="data.settlement_no > 0"
              :to="{ name: 'settlement', params: { id: data.settlement_id } }"
              @click.stop
            >
              <Tag severity="success" :value="`Liquidated (No. ${data.settlement_no})`" />
            </RouterLink>
            <Tag v-else severity="info" :value="paymentStatusLabel(data.status, 0)" />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.payments__error {
  margin-bottom: 1rem;
}

.payments__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.payments__search {
  position: relative;
  flex: 1;
  min-width: 12rem;
  max-width: 26rem;
}

.payments__search-icon {
  position: absolute;
  top: 50%;
  left: 0.65rem;
  transform: translateY(-50%);
  font-size: 0.8125rem;
  color: var(--p-surface-400);
  pointer-events: none;
}

.payments__search :deep(input) {
  padding-left: 2rem;
}

.payments__status {
  min-width: 9rem;
}

.payments__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

/* The OR No. is what people say out loud and write on the receipt, so it is the
   column the eye should land on first. */
.payments__table :deep(.payments__or) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
}

.payments__unnumbered {
  color: var(--shell-muted);
}

.payments__table :deep(.payments__date),
.payments__table :deep(.payments__method) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.payments__table :deep(.payments__amount) {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.payments__table :deep(.payments__status-cell) {
  width: 1%;
  white-space: nowrap;
}

.payments__table :deep(tbody tr) {
  cursor: pointer;
}

.payments__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.payments__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.payments__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .payments__filters {
    flex-wrap: wrap;
  }

  .payments__search {
    max-width: none;
    flex-basis: 100%;
  }

  .payments__count {
    margin-left: 0;
  }
}
</style>
