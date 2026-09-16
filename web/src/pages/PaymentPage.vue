<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import PageHeader from '@/components/PageHeader.vue'
import PaymentFields from '@/components/PaymentFields.vue'
import { ApiError } from '@/api/client'
import {
  deletePayment,
  getPayment,
  methodLabel,
  postPayment,
  updatePayment,
  voidPayment,
  type LedgerEntryView,
  type Payment,
  type PaymentDraft,
} from '@/api/payments'
import type { Balance } from '@/api/customers'
import { formatPesos, parseCentavos } from '@/format'
import { useSessionStore } from '@/stores/session'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()
const { isManager } = storeToRefs(useSessionStore())

const id = computed(() => Number(route.params.id))

const payment = ref<Payment | null>(null)
const entries = ref<LedgerEntryView[]>([])
const balance = ref<Balance | null>(null)

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const saved = ref('')

const voiding = ref(false)
const voidReason = ref('')

const isDraft = computed(() => payment.value?.status === 'draft')

/**
 * What the payment actually did, read off the ledger rather than off the
 * receipt.
 *
 * The division between principal and interest is stored on the entry and
 * nowhere else, so this panel is the only honest place to show it. The entry's
 * deltas are negative — a payment reduces what is owed — and they are printed
 * as they are, because a statement that shows a payment as a positive number is
 * a statement somebody will read the wrong way round.
 */
const posted = computed(() => entries.value.find((e) => e.type === 'payment') ?? null)
const reversal = computed(() => entries.value.find((e) => e.type === 'reversal') ?? null)

/**
 * The same two figures said in words rather than as deltas.
 *
 * "Cleared ₱600.00 of interest" is what somebody at the counter is actually
 * asking, and printing it from the entry's own negative numbers would read as
 * though the interest went up. The sign is dropped by string surgery, never by
 * arithmetic — the value never becomes a number on its way to the screen.
 */
const cleared = computed(() => {
  if (!posted.value) return null

  const principal = posted.value.principal.replace(/^-/, '')
  const interest = posted.value.interest.replace(/^-/, '')

  // Only the halves that actually moved. Until step 7 charges interest, every
  // payment in the system clears principal alone, and "cleared ₱0.00 of
  // interest" on every receipt is a figure that means nothing and is read
  // anyway.
  const parts: string[] = []
  if (parseCentavos(interest)) parts.push(`${formatPesos(interest)} of interest`)
  if (parseCentavos(principal)) parts.push(`${formatPesos(principal)} of principal`)
  if (parts.length === 0) return null

  return `Cleared ${parts.join(' and ')}.`
})

