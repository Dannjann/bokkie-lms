<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import DatePicker from 'primevue/datepicker'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { listStatements, type StatementIndexRow } from '@/api/statements'
import type { CustomerStatus } from '@/api/customers'
import { formatPesos, toISODate } from '@/format'

/**
 * Every farmer, with what they owe, as a way into one farmer's statement.
 *
 * Nothing prints from here. A statement is one farmer's history and is printed
 * from their own page; this is the index over those, not a batch of them.
 */

const router = useRouter()

const rows = ref<StatementIndexRow[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const search = ref('')
const status = ref<CustomerStatus>('active')
const asOf = ref<Date>(new Date())
const first = ref(0)
const perPage = ref(25)

const latest = new Date()

const statusOptions = [
  { label: 'Active', value: 'active' },
  { label: 'Deactivated', value: 'inactive' },
  { label: 'All', value: 'all' },
]

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listStatements({
      asOf: toISODate(asOf.value),
      q: search.value.trim(),
      status: status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    rows.value = page.rows
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

// One request per pause in typing, as the customer list does — the station's
// counter PCs are not fast and there is no reason to make them work for letters
// nobody has finished typing.
let debounce: ReturnType<typeof setTimeout> | undefined

watch([search, status, asOf], () => {
  first.value = 0
  clearTimeout(debounce)
  debounce = setTimeout(load, 200)
})

function onPage(event: DataTablePageEvent): void {
  first.value = event.first
  perPage.value = event.rows
  load()
}

function open(row: StatementIndexRow): void {
  router.push({ name: 'statement', params: { id: row.customer.id } })
}

/** Nothing owed is not the same number as something owed; it should not shout. */
function settled(row: StatementIndexRow): boolean {
  return row.balance.total === '0.00'
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Statements"
      description="Find a farmer and read them their history. Balances are as at the date below."
    />

    <Message v-if="error" severity="error" :closable="false" class="statements__banner">
      {{ error }}
    </Message>

    <div class="card">
      <div class="statements__filters">
        <span class="statements__search">
          <i class="pi pi-search statements__search-icon" aria-hidden="true" />
          <InputText
            v-model="search"
            placeholder="Search by code, name or phone"
            aria-label="Search farmers"
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
          class="statements__status"
        />
        <span class="statements__as-of">
          <label for="statements-as-of" class="label">As at</label>
          <DatePicker
            input-id="statements-as-of"
            v-model="asOf"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
          />
        </span>
        <span class="statements__count numeric">
          {{ total }} {{ total === 1 ? 'farmer' : 'farmers' }}
        </span>
      </div>

      <DataTable
        :value="rows"
        :loading="loading"
        lazy
        :paginator="total > 25"
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="customer.id"
        selection-mode="single"
        class="statements__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="statements__empty">
            {{ search ? `Nobody matches “${search}”.` : 'No customers yet.' }}
          </p>
        </template>

        <Column header="Code" class="statements__code">
          <template #body="{ data }">{{ data.customer.code }}</template>
        </Column>

        <Column header="Name" class="statements__name">
          <template #body="{ data }">
            {{ data.customer.name }}
            <Tag v-if="!data.customer.active" severity="secondary" value="Deactivated" />
          </template>
        </Column>

        <Column header="Principal" class="statements__amount">
          <template #body="{ data }">{{ formatPesos(data.balance.principal) }}</template>
        </Column>

        <Column header="Interest" class="statements__amount">
          <template #body="{ data }">{{ formatPesos(data.balance.interest) }}</template>
        </Column>

        <Column header="Balance" class="statements__amount statements__total">
          <template #body="{ data }">
            <span :class="{ 'statements__settled': settled(data) }">
              {{ formatPesos(data.balance.total) }}
            </span>
          </template>
        </Column>

        <Column header="Last movement" class="statements__last">
          <template #body="{ data }">
            {{ data.balance.last_activity || '—' }}
          </template>
        </Column>

        <Column class="statements__go">
          <template #body>
            <i class="pi pi-angle-right" aria-hidden="true" />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.statements__banner {
  margin-bottom: 1rem;
}

.statements__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.statements__search {
  position: relative;
  flex: 1 1 12rem;
  min-width: 10rem;
  max-width: 24rem;
}

.statements__search-icon {
  position: absolute;
  top: 50%;
  left: 0.65rem;
  transform: translateY(-50%);
  font-size: 0.8125rem;
  color: var(--p-surface-400);
  pointer-events: none;
}

.statements__search :deep(input) {
  padding-left: 2rem;
}

.statements__status {
  min-width: 8rem;
}

.statements__as-of {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.statements__as-of :deep(input) {
  width: 9.5rem;
}

.statements__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

.statements__table :deep(.statements__code) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
  width: 1%;
}

/* Every other column is sized to its content, so without a floor the name —
   the column somebody actually reads — is the one that gets squeezed, and
   "Juan dela Cruz" wraps to two lines at the width the station runs at. */
.statements__table :deep(.statements__name) {
  min-width: 11rem;
}

.statements__table :deep(.statements__amount) {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  width: 1%;
}

.statements__table :deep(.statements__total) {
  font-weight: 600;
}

.statements__table :deep(.statements__last) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  width: 1%;
  color: var(--shell-muted);
}

.statements__table :deep(.statements__go) {
  width: 1%;
  color: var(--p-surface-400);
}

/* A farmer who owes nothing is a fact, not a figure to read out. */
.statements__settled {
  color: var(--shell-muted);
  font-weight: 400;
}

.statements__table :deep(tbody tr) {
  cursor: pointer;
}

.statements__table :deep(.p-datatable-table-container) {
  border-radius: 0;
  overflow-x: auto;
}

.statements__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.statements__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 60rem) {
  .statements__filters {
    flex-wrap: wrap;
  }

  .statements__search {
    max-width: none;
    flex-basis: 100%;
  }

  .statements__count {
    margin-left: 0;
  }
}

/* Interest and the last movement are the two columns a farmer at the counter is
   not being read; the balance is. */
@media (max-width: 50rem) {
  .statements__table :deep(.statements__last) {
    display: none;
  }
}
</style>
