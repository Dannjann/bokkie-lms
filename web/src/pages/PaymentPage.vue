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
  paymentStatusLabel,
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

/** Recorded, and waiting for a settlement to apply it. */
const waiting = computed(
  () => payment.value?.status === 'posted' && payment.value.settlement_no === 0,
)
const settled = computed(
  () => payment.value?.status === 'posted' && payment.value.settlement_no > 0,
)

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

  // Only the halves that actually moved: "cleared ₱0.00 of interest" is a
  // figure that means nothing and is read anyway.
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
// and voiding both change what the ledger says, and the sentence about how the
// payment split is read from it.
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
  void run('Recorded. It will be applied when the farmer is liquidated.', () => postPayment(id.value))
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
            <Tag
              :severity="
                payment.status === 'draft'
                  ? 'warn'
                  : payment.status === 'voided'
                    ? 'danger'
                    : settled
                      ? 'success'
                      : 'info'
              "
              :value="paymentStatusLabel(payment.status, payment.settlement_no)"
            />
          </h1>
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
            <span class="payment__hint">
              Recording numbers it; it is applied to the balance when the farmer is liquidated.
            </span>
            <Button
              label="Delete draft"
              icon="pi pi-trash"
              severity="danger"
              text
              :disabled="busy"
              @click="discard"
            />
            <Button
              label="Record payment"
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

        <section v-if="payment.lines && payment.lines.length > 0" class="card payment__grid">
          <h2 class="payment__section">Palay delivered</h2>
          <table class="payment__table">
            <thead>
              <tr>
                <th>Class</th>
                <th class="payment__amount">Sacks</th>
                <th class="payment__amount">Kilos</th>
                <th class="payment__amount">Price / kg</th>
                <th class="payment__amount">Cost</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="line in payment.lines" :key="line.id">
                <td>{{ line.class }}</td>
                <td class="payment__amount numeric">{{ line.sacks }}</td>
                <td class="payment__amount numeric">{{ line.kilos }}</td>
                <td class="payment__amount numeric">{{ formatPesos(line.price) }}</td>
                <td class="payment__amount numeric">{{ formatPesos(line.cost) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td colspan="4" class="payment__total-label">Total</td>
                <td class="payment__amount numeric payment__total">
                  {{ formatPesos(payment.amount) }}
                </td>
              </tr>
            </tfoot>
          </table>
        </section>

        <Message v-if="waiting" severity="info" :closable="false">
          <div class="payment__settle">
            <span>
              Recorded and waiting. It is applied to what {{ payment.customer_name }} owes —
              interest first — when the farmer is liquidated.
            </span>
            <Button
              label="Liquidate this farmer"
              icon="pi pi-calculator"
              size="small"
              @click="
                router.push({
                  name: 'settlement-new',
                  query: { customer_id: String(payment.customer_id) },
                })
              "
            />
          </div>
        </Message>
        <Message v-else-if="settled" severity="success" :closable="false">
          Liquidated in
          <RouterLink :to="{ name: 'settlement', params: { id: payment.settlement_id } }">
            Liquidation No. {{ payment.settlement_no }}</RouterLink
          >.
        </Message>

        <p v-if="cleared && !reversal" class="payment__note">{{ cleared }}</p>

        <section v-if="balance" class="card card--padded payment__record">
          <div class="payment__row payment__row--total">
            <span class="payment__label">
              {{ payment.customer_name }} owes now{{ waiting ? ', before this payment is applied' : '' }}
            </span>
            <span class="numeric">{{ formatPesos(balance.total) }}</span>
          </div>
        </section>

        <p v-if="isManager && settled" class="payment__note payment__note--right">
          To void this payment, void Liquidation No. {{ payment.settlement_no }} first.
        </p>
        <div v-if="isManager && waiting" class="payment__actions">
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
        The payment is cancelled and no liquidation will apply it. It stays on the record, with
        this reason, under its OR No.
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

/* The table's own `th` rule is more specific than the class above; without
   this the heading sat left while its figures sat right. */
.payment__table th.payment__amount {
  text-align: right;
}

.payment__note {
  margin: 0.6rem 0.25rem 0.25rem;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.payment__note--right {
  text-align: right;
}

.payment__hint {
  margin-right: auto;
  align-self: center;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.payment__settle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.payment__total-label {
  color: var(--shell-muted);
  text-align: right;
}

.payment__total {
  font-weight: 600;
}

.payment__table tfoot td {
  border-bottom: none;
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
