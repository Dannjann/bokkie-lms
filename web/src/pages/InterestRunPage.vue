<script setup lang="ts">
/**
 * One run: what it would charge, or what it charged.
 *
 * This is the screen the whole step is shaped around. Everything on it exists
 * to make the size of the act visible before it is taken — how many farmers,
 * how much in total, at what rate, computed from what — because the button at
 * the bottom writes an entry against every one of those farmers at once.
 */
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  basisLabel,
  deleteRun,
  getRun,
  methodLabel,
  periodLabel,
  postRun,
  ratePercent,
  recomputeRun,
  voidRun,
  type Run,
} from '@/api/interest'
import type { LedgerEntryView } from '@/api/payments'
import { formatPesos } from '@/format'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()

const id = computed(() => Number(route.params.id))

const run = ref<Run | null>(null)
const entries = ref<LedgerEntryView[]>([])

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const saved = ref('')

const voiding = ref(false)
const voidReason = ref('')

const isDraft = computed(() => run.value?.status === 'draft')

/** "3% on the balance (principal only)", as one sentence. */
const ruleLabel = computed(() => {
  const r = run.value?.rule
  if (!r) return ''
  const method = methodLabel(r.method).toLowerCase()
  const basis = r.method === 'monthly-on-balance' ? ` (${basisLabel(r.basis).toLowerCase()})` : ''
  return `${ratePercent(r.rate_bps)} ${method}${basis}`
})

/** What the base column means, which depends on the method and not on the row. */
const baseHeader = computed(() =>
  run.value?.rule.method === 'flat-on-advance' ? 'Advanced' : 'Owed',
)

const charges = computed(() => entries.value.filter((e) => e.type === 'interest'))
const reversals = computed(() => entries.value.filter((e) => e.type === 'reversal'))

