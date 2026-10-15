<script setup lang="ts">
/**
 * The voucher form: a date, a note, and the grid of lines beneath it.
 *
 * This is the one screen in the system that is genuinely a data-entry grid, and
 * it is what PrimeVue was chosen for. The cells are ordinary always-editable
 * inputs rather than PrimeVue's cell-edit mode, because entry here is keyboard
 * work: somebody with a stack of paper slips tabs from customer to amount to
 * the next row, and a grid that needs a click to open each cell doubles the
 * work. Enter on an amount starts the next line and puts the cursor in it.
 */
import { computed, nextTick, ref, watch } from 'vue'
import AutoComplete, { type AutoCompleteCompleteEvent } from 'primevue/autocomplete'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import { listCustomers, type Customer } from '@/api/customers'
import type { AdvanceLine, VoucherDraft } from '@/api/advances'
import { formatCentavos, fromISODate, parseCentavos, sumCentavos, toISODate } from '@/format'

const props = defineProps<{
  date: string
  notes: string
  lines: AdvanceLine[]
  busy?: boolean
  submitLabel?: string
}>()

const emit = defineEmits<{ submit: [draft: VoucherDraft]; cancel: [] }>()

/** One row as it is being typed, before the server is told about any of it. */
interface Row {
  customer: Customer | null
  /** Null means "the voucher's date", which is what most rows are. */
  date: Date | null
  particular: string
  amount: string
}

const voucherDate = ref<Date>(new Date())
const notes = ref('')
const rows = ref<Row[]>([])
const grid = ref<HTMLElement | null>(null)

// The picker refuses tomorrow for the same reason the server does: a future
// date is almost always a wrong year on a January slip, and it would put money
// in the ledger that has not moved yet.
const latestDate = new Date()

watch(
  () => [props.date, props.notes, props.lines] as const,
  ([date, note, lines]) => {
    voucherDate.value = fromISODate(date) ?? new Date()
    notes.value = note
    rows.value = lines.length ? lines.map(toRow) : [blankRow()]
  },
  { immediate: true },
)

function toRow(line: AdvanceLine): Row {
  return {
    customer: {
      id: line.customer_id,
      code: line.customer_code,
      name: line.customer_name,
    } as Customer,
    date: line.date === props.date ? null : fromISODate(line.date),
    particular: line.particular,
    amount: line.amount,
  }
}

function blankRow(): Row {
  return { customer: null, date: null, particular: '', amount: '' }
}

/**
 * A new row starts on the customer above it.
 *
 * Whether one Ref. No. really spans several farmers is still a question for the
 * owner. This costs one keystroke to change if it does, and saves one pick per
 * line if it does not.
 */
function addRow(): void {
  const previous = rows.value[rows.value.length - 1]
  rows.value.push({ ...blankRow(), customer: previous?.customer ?? null })

  void nextTick(() => {
    const inputs = grid.value?.querySelectorAll<HTMLInputElement>('tbody tr:last-child input')
    inputs?.[0]?.focus()
  })
}

function removeRow(index: number): void {
  rows.value.splice(index, 1)
  if (rows.value.length === 0) rows.value.push(blankRow())
}

const suggestions = ref<Customer[]>([])

async function searchCustomers(event: AutoCompleteCompleteEvent): Promise<void> {
  const page = await listCustomers({ q: event.query, status: 'active', perPage: 10 })
  suggestions.value = page.customers
}

function customerLabel(customer: Customer): string {
  return `${customer.code} — ${customer.name}`
}

// Displayed, never sent. The server adds the lines up again and its answer is
// the one that is stored.
const total = computed(() => {
  const centavos = sumCentavos(rows.value.map((row) => row.amount).filter((a) => a !== ''))
  return centavos === null ? null : formatCentavos(centavos)
})

const complete = computed(() =>
  rows.value.length > 0 &&
  rows.value.every((row) => row.customer !== null && parseCentavos(row.amount) !== null),
)

function submit(): void {
  emit('submit', {
    date: toISODate(voucherDate.value),
    notes: notes.value,
    lines: rows.value.map((row) => ({
      customer_id: row.customer?.id ?? 0,
      date: row.date ? toISODate(row.date) : '',
      particular: row.particular,
      amount: row.amount,
    })),
  })
}
</script>

