<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import ProgressSpinner from 'primevue/progressspinner'
import CustomerFields from '@/components/CustomerFields.vue'
import { ApiError } from '@/api/client'
import {
  getCustomer,
  setCustomerActive,
  updateCustomer,
  type Balance,
  type Customer,
  type CustomerDraft,
} from '@/api/customers'
import { useSessionStore } from '@/stores/session'

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
    <ProgressSpinner v-if="loading" class="customer__spinner" />

    <template v-else-if="customer">
      <header class="customer__header">
        <div>
          <p class="customer__code">{{ customer.code }}</p>
          <h1 class="customer__name">
            {{ customer.name }}
            <Tag v-if="!customer.active" severity="secondary" value="Deactivated" />
          </h1>
        </div>
        <Button
          label="Back to customers"
          icon="pi pi-arrow-left"
          severity="secondary"
          text
          @click="router.push({ name: 'customers' })"
        />
      </header>

      <!--
        The balance comes from the ledger, not from a column on this record.
        It stays zero until step 5 can post an advance, which is expected.
      -->
      <section v-if="balance" class="balance">
        <div class="balance__item">
          <span class="balance__label">Principal</span>
          <span class="balance__value">₱{{ balance.principal }}</span>
        </div>
        <div class="balance__item">
          <span class="balance__label">Interest</span>
          <span class="balance__value">₱{{ balance.interest }}</span>
        </div>
        <div class="balance__item balance__item--total">
          <span class="balance__label">Total owed</span>
          <span class="balance__value">₱{{ balance.total }}</span>
        </div>
        <p class="balance__note">
          {{
            balance.last_activity
              ? `Last movement ${balance.last_activity}.`
              : 'No advances or payments recorded yet.'
          }}
        </p>
      </section>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">Saved.</Message>

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
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such customer.' }}</Message>
  </div>
</template>

<style scoped>
.customer {
  max-width: 32rem;
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.customer__spinner {
  align-self: center;
}

.customer__header {
  display: flex;
  align-items: flex-start;
  gap: 1rem;
}

.customer__header > :last-child {
  margin-left: auto;
}

.customer__code {
  margin: 0;
  font-variant-numeric: tabular-nums;
  color: var(--p-surface-500);
  font-size: 0.875rem;
  letter-spacing: 0.04em;
}

.customer__name {
  margin: 0.15rem 0 0;
  font-size: 1.5rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.balance {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.75rem 1rem;
  padding: 1rem;
  background: var(--p-surface-0);
  border: 1px solid var(--p-surface-200);
  border-radius: 8px;
}

.balance__item {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.balance__label {
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--p-surface-500);
}

.balance__value {
  font-variant-numeric: tabular-nums;
  font-size: 1.125rem;
}

.balance__item--total .balance__value {
  font-weight: 600;
}

.balance__note {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 0.8125rem;
  color: var(--p-surface-500);
}

.customer__form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.customer__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

@media (max-width: 40rem) {
  .balance {
    grid-template-columns: 1fr 1fr;
  }
}
</style>
