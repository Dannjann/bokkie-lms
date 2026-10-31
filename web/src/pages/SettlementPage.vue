<script setup lang="ts">
/**
 * One settlement: what a farmer owed up to a cut-off date, what his recorded
 * payments paid of it, and what is left.
 *
 * It reads top to bottom the way the legacy "Compute Percentage" screen did:
 * the balance carried from his last settlement, the open advances with the
 * interest each has earned, the totals, less the payments — interest first —
 * and the present balance. A draft is the preview; settling writes exactly
 * these figures, and refuses if the record has moved since.
 */
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { entryLabel, methodLabel, type LedgerEntryView } from '@/api/payments'
import {
  balanceLabel,
  deleteSettlement,
  getSettlement,
  inCredit,
  postSettlement,
  recomputeSettlement,
  settlementStatusLabel,
  voidSettlement,
  type Settlement,
} from '@/api/settlements'
import { formatPesos, fromISODate, percentToBps, ratePercent, toISODate } from '@/format'
import { useSessionStore } from '@/stores/session'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()
const { user, isManager } = storeToRefs(useSessionStore())

const id = computed(() => Number(route.params.id))

const settlement = ref<Settlement | null>(null)
const entries = ref<LedgerEntryView[]>([])

const loading = ref(true)
const busy = ref(false)
const error = ref('')
/** The server said the preview is stale: offer to recompute it. */
const stale = ref(false)
const saved = ref('')

const voiding = ref(false)
const voidReason = ref('')

/** The draft's cut-off and rate, as they are being edited. */
const cutoff = ref<Date>(new Date())
const rate = ref('')
const notes = ref('')
const latest = new Date()

const isDraft = computed(() => settlement.value?.status === 'draft')
const isPosted = computed(() => settlement.value?.status === 'posted')
const hasPayments = computed(() => (settlement.value?.payments.length ?? 0) > 0)

/** "0.00", "-0.00" and "0" all read as nothing. Text only, no arithmetic. */
function isZero(amount: string): boolean {
  return /^-?0*(\.0*)?$/.test(amount)
}

const hasCarried = computed(() => {
  const c = settlement.value?.carried
  if (!c) return false
  return c.since !== '' || !isZero(c.principal) || !isZero(c.interest)
})

const printedAt = computed(() =>
  new Date().toLocaleString('en-PH', { dateStyle: 'medium', timeStyle: 'short' }),
)

function adopt(loaded: Settlement): void {
  settlement.value = loaded
  cutoff.value = fromISODate(loaded.cutoff) ?? new Date()
  rate.value = ratePercent(loaded.rate_bps).replace('%', '')
  notes.value = loaded.notes
  setPageTitle(
    loaded.settlement_no
      ? `Liquidation No. ${loaded.settlement_no}`
      : `Draft liquidation — ${loaded.customer_name}`,
  )
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const detail = await getSettlement(id.value)
    adopt(detail.settlement)
    entries.value = detail.entries
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

// Every one of these reloads rather than trusting the response alone: posting
// and voiding both change what the ledger says, and the entries underneath are
// the point of looking at a settled farmer.
async function act(what: string, action: () => Promise<Settlement>): Promise<void> {
  error.value = ''
  stale.value = false
  saved.value = ''
  busy.value = true
  try {
    adopt(await action())
    await load()
    saved.value = what
  } catch (err) {
    if (err instanceof ApiError) {
      error.value = err.message
      stale.value = err.status === 409 && /recompute/i.test(err.message)
    } else {
      error.value = 'Cannot reach the server.'
    }
  } finally {
    busy.value = false
  }
}

const rateInvalid = ref(false)

/**
 * Recompute the draft with the cut-off and rate on the form — which is also
 * the answer to a stale preview, since the form starts with the draft's own.
 */
function recompute(): void {
  const current = settlement.value
  if (!current) return

  rateInvalid.value = false
  const bps = percentToBps(rate.value.trim())
  if (bps === null || bps > 10000) {
    rateInvalid.value = true
    error.value = 'The rate must be a percentage a month, as 5 or 2.5 — at most two decimals.'
    return
  }

  void act('Recomputed.', () =>
    recomputeSettlement(id.value, {
      customer_id: current.customer_id,
      cutoff: toISODate(cutoff.value),
      rate_bps: bps,
      notes: notes.value.trim(),
    }),
  )
}

function settle(): void {
  void act('Liquidated.', () => postSettlement(id.value))
}

function confirmVoid(): void {
  voiding.value = false
  void act('Voided.', () => voidSettlement(id.value, voidReason.value))
}

async function discard(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await deleteSettlement(id.value)
    await router.replace({ name: 'settlements' })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    busy.value = false
  }
}


