<script setup lang="ts">
/**
 * The payment form: who paid, when, how much, and how.
 *
 * Unlike the voucher this is not a grid — one receipt is one farmer handing
 * money over — so it is an ordinary form, and the thing that makes it a
 * *payment* form is the balance sitting beside the amount. Somebody at the
 * counter needs to see what is owed while they type what was handed over, and
 * to be told before they post that ₱7,000.00 against a ₱5,000.00 debt will put
 * the farmer in credit. Afterwards is too late: undoing it is a manager's job.
 */
import { computed, ref, watch } from 'vue'
import AutoComplete, { type AutoCompleteCompleteEvent } from 'primevue/autocomplete'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Select from 'primevue/select'
import { getCustomer, listCustomers, type Balance, type Customer } from '@/api/customers'
import { paymentMethods, type Payment, type PaymentDraft, type PaymentMethod } from '@/api/payments'
import { formatCentavos, formatPesos, fromISODate, parseCentavos, toISODate } from '@/format'

const props = defineProps<{
  /** The payment being edited, or null for a new one. */
  payment: Payment | null
  busy?: boolean
  submitLabel?: string
}>()

const emit = defineEmits<{ submit: [draft: PaymentDraft]; cancel: [] }>()

const customer = ref<Customer | null>(null)
const date = ref<Date>(new Date())
const amount = ref('')
const method = ref<PaymentMethod>('cash')
const particular = ref('')

const balance = ref<Balance | null>(null)
const loadingBalance = ref(false)

// The picker refuses tomorrow for the same reason the server does: a future
// date is almost always a wrong year on a January receipt, and it would take
// money out of the ledger that has not arrived yet.
const latestDate = new Date()

watch(
  () => props.payment,
  (p) => {
    if (!p) return
    customer.value = {
      id: p.customer_id,
      code: p.customer_code,
      name: p.customer_name,
    } as Customer
    date.value = fromISODate(p.date) ?? new Date()
    amount.value = p.amount
    method.value = p.method
    particular.value = p.particular
  },
  { immediate: true },
)

// The balance follows whoever is picked, because it is the number the person
// typing is checking against.
watch(
  customer,
  async (picked) => {
    if (!picked) {
      balance.value = null
      return
    }
    loadingBalance.value = true
    try {
      balance.value = (await getCustomer(picked.id)).balance
    } catch {
      // A balance that cannot be read is not worth blocking a payment over:
      // the server splits it again at posting from its own reading, and this
      // panel is an aid to the person typing rather than an input.
      balance.value = null
    } finally {
      loadingBalance.value = false
    }
  },
  { immediate: true },
)

const suggestions = ref<Customer[]>([])

// Deactivated farmers are included on purpose, which is the opposite of the
// voucher grid. Deactivation says who the station lends to today; the farmer
// whose relationship ended is the one most likely to be settling up.
async function searchCustomers(event: AutoCompleteCompleteEvent): Promise<void> {
  const page = await listCustomers({ q: event.query, perPage: 10 })
  suggestions.value = page.customers
}

function customerLabel(c: Customer): string {
  return `${c.code} — ${c.name}`
}

const typedCentavos = computed(() => parseCentavos(amount.value))

const owedCentavos = computed(() => {
  if (!balance.value) return null
  return parseCentavos(balance.value.total)
})

/**
 * How much of this payment is more than the farmer owes.
 *
 * Compared in whole centavos parsed out of both strings — the one place this
 * screen does arithmetic on money, and the same discipline the voucher's
 * running total uses. It decides whether to show a warning and nothing else;
 * the server does the real division and its answer is what is stored.
 */
const overpayment = computed(() => {
  const typed = typedCentavos.value
  const owed = owedCentavos.value
  if (typed === null || owed === null || typed <= owed) return null
  return formatCentavos(typed - owed)
})

const complete = computed(() => customer.value !== null && typedCentavos.value !== null)

function submit(): void {
  if (!complete.value || !customer.value) return
  emit('submit', {
    customer_id: customer.value.id,
    date: toISODate(date.value),
    amount: amount.value,
    method: method.value,
    particular: particular.value.trim(),
  })
}
</script>

