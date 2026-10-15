<script setup lang="ts">
/**
 * Settling a farmer: who, up to what day, and at what rate.
 *
 * Nothing is applied here. Pressing Compute writes a draft and opens it, which
 * is the preview — the legacy "Compute Percentage" screen before its Liquidate
 * button. The rate starts at the station's usual one and can be changed for
 * this farmer, down to 0 to waive it.
 */
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AutoComplete, { type AutoCompleteCompleteEvent } from 'primevue/autocomplete'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { getCustomer, listCustomers, type Customer } from '@/api/customers'
import { createSettlement } from '@/api/settlements'
import { listSettings } from '@/api/settings'
import { percentToBps, ratePercent, toISODate } from '@/format'

const route = useRoute()
const router = useRouter()

const error = ref('')
const busy = ref(false)

const customer = ref<Customer | null>(null)
const cutoff = ref(new Date())
const rate = ref('')
const notes = ref('')

// A cut-off in the future would charge interest for days that have not
// happened yet.
const latest = new Date()

const suggestions = ref<Customer[]>([])

// Deactivated farmers are included, as on the payment form: the farmer whose
// relationship has ended is the one most likely to be settling up.
async function searchCustomers(event: AutoCompleteCompleteEvent): Promise<void> {
  const page = await listCustomers({ q: event.query, perPage: 10 })
  suggestions.value = page.customers
}

function customerLabel(c: Customer): string {
  return `${c.code} — ${c.name}`
}

/** The farmer named in ?customer_id=, from a customer or a payment. */
async function prefill(): Promise<void> {
  const id = Number(route.query.customer_id)
  if (!Number.isInteger(id) || id <= 0) return
  try {
    customer.value = (await getCustomer(id)).customer
  } catch {
    // The picker opens blank, which is what it would have been without the link.
  }
}

/** The station's usual rate, as a percentage the person can change. */
async function loadRate(): Promise<void> {
  try {
    const { settings } = await listSettings()
    const bps = settings.find((s) => s.key === 'interest.rate_bps')?.value ?? ''
    if (/^\d+$/.test(bps) && rate.value === '') rate.value = ratePercent(Number(bps)).replace('%', '')
  } catch {
    // Left blank, which the server reads as the usual rate.
  }
}

const rateInvalid = ref(false)

async function compute(): Promise<void> {
  error.value = ''
  rateInvalid.value = false
  if (!customer.value) {
    error.value = 'Pick the farmer to settle.'
    return
  }

  let bps: number | null = null
  if (rate.value.trim() !== '') {
    bps = percentToBps(rate.value.trim())
    if (bps === null || bps > 10000) {
      rateInvalid.value = true
      error.value = 'The rate must be a percentage a month, as 5 or 2.5 — at most two decimals.'
      return
    }
  }

  busy.value = true
  try {
    const created = await createSettlement({
      customer_id: customer.value.id,
      cutoff: toISODate(cutoff.value),
      rate_bps: bps,
      notes: notes.value.trim(),
    })
    await router.replace({ name: 'settlement', params: { id: created.id } })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  void prefill()
  void loadRate()
})
</script>

<template>
  <div>
    <PageHeader
      title="Settle a farmer"
      description="Computed and saved as a draft. Nothing is applied until it is settled."
    />

    <Message v-if="error" severity="error" :closable="false" class="settle-new__error">
      {{ error }}
    </Message>

    <form class="card card--padded settle-new" @submit.prevent="compute">
      <div class="settle-new__field">
        <label for="settle-customer" class="label">Farmer</label>
        <AutoComplete
          input-id="settle-customer"
          v-model="customer"
          :suggestions="suggestions"
          :option-label="customerLabel"
          placeholder="Search by code or name"
          force-selection
          fluid
          @complete="searchCustomers"
        />
      </div>

      <div class="settle-new__fields">
        <div class="settle-new__field">
          <label for="settle-cutoff" class="label">Cut-off date</label>
          <DatePicker
            input-id="settle-cutoff"
            v-model="cutoff"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            fluid
          />
        </div>
        <div class="settle-new__field settle-new__field--rate">
          <label for="settle-rate" class="label">Rate a month</label>
          <div class="settle-new__rate">
            <InputText
              id="settle-rate"
              v-model="rate"
              inputmode="decimal"
              placeholder="usual"
              autocomplete="off"
              :invalid="rateInvalid"
              aria-describedby="settle-rate-help"
              fluid
            />
            <span class="settle-new__percent" aria-hidden="true">%</span>
          </div>
          <small id="settle-rate-help" class="settle-new__help">
            0 waives it. Blank takes the usual rate.
          </small>
        </div>
      </div>

      <div class="settle-new__field">
        <label for="settle-notes" class="label">Notes</label>
        <InputText id="settle-notes" v-model="notes" fluid />
      </div>

      <p class="settle-new__hint">
        Interest is principal × rate × days ÷ 30 on each open advance, and on any principal carried
        from his last settlement. His recorded payments are then applied, interest first.
      </p>

      <div class="settle-new__actions">
        <Button label="Cancel" severity="secondary" text :disabled="busy" @click="router.back()" />
        <Button
          type="submit"
          label="Compute"
          icon="pi pi-calculator"
          :disabled="busy || !customer"
          :loading="busy"
        />
      </div>
    </form>
  </div>
</template>

<style scoped>
.settle-new {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}

.settle-new__error {
  margin-bottom: 1rem;
}

.settle-new__fields {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.settle-new__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 11rem;
  flex: 1;
}

.settle-new__field--rate {
  max-width: 14rem;
}

.settle-new__rate {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.settle-new__rate :deep(input),
.settle-new__rate input {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.settle-new__percent {
  color: var(--shell-muted);
}

.settle-new__help {
  color: var(--shell-muted);
  font-size: 0.8125rem;
}

.settle-new__hint {
  margin: 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.settle-new__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
