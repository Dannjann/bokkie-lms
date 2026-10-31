<script setup lang="ts">
/**
 * The payment form: who paid, when, how much, and how.
 *
 * Cash is an ordinary form. Palay — which is how the station is paid nearly
 * every time — is a lot per line off the weighing slips: class, sacks, kilos
 * and the price per kilo, the way the legacy "Payments From Customer" screen
 * took it. The amount is then the sum of the lines, worked out by the server.
 *
 * The balance beside it is what the farmer owes before this payment is
 * applied. Recording a payment does not move it: a settlement does, interest
 * first, which is where somebody sees whether the delivery left him in credit.
 */
import { computed, ref, watch } from 'vue'
import AutoComplete, { type AutoCompleteCompleteEvent } from 'primevue/autocomplete'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import { getCustomer, listCustomers, type Balance, type Customer } from '@/api/customers'
import {
  palayClasses,
  paymentMethods,
  type Payment,
  type PaymentDraft,
  type PaymentMethod,
} from '@/api/payments'
import {
  formatCentavos,
  formatPesos,
  fromISODate,
  palayCostCentavos,
  parseCentavos,
  parseKiloTenths,
  toISODate,
} from '@/format'

const props = defineProps<{
  /** The payment being edited, or null for a new one. */
  payment: Payment | null
  busy?: boolean
  submitLabel?: string
  /** Offers a second button that records the payment straight away. */
  postLabel?: string
}>()

const emit = defineEmits<{ submit: [draft: PaymentDraft]; post: [draft: PaymentDraft]; cancel: [] }>()

const customer = ref<Customer | null>(null)
const date = ref<Date>(new Date())
const amount = ref('')
const method = ref<PaymentMethod>('cash')
const particular = ref('')

/** A lot as it is being typed: every field still text. */
interface LineRow {
  key: number
  class: string
  sacks: string
  kilos: string
  price: string
}

let nextKey = 1
function blankLine(): LineRow {
  return { key: nextKey++, class: '', sacks: '', kilos: '', price: '' }
}

const lines = ref<LineRow[]>([blankLine()])

const isPalay = computed(() => method.value === 'palay')

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
    lines.value =
      p.lines && p.lines.length > 0
        ? p.lines.map((l) => ({
            key: nextKey++,
            class: l.class,
            sacks: String(l.sacks),
            kilos: l.kilos,
            price: l.price,
          }))
        : [blankLine()]
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

function sacksOf(row: LineRow): number | null {
  const text = row.sacks.trim()
  if (text === '') return 0
  return /^\d+$/.test(text) ? Number(text) : null
}

/**
 * What one lot is worth, as a preview: integer arithmetic on tenths of a kilo
 * and centavos, the same half-up rounding the server does. Null until the
 * weight and price both read.
 */
function rowCost(row: LineRow): number | null {
  return palayCostCentavos(row.kilos, row.price)
}

function rowValid(row: LineRow): boolean {
  const cost = rowCost(row)
  return row.class.trim() !== '' && sacksOf(row) !== null && cost !== null && cost > 0
}

/** The lines' total, or null while any of them is incomplete. */
const linesTotal = computed(() => {
  let total = 0
  for (const row of lines.value) {
    const cost = rowCost(row)
    if (cost === null) return null
    total += cost
  }
  return total
})

function addLine(): void {
  lines.value = [...lines.value, blankLine()]
}

function removeLine(key: number): void {
  const left = lines.value.filter((l) => l.key !== key)
  lines.value = left.length > 0 ? left : [blankLine()]
}

const complete = computed(() => {
  if (customer.value === null) return false
  if (isPalay.value) return lines.value.length > 0 && lines.value.every(rowValid)
  return typedCentavos.value !== null
})

function draft(): PaymentDraft | null {
  if (!complete.value || !customer.value) return null
  return {
    customer_id: customer.value.id,
    date: toISODate(date.value),
    // The server ignores the amount of a palay payment and adds up the lines.
    amount: isPalay.value ? '' : amount.value,
    method: method.value,
    lines: isPalay.value
      ? lines.value.map((row) => ({
          class: row.class.trim().toUpperCase(),
          sacks: sacksOf(row) ?? 0,
          kilos: row.kilos.replace(/[,\s]/g, ''),
          price: row.price,
        }))
      : [],
    particular: particular.value.trim(),
  }
}

// Enter in a field saves a draft rather than recording: see VoucherLines.
function submit(): void {
  const d = draft()
  if (d) emit('submit', d)
}

