<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
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
    <header class="import__header">
      <h1 class="import__title">Import customers</h1>
      <Button
        label="Back to customers"
        icon="pi pi-arrow-left"
        severity="secondary"
        text
        @click="router.push({ name: 'customers' })"
      />
    </header>

    <p class="import__intro">
      A CSV with the columns
      <code>code, name, address, cell_no, active, notes</code>. Only
      <code>code</code> and <code>name</code> are required, and codes must look like
      <code>BA-0001</code>.
    </p>

    <!--
      Check first is not a suggestion. The check runs the whole import and stops
      before the commit, so what it reports is exactly what would happen.
    -->
    <p class="import__intro">
      The import is all-or-nothing: if any row is wrong, nothing at all is written.
      Check the file first — it reports every problem at once, and changes nothing.
    </p>

    <div class="import__pick">
      <input type="file" accept=".csv,text/csv" @change="choose" />
      <div class="import__buttons">
        <Button
          label="Check file"
          severity="secondary"
          outlined
          :disabled="!file || busy"
          @click="run(true)"
        />
        <Button
          label="Import"
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

      <DataTable
        v-if="report.rejections.length"
        :value="report.rejections"
        class="import__rejections"
        size="small"
      >
        <Column field="line" header="Line" class="import__line" />
        <Column field="code" header="Code" />
        <Column field="reason" header="Why" />
      </DataTable>

      <p class="import__hash">
        <span>{{ report.file_name }}</span>
        <code>{{ report.sha256 }}</code>
      </p>
    </template>
  </div>
</template>

<style scoped>
.import {
  max-width: 44rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.import__header {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.import__header > :last-child {
  margin-left: auto;
}

.import__title {
  margin: 0;
  font-size: 1.5rem;
}

.import__intro {
  margin: 0;
  color: var(--p-surface-600);
  font-size: 0.875rem;
  line-height: 1.5;
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
  padding: 1rem;
  background: var(--p-surface-0);
  border: 1px solid var(--p-surface-200);
  border-radius: 8px;
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
  color: var(--p-surface-500);
  word-break: break-all;
}
</style>