<template>
  <form class="payment-form" @submit.prevent="submit">
    <div class="payment-form__columns">
      <div class="payment-form__fields">
        <div class="payment-form__field">
          <label for="payment-customer">Customer</label>
          <AutoComplete
            input-id="payment-customer"
            v-model="customer"
            :suggestions="suggestions"
            :option-label="customerLabel"
            placeholder="Search by code or name"
            force-selection
            fluid
            @complete="searchCustomers"
          />
        </div>

        <div class="payment-form__pair">
          <div class="payment-form__field">
            <label for="payment-date">Date</label>
            <DatePicker
              input-id="payment-date"
              v-model="date"
              date-format="yy-mm-dd"
              :max-date="latestDate"
              show-icon
              icon-display="input"
              fluid
            />
          </div>

          <div class="payment-form__field">
            <label for="payment-amount">Amount</label>
            <InputText
              id="payment-amount"
              v-model="amount"
              inputmode="decimal"
              placeholder="0.00"
              class="numeric payment-form__amount"
              :invalid="amount !== '' && typedCentavos === null"
              fluid
            />
          </div>
        </div>

        <div class="payment-form__pair">
          <div class="payment-form__field">
            <label for="payment-method">Method</label>
            <Select
              input-id="payment-method"
              v-model="method"
              :options="paymentMethods"
              option-label="label"
              option-value="value"
              fluid
            />
          </div>

          <div class="payment-form__field">
            <label for="payment-particular">Particular</label>
            <InputText id="payment-particular" v-model="particular" fluid />
          </div>
        </div>
      </div>

      <!--
        What the farmer owes, beside what is being typed. Not an input: the
        server reads the balance again inside the posting transaction, and that
        reading is the one the split is made from.
      -->
      <aside class="payment-form__balance">
        <h2 class="payment-form__balance-heading">Owes now</h2>

        <template v-if="balance">
          <div class="payment-form__row">
            <span>Principal</span>
            <span class="numeric">{{ formatPesos(balance.principal) }}</span>
          </div>
          <div class="payment-form__row">
            <span>Interest</span>
            <span class="numeric">{{ formatPesos(balance.interest) }}</span>
          </div>
          <div class="payment-form__row payment-form__row--total">
            <span>Total</span>
            <span class="numeric">{{ formatPesos(balance.total) }}</span>
          </div>
        </template>
        <p v-else-if="loadingBalance" class="payment-form__hint">Reading the ledger…</p>
        <p v-else class="payment-form__hint">Pick a customer to see what they owe.</p>
      </aside>
    </div>

    <Message v-if="overpayment" severity="warn" :closable="false">
      That is {{ formatPesos(overpayment) }} more than
      {{ customer ? customer.name : 'this customer' }} owes. Posting it will leave them in credit.
    </Message>

    <div class="payment-form__actions">
      <Button label="Cancel" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
      <slot name="actions" />
      <Button
        type="submit"
        :label="submitLabel ?? 'Save'"
        :disabled="!complete || busy"
        :loading="busy"
      />
    </div>
  </form>
</template>

<style scoped>
.payment-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.payment-form__columns {
  display: flex;
  gap: 1.25rem;
  align-items: flex-start;
}

.payment-form__fields {
  /* min-width: 0 so the fields column can shrink rather than pushing the
     balance panel off the card at the width the counter PC runs at. */
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}

.payment-form__pair {
  display: flex;
  gap: 0.85rem;
}

.payment-form__pair > * {
  flex: 1;
  min-width: 0;
}

.payment-form__field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.payment-form__field label {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--shell-muted);
}

/* The class lands on the input itself — PrimeVue's InputText *is* the input,
   so a descendant selector here matches nothing. Found by looking at the
   screen: the amount sat left-aligned beside a panel of right-aligned pesos. */
.payment-form__amount {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.payment-form__balance {
  width: 15rem;
  flex-shrink: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--shell-border);
  border-radius: var(--shell-radius);
  background: var(--p-surface-50);
}

.payment-form__balance-heading {
  margin: 0 0 0.5rem;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.payment-form__row {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.2rem 0;
  font-size: 0.9375rem;
}

.payment-form__row--total {
  margin-top: 0.3rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--shell-border);
  font-weight: 600;
}

.payment-form__hint {
  margin: 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.payment-form__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

/* The counter PC runs at about 1024px and the two columns fit there. Below
   that the balance panel goes under the fields rather than squeezing both. */
@media (max-width: 52rem) {
  .payment-form__columns {
    flex-direction: column;
  }

  .payment-form__balance {
    width: 100%;
  }
}

@media (max-width: 34rem) {
  .payment-form__pair {
    flex-direction: column;
  }
}
</style>