<template>
  <form class="voucher" @submit.prevent="submit">
    <div class="card card--padded voucher__header">
      <label class="voucher__field">
        <span class="label">Slip date</span>
        <DatePicker
          v-model="voucherDate"
          date-format="yy-mm-dd"
          :max-date="latestDate"
          show-icon
          icon-display="input"
          fluid
        />
      </label>

      <label class="voucher__field voucher__field--wide">
        <span class="label">Notes</span>
        <Textarea v-model="notes" rows="1" auto-resize fluid />
      </label>
    </div>

    <div ref="grid" class="card voucher__grid">
      <table class="voucher__table">
        <thead>
          <tr>
            <th class="voucher__col-customer">Customer</th>
            <th class="voucher__col-date">Date</th>
            <th>Particular</th>
            <th class="voucher__col-amount">Amount</th>
            <th class="voucher__col-remove"><span class="sr-only">Remove</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, index) in rows" :key="index">
            <td>
              <AutoComplete
                v-model="row.customer"
                :suggestions="suggestions"
                :option-label="customerLabel"
                force-selection
                fluid
                placeholder="Code or name"
                :aria-label="`Customer on line ${index + 1}`"
                @complete="searchCustomers"
              />
            </td>
            <td>
              <DatePicker
                v-model="row.date"
                date-format="yy-mm-dd"
                :max-date="latestDate"
                :placeholder="date"
                fluid
                :aria-label="`Date on line ${index + 1}`"
              />
            </td>
            <td>
              <InputText
                v-model="row.particular"
                fluid
                :aria-label="`Particular on line ${index + 1}`"
              />
            </td>
            <td>
              <InputText
                v-model="row.amount"
                class="voucher__amount"
                inputmode="decimal"
                placeholder="0.00"
                fluid
                :aria-label="`Amount on line ${index + 1}`"
                @keydown.enter.prevent="addRow"
              />
            </td>
            <td>
              <Button
                type="button"
                icon="pi pi-times"
                severity="secondary"
                text
                :aria-label="`Remove line ${index + 1}`"
                @click="removeRow(index)"
              />
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td colspan="3">
              <Button
                type="button"
                label="Add line"
                icon="pi pi-plus"
                severity="secondary"
                text
                @click="addRow"
              />
            </td>
            <td class="voucher__amount voucher__total numeric">
              {{ total === null ? '—' : `₱${total}` }}
            </td>
            <td />
          </tr>
        </tfoot>
      </table>
    </div>

    <div class="voucher__actions">
      <slot name="actions" />
      <Button type="button" label="Cancel" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
      <Button type="submit" :label="submitLabel ?? 'Save draft'" :loading="busy" :disabled="!complete" />
    </div>
  </form>
</template>

<style scoped>
.voucher {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.voucher__header {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.voucher__field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-width: 12rem;
}

.voucher__field--wide {
  flex: 1;
}

/*
 * The grid scrolls inside its own card on a narrow screen rather than pushing
 * the page sideways. Entering a voucher is not a phone job, but a manager
 * opening one on a phone should not have to scroll the whole page to read it.
 *
 * min-width: 0 is what makes overflow-x actually contain anything here: this
 * card is a flex item, a flex item's min-width is auto, and auto means "never
 * shrink below your content" — so without it the card grows to the table's
 * 46rem and takes the page with it. Measured at 390px, where the page scrolled
 * sideways by 316px, and at 700px, where it scrolled by 6px.
 */
.voucher__grid {
  min-width: 0;
  overflow-x: auto;
}

.voucher__table {
  width: 100%;
  min-width: 46rem;
  border-collapse: collapse;
}

/*
 * Each header cell is the containing block for its own visually-hidden label.
 *
 * Without this the label below is positioned against the document instead, and
 * because it sits at the far edge of a 46rem table inside a card that scrolls,
 * it escapes that card and drags the page 316px wide at 390px — one pixel of
 * text widening the whole app. Found by measuring the overflow at the widths
 * the station and a phone actually use, not by looking at it.
 */
.voucher__table th {
  position: relative;
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.voucher__table td {
  padding: 0.35rem 0.5rem;
  vertical-align: middle;
}

.voucher__table tfoot td {
  border-top: 1px solid var(--shell-border);
}

/*
 * Measured at 1024px with the rail in place, which is the width the station's
 * Edge --app window actually runs at: at 30% / 11rem the customer cell cut off
 * "BA-0001 — Juan dela Cru" and the particular showed six characters. The date
 * column is the one with slack — it holds ten characters and nothing else —
 * so it is the one that gives.
 */
.voucher__col-customer {
  width: 34%;
}

.voucher__col-date {
  width: 8.5rem;
}

.voucher__col-amount {
  width: 8.5rem;
  text-align: right;
}

.voucher__col-remove {
  width: 2.5rem;
}

.voucher__amount :deep(input),
.voucher__amount {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.voucher__total {
  padding-right: 0.5rem;
  font-weight: 600;
}

.voucher__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

@media (max-width: 40rem) {
  .voucher__actions {
    flex-wrap: wrap;
  }
}
</style>
