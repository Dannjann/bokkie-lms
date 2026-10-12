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
import { listAdvances, type AdvanceStatus, type VoucherSummary } from '@/api/advances'
import { formatPesos } from '@/format'

const router = useRouter()

const vouchers = ref<VoucherSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const search = ref('')
const status = ref<AdvanceStatus | 'all'>('all')
const first = ref(0)
const perPage = ref(25)

// All, and not posted: an unfinished draft is something somebody needs to see
// on opening this screen, not something to go looking for.
const statusOptions = [
  { label: 'All', value: 'all' },
  { label: 'Drafts', value: 'draft' },
  { label: 'Posted', value: 'posted' },
  { label: 'Voided', value: 'voided' },
]

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listAdvances({
      q: search.value.trim(),
      status: status.value === 'all' ? undefined : status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    vouchers.value = page.vouchers
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    vouchers.value = []
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

function open(voucher: VoucherSummary): void {
  router.push({ name: 'advance', params: { id: voucher.id } })
}

function who(voucher: VoucherSummary): string {
  if (voucher.customers.length === 0) return '—'
  if (voucher.customers.length === 1) return voucher.customers[0]
  return `${voucher.customers[0]} and ${voucher.customers.length - 1} other${
    voucher.customers.length > 2 ? 's' : ''
  }`
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Advances"
      description="Vouchers are numbered when they are posted, and undone by voiding, never by editing."
    >
      <template #actions>
        <Button label="New voucher" icon="pi pi-plus" @click="router.push({ name: 'advance-new' })" />
      </template>
    </PageHeader>

    <Message v-if="error" severity="error" :closable="false" class="advances__error">
      {{ error }}
    </Message>

    <div class="card">
      <div class="advances__filters">
        <span class="advances__search">
          <i class="pi pi-search advances__search-icon" aria-hidden="true" />
          <InputText
            v-model="search"
            placeholder="Search by Ref. No. or farmer"
            aria-label="Search vouchers"
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
          class="advances__status"
        />
        <span class="advances__count numeric">
          {{ total }} {{ total === 1 ? 'voucher' : 'vouchers' }}
        </span>
      </div>

      <DataTable
        :value="vouchers"
        :loading="loading"
        lazy
        paginator
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="id"
        selection-mode="single"
        class="advances__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="advances__empty">
            {{ search ? `Nothing matches “${search}”.` : 'No vouchers yet.' }}
          </p>
        </template>

        <Column header="Ref. No." class="advances__ref">
          <template #body="{ data }">
            <span v-if="data.ref_no">{{ data.ref_no }}</span>
            <span v-else class="advances__unnumbered">Draft</span>
          </template>
        </Column>
        <Column field="date" header="Date" class="advances__date" />
        <Column header="Customer">
          <template #body="{ data }">{{ who(data) }}</template>
        </Column>
        <Column header="Lines" class="advances__lines">
          <template #body="{ data }">{{ data.line_count }}</template>
        </Column>
        <Column header="Amount" class="advances__amount">
          <template #body="{ data }">{{ formatPesos(data.total) }}</template>
        </Column>
        <Column header="Status" class="advances__status-cell">
          <template #body="{ data }">
            <Tag v-if="data.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="data.status === 'voided'" severity="danger" value="Voided" />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.advances__error {
  margin-bottom: 1rem;
}

.advances__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.advances__search {
  position: relative;
  flex: 1;
  min-width: 12rem;
  max-width: 26rem;
}

.advances__search-icon {
  position: absolute;
  top: 50%;
  left: 0.65rem;
  transform: translateY(-50%);
  font-size: 0.8125rem;
  color: var(--p-surface-400);
  pointer-events: none;
}

.advances__search :deep(input) {
  padding-left: 2rem;
}

.advances__status {
  min-width: 9rem;
}

.advances__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

/* The Ref. No. is what people say out loud and write on the slip, so it is the
   column the eye should land on first. */
.advances__table :deep(.advances__ref) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
}

.advances__unnumbered {
  color: var(--shell-muted);
}

.advances__table :deep(.advances__date),
.advances__table :deep(.advances__lines) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.advances__table :deep(.advances__amount) {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.advances__table :deep(.advances__status-cell) {
  width: 1%;
  white-space: nowrap;
}

.advances__table :deep(tbody tr) {
  cursor: pointer;
}

.advances__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.advances__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.advances__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .advances__filters {
    flex-wrap: wrap;
  }

  .advances__search {
    max-width: none;
    flex-basis: 100%;
  }

  .advances__count {
    margin-left: 0;
  }
}
</style>