function adopt(loaded: Run): void {
  run.value = loaded
  setPageTitle(loaded.run_no ? `Interest run ${loaded.run_no}` : 'Draft interest run')
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const detail = await getRun(id.value)
    adopt(detail.run)
    entries.value = detail.entries
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

// Every one of these reloads rather than trusting the response alone: posting
// and voiding both change what the ledger says, and the entries underneath are
// the point of looking at a posted run.
async function act(what: string, action: () => Promise<Run>): Promise<void> {
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

/**
 * Recompute against the same period.
 *
 * This is the answer to both refusals a post can carry: the figures are stale
 * because somebody paid, or the rate was wrong and has been changed. One click
 * either way.
 */
function recompute(): void {
  const current = run.value
  if (!current) return
  void act('Recomputed.', () =>
    recomputeRun(id.value, {
      period_from: current.period_from,
      period_to: current.period_to,
      notes: current.notes,
    }),
  )
}

function post(): void {
  void act('Charged.', () => postRun(id.value))
}

function confirmVoid(): void {
  voiding.value = false
  void act('Voided.', () => voidRun(id.value, voidReason.value))
}

async function discard(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await deleteRun(id.value)
    await router.replace({ name: 'interest' })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="run">
    <div v-if="loading" class="run__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="run">
      <PageHeader>
        <template #title>
          <p class="run__no numeric label">
            {{ run.run_no ? `Interest run ${run.run_no}` : 'Uncharged draft' }}
          </p>
          <h1 class="run__heading">
            {{ formatPesos(run.total) }}
            <Tag v-if="run.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="run.status === 'voided'" severity="danger" value="Voided" />
          </h1>
        </template>
        <template #actions>
          <Button
            label="Back to interest"
            icon="pi pi-arrow-left"
            severity="secondary"
            text
            @click="router.push({ name: 'interest' })"
          />
        </template>
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">{{ saved }}</Message>

      <Message v-if="run.status === 'voided'" severity="warn" :closable="false">
        Voided by {{ run.voided_by.Username }} — {{ run.void_reason }}
      </Message>

      <section class="card card--padded run__record">
        <div class="run__row">
          <span class="run__label">Period</span>
          <span class="numeric">{{ periodLabel(run.period_from, run.period_to) }}</span>
        </div>
        <div class="run__row">
          <span class="run__label">Rule</span>
          <span>{{ ruleLabel }}</span>
        </div>
        <div class="run__row">
          <span class="run__label">Farmers</span>
          <span class="numeric">{{ run.lines.length }}</span>
        </div>
        <div v-if="run.notes" class="run__row">
          <span class="run__label">Notes</span>
          <span>{{ run.notes }}</span>
        </div>
        <div v-if="run.status !== 'draft'" class="run__row">
          <span class="run__label">Charged by</span>
          <span>{{ run.posted_by.Username }}</span>
        </div>
      </section>

      <!--
        The preview. These are rows the server computed and stored, not a
        description of what it would do — posting writes these same rows, and
        refuses if any of them has moved since.
      -->
      <section class="card run__grid">
        <h2 class="run__section">
          {{ isDraft ? 'What this would charge' : 'What this charged' }}
        </h2>

        <p v-if="run.lines.length === 0" class="run__empty">
          Nobody owed anything over this period, so there is nothing to charge.
        </p>

        <table v-else class="run__table">
          <thead>
            <tr>
              <th class="run__code">Code</th>
              <th>Farmer</th>
              <th class="run__amount">{{ baseHeader }}</th>
              <th class="run__amount">Interest</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="line in run.lines" :key="line.id">
              <td class="run__code numeric">{{ line.customer_code }}</td>
              <td>
                <RouterLink :to="{ name: 'customer', params: { id: line.customer_id } }">
                  {{ line.customer_name }}
                </RouterLink>
              </td>
              <td class="run__amount numeric">{{ formatPesos(line.base) }}</td>
              <td class="run__amount numeric">{{ formatPesos(line.charge) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr>
              <td colspan="3" class="run__total-label">
                {{ run.lines.length }} {{ run.lines.length === 1 ? 'farmer' : 'farmers' }}
              </td>
              <td class="run__amount numeric run__total">{{ formatPesos(run.total) }}</td>
            </tr>
          </tfoot>
        </table>
      </section>

      <div v-if="isDraft" class="run__actions">
        <Button
          label="Delete draft"
          icon="pi pi-trash"
          severity="danger"
          text
          :disabled="busy"
          @click="discard"
        />
        <Button
          label="Recompute"
          icon="pi pi-refresh"
          severity="secondary"
          outlined
          :disabled="busy"
          @click="recompute"
        />
        <Button
          label="Charge these farmers"
          icon="pi pi-check"
          severity="success"
          :disabled="busy || run.lines.length === 0"
          @click="post"
        />
      </div>

      <template v-else>
        <section class="card run__grid">
          <h2 class="run__section">In the ledger</h2>
          <p class="run__note">
            {{ charges.length }} {{ charges.length === 1 ? 'charge' : 'charges' }}
            <template v-if="reversals.length">
              and {{ reversals.length }}
              {{ reversals.length === 1 ? 'reversal' : 'reversals' }}
            </template>
            written by this run.
          </p>
          <table class="run__table">
            <thead>
              <tr>
                <th class="run__code">Date</th>
                <th>Entry</th>
                <th>By</th>
                <th class="run__amount">Interest</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in entries" :key="entry.id">
                <td class="run__code numeric">{{ entry.date }}</td>
                <td>
                  {{ entry.type === 'reversal' ? 'Reversal' : 'Interest' }}
                  <span v-if="entry.reason" class="run__muted">— {{ entry.reason }}</span>
                </td>
                <td>{{ entry.by }}</td>
                <td class="run__amount numeric">{{ formatPesos(entry.interest) }}</td>
              </tr>
            </tbody>
          </table>
        </section>

        <div v-if="run.status === 'posted'" class="run__actions">
          <Button
            label="Void this run"
            icon="pi pi-ban"
            severity="danger"
            outlined
            :disabled="busy"
            @click="voiding = true"
          />
        </div>
      </template>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such interest run.' }}</Message>

    <Dialog v-model:visible="voiding" modal header="Void this run" :style="{ width: '28rem' }">
      <p class="run__dialog-text">
        Every charge this run made will be reversed, all at once, and
        {{ run?.lines.length ?? 0 }}
        {{ (run?.lines.length ?? 0) === 1 ? 'farmer' : 'farmers' }} will owe the interest back. The
        run and its reversals both stay on the record, and this reason is written onto each one.
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
.run {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.run__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.run__no {
  margin: 0;
}

.run__heading {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  font-variant-numeric: tabular-nums;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.run__record {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.run__row {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
}

.run__label {
  color: var(--shell-muted);
}

.run__grid {
  /* min-width: 0 so the flex item can shrink below its content; without it the
     overflow-x below contains nothing and the page scrolls sideways instead. */
  min-width: 0;
  overflow-x: auto;
  padding: 0.25rem 0.75rem 0.75rem;
}

.run__section {
  margin: 0.75rem 0.25rem 0.5rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.run__table {
  width: 100%;
  min-width: 32rem;
  border-collapse: collapse;
}

.run__table th {
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.run__table td {
  padding: 0.5rem;
  border-bottom: 1px solid var(--shell-border);
}

.run__code {
  white-space: nowrap;
}

.run__amount {
  text-align: right;
  white-space: nowrap;
}

.run__total-label {
  color: var(--shell-muted);
  text-align: right;
}

.run__total {
  font-weight: 600;
}

.run__table tfoot td {
  border-bottom: none;
}

.run__muted {
  color: var(--shell-muted);
}

.run__note {
  margin: 0 0.25rem 0.5rem;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.run__empty {
  margin: 0;
  padding: 1rem 0.25rem 1.25rem;
  color: var(--shell-muted);
}

.run__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

.run__dialog-text {
  margin: 0 0 0.75rem;
  color: var(--shell-muted);
}
</style>
