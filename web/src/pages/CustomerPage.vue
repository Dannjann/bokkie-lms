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
import { entryLabel, getStatement, type StatementRow } from '@/api/payments'
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

/**
 * What has happened to this customer's account, read from the same statement
 * the statement screen reads. There is no second endpoint for "recent
 * transactions": a shorter list computed a different way is a list that can
 * disagree with the statement, and the statement is what the farmer is shown.
 */
const entries = ref<StatementRow[]>([])
const entriesLoading = ref(true)
const entriesError = ref('')

/** How many fit on a record card before it stops being a summary. */
const recentLimit = 10

// The server returns a statement in the order it happened, oldest first, which
// is how a statement is read. A record card is not: the question asked at the
// counter is what moved last.
const recent = computed(() => entries.value.slice(-recentLimit).reverse())
const olderCount = computed(() => Math.max(0, entries.value.length - recentLimit))

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

/**
 * Loaded on its own rather than with the customer: a statement that cannot be
 * built is a reason to show an empty transactions card, not a reason to hide
 * the record and the balance behind an error.
 */
async function loadEntries(): Promise<void> {
  entriesLoading.value = true
  entriesError.value = ''
  try {
    entries.value = (await getStatement(id.value)).rows
  } catch (err) {
    entriesError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    entriesLoading.value = false
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

onMounted(() => {
  void load()
  void loadEntries()
})
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
          <!--
            The things somebody opens a customer to do, once there is money on
            the record: take a payment, settle them up, or read them their
            history. All are here rather than in the sidebar because this is
            where the balance they are asking about is.
          -->
          <Button
            label="Record payment"
            icon="pi pi-money-bill"
            @click="router.push({ name: 'payment-new', query: { customer: customer.id } })"
          />
          <Button
            label="Liquidate"
            icon="pi pi-calculator"
            severity="secondary"
            outlined
            @click="
              router.push({ name: 'settlement-new', query: { customer_id: String(customer.id) } })
            "
          />
          <Button
            label="Statement"
            icon="pi pi-file"
            severity="secondary"
            outlined
            @click="router.push({ name: 'statement', params: { id: customer.id } })"
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

          <RouterLink
            v-if="balance.last_activity"
            class="balance__link"
            :to="{ name: 'statement', params: { id: customer.id } }"
          >
            See the statement
          </RouterLink>
        </section>
      </div>

      <!--
        The history, on the record itself. Somebody standing at the counter with
        the farmer in front of them asks "what did I pay last month" far more
        often than they want a full statement printed, and sending them to
        another screen to answer it loses the balance they were just reading.
        Every figure here is the server's, including the running balance.
      -->
      <section class="card ledger">
        <div class="ledger__head">
          <h2 class="customer__heading ledger__heading">Transactions</h2>
          <RouterLink
            class="ledger__link"
            :to="{ name: 'statement', params: { id: customer.id } }"
          >
            Full statement
          </RouterLink>
        </div>

        <div v-if="entriesLoading" class="ledger__loading">
          <ProgressSpinner style="width: 2rem; height: 2rem" />
        </div>

        <Message v-else-if="entriesError" severity="error" :closable="false">
          {{ entriesError }}
        </Message>

        <p v-else-if="recent.length === 0" class="ledger__empty">
          Nothing has been advanced to or paid by this customer yet.
        </p>

        <template v-else>
          <div class="ledger__scroll">
            <table class="ledger__table">
              <thead>
                <tr>
                  <th class="ledger__date">Date</th>
                  <th>Entry</th>
                  <th>Document</th>
                  <th class="ledger__amount">Principal</th>
                  <th class="ledger__amount">Interest</th>
                  <th class="ledger__amount">Balance</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in recent" :key="row.entry.id">
                  <td class="ledger__date numeric">{{ row.entry.date }}</td>
                  <td>
                    {{ entryLabel(row.entry.type) }}
                    <span v-if="row.entry.reason" class="ledger__reason">
                      — {{ row.entry.reason }}
                    </span>
                  </td>
                  <td class="ledger__document">{{ row.document ?? '—' }}</td>
                  <td class="ledger__amount numeric">{{ formatPesos(row.entry.principal) }}</td>
                  <td class="ledger__amount numeric">{{ formatPesos(row.entry.interest) }}</td>
                  <td class="ledger__amount numeric">{{ formatPesos(row.running.total) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <!--
            Said plainly rather than left to be inferred from the row count: a
            card showing the last ten of forty movements must not read as the
            whole history.
          -->
          <p v-if="olderCount > 0" class="ledger__more">
            The {{ recentLimit }} most recent of
            <span class="numeric">{{ entries.length }}</span> entries.
            <RouterLink :to="{ name: 'statement', params: { id: customer.id } }">
              See them all on the statement
            </RouterLink>
          </p>
        </template>
      </section>
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

.balance__link {
  font-size: 0.875rem;
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

.ledger__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem 0.75rem 0;
}

.ledger__heading {
  margin: 0;
}

.ledger__link {
  font-size: 0.875rem;
}

.ledger__loading {
  display: grid;
  place-items: center;
  padding: 2rem 0;
}

.ledger__empty {
  margin: 0;
  padding: 1.5rem 0.75rem;
  text-align: center;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

/* Six columns of figures do not fit on a phone. The table scrolls inside its
   own card rather than dragging the page sideways with it, and min-width: 0 is
   what makes the overflow contain anything at all. */
.ledger__scroll {
  min-width: 0;
  overflow-x: auto;
  padding: 0.75rem;
}

.ledger__table {
  width: 100%;
  min-width: 44rem;
  border-collapse: collapse;
}

.ledger__table th {
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.ledger__table td {
  padding: 0.5rem;
  border-bottom: 1px solid var(--shell-border);
}

.ledger__table tbody tr:last-child td {
  border-bottom: none;
}

.ledger__date {
  white-space: nowrap;
}

.ledger__document,
.ledger__reason {
  color: var(--shell-muted);
}

.ledger__document {
  white-space: nowrap;
}

.ledger__amount {
  text-align: right;
  white-space: nowrap;
}

.ledger__table th.ledger__amount {
  text-align: right;
}

.ledger__more {
  margin: 0;
  padding: 0 0.75rem 0.75rem;
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
