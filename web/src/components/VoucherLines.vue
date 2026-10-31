<script setup lang="ts">
/**
 * The voucher form: a date, a note, and the grid of lines beneath it.
 *
 * This is the one screen in the system that is genuinely a data-entry grid, and
 * it is what PrimeVue was chosen for. The cells are ordinary always-editable
 * inputs rather than PrimeVue's cell-edit mode, because entry here is keyboard
 * work: somebody with a stack of paper slips tabs from customer to amount to
 * the next row, and a grid that needs a click to open each cell doubles the
 * work. Lines are typed grouped by customer: Enter on an amount starts the next
 * particular for the same customer, and Add customer starts a new, empty group.
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
  /**
   * Offers a second button that posts the slip straight away, beside the one
   * that saves a draft. Only a new slip has it: a draft already saved is posted
   * from its own page.
   */
  postLabel?: string
}>()

const emit = defineEmits<{ submit: [draft: VoucherDraft]; post: [draft: VoucherDraft]; cancel: [] }>()

/** One particular and its amount, as it is being typed. */
interface Item {
  key: number
  particular: string
  amount: string
}

/**
 * One customer on one date, and everything they were advanced then.
 *
 * The slip at the counter is usually one farmer taking several things at once
 * — fertilizer, seed, cash — so the customer and the date are picked once and
 * the particulars are typed beneath them. The server still stores flat lines;
 * grouping is how they are typed, not how they are kept.
 */
interface Group {
  key: number
  customer: Customer | null
  /** Null means "the voucher's date", which is what most groups are. */
  date: Date | null
  items: Item[]
}

const voucherDate = ref<Date>(new Date())
const notes = ref('')
const groups = ref<Group[]>([])
const grid = ref<HTMLElement | null>(null)

// Keys for v-for only. An index would hand a removed row's input state to the
// row that slid into its place.
let nextKey = 0

// The picker refuses tomorrow for the same reason the server does: a future
// date is almost always a wrong year on a January slip, and it would put money
// in the ledger that has not moved yet.
const latestDate = new Date()

watch(
  () => [props.date, props.notes, props.lines] as const,
  ([date, note, lines]) => {
    voucherDate.value = fromISODate(date) ?? new Date()
    notes.value = note
    groups.value = lines.length ? toGroups(lines) : [blankGroup()]
  },
  { immediate: true },
)

/**
 * A saved slip back into groups: consecutive lines for the same customer on the
 * same date are one group, which is how they were typed in the first place.
 */
function toGroups(lines: AdvanceLine[]): Group[] {
  const out: Group[] = []
  for (const line of lines) {
    const date = line.date === props.date ? null : fromISODate(line.date)
    const last = out[out.length - 1]
    const sameDate = (last?.date?.getTime() ?? null) === (date?.getTime() ?? null)
    if (last && last.customer?.id === line.customer_id && sameDate) {
      last.items.push(item(line.particular, line.amount))
      continue
    }
    out.push({
      key: nextKey++,
      customer: { id: line.customer_id, code: line.customer_code, name: line.customer_name } as Customer,
      date,
      items: [item(line.particular, line.amount)],
    })
  }
  return out
}

function item(particular = '', amount = ''): Item {
  return { key: nextKey++, particular, amount }
}

function blankGroup(): Group {
  return { key: nextKey++, customer: null, date: null, items: [item()] }
}

function focus(selector: string): void {
  void nextTick(() => grid.value?.querySelector<HTMLInputElement>(selector)?.focus())
}

/** Another particular for the same customer, with the cursor in it. */
function addItem(group: Group): void {
  const added = item()
  group.items.push(added)
  focus(`[data-item="${added.key}"] input`)
}

/** A new customer starts empty: the client's rule, so nobody advances the wrong farmer by default. */
function addGroup(): void {
  const added = blankGroup()
  groups.value.push(added)
  focus(`[data-group="${added.key}"] input`)
}

function removeItem(group: Group, index: number): void {
  group.items.splice(index, 1)
  if (group.items.length === 0) groups.value = groups.value.filter((g) => g !== group)
  if (groups.value.length === 0) groups.value.push(blankGroup())
}

const suggestions = ref<Customer[]>([])

async function searchCustomers(event: AutoCompleteCompleteEvent): Promise<void> {
  const page = await listCustomers({ q: event.query, status: 'active', perPage: 10 })
  suggestions.value = page.customers
}