function post(): void {
  const d = draft()
  if (d) emit('post', d)
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
              v-if="isPalay"
              id="payment-amount"
              :model-value="linesTotal === null ? '—' : formatCentavos(linesTotal)"
              readonly
              class="numeric payment-form__amount"
              aria-describedby="payment-amount-help"
              fluid
            />
            <InputText
              v-else
              id="payment-amount"
              v-model="amount"
              inputmode="decimal"
              placeholder="0.00"
              class="numeric payment-form__amount"
              :invalid="amount !== '' && typedCentavos === null"
              fluid
            />
            <small v-if="isPalay" id="payment-amount-help" class="payment-form__hint">
              The sum of the lots below.
            </small>
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
        <p class="payment-form__hint payment-form__hint--small">
          Before any recorded payment is applied — that happens when the farmer is settled.
        </p>

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

    <!--
      The lots off the weighing slips. Each cost is a preview worked out in
      whole centavos; the server works it out again and its figure is stored.
    -->
    <section v-if="isPalay" class="payment-form__lines" aria-label="Palay delivered">
      <datalist id="palay-classes">
        <option v-for="c in palayClasses" :key="c" :value="c" />
      </datalist>
      <table class="payment-form__table">
        <thead>
          <tr>
            <th class="payment-form__no">#</th>
            <th>Class</th>
            <th class="payment-form__num">Sacks</th>
            <th class="payment-form__num">Kilos</th>
            <th class="payment-form__num">Price / kg</th>
            <th class="payment-form__num">Cost</th>
            <th class="payment-form__remove"><span class="sr-only">Remove</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, index) in lines" :key="row.key">
            <td class="payment-form__no numeric">{{ index + 1 }}</td>
            <td>
              <InputText
                v-model="row.class"
                list="palay-classes"
                autocomplete="off"
                class="payment-form__class"
                :aria-label="`Line ${index + 1} class`"
                fluid
              />
            </td>
            <td>
              <InputText
                v-model="row.sacks"
                inputmode="numeric"
                class="numeric payment-form__amount"
                :invalid="sacksOf(row) === null"
                :aria-label="`Line ${index + 1} sacks`"
                fluid
              />
            </td>
            <td>
              <InputText
                v-model="row.kilos"
                inputmode="decimal"
                placeholder="0.0"
                class="numeric payment-form__amount"
                :invalid="row.kilos !== '' && parseKiloTenths(row.kilos) === null"
                :aria-label="`Line ${index + 1} kilos`"
                fluid
              />
            </td>
            <td>
              <InputText
                v-model="row.price"
                inputmode="decimal"
                placeholder="0.00"
                class="numeric payment-form__amount"
                :invalid="row.price !== '' && parseCentavos(row.price) === null"
                :aria-label="`Line ${index + 1} price per kilo`"
                fluid
              />
            </td>
            <td class="payment-form__num numeric">
              {{ rowCost(row) === null ? '—' : formatPesos(formatCentavos(rowCost(row)!)) }}
            </td>
            <td class="payment-form__remove">
              <Button
                icon="pi pi-times"
                severity="secondary"
                text
                rounded
                :aria-label="`Remove line ${index + 1}`"
                :disabled="busy"
                @click="removeLine(row.key)"
              />
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td colspan="2">
              <Button
                label="Add a lot"
                icon="pi pi-plus"
                severity="secondary"
                text
                size="small"
                :disabled="busy"
                @click="addLine"
              />
            </td>
            <td colspan="3" class="payment-form__num payment-form__muted">Total</td>
            <td class="payment-form__num numeric payment-form__total">
              {{ linesTotal === null ? '—' : formatPesos(formatCentavos(linesTotal)) }}
            </td>
            <td />
          </tr>
        </tfoot>
      </table>
    </section>

    <div class="payment-form__actions">
      <Button label="Cancel" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
      <slot name="actions" />
      <Button
        type="submit"
        :label="submitLabel ?? 'Save'"
        :severity="postLabel ? 'secondary' : undefined"
        :outlined="!!postLabel"
        :disabled="!complete || busy"
        :loading="busy && !postLabel"
      />
      <Button
        v-if="postLabel"
        type="button"
        :label="postLabel"
        icon="pi pi-check"
        :disabled="!complete || busy"
        :loading="busy"
        @click="post"
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

/* The table's own `th` rule is more specific than the class above; without
   this the heading sat left while its figures sat right. */
.payment-form__table th.payment-form__amount {
  text-align: right;
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

.payment-form__hint--small {
  margin-bottom: 0.5rem;
  font-size: 0.8125rem;
}

.payment-form__lines {
  /* min-width: 0 so the table scrolls inside the card rather than pushing the
     page sideways at the counter PC's width. */
  min-width: 0;
  overflow-x: auto;
}

.payment-form__table {
  width: 100%;
  min-width: 40rem;
  border-collapse: collapse;
}

.payment-form__table th {
  padding: 0.45rem 0.4rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.payment-form__table td {
  padding: 0.3rem 0.4rem;
  border-bottom: 1px solid var(--shell-border);
}

.payment-form__table tfoot td {
  border-bottom: none;
}

.payment-form__table .payment-form__num {
  text-align: right;
  white-space: nowrap;
}

.payment-form__no {
  width: 2rem;
  color: var(--shell-muted);
}

.payment-form__remove {
  width: 2.5rem;
}

.payment-form__class {
  text-transform: uppercase;
}

.payment-form__muted {
  color: var(--shell-muted);
}

.payment-form__total {
  font-weight: 600;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
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
