<script setup lang="ts">
/**
 * What every report has in common: the question at the top, the answer below
 * it, and a head that only exists on paper.
 *
 * The printed head is in the DOM on every report and hidden on screen, rather
 * than being built at print time. There is one render path, so what comes out
 * of the printer is what was on the screen — a report assembled a second way
 * for printing is a report that can disagree with itself.
 */
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import PageHeader from '@/components/PageHeader.vue'
import { useSessionStore } from '@/stores/session'

const props = defineProps<{
  title: string
  description: string
  /** 'as-of' asks for one day; 'period' asks for two. */
  mode: 'as-of' | 'period'
  asOf?: Date
  from?: Date
  to?: Date
  loading?: boolean
  error?: string
  /** How many rows the report holds, for the empty state. */
  rows: number
}>()

const emit = defineEmits<{
  'update:asOf': [value: Date]
  'update:from': [value: Date]
  'update:to': [value: Date]
  refresh: []
}>()

const { user } = storeToRefs(useSessionStore())

// A report may not be run into the future: there is nothing there to report.
const latest = new Date()

const printedAt = computed(() =>
  new Date().toLocaleString('en-PH', { dateStyle: 'medium', timeStyle: 'short' }),
)

const covers = computed(() => {
  if (props.mode === 'as-of') return `As at ${props.asOf ? iso(props.asOf) : ''}`
  return `${props.from ? iso(props.from) : ''} to ${props.to ? iso(props.to) : ''}`
})

/** The browser's own print dialog: Decision 4 chose it over a PDF library. */
function print(): void {
  window.print()
}

function iso(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}
</script>

<template>
  <div class="report">
    <PageHeader :title="title" :description="description">
      <template #actions>
        <Button
          label="Print"
          icon="pi pi-print"
          severity="secondary"
          outlined
          :disabled="loading || rows === 0"
          @click="print"
        />
      </template>
    </PageHeader>

    <!--
      The head the printer sees and the screen does not. Everything on it is a
      fact somebody reading the page a week later needs and cannot recover:
      which station, which report, what it covers, and who took it when.
    -->
    <div class="report__head">
      <div class="report__station">TLS Palay &amp; Corn Buying Station</div>
      <div class="report__title">{{ title }}</div>
      <div class="report__meta">
        {{ covers }} · printed by {{ user?.display_name ?? user?.username }} on {{ printedAt }}
      </div>
    </div>

    <div class="card card--padded report__controls">
      <template v-if="mode === 'as-of'">
        <div class="report__field">
          <label for="report-as-of" class="label">As at</label>
          <DatePicker
            input-id="report-as-of"
            :model-value="asOf"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            @update:model-value="(v) => emit('update:asOf', v as Date)"
          />
        </div>
      </template>
      <template v-else>
        <div class="report__field">
          <label for="report-from" class="label">From</label>
          <DatePicker
            input-id="report-from"
            :model-value="from"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            @update:model-value="(v) => emit('update:from', v as Date)"
          />
        </div>
        <div class="report__field">
          <label for="report-to" class="label">To</label>
          <DatePicker
            input-id="report-to"
            :model-value="to"
            date-format="yy-mm-dd"
            :max-date="latest"
            show-icon
            icon-display="input"
            @update:model-value="(v) => emit('update:to', v as Date)"
          />
        </div>
      </template>

      <Button label="Run" icon="pi pi-refresh" :disabled="loading" @click="emit('refresh')" />
    </div>

    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

    <div v-else-if="loading" class="report__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <div v-else-if="rows === 0" class="card card--padded report__empty">
      <slot name="empty">Nothing to report over this period.</slot>
    </div>

    <div v-else class="card report__body">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.report {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.report__controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 0.75rem;
}

.report__field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 11rem;
}

.report__loading {
  display: grid;
  place-items: center;
  padding: 3rem 0;
}

.report__empty {
  color: var(--shell-muted);
}

.report__body {
  /* min-width: 0 so the flex item can shrink below its content; without it the
     table's own overflow-x contains nothing and the page scrolls sideways. */
  min-width: 0;
  overflow-x: auto;
  padding: 0.25rem 0.75rem 0.75rem;
}
</style>
