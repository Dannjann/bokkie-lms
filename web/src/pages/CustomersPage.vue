<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { listCustomers, type Customer, type CustomerStatus } from '@/api/customers'
import { formatPhone } from '@/format'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const { isManager } = storeToRefs(useSessionStore())

const customers = ref<Customer[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const search = ref('')
const status = ref<CustomerStatus>('active')
const first = ref(0)
const perPage = ref(25)

const statusOptions = [
  { label: 'Active', value: 'active' },
  { label: 'Deactivated', value: 'inactive' },
  { label: 'All', value: 'all' },
]

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listCustomers({
      q: search.value.trim(),
      status: status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    customers.value = page.customers
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    customers.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

// One request per pause in typing, not per keystroke. On a LAN the round trip
// is not perceptible, but the station's counter PCs are not fast and there is
// no reason to make them work for letters nobody has finished typing.
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

function open(customer: Customer): void {
  router.push({ name: 'customer', params: { id: customer.id } })
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Customers"
      description="The master list. Codes are assigned on save and are never reused."
    >
      <template #actions>
        <Button
          v-if="isManager"
          label="Import CSV"
          icon="pi pi-upload"
          severity="secondary"
          outlined
          @click="router.push({ name: 'customers-import' })"
        />
        <Button label="New customer" icon="pi pi-plus" @click="router.push({ name: 'customer-new' })" />
      </template>
    </PageHeader>

    <Message v-if="error" severity="error" :closable="false" class="customers__error">
      {{ error }}
    </Message>

    <div class="card">
      <div class="customers__filters">
        <span class="customers__search">
          <i class="pi pi-search customers__search-icon" aria-hidden="true" />
          <InputText
            v-model="search"
            placeholder="Search by code, name or phone"
            aria-label="Search customers"
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
          class="customers__status"
        />
        <span class="customers__count numeric">{{ total }} {{ total === 1 ? 'record' : 'records' }}</span>
      </div>

      <DataTable
        :value="customers"
        :loading="loading"
        lazy
        :paginator="total > 25"
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="id"
        selection-mode="single"
        class="customers__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="customers__empty">
            {{ search ? `Nobody matches “${search}”.` : 'No customers yet.' }}
          </p>
        </template>

        <Column field="code" header="Code" class="customers__code" />
        <Column field="name" header="Name" />
        <Column header="Phone" class="customers__phone">
          <template #body="{ data }">{{ formatPhone(data.cell_no) }}</template>
        </Column>
        <Column field="address" header="Address" />
        <Column header="Status" class="customers__status-cell">
          <template #body="{ data }">
            <Tag v-if="!data.active" severity="secondary" value="Deactivated" />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.customers__error {
  margin-bottom: 1rem;
}

.customers__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.customers__search {
  position: relative;
  flex: 1;
  min-width: 12rem;
  max-width: 26rem;
}

.customers__search-icon {
  position: absolute;
  top: 50%;
  left: 0.65rem;
  transform: translateY(-50%);
  font-size: 0.8125rem;
  color: var(--p-surface-400);
  pointer-events: none;
}

.customers__search :deep(input) {
  padding-left: 2rem;
}

.customers__status {
  min-width: 9rem;
}

.customers__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

.customers__table :deep(.customers__phone) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

/* The code is what a farmer reads off their slip, so it is the column the eye
   should land on first. */
.customers__table :deep(.customers__code) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
}

.customers__table :deep(.customers__status-cell) {
  width: 1%;
  white-space: nowrap;
}

.customers__table :deep(tbody tr) {
  cursor: pointer;
}

/* The table lives inside the card, so it must not draw its own outer edges. */
.customers__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.customers__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.customers__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .customers__filters {
    flex-wrap: wrap;
  }

  .customers__search {
    max-width: none;
    flex-basis: 100%;
  }

  .customers__count {
    margin-left: 0;
  }
}
</style>
