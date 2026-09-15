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
import { ApiError } from '@/api/client'
import { listCustomers, type Customer, type CustomerStatus } from '@/api/customers'
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
  <div class="customers">
    <header class="customers__header">
      <h1 class="customers__title">Customers</h1>
      <div class="customers__actions">
        <Button
          v-if="isManager"
          label="Import CSV"
          icon="pi pi-upload"
          severity="secondary"
          outlined
          @click="router.push({ name: 'customers-import' })"
        />
        <Button label="New customer" icon="pi pi-plus" @click="router.push({ name: 'customer-new' })" />
      </div>
    </header>

    <div class="customers__filters">
      <InputText
        v-model="search"
        placeholder="Search by code, name or phone"
        class="customers__search"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
      />
      <Select v-model="status" :options="statusOptions" option-label="label" option-value="value" />
    </div>

    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

    <DataTable
      :value="customers"
      :loading="loading"
      lazy
      paginator
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
      <Column field="cell_no" header="Phone" />
      <Column field="address" header="Address" />
      <Column header="Status">
        <template #body="{ data }">
          <Tag
            v-if="!data.active"
            severity="secondary"
            value="Deactivated"
          />
        </template>
      </Column>
    </DataTable>
  </div>
</template>

<style scoped>
.customers__header {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1rem;
}

.customers__title {
  margin: 0;
  font-size: 1.5rem;
}

.customers__actions {
  margin-left: auto;
  display: flex;
  gap: 0.5rem;
}

.customers__filters {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.customers__search {
  flex: 1;
  min-width: 12rem;
}

/* The code is what a farmer reads off their slip, so it is the column the eye
   should land on first. */
.customers__table :deep(.customers__code) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.customers__table :deep(tbody tr) {
  cursor: pointer;
}

.customers__empty {
  margin: 0;
  padding: 1rem 0;
  color: var(--p-surface-500);
}

@media (max-width: 40rem) {
  .customers__header,
  .customers__filters {
    flex-wrap: wrap;
  }

  .customers__actions {
    margin-left: 0;
    width: 100%;
  }
}
</style>
