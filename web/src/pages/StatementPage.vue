<script setup lang="ts">
/**
 * One farmer's statement: what they owed at the start, everything that has
 * happened since, and what they owe now.
 *
 * Every peso figure on this screen comes from the server already computed —
 * the opening balance, the running balance on each row, the close, the totals.
 * A running balance is the most tempting place in this whole application to add
 * money up in JavaScript, and it is the one that would be hardest to notice
 * going wrong.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { getStatement, type Statement } from '@/api/payments'
import { formatPesos, toISODate } from '@/format'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))

const statement = ref<Statement | null>(null)
const loading = ref(true)
const error = ref('')

// Both ends optional. The statement a farmer asks for at the counter is
// usually "everything", and a date range is what a manager reaches for when
// they are reconciling a season.
const from = ref<Date | null>(null)
const to = ref<Date | null>(null)

const latestDate = new Date()

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const loaded = await getStatement(
      id.value,
      from.value ? toISODate(from.value) : undefined,
      to.value ? toISODate(to.value) : undefined,
    )
    statement.value = loaded
    setPageTitle(`${loaded.customer.name} — statement`)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

let debounce: ReturnType<typeof setTimeout> | undefined

watch([from, to], () => {
  clearTimeout(debounce)
  debounce = setTimeout(load, 150)
})

function clearRange(): void {
  from.value = null
  to.value = null
}

/** What an entry is called on a statement, in the words the station uses. */
function entryLabel(type: string): string {
  switch (type) {
    case 'advance':
      return 'Advance'
    case 'payment':
      return 'Payment'
    case 'interest':
      return 'Interest'
    case 'reversal':
      return 'Reversal'
    case 'opening':
      return 'Opening balance'
    case 'adjustment':
      return 'Adjustment'
    default:
      return type
  }
}

onMounted(load)
</script>

<template>
  <div class="statement">
    <div v-if="loading && !statement" class="statement__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="statement">
      <PageHeader>
        <template #title>
          <p class="statement__code numeric label">{{ statement.customer.code }}</p>
          <h1 class="statement__name">{{ statement.customer.name }}</h1>
        </template>
        <template #actions>
          <Button
            label="Record payment"
            icon="pi pi-money-bill"
            severity="secondary"
            outlined
            @click="router.push({ name: 'payment-new', query: { customer: id } })"
          />
          <Button
            label="Back to customer"
            icon="pi pi-arrow-left"
            severity="secondary"
            text
            @click="router.push({ name: 'customer', params: { id } })"
          />
        </template>
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

      <div class="card">
        <div class="statement__filters">
          <div class="statement__range">
            <label for="statement-from">From</label>
            <DatePicker
              id="statement-from"
              v-model="from"
              date-format="yy-mm-dd"
              :max-date="latestDate"
              show-icon
              icon-display="input"
              placeholder="The beginning"
            />
          </div>
          <div class="statement__range">
            <label for="statement-to">To</label>
            <DatePicker
              id="statement-to"
              v-model="to"
              date-format="yy-mm-dd"
              :max-date="latestDate"
              show-icon
              icon-display="input"
              placeholder="Today"
            />
          </div>
          <Button
            v-if="from || to"
            label="Whole history"
            severity="secondary"
            text
            @click="clearRange"
          />
          <span class="statement__count numeric">
            {{ statement.rows.length }}
            {{ statement.rows.length === 1 ? 'entry' : 'entries' }}
          </span>
        </div>

        <div class="statement__scroll">
          <table class="statement__table">
            <thead>
              <tr>
                <th class="statement__date">Date</th>
                <th>Entry</th>
                <th>Document</th>
                <th class="statement__amount">Principal</th>
                <th class="statement__amount">Interest</th>
                <th class="statement__amount">Balance</th>
              </tr>
            </thead>
            <tbody>
              <!--
                The opening row is not an entry and is not styled like one. It
                is what the farmer owed before this window began, and without it
                a statement of the last month reads as though they started it
                owing nothing.
              -->
              <tr class="statement__carry">
                <td class="statement__date numeric">{{ statement.from ?? '' }}</td>
                <td colspan="4">Balance brought forward</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.opening.total) }}</td>
              </tr>

              <tr v-for="row in statement.rows" :key="row.entry.id">
                <td class="statement__date numeric">{{ row.entry.date }}</td>
                <td>
                  {{ entryLabel(row.entry.type) }}
                  <span v-if="row.entry.reason" class="statement__reason">
                    — {{ row.entry.reason }}
                  </span>
                </td>
                <td class="statement__document">{{ row.document ?? '—' }}</td>
                <td class="statement__amount numeric">{{ formatPesos(row.entry.principal) }}</td>
                <td class="statement__amount numeric">{{ formatPesos(row.entry.interest) }}</td>
                <td class="statement__amount numeric">{{ formatPesos(row.running.total) }}</td>
              </tr>

              <tr v-if="statement.rows.length === 0" class="statement__empty-row">
                <td colspan="6">Nothing happened in this period.</td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td colspan="3">Movement over the period</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.moved.principal) }}</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.moved.interest) }}</td>
                <td class="statement__amount numeric" />
              </tr>
              <tr class="statement__closing">
                <td colspan="3">Owed as at {{ statement.to }}</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.closing.principal) }}</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.closing.interest) }}</td>
                <td class="statement__amount numeric">{{ formatPesos(statement.closing.total) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </div>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such customer.' }}</Message>
  </div>
</template>

<style scoped>
.statement {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.statement__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.statement__code {
  margin: 0;
}

.statement__name {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.statement__filters {
  display: flex;
  align-items: flex-end;
  gap: 0.75rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.statement__range {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.statement__range label {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--shell-muted);
}

.statement__count {
  margin-left: auto;
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

/* The table scrolls inside the card rather than dragging the page sideways —
   six columns do not fit on a phone, and the page itself must never scroll
   horizontally. min-width: 0 is what makes the overflow actually contain
   something. */
.statement__scroll {
  min-width: 0;
  overflow-x: auto;
  padding: 0 0.75rem 0.75rem;
}

.statement__table {
  width: 100%;
  min-width: 44rem;
  border-collapse: collapse;
}

.statement__table th {
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.statement__table td {
  padding: 0.5rem;
  border-bottom: 1px solid var(--shell-border);
}

.statement__carry td {
  color: var(--shell-muted);
  background: var(--p-surface-50);
}

.statement__table tfoot td {
  border-bottom: none;
  font-weight: 600;
}

.statement__closing td {
  border-top: 2px solid var(--shell-border);
}

.statement__date {
  white-space: nowrap;
}

.statement__document {
  white-space: nowrap;
  color: var(--shell-muted);
}

.statement__amount {
  text-align: right;
  white-space: nowrap;
}

.statement__reason {
  color: var(--shell-muted);
}

.statement__empty-row td {
  padding: 1.25rem 0.5rem;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .statement__filters {
    flex-wrap: wrap;
  }

  .statement__count {
    margin-left: 0;
  }
}
</style>