function customerLabel(customer: Customer): string {
  return `${customer.code} — ${customer.name}`
}

const allItems = computed(() => groups.value.flatMap((g) => g.items))

function totalOf(items: Item[]): string | null {
  const centavos = sumCentavos(items.map((i) => i.amount).filter((a) => a !== ''))
  return centavos === null ? null : formatCentavos(centavos)
}

// Displayed, never sent. The server adds the lines up again and its answer is
// the one that is stored.
const total = computed(() => totalOf(allItems.value))

const complete = computed(() =>
  groups.value.length > 0 &&
  groups.value.every(
    (g) => g.customer !== null && g.items.length > 0 && g.items.every((i) => parseCentavos(i.amount) !== null),
  ),
)

function draft(): VoucherDraft {
  return {
    date: toISODate(voucherDate.value),
    notes: notes.value,
    lines: groups.value.flatMap((g) =>
      g.items.map((i) => ({
        customer_id: g.customer?.id ?? 0,
        date: g.date ? toISODate(g.date) : '',
        particular: i.particular,
        amount: i.amount,
      })),
    ),
  }
}

// Enter in a field saves a draft rather than posting: a draft moves no money,
// so it is the safe thing for a stray keypress to do.
function submit(): void {
  emit('submit', draft())
}

function post(): void {
  if (complete.value) emit('post', draft())
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
        <!-- One tbody per customer, so each group reads as a block. -->
        <tbody
          v-for="(group, g) in groups"
          :key="group.key"
          class="voucher__group"
          :data-group="group.key"
        >
          <tr v-for="(entry, i) in group.items" :key="entry.key">
            <td>
              <AutoComplete
                v-if="i === 0"
                v-model="group.customer"
                :suggestions="suggestions"
                :option-label="customerLabel"
                force-selection
                fluid
                placeholder="Code or name"
                :aria-label="`Customer ${g + 1}`"
                @complete="searchCustomers"
              />
            </td>
            <td>
              <DatePicker
                v-if="i === 0"
                v-model="group.date"
                date-format="yy-mm-dd"
                :max-date="latestDate"
                :placeholder="toISODate(voucherDate)"
                fluid
                :aria-label="`Date for customer ${g + 1}`"
              />
            </td>
            <td :data-item="entry.key">
              <InputText
                v-model="entry.particular"
                fluid
                placeholder="What it was for"
                :aria-label="`Particular ${i + 1} for customer ${g + 1}`"
              />
            </td>
            <td>
              <InputText
                v-model="entry.amount"
                class="voucher__amount"
                inputmode="decimal"
                placeholder="0.00"
                fluid
                :aria-label="`Amount ${i + 1} for customer ${g + 1}`"
                @keydown.enter.prevent="addItem(group)"
              />
            </td>
            <td>
              <Button
                type="button"
                icon="pi pi-times"
                severity="secondary"
                text
                :aria-label="`Remove particular ${i + 1} for customer ${g + 1}`"
                @click="removeItem(group, i)"
              />
            </td>
          </tr>
          <tr class="voucher__group-foot">
            <td colspan="2" />
            <td>
              <Button
                type="button"
                label="Add particular"
                icon="pi pi-plus"
                severity="secondary"
                text
                size="small"
                @click="addItem(group)"
              />
            </td>
            <td class="voucher__amount voucher__subtotal numeric">
              <template v-if="group.items.length > 1">
                {{ totalOf(group.items) === null ? '—' : `₱${totalOf(group.items)}` }}
              </template>
            </td>
            <td />
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td colspan="3">
              <Button
                type="button"
                label="Add customer"
                icon="pi pi-user-plus"
                severity="secondary"
                text
                @click="addGroup"
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
      <Button
        type="submit"
        :label="submitLabel ?? 'Save draft'"
        :severity="postLabel ? 'secondary' : undefined"
        :outlined="!!postLabel"
        :loading="busy && !postLabel"
        :disabled="!complete || busy"
      />
      <Button
        v-if="postLabel"
        type="button"
        :label="postLabel"
        icon="pi pi-check"
        :loading="busy"
        :disabled="!complete"
        @click="post"
      />
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

/* Each customer is a block: a rule between groups, none between its rows. */
.voucher__group + .voucher__group {
  border-top: 1px solid var(--shell-border);
}

.voucher__group-foot td {
  padding-top: 0;
}

.voucher__subtotal {
  padding-right: 0.5rem;
  font-size: 0.875rem;
  color: var(--shell-muted);
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
