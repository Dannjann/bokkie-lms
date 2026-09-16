<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import ProgressSpinner from 'primevue/progressspinner'
import CustomerFields from '@/components/CustomerFields.vue'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  getCustomer,
  setCustomerActive,
  updateCustomer,
  type Balance,
  type Customer,
  type CustomerDraft,
} from '@/api/customers'
import { formatPesos } from '@/format'
import { useSessionStore } from '@/stores/session'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()
const { isManager } = storeToRefs(useSessionStore())

const id = computed(() => Number(route.params.id))

const customer = ref<Customer | null>(null)
const balance = ref<Balance | null>(null)
const draft = ref<CustomerDraft>({ name: '', address: '', cell_no: '', notes: '' })

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const saved = ref(false)

function adopt(loaded: Customer): void {
  customer.value = loaded
  setPageTitle(loaded.name)
  draft.value = {
    name: loaded.name,
    address: loaded.address,
    cell_no: loaded.cell_no,
    notes: loaded.notes,
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const detail = await getCustomer(id.value)
    adopt(detail.customer)
    balance.value = detail.balance
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  error.value = ''
  saved.value = false
  busy.value = true
  try {
    adopt(await updateCustomer(id.value, draft.value))
    saved.value = true
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

async function toggleActive(): Promise<void> {
  if (!customer.value) return

  error.value = ''
  saved.value = false
  busy.value = true
  try {
    adopt(await setCustomerActive(id.value, !customer.value.active))
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="customer">
    <div v-if="loading" class="customer__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="customer">
      <PageHeader>
        <template #title>
          <p class="customer__code numeric label">{{ customer.code }}</p>
          <h1 class="customer__name">
            {{ customer.name }}
            <Tag v-if="!customer.active" severity="secondary" value="Deactivated" />
          </h1>
        </template>
        <template #actions>
          <Button
            label="Back to customers"
            icon="pi pi-arrow-left"
            severity="secondary"
            text
            @click="router.push({ name: 'customers' })"
          />
        </template>
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">Saved.</Message>

      <div class="customer__columns">
        <section class="card card--padded">
          <h2 class="customer__heading">Details</h2>

          <form class="customer__form" @submit.prevent="save">
            <CustomerFields v-model="draft" />

            <div class="customer__actions">
              <!--
                Deactivation is a manager's, and it is the nearest thing to a
                deletion this system has: nothing is ever removed, because a code
                released here would be handed to the next customer created.
              -->
              <Button
                v-if="isManager"
                :label="customer.active ? 'Deactivate' : 'Reactivate'"
                :severity="customer.active ? 'danger' : 'secondary'"
                outlined
                :disabled="busy"
                @click="toggleActive"
              />
              <Button type="submit" label="Save changes" :loading="busy" />
            </div>
          </form>
        </section>

        <!--
          The balance comes from the ledger, not from a column on this record.
          It stays zero until step 5 can post an advance, which is expected.
        -->
        <section v-if="balance" class="card card--padded balance">
          <h2 class="customer__heading">Balance</h2>

          <div class="balance__row">
            <span class="balance__label">Principal</span>
            <span class="balance__value numeric">{{ formatPesos(balance.principal) }}</span>
          </div>
          <div class="balance__row">
            <span class="balance__label">Interest</span>
            <span class="balance__value numeric">{{ formatPesos(balance.interest) }}</span>
          </div>
          <div class="balance__row balance__row--total">
            <span class="balance__label">Total owed</span>
            <span class="balance__value numeric">{{ formatPesos(balance.total) }}</span>
          </div>

          <p class="balance__note">
            {{
              balance.last_activity
                ? `Last movement ${balance.last_activity}.`
                : 'No advances or payments recorded yet.'
            }}
          </p>
        </section>
      </div>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such customer.' }}</Message>
  </div>
</template>

<style scoped>
.customer {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.customer__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.customer__code {
  margin: 0;
}

.customer__name {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.customer__columns {
  display: grid;
  grid-template-columns: minmax(0, 34rem) minmax(0, 20rem);
  gap: 1rem;
  align-items: start;
}

.customer__heading {
  margin: 0 0 1rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.customer__form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.customer__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

.balance__row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.5rem 0;
  border-top: 1px solid var(--shell-border);
}

.balance__row:first-of-type {
  border-top: none;
  padding-top: 0;
}

.balance__label {
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.balance__value {
  font-size: 1rem;
}

.balance__row--total {
  border-top-width: 2px;
  border-top-color: var(--p-surface-300);
}

.balance__row--total .balance__label,
.balance__row--total .balance__value {
  font-weight: 600;
  color: var(--p-surface-900);
}

.balance__row--total .balance__value {
  font-size: 1.25rem;
}

.balance__note {
  margin: 0.75rem 0 0;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

@media (max-width: 62rem) {
  .customer__columns {
    grid-template-columns: 1fr;
  }

  /* Stacked, what is owed is the first thing anyone came here to read. */
  .balance {
    order: -1;
  }
}
</style>
