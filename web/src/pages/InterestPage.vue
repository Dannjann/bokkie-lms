<script setup lang="ts">
/**
 * The interest section: the rule the station charges under, and every run it
 * has made.
 *
 * The rule lives here rather than on a general settings screen because there is
 * no general settings screen yet, and a manager who is told "set the rule in
 * Settings" and finds no Settings has been sent nowhere. The three keys are
 * written through the ordinary settings API, so when that screen does arrive it
 * edits the same values and neither has to be unwound.
 */
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  interestBases,
  interestMethods,
  listRuns,
  methodLabel,
  periodLabel,
  ratePercent,
  type InterestBasis,
  type InterestMethod,
  type RunStatus,
  type RunSummary,
} from '@/api/interest'
import { listSettings, updateSetting } from '@/api/settings'
import { formatPesos, parseCentavos } from '@/format'

const router = useRouter()

const runs = ref<RunSummary[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')

const status = ref<RunStatus | 'all'>('all')
const first = ref(0)
const perPage = ref(25)

const statusOptions = [
  { label: 'All', value: 'all' },
  { label: 'Drafts', value: 'draft' },
  { label: 'Posted', value: 'posted' },
  { label: 'Voided', value: 'voided' },
]

/** The rule, as the settings currently hold it. */
const method = ref<InterestMethod>('none')
const basis = ref<InterestBasis>('principal')
const rate = ref('')
const ruleBusy = ref(false)
const ruleError = ref('')
const ruleSaved = ref('')

/**
 * A percentage typed by a person, as the basis points the server stores.
 *
 * parseCentavos is exactly the right parser and not a borrowed one: a
 * percentage with at most two decimal places and an integer of hundredths are
 * the same thing. "3" is 300 basis points, "0.25" is 25, and nothing passes
 * through a float on the way.
 */
function percentToBps(text: string): number | null {
  return parseCentavos(text)
}

async function loadRule(): Promise<void> {
  try {
    const { settings } = await listSettings()
    for (const setting of settings) {
      if (setting.key === 'interest.method') method.value = setting.value as InterestMethod
      if (setting.key === 'interest.basis') basis.value = setting.value as InterestBasis
      if (setting.key === 'interest.rate_bps') {
        rate.value = Number(setting.value) ? ratePercent(Number(setting.value)).replace('%', '') : ''
      }
    }
  } catch (err) {
    ruleError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  }
}

async function saveRule(): Promise<void> {
  ruleError.value = ''
  ruleSaved.value = ''

  const bps = method.value === 'none' ? 0 : percentToBps(rate.value)
  if (bps === null) {
    ruleError.value = 'The rate must be a percentage, as 3 or 2.5.'
    return
  }

  ruleBusy.value = true
  try {
    await updateSetting('interest.method', method.value)
    await updateSetting('interest.rate_bps', String(bps))
    await updateSetting('interest.basis', basis.value)
    ruleSaved.value = 'Saved. Runs already computed keep the rule they were computed under.'
  } catch (err) {
    ruleError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    ruleBusy.value = false
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const page = await listRuns({
      status: status.value === 'all' ? undefined : status.value,
      page: Math.floor(first.value / perPage.value) + 1,
      perPage: perPage.value,
    })
    runs.value = page.runs
    total.value = page.total
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    runs.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

watch(status, () => {
  first.value = 0
  load()
})

function onPage(event: DataTablePageEvent): void {
  first.value = event.first
  perPage.value = event.rows
  load()
}

function open(run: RunSummary): void {
  router.push({ name: 'interest-run', params: { id: run.id } })
}

onMounted(() => {
  void loadRule()
  void load()
})
</script>

<template>
  <div class="interest">
    <PageHeader
      title="Interest"
      description="A run charges every borrower at once. It is previewed before it is charged, and undone in one act."
    >
      <template #actions>
        <Button
          label="New run"
          icon="pi pi-plus"
          :disabled="method === 'none'"
          @click="router.push({ name: 'interest-new' })"
        />
      </template>
    </PageHeader>

    <Message v-if="method === 'none'" severity="warn" :closable="false">
      No interest rule is set, so nothing is being charged. Choose one below before making a run.
    </Message>

    <section class="card card--padded interest__rule">
      <h2 class="interest__section">The rule</h2>
      <p class="interest__hint">
        What a run charges, and on what. Changing it here does not change a run that has already
        been computed — that run keeps the rule it was made under, and recomputing it is what picks
        a new one up.
      </p>

      <Message v-if="ruleError" severity="error" :closable="false">{{ ruleError }}</Message>
      <Message v-else-if="ruleSaved" severity="success" :closable="false">{{ ruleSaved }}</Message>

      <div class="interest__fields">
        <div class="interest__field">
          <label for="interest-method" class="label">Charged</label>
          <Select
            input-id="interest-method"
            v-model="method"
            :options="interestMethods"
            option-label="label"
            option-value="value"
            fluid
          />
          <small class="interest__help">
            {{ interestMethods.find((m) => m.value === method)?.help }}
          </small>
        </div>

        <div v-if="method !== 'none'" class="interest__field interest__field--rate">
          <label for="interest-rate" class="label">Rate</label>
          <div class="interest__rate">
            <InputText
              id="interest-rate"
              v-model="rate"
              inputmode="decimal"
              placeholder="3"
              autocomplete="off"
              fluid
            />
            <span class="interest__percent">%</span>
          </div>
          <small class="interest__help">Of the period. 2.5 and 0.25 are both allowed.</small>
        </div>

        <div v-if="method === 'monthly-on-balance'" class="interest__field">
          <label for="interest-basis" class="label">On</label>
          <Select
            input-id="interest-basis"
            v-model="basis"
            :options="interestBases"
            option-label="label"
            option-value="value"
            fluid
          />
          <small class="interest__help">
            {{ interestBases.find((b) => b.value === basis)?.help }}
          </small>
        </div>
      </div>

      <div class="interest__rule-actions">
        <Button label="Save rule" icon="pi pi-check" :disabled="ruleBusy" @click="saveRule" />
      </div>
    </section>

    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

    <div class="card">
      <div class="interest__filters">
        <h2 class="interest__section interest__section--inline">Runs</h2>
        <Select
          v-model="status"
          :options="statusOptions"
          option-label="label"
          option-value="value"
          aria-label="Filter by status"
          class="interest__status"
        />
        <span class="interest__count numeric">{{ total }} {{ total === 1 ? 'run' : 'runs' }}</span>
      </div>

      <DataTable
        :value="runs"
        :loading="loading"
        lazy
        paginator
        :rows="perPage"
        :first="first"
        :total-records="total"
        :rows-per-page-options="[25, 50, 100]"
        data-key="id"
        selection-mode="single"
        class="interest__table"
        @page="onPage"
        @row-select="open($event.data)"
      >
        <template #empty>
          <p class="interest__empty">No interest has been run yet.</p>
        </template>

        <Column header="Run" class="interest__no">
          <template #body="{ data }">
            <span v-if="data.run_no">{{ data.run_no }}</span>
            <span v-else class="interest__unnumbered">Draft</span>
          </template>
        </Column>
        <Column header="Period" class="interest__period">
          <template #body="{ data }">{{ periodLabel(data.period_from, data.period_to) }}</template>
        </Column>
        <Column header="Rule" class="interest__rule-cell">
          <!--
            The basis is named only when it is the one that changes what money
            does. "Principal only" is the default and the ordinary case, and
            spelling it out on every row wrapped this column onto three lines at
            the width the station runs — which buried the rate, the one figure
            the column exists for.
          -->
          <template #body="{ data }">
            {{ ratePercent(data.rule.rate_bps) }} {{ methodLabel(data.rule.method).toLowerCase() }}
            <span
              v-if="data.rule.basis === 'principal-and-interest'"
              class="interest__muted"
            >
              (compounding)
            </span>
          </template>
        </Column>
        <Column header="Farmers" class="interest__farmers">
          <template #body="{ data }">{{ data.customers }}</template>
        </Column>
        <Column header="Charged" class="interest__amount">
          <template #body="{ data }">{{ formatPesos(data.total) }}</template>
        </Column>
        <Column header="Status" class="interest__status-cell">
          <template #body="{ data }">
            <Tag v-if="data.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="data.status === 'voided'" severity="danger" value="Voided" />
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>

<style scoped>
.interest {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.interest__section {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
}

.interest__section--inline {
  margin-right: auto;
}

.interest__hint {
  margin: 0.35rem 0 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
  max-width: 44rem;
}

.interest__rule {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.interest__fields {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.interest__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 14rem;
  flex: 1;
}

.interest__field--rate {
  max-width: 11rem;
  min-width: 8rem;
}

.interest__rate {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.interest__percent {
  color: var(--shell-muted);
}

.interest__help {
  color: var(--shell-muted);
  font-size: 0.8125rem;
}

.interest__rule-actions {
  display: flex;
  justify-content: flex-end;
}

.interest__filters {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.75rem;
  border-bottom: 1px solid var(--shell-border);
}

.interest__status {
  min-width: 9rem;
}

.interest__count {
  font-size: 0.8125rem;
  color: var(--shell-muted);
  white-space: nowrap;
}

.interest__muted {
  color: var(--shell-muted);
}

.interest__table :deep(.interest__no) {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  white-space: nowrap;
}

.interest__unnumbered {
  color: var(--shell-muted);
}

/* The rate is the figure this column exists for, so it is not allowed to wrap
   away from the method it belongs to. There is room: the period beside it is
   fixed-width and the two columns after it are narrow. */
.interest__table :deep(.interest__rule-cell) {
  white-space: nowrap;
}

.interest__table :deep(.interest__period),
.interest__table :deep(.interest__farmers) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.interest__table :deep(.interest__farmers) {
  text-align: right;
}

.interest__table :deep(.interest__amount) {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.interest__table :deep(.interest__status-cell) {
  width: 1%;
  white-space: nowrap;
}

.interest__table :deep(tbody tr) {
  cursor: pointer;
}

.interest__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.interest__table :deep(.p-datatable-paginator-bottom) {
  border-width: 1px 0 0;
  border-radius: 0 0 var(--shell-radius) var(--shell-radius);
}

.interest__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

@media (max-width: 40rem) {
  .interest__filters {
    flex-wrap: wrap;
  }
}
</style>
