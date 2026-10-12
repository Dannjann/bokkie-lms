<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import PageHeader from '@/components/PageHeader.vue'
import { importCustomers, type ImportReport } from '@/api/customers'

const router = useRouter()

const file = ref<File | null>(null)
const report = ref<ImportReport | null>(null)
const error = ref('')
const busy = ref(false)

/** A report with no rejections is the only thing that may be imported. */
const importable = computed(() => report.value !== null && report.value.rejections.length === 0)

function choose(event: Event): void {
  const input = event.target as HTMLInputElement
  file.value = input.files?.[0] ?? null
  report.value = null
  error.value = ''
}

async function run(dryRun: boolean): Promise<void> {
  if (!file.value) return

  error.value = ''
  busy.value = true
  try {
    report.value = await importCustomers(file.value, dryRun)
  } catch (err) {
    report.value = null
    error.value = err instanceof Error ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="import">
    <PageHeader title="Import customers">
      <template #actions>
        <Button
          label="Back to customers"
          icon="pi pi-arrow-left"
          severity="secondary"
          text
          @click="router.push({ name: 'customers' })"
        />
      </template>
    </PageHeader>

    <section class="card card--padded import__intro">
      <p>
        A CSV with the columns
        <code>code, name, address, cell_no, active, notes</code>. Only
        <code>code</code> and <code>name</code> are required, and codes must look like
        <code>BA-0001</code>.
      </p>

      <!--
        Check first is not a suggestion. The check runs the whole import and stops
        before the commit, so what it reports is exactly what would happen.
      -->
      <p>
        The import is all-or-nothing: if any row is wrong, nothing at all is written.
        Check the file first — it reports every problem at once, and changes nothing.
      </p>
    </section>

    <div class="card card--padded import__pick">
      <input
        type="file"
        accept=".csv,text/csv"
        aria-label="CSV file"
        class="import__file"
        @change="choose"
      />
      <div class="import__buttons">
        <Button
          label="Check file"
          icon="pi pi-search"
          severity="secondary"
          outlined
          :disabled="!file || busy"
          @click="run(true)"
        />
        <Button
          label="Import"
          icon="pi pi-check"
          :disabled="!importable || busy || report?.applied"
          :loading="busy"
          @click="run(false)"
        />
      </div>
    </div>

    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

    <template v-if="report">
      <Message v-if="report.applied" severity="success" :closable="false">
        Imported {{ report.accepted }}
        {{ report.accepted === 1 ? 'customer' : 'customers' }},
        {{ report.first_code }} to {{ report.last_code }}.
      </Message>
      <Message v-else-if="importable" severity="info" :closable="false">
        {{ report.accepted }} {{ report.accepted === 1 ? 'customer' : 'customers' }} ready to
        import, {{ report.first_code }} to {{ report.last_code }}. Nothing has been written yet.
      </Message>
      <Message v-else severity="warn" :closable="false">
        {{ report.rejections.length }}
        {{ report.rejections.length === 1 ? 'row' : 'rows' }} cannot be imported, so none of the
        file will be. Fix them and check again.
      </Message>

      <div v-if="report.rejections.length" class="card">
        <DataTable :value="report.rejections" class="import__rejections" size="small">
          <Column field="line" header="Line" class="import__line" />
          <Column field="code" header="Code" />
          <Column field="reason" header="Why" />
        </DataTable>
      </div>

      <p class="import__hash">
        <span>{{ report.file_name }}</span>
        <code>{{ report.sha256 }}</code>
      </p>
    </template>
  </div>
</template>

<style scoped>
.import {
  max-width: 48rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.import__intro {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  color: var(--p-surface-600);
  font-size: 0.875rem;
  line-height: 1.55;
}

.import__intro p {
  margin: 0;
}

.import__intro code {
  font-size: 0.8125rem;
  background: var(--p-surface-100);
  padding: 0.1rem 0.3rem;
  border-radius: 4px;
}

.import__pick {
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
}

/* The native control, dressed to sit next to the two PrimeVue buttons rather
   than looking like something the page forgot to style. */
.import__file {
  min-width: 0;
  font-size: 0.875rem;
  color: var(--shell-muted);
}

.import__file::file-selector-button {
  margin-right: 0.75rem;
  padding: 0.45rem 0.9rem;
  border: 1px solid var(--p-surface-300);
  border-radius: 6px;
  background: var(--p-surface-0);
  color: var(--p-surface-700);
  font: inherit;
  font-weight: 500;
  cursor: pointer;
}

.import__file::file-selector-button:hover {
  background: var(--p-surface-100);
}

.import__buttons {
  margin-left: auto;
  display: flex;
  gap: 0.5rem;
}

.import__rejections :deep(.import__line) {
  font-variant-numeric: tabular-nums;
  width: 5rem;
}

/* The hash is what makes "which file produced this master?" answerable months
   from now, so it is on screen rather than only in the audit log. */
.import__hash {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  font-size: 0.75rem;
  color: var(--shell-muted);
  word-break: break-all;
}

@media (max-width: 40rem) {
  .import__buttons {
    margin-left: 0;
    width: 100%;
  }
}
</style>
