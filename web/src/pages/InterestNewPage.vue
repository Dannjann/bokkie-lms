<script setup lang="ts">
/**
 * Choosing what a run covers.
 *
 * Nothing is charged here and nothing is decided here except the period: the
 * rule comes from the settings and the figures come from the ledger. Pressing
 * Compute writes a draft and opens it, which is the preview — the run minus the
 * commit.
 */
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { createRun, methodLabel, ratePercent, type InterestMethod } from '@/api/interest'
import { listSettings } from '@/api/settings'
import { toISODate } from '@/format'

const router = useRouter()

const error = ref('')
const busy = ref(false)

const rule = ref<{ method: InterestMethod; rateBps: number } | null>(null)

/**
 * Last calendar month, which is what a monthly run almost always covers.
 *
 * Both ends are dates rather than a month picker because Q1 may yet come back
 * as "per cropping season", and a season is not a month. The default costs
 * nothing if it is wrong and saves two clicks every time it is right.
 */
const now = new Date()
const from = ref(new Date(now.getFullYear(), now.getMonth() - 1, 1))
const to = ref(new Date(now.getFullYear(), now.getMonth(), 0))
const notes = ref('')

// The period may end today at the latest. Charging for a period that has not
// finished would compute balances from entries nobody has typed in yet.
const latest = new Date()

async function loadRule(): Promise<void> {
  try {
    const { settings } = await listSettings()
    const method = settings.find((s) => s.key === 'interest.method')?.value as InterestMethod
    const bps = Number(settings.find((s) => s.key === 'interest.rate_bps')?.value ?? '0')
    rule.value = { method: method ?? 'none', rateBps: bps }
  } catch {
    // Not worth blocking on: the server refuses a run with no rule anyway, and
    // its refusal names the setting to change.
  }
}

async function compute(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    const run = await createRun({
      period_from: toISODate(from.value),
      period_to: toISODate(to.value),
      notes: notes.value,
    })
    await router.replace({ name: 'interest-run', params: { id: run.id } })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

onMounted(loadRule)
</script>

<template>
  <div>
    <PageHeader
      title="New interest run"
      description="Computed and saved as a draft. Nothing is charged until it is posted."
    />

    <Message v-if="error" severity="error" :closable="false" class="run-new__error">
      {{ error }}
    </Message>

    <div class="card card--padded run-new">
      <p v-if="rule && rule.method !== 'none'" class="run-new__rule">
        This run will charge <strong>{{ ratePercent(rule.rateBps) }}</strong>
        {{ methodLabel(rule.method).toLowerCase() }}, and will carry that rule for good.
      </p>

      <div class="run-new__fields">
        <div class="run-new__field">
          <label for="run-from" class="label">From</label>
          <DatePicker
            input-id="run-from"
            v-model="from"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            fluid
          />
        </div>
        <div class="run-new__field">
          <label for="run-to" class="label">To</label>
          <DatePicker
            input-id="run-to"
            v-model="to"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            fluid
          />
        </div>
        <div class="run-new__field run-new__field--notes">
          <label for="run-notes" class="label">Notes</label>
          <InputText id="run-notes" v-model="notes" placeholder="September interest" fluid />
        </div>
      </div>

      <p class="run-new__hint">
        A period that has already been charged is refused, whole or in part, so the same month
        cannot be charged twice.
      </p>

      <div class="run-new__actions">
        <Button label="Cancel" severity="secondary" text :disabled="busy" @click="router.back()" />
        <Button label="Compute" icon="pi pi-calculator" :disabled="busy" @click="compute" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.run-new {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.run-new__error {
  margin-bottom: 1rem;
}

.run-new__rule {
  margin: 0;
  color: var(--shell-muted);
}

.run-new__fields {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.run-new__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 11rem;
  flex: 1;
}

.run-new__field--notes {
  flex-basis: 100%;
}

.run-new__hint {
  margin: 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.run-new__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