function adopt(loaded: Payment): void {
  payment.value = loaded
  setPageTitle(loaded.or_no ? `OR No. ${loaded.or_no}` : 'Draft payment')
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const detail = await getPayment(id.value)
    adopt(detail.payment)
    entries.value = detail.entries
    balance.value = detail.balance
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

// Every one of these reloads rather than trusting the response alone: posting
// and voiding both change what the ledger says, and the entries underneath are
// the point of looking at a posted receipt.
async function run(what: string, action: () => Promise<Payment>): Promise<void> {
  error.value = ''
  saved.value = ''
  busy.value = true
  try {
    adopt(await action())
    await load()
    saved.value = what
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

function save(draft: PaymentDraft): void {
  void run('Saved.', () => updatePayment(id.value, draft))
}

function post(): void {
  void run('Posted.', () => postPayment(id.value))
}

function confirmVoid(): void {
  voiding.value = false
  void run('Voided.', () => voidPayment(id.value, voidReason.value))
}

async function discard(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await deletePayment(id.value)
    await router.replace({ name: 'payments' })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="payment">
    <div v-if="loading" class="payment__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="payment">
      <PageHeader>
        <template #title>
          <p class="payment__or numeric label">
            {{ payment.or_no ? `OR No. ${payment.or_no}` : 'Unnumbered draft' }}
          </p>
          <h1 class="payment__heading">
            {{ formatPesos(payment.amount) }}
            <Tag v-if="payment.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="payment.status === 'voided'" severity="danger" value="Voided" />
          </h1>
        </template>
        <template #actions>
          <Button
            label="Back to payments"
            icon="pi pi-arrow-left"
            severity="secondary"
            text
            @click="router.push({ name: 'payments' })"
          />
        </template>
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">{{ saved }}</Message>

      <Message v-if="payment.status === 'voided'" severity="warn" :closable="false">
        Voided by {{ payment.voided_by.Username }} — {{ payment.void_reason }}
      </Message>

      <!--
        A draft is the form. Everything else is a record of what happened, and
        the only thing that can be done to it is a void, which is a manager's.
      -->
      <div v-if="isDraft" class="card card--padded">
        <PaymentFields
          :payment="payment"
          :busy="busy"
          submit-label="Save changes"
          @submit="save"
          @cancel="router.push({ name: 'payments' })"
        >
          <template #actions>
            <Button
              label="Delete draft"
              icon="pi pi-trash"
              severity="danger"
              text
              :disabled="busy"
              @click="discard"
            />
            <Button
              label="Post"
              icon="pi pi-check"
              severity="success"
              :disabled="busy"
              @click="post"
            />
          </template>
        </PaymentFields>
      </div>

      <template v-else>
        <section class="card card--padded payment__record">
          <div class="payment__row">
            <span class="payment__label">From</span>
            <RouterLink :to="{ name: 'customer', params: { id: payment.customer_id } }">
              {{ payment.customer_code }} — {{ payment.customer_name }}
            </RouterLink>
          </div>
          <div class="payment__row">
            <span class="payment__label">Date</span>
            <span class="numeric">{{ payment.date }}</span>
          </div>
          <div class="payment__row">
            <span class="payment__label">Method</span>
            <span>{{ methodLabel(payment.method) }}</span>
          </div>
          <div v-if="payment.particular" class="payment__row">
            <span class="payment__label">Particular</span>
            <span>{{ payment.particular }}</span>
          </div>
          <div class="payment__row">
            <span class="payment__label">Received by</span>
            <span>{{ payment.posted_by.Username }}</span>
          </div>
        </section>

        <!--
          Where the split is read from. The receipt does not store how the money
          divided; the ledger entry does, and this is that entry.
        -->
        <section class="card payment__grid">
          <h2 class="payment__section">In the ledger</h2>
          <table class="payment__table">
            <thead>
              <tr>
                <th class="payment__date">Date</th>
                <th>Entry</th>
                <th>By</th>
                <th class="payment__amount">Principal</th>
                <th class="payment__amount">Interest</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in entries" :key="entry.id">
                <td class="payment__date numeric">{{ entry.date }}</td>
                <td>
                  {{ entry.type === 'reversal' ? 'Reversal' : 'Payment' }}
                  <span v-if="entry.reason" class="payment__reason">— {{ entry.reason }}</span>
                </td>
                <td>{{ entry.by }}</td>
                <td class="payment__amount numeric">{{ formatPesos(entry.principal) }}</td>
                <td class="payment__amount numeric">{{ formatPesos(entry.interest) }}</td>
              </tr>
            </tbody>
          </table>

          <p v-if="cleared && !reversal" class="payment__note">{{ cleared }}</p>
        </section>

        <section v-if="balance" class="card card--padded payment__record">
          <div class="payment__row payment__row--total">
            <span class="payment__label">
              {{ payment.customer_name }} owes now
            </span>
            <span class="numeric">{{ formatPesos(balance.total) }}</span>
          </div>
        </section>

        <div v-if="isManager && payment.status === 'posted'" class="payment__actions">
          <Button
            label="Void this payment"
            icon="pi pi-ban"
            severity="danger"
            outlined
            :disabled="busy"
            @click="voiding = true"
          />
        </div>
      </template>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such payment.' }}</Message>

    <Dialog v-model:visible="voiding" modal header="Void this payment" :style="{ width: '28rem' }">
      <p class="payment__dialog-text">
        The entry will be reversed in the ledger and the farmer will owe this money again. The
        payment and its reversal both stay on the record, and the reason is written onto the
        reversal.
      </p>
      <Textarea v-model="voidReason" rows="3" fluid aria-label="Reason for voiding" />
      <template #footer>
        <Button label="Cancel" severity="secondary" text @click="voiding = false" />
        <Button
          label="Void"
          severity="danger"
          :disabled="voidReason.trim() === ''"
          @click="confirmVoid"
        />
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
.payment {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.payment__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.payment__or {
  margin: 0;
}

.payment__heading {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  font-variant-numeric: tabular-nums;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.payment__record {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.payment__row {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
}

.payment__row--total {
  font-weight: 600;
}

.payment__label {
  color: var(--shell-muted);
}

.payment__grid {
  /* min-width: 0 for the same reason as the voucher's: a flex item will not
     shrink below its content without it, and overflow-x then contains nothing. */
  min-width: 0;
  overflow-x: auto;
  padding: 0.25rem 0.75rem 0.75rem;
}

.payment__section {
  margin: 0.75rem 0.25rem 0.5rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.payment__table {
  width: 100%;
  min-width: 34rem;
  border-collapse: collapse;
}

.payment__table th {
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.payment__table td {
  padding: 0.5rem;
  border-bottom: 1px solid var(--shell-border);
}

.payment__date {
  white-space: nowrap;
}

.payment__amount {
  text-align: right;
  white-space: nowrap;
}

.payment__reason {
  color: var(--shell-muted);
}

.payment__note {
  margin: 0.6rem 0.25rem 0.25rem;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.payment__actions {
  display: flex;
  justify-content: flex-end;
}

.payment__dialog-text {
  margin: 0 0 0.75rem;
  color: var(--shell-muted);
}
</style>