function print(): void {
  window.print()
}

onMounted(load)
</script>

<template>
  <div class="settlement">
    <div v-if="loading" class="settlement__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="settlement">
      <PageHeader>
        <template #title>
          <p class="settlement__no numeric label">
            {{
              settlement.settlement_no
                ? `Liquidation No. ${settlement.settlement_no}`
                : 'Unnumbered draft'
            }}
          </p>
          <h1 class="settlement__heading">
            {{ settlement.customer_name }}
            <Tag
              :severity="
                settlement.status === 'draft'
                  ? 'warn'
                  : settlement.status === 'voided'
                    ? 'danger'
                    : 'success'
              "
              :value="settlementStatusLabel(settlement.status)"
            />
          </h1>
        </template>
        <template #actions>
          <Button
            v-if="!isDraft"
            label="Print"
            icon="pi pi-print"
            severity="secondary"
            outlined
            @click="print"
          />
        </template>
      </PageHeader>

      <!-- The head the printer sees and the screen does not; see ReportShell. -->
      <div class="report__head">
        <div class="report__station">TLS Palay &amp; Corn Buying Station</div>
        <div class="report__title">
          {{
            settlement.settlement_no
              ? `Liquidation No. ${settlement.settlement_no}`
              : 'Liquidation (draft)'
          }}
          — {{ settlement.customer_code }} {{ settlement.customer_name }}
        </div>
        <div class="report__meta">
          Cut-off {{ settlement.cutoff }} at {{ ratePercent(settlement.rate_bps) }} a month ·
          printed by {{ user?.display_name ?? user?.username }} on {{ printedAt }}
        </div>
      </div>

      <div class="settlement__screen">
        <Message v-if="error" severity="error" :closable="false">
          <div class="settlement__error">
            <span>{{ error }}</span>
            <Button
              v-if="stale && isDraft"
              label="Recompute"
              icon="pi pi-refresh"
              size="small"
              :disabled="busy"
              @click="recompute"
            />
          </div>
        </Message>
        <Message v-else-if="saved" severity="success" :closable="false">{{ saved }}</Message>
      </div>

      <Message v-if="settlement.status === 'voided'" severity="warn" :closable="false">
        Voided by {{ settlement.voided_by.Username }} — {{ settlement.void_reason }}
      </Message>

      <!-- Header: who, up to when, at what rate. -->
      <section class="card card--padded settlement__record">
        <div class="settlement__row">
          <span class="settlement__label">Farmer</span>
          <RouterLink :to="{ name: 'customer', params: { id: settlement.customer_id } }">
            {{ settlement.customer_code }} — {{ settlement.customer_name }}
          </RouterLink>
        </div>
        <div class="settlement__row">
          <span class="settlement__label">Cut-off</span>
          <span class="numeric">{{ settlement.cutoff }}</span>
        </div>
        <div class="settlement__row">
          <span class="settlement__label">Rate</span>
          <span class="numeric">{{ ratePercent(settlement.rate_bps) }} a month</span>
        </div>
        <div v-if="settlement.notes" class="settlement__row">
          <span class="settlement__label">Notes</span>
          <span>{{ settlement.notes }}</span>
        </div>
        <div class="settlement__row">
          <span class="settlement__label">Computed by</span>
          <span>{{ settlement.created_by.Username }}</span>
        </div>
        <div v-if="settlement.status !== 'draft'" class="settlement__row">
          <span class="settlement__label">Settled by</span>
          <span>{{ settlement.posted_by.Username }}</span>
        </div>
      </section>

      <!-- Previous balance: what the last settlement left, and its interest since. -->
      <section class="card card--padded settlement__record">
        <h2 class="settlement__section settlement__section--flush">Previous balance</h2>
        <template v-if="hasCarried">
          <div class="settlement__row">
            <span class="settlement__label">Principal carried</span>
            <span class="numeric">{{ formatPesos(settlement.carried.principal) }}</span>
          </div>
          <div class="settlement__row">
            <span class="settlement__label">Unpaid interest</span>
            <span class="numeric">{{ formatPesos(settlement.carried.interest) }}</span>
          </div>
          <div class="settlement__row">
            <span class="settlement__label">Since</span>
            <span class="numeric">{{ settlement.carried.since || '—' }}</span>
          </div>
          <div class="settlement__row">
            <span class="settlement__label">Days</span>
            <span class="numeric">{{ settlement.carried.days }}</span>
          </div>
          <div class="settlement__row">
            <span class="settlement__label">Interest on the carried principal</span>
            <span class="numeric">{{ formatPesos(settlement.carried.charge) }}</span>
          </div>
        </template>
        <p v-else class="settlement__muted settlement__flush">
          Nothing carried — this farmer has not been liquidated before.
        </p>
      </section>

      <!-- Open advances, each charged for the days it has been out. -->
      <section class="card settlement__grid">
        <h2 class="settlement__section">Open advances</h2>
        <p v-if="settlement.advances.length === 0" class="settlement__empty">
          No open advances up to the cut-off.
        </p>
        <table v-else class="report__table settlement__table">
          <thead>
            <tr>
              <th class="report__code">Ref. No.</th>
              <th class="report__code">Date</th>
              <th>Particular</th>
              <th class="report__amount">Principal</th>
              <th class="report__amount">Days</th>
              <th class="report__amount">Interest</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="line in settlement.advances" :key="line.advance_line_id">
              <td class="report__code numeric">
                <RouterLink :to="{ name: 'advance', params: { id: line.voucher_id } }">
                  {{ line.ref_no }}
                </RouterLink>
              </td>
              <td class="report__code numeric">{{ line.date }}</td>
              <td>{{ line.particular }}</td>
              <td class="report__amount numeric">{{ formatPesos(line.principal) }}</td>
              <td class="report__amount numeric">{{ line.days }}</td>
              <td class="report__amount numeric">{{ formatPesos(line.charge) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="report__totals">
              <td colspan="3">
                {{ settlement.advances.length }}
                {{ settlement.advances.length === 1 ? 'advance' : 'advances' }}
              </td>
              <td class="report__amount numeric">{{ formatPesos(settlement.totals.advanced) }}</td>
              <td />
              <td class="report__amount numeric">
                {{ formatPesos(settlement.totals.advance_interest) }}
              </td>
            </tr>
          </tfoot>
        </table>
      </section>

      <!-- Totals: what he owed at the cut-off, before any payment. -->
      <section class="card card--padded settlement__record">
        <h2 class="settlement__section settlement__section--flush">Totals at the cut-off</h2>
        <div class="settlement__row">
          <span class="settlement__label">Principal</span>
          <span class="numeric">{{ formatPesos(settlement.totals.principal) }}</span>
        </div>
        <div class="settlement__row">
          <span class="settlement__label">
            Interest
            <span class="settlement__aside">
              ({{ formatPesos(settlement.totals.interest_charged) }} charged by this settlement)
            </span>
          </span>
          <span class="numeric">{{ formatPesos(settlement.totals.interest) }}</span>
        </div>
        <div class="settlement__row settlement__row--total">
          <span>Owed</span>
          <span class="numeric">{{ formatPesos(settlement.totals.owed) }}</span>
        </div>
      </section>

      <!-- Less: payments, applied interest first. -->
      <section class="card settlement__grid">
        <h2 class="settlement__section">Less: payments</h2>
        <p v-if="!hasPayments" class="settlement__empty">No recorded payment to apply.</p>
        <table v-else class="report__table settlement__table">
          <thead>
            <tr>
              <th class="report__code">OR No.</th>
              <th class="report__code">Date</th>
              <th>Method</th>
              <th class="report__amount">Amount</th>
              <th class="report__amount">To interest</th>
              <th class="report__amount">To principal</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in settlement.payments" :key="p.payment_id">
              <td class="report__code numeric">
                <RouterLink :to="{ name: 'payment', params: { id: p.payment_id } }">
                  {{ p.or_no }}
                </RouterLink>
              </td>
              <td class="report__code numeric">{{ p.date }}</td>
              <td>{{ methodLabel(p.method) }}</td>
              <td class="report__amount numeric">{{ formatPesos(p.amount) }}</td>
              <td class="report__amount numeric">{{ formatPesos(p.interest) }}</td>
              <td class="report__amount numeric">{{ formatPesos(p.principal) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="report__totals">
              <td colspan="3">
                {{ settlement.payments.length }}
                {{ settlement.payments.length === 1 ? 'payment' : 'payments' }}
              </td>
              <td class="report__amount numeric">{{ formatPesos(settlement.totals.paid) }}</td>
              <td class="report__amount numeric">
                {{ formatPesos(settlement.totals.paid_interest) }}
              </td>
              <td class="report__amount numeric">
                {{ formatPesos(settlement.totals.paid_principal) }}
              </td>
            </tr>
          </tfoot>
        </table>
      </section>

      <!-- Present balance: what is left, or by how much he is in credit. -->
      <section class="card card--padded settlement__record">
        <h2 class="settlement__section settlement__section--flush">Present balance</h2>
        <div class="settlement__row">
          <span class="settlement__label">Principal</span>
          <span class="numeric">{{ balanceLabel(settlement.totals.principal_after) }}</span>
        </div>
        <div class="settlement__row">
          <span class="settlement__label">Interest</span>
          <span class="numeric">{{ balanceLabel(settlement.totals.interest_after) }}</span>
        </div>
        <div
          class="settlement__row settlement__row--total"
          :class="{ 'settlement__row--credit': inCredit(settlement.totals.balance_after) }"
        >
          <span>Balance</span>
          <span class="numeric">{{ balanceLabel(settlement.totals.balance_after) }}</span>
        </div>
        <p v-if="inCredit(settlement.totals.balance_after)" class="settlement__muted settlement__flush">
          The delivery was worth more than the debt: the station owes the farmer the difference.
        </p>
      </section>

      <!-- Draft: change the cut-off or rate, discard, or settle. -->
      <template v-if="isDraft">
        <Message v-if="!hasPayments" severity="info" :closable="false" class="settlement__screen">
          This is a statement of what {{ settlement.customer_name }} owes up to the cut-off. It
          cannot be liquidated until a payment is recorded for him.
        </Message>

        <section class="card card--padded settlement__screen settlement__edit">
          <h2 class="settlement__section settlement__section--flush">Change and recompute</h2>
          <div class="settlement__fields">
            <div class="settlement__field">
              <label for="settlement-cutoff" class="label">Cut-off date</label>
              <DatePicker
                input-id="settlement-cutoff"
                v-model="cutoff"
                date-format="yy-mm-dd"
                :max-date="latest"
                show-icon
                icon-display="input"
                fluid
              />
            </div>
            <div class="settlement__field settlement__field--rate">
              <label for="settlement-rate" class="label">Rate a month</label>
              <div class="settlement__rate">
                <InputText
                  id="settlement-rate"
                  v-model="rate"
                  inputmode="decimal"
                  autocomplete="off"
                  :invalid="rateInvalid"
                  fluid
                />
                <span class="settlement__muted" aria-hidden="true">%</span>
              </div>
            </div>
            <div class="settlement__field settlement__field--notes">
              <label for="settlement-notes" class="label">Notes</label>
              <InputText id="settlement-notes" v-model="notes" fluid />
            </div>
          </div>

          <div class="settlement__actions">
            <Button
              label="Discard draft"
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
              label="Liquidate"
              icon="pi pi-check"
              severity="success"
              :disabled="busy || !hasPayments"
              @click="settle"
            />
          </div>
        </section>
      </template>

      <template v-else>
        <section v-if="entries.length > 0" class="card settlement__grid settlement__screen">
          <h2 class="settlement__section">In the ledger</h2>
          <table class="report__table settlement__table">
            <thead>
              <tr>
                <th class="report__code">Date</th>
                <th>Entry</th>
                <th>By</th>
                <th class="report__amount">Principal</th>
                <th class="report__amount">Interest</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in entries" :key="entry.id">
                <td class="report__code numeric">{{ entry.date }}</td>
                <td>
                  {{ entryLabel(entry.type) }}
                  <span v-if="entry.reason" class="settlement__muted">— {{ entry.reason }}</span>
                </td>
                <td>{{ entry.by }}</td>
                <td class="report__amount numeric">{{ formatPesos(entry.principal) }}</td>
                <td class="report__amount numeric">{{ formatPesos(entry.interest) }}</td>
              </tr>
            </tbody>
          </table>
        </section>

        <div v-if="isManager && isPosted" class="settlement__actions settlement__screen">
          <Button
            label="Void this liquidation"
            icon="pi pi-ban"
            severity="danger"
            outlined
            :disabled="busy"
            @click="voiding = true"
          />
        </div>
      </template>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such liquidation.' }}</Message>

    <Dialog v-model:visible="voiding" modal header="Void this liquidation" :style="{ width: '28rem' }">
      <p class="settlement__dialog-text">
        The interest it charged and the payments it applied are reversed in the ledger. Its
        advances and payments open again, waiting for a new liquidation. The liquidation and its
        reversals stay on the record, and this reason is written onto each one.
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
@import '@/styles/report-table.css';

.settlement {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.settlement__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.settlement__no {
  margin: 0;
}

.settlement__heading {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.settlement__screen {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.settlement__error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.settlement__record {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.settlement__row {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
}

.settlement__row .numeric {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.settlement__row--total {
  margin-top: 0.3rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--shell-border);
  font-weight: 600;
}

.settlement__row--credit {
  color: var(--p-green-700);
}

.settlement__label {
  color: var(--shell-muted);
}

.settlement__aside {
  font-size: 0.8125rem;
}

.settlement__muted {
  color: var(--shell-muted);
}

.settlement__flush {
  margin: 0;
  font-size: 0.875rem;
}

.settlement__grid {
  /* min-width: 0 so the flex item can shrink below its content; without it the
     overflow-x below contains nothing and the page scrolls sideways instead. */
  min-width: 0;
  overflow-x: auto;
  padding: 0.25rem 0.75rem 0.75rem;
}

.settlement__section {
  margin: 0.75rem 0.25rem 0.5rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.settlement__section--flush {
  margin: 0 0 0.35rem;
}

.settlement__empty {
  margin: 0;
  padding: 0.5rem 0.25rem 1rem;
  color: var(--shell-muted);
}

.settlement__edit {
  gap: 0.75rem;
}

.settlement__fields {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.settlement__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 11rem;
  flex: 1;
}

.settlement__field--rate {
  max-width: 11rem;
}

.settlement__field--notes {
  flex-basis: 100%;
}

.settlement__rate {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.settlement__actions {
  display: flex;
  flex-direction: row;
  justify-content: flex-end;
  gap: 0.5rem;
}

.settlement__dialog-text {
  margin: 0 0 0.75rem;
  color: var(--shell-muted);
}

/* On paper a settlement is the farmer's copy: the figures and nothing to press. */
@media print {
  .settlement__screen {
    display: none !important;
  }

  .settlement {
    gap: 0.35cm;
  }

  .settlement__section {
    margin: 0.2cm 0 0.1cm;
    font-size: 10.5pt;
  }

  .settlement__record {
    font-size: 9.5pt;
    gap: 1pt;
  }

  .settlement__label,
  .settlement__muted,
  .settlement__row--credit {
    color: #000 !important;
  }

  .settlement__grid {
    overflow: visible;
    padding: 0;
  }

  .settlement__table {
    min-width: 0;
  }
}
</style>
